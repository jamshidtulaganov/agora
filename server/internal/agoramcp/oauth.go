package agoramcp

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/connectauth"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

// Public OAuth clients have a fixed callback and use PKCE.
const claudeClient = "agora-claude"
const claudeCallback = "https://claude.ai/api/mcp/auth_callback"
const chatGPTClient = "agora-chatgpt"
const chatGPTCallback = "https://chatgpt.com/connector_platform_oauth_redirect"

type connectorClient struct{ Name, Callback string }

var clients = map[string]connectorClient{
	claudeClient:  {"Claude", claudeCallback},
	chatGPTClient: {"ChatGPT", chatGPTCallback},
}

const grantLifetime = 24 * time.Hour

// refreshLifetime matches the 90-day personal access token the consent mints,
// so a connection lasts as long as its token without daily reconnects.
const refreshLifetime = 90 * 24 * time.Hour

// consentCookie binds the Agora consent round trip to the browser that started
// it. Lax, not Strict: the browser returns from Agora's site with a top-level
// navigation, on which Strict cookies are not sent.
const consentCookie = "__Host-agora-mcp-consent"
const scope = "agora.workspace"

var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type memberGrant struct {
	Audience  string
	Workspace string
	PAT       string
	Expires   int64
}

type pendingCode struct {
	ClientID  string
	Challenge string
	Access    string
	Refresh   string
	Expires   time.Time
}

type memberOAuth struct {
	issuer, api, app string
	box              *secretbox.Box
	mu               sync.Mutex
	codes            map[string]pendingCode
}

// NewMemberConnector wraps the MCP endpoint with per-member OAuth. Members sign
// in on Agora itself (app) and approve the connection there; Agora mints a
// personal access token and returns it sealed. Only this layer handles
// connector tokens; the upstream REST API receives the member PAT.
func NewMemberConnector(issuer, api, app, version string, key []byte) (http.Handler, error) {
	upstream, err := newRemoteHandler(api, version)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("connector issuer must be a public HTTPS origin")
	}
	a, err := url.Parse(app)
	if err != nil || a.Scheme != "https" || a.Host == "" || a.User != nil || (a.Path != "" && a.Path != "/") || a.RawQuery != "" || a.Fragment != "" {
		return nil, errors.New("Agora app URL must be a public HTTPS origin")
	}
	box, err := secretbox.New(key)
	if err != nil {
		return nil, err
	}
	o := &memberOAuth{issuer: issuer, api: api, app: strings.TrimRight(app, "/"), box: box, codes: make(map[string]pendingCode)}
	mux := http.NewServeMux()
	res := func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, 200, map[string]any{"resource": issuer + "/mcp", "authorization_servers": []string{issuer}, "scopes_supported": []string{scope}, "bearer_methods_supported": []string{"header"}})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", res)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", res)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, 200, map[string]any{"issuer": issuer, "authorization_response_iss_parameter_supported": true, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{scope}, "registration_endpoint": issuer + "/register"})
	})
	mux.HandleFunc("POST /register", register)
	mux.HandleFunc("GET /authorize", o.authorize)
	mux.HandleFunc("GET /authorize/callback", o.callback)
	mux.HandleFunc("POST /token", o.exchange)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		grant, ok := o.openGrant(token, o.issuer+"/mcp")
		if !strings.EqualFold(scheme, "Bearer") || !ok {
			w.Header().Set("WWW-Authenticate", o.challenge())
			http.Error(w, "Connect your Agora account", 401)
			return
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+grant.PAT)
		// Grants issued before multi-workspace access pin one workspace; newer
		// grants cover every workspace the member belongs to.
		r.Header.Del("X-Workspace-ID")
		if grant.Workspace != "" {
			r.Header.Set("X-Workspace-ID", grant.Workspace)
		}
		upstream.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Redirects carry Claude's state and the sealed consent request; keep
		// them out of Referer headers sent to other sites.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	}), nil
}

func (o *memberOAuth) challenge() string {
	return `Bearer resource_metadata="` + o.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + scope + `"`
}

func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func digest(s string) string {
	hash := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// validAuthorize checks Claude's or ChatGPT's authorization parameters.
func (o *memberOAuth) validAuthorize(q url.Values) (connectorClient, bool) {
	app, registered := clients[q.Get("client_id")]
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(q.Get("code_challenge"))
	ok := q.Get("response_type") == "code" && registered && q.Get("redirect_uri") == app.Callback && q.Get("code_challenge_method") == "S256" && err == nil && len(decoded) == 32 && q.Get("state") != "" && len(q.Get("state")) <= 2048 && o.validResource(q.Get("resource")) && (q.Get("scope") == "" || q.Get("scope") == scope)
	return app, ok
}

// authorize sends the member to Agora's consent page with a sealed request
// that names the app and where to come back to.
func (o *memberOAuth) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	app, ok := o.validAuthorize(q)
	if !ok {
		http.Error(w, "Invalid authorization request. Check the client ID, callback and PKCE settings.", 400)
		return
	}
	nonce := rand.Text()
	request, err := connectauth.SealRequest(o.box, connectauth.Request{
		ClientID: q.Get("client_id"), ClientName: app.Name,
		Return:  o.issuer + "/authorize/callback?" + q.Encode(),
		Binding: digest(nonce + " " + q.Encode()),
	}, time.Now())
	if err != nil {
		http.Error(w, "Could not start authorization", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: consentCookie, Value: nonce, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: int(connectauth.RequestLifetime.Seconds())})
	http.Redirect(w, r, o.app+"/oauth/connect?"+url.Values{"request": {request}}.Encode(), http.StatusSeeOther)
}

// callback receives the member's decision from Agora. An approval carries a
// sealed token that must belong to this browser's consent cookie.
func (o *memberOAuth) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	reply, denied := q.Get("reply"), q.Get("error")
	q.Del("reply")
	q.Del("error")
	app, ok := o.validAuthorize(q)
	if !ok {
		http.Error(w, "Invalid authorization request. Reconnect from your app.", 400)
		return
	}
	cookie, cookieErr := r.Cookie(consentCookie)
	http.SetCookie(w, &http.Cookie{Name: consentCookie, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Path: "/", MaxAge: -1})
	if denied != "" {
		values := url.Values{"state": {q.Get("state")}, "error": {"access_denied"}, "iss": {o.issuer}}
		http.Redirect(w, r, app.Callback+"?"+values.Encode(), http.StatusSeeOther)
		return
	}
	now := time.Now()
	granted, err := connectauth.OpenReply(o.box, reply, now)
	if err != nil || cookieErr != nil || subtle.ConstantTimeCompare([]byte(granted.Binding), []byte(digest(cookie.Value+" "+q.Encode()))) != 1 || !strings.HasPrefix(granted.Token, "mul_") {
		http.Error(w, "Authorization expired. Reconnect from your app.", 403)
		return
	}
	access, err := o.sealGrant(memberGrant{o.issuer + "/mcp", "", granted.Token, now.Add(grantLifetime).Unix()})
	if err != nil {
		http.Error(w, "Could not authorize", 500)
		return
	}
	refresh, err := o.sealGrant(memberGrant{o.refreshAudience(q.Get("client_id")), "", granted.Token, now.Add(refreshLifetime).Unix()})
	if err != nil {
		http.Error(w, "Could not authorize", 500)
		return
	}
	code := rand.Text()
	o.mu.Lock()
	for id, entry := range o.codes {
		if !entry.Expires.After(now) {
			delete(o.codes, id)
		}
	}
	if len(o.codes) >= 2048 {
		o.mu.Unlock()
		http.Error(w, "Please retry shortly", 503)
		return
	}
	o.codes[digest(code)] = pendingCode{q.Get("client_id"), q.Get("code_challenge"), access, refresh, now.Add(2 * time.Minute)}
	o.mu.Unlock()
	values := url.Values{"state": {q.Get("state")}, "code": {code}, "iss": {o.issuer}}
	http.Redirect(w, r, app.Callback+"?"+values.Encode(), http.StatusSeeOther)
}

func (o *memberOAuth) refreshAudience(clientID string) string {
	return o.issuer + "/refresh/" + clientID
}

// register implements RFC 7591 dynamic client registration for the known
// public clients only. It is stateless: a request whose redirect URIs all
// match one fixed client gets that client's ID back, so Claude and ChatGPT can
// connect without the member typing a client ID. Any other callback is refused.
func register(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		AuthMethod   string   `json:"token_endpoint_auth_method"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		jsonReply(w, 400, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	if req.AuthMethod != "" && req.AuthMethod != "none" {
		jsonReply(w, 400, map[string]string{"error": "invalid_client_metadata", "error_description": "only public clients with PKCE are supported"})
		return
	}
	matched := ""
	for _, uri := range req.RedirectURIs {
		id := ""
		for clientID, app := range clients {
			if app.Callback == uri {
				id = clientID
			}
		}
		if id == "" || (matched != "" && matched != id) {
			matched = ""
			break
		}
		matched = id
	}
	if matched == "" {
		jsonReply(w, 400, map[string]string{"error": "invalid_redirect_uri", "error_description": "redirect URI is not an allowed connector callback"})
		return
	}
	app := clients[matched]
	jsonReply(w, 201, map[string]any{"client_id": matched, "client_id_issued_at": time.Now().Unix(), "client_name": app.Name, "redirect_uris": []string{app.Callback}, "token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code"}, "response_types": []string{"code"}, "scope": scope})
}

func (o *memberOAuth) validResource(resource string) bool {
	return resource == "" || resource == o.issuer+"/mcp"
}

func (o *memberOAuth) exchange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil {
		jsonReply(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	clientID := r.PostForm.Get("client_id")
	app, registered := clients[clientID]
	if r.PostForm.Get("grant_type") == "refresh_token" && registered {
		grant, ok := o.openGrant(r.PostForm.Get("refresh_token"), o.refreshAudience(clientID))
		if !ok {
			jsonReply(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		access, err := o.sealGrant(memberGrant{o.issuer + "/mcp", "", grant.PAT, time.Now().Add(grantLifetime).Unix()})
		if err != nil {
			jsonReply(w, 500, map[string]string{"error": "server_error"})
			return
		}
		jsonReply(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(grantLifetime.Seconds()) - 120, "scope": scope})
		return
	}
	if r.PostForm.Get("grant_type") != "authorization_code" || !registered || r.PostForm.Get("redirect_uri") != app.Callback || !o.validResource(r.PostForm.Get("resource")) {
		jsonReply(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	code := digest(r.PostForm.Get("code"))
	verifier := r.PostForm.Get("code_verifier")
	o.mu.Lock()
	entry, ok := o.codes[code]
	valid := ok && entry.ClientID == clientID && entry.Expires.After(time.Now()) && verifierPattern.MatchString(verifier) && subtle.ConstantTimeCompare([]byte(entry.Challenge), []byte(digest(verifier))) == 1
	if valid {
		delete(o.codes, code)
	}
	o.mu.Unlock()
	if !valid {
		jsonReply(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	jsonReply(w, 200, map[string]any{"access_token": entry.Access, "refresh_token": entry.Refresh, "token_type": "Bearer", "expires_in": int(grantLifetime.Seconds()) - 120, "scope": scope})
}

func (o *memberOAuth) sealGrant(g memberGrant) (string, error) {
	payload, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	sealed, err := o.box.Seal(payload)
	return base64.RawURLEncoding.EncodeToString(sealed), err
}

func (o *memberOAuth) openGrant(token, audience string) (memberGrant, bool) {
	if token == "" || len(token) > 2048 {
		return memberGrant{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return memberGrant{}, false
	}
	data, err := o.box.Open(b)
	if err != nil {
		return memberGrant{}, false
	}
	var g memberGrant
	if json.Unmarshal(data, &g) != nil || g.Audience != audience || g.Expires <= time.Now().Unix() {
		return memberGrant{}, false
	}
	return g, true
}
