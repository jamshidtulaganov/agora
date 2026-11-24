package agoramcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jamshidtulaganov/agora/server/internal/util"
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
const scope = "agora.workspace"

var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var consent = template.Must(template.New("consent").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect Agora to {{.Client}}</title>
<main><h1>Connect Agora to {{.Client}}</h1><p>Choose the workspace {{.Client}} can read and post comments in under your name.</p>
{{if not .Ticket}}<p>Open <a href="{{.Agora}}" target="_blank" rel="noreferrer">Agora</a>, sign in, and create a personal access token in Settings → Personal Access Tokens. Use a separate token for this connection. You can revoke it in Agora at any time.</p>{{end}}
<form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}">{{if .Ticket}}<input type="hidden" name="ticket" value="{{.Ticket}}"><label>Workspace <select name="workspace" required>{{range .Workspaces}}<option value="{{.ID}}">{{.Name}} ({{.Slug}})</option>{{end}}</select></label> <button type="submit">Authorize {{.Client}}</button>{{else}}<label>Your Agora personal access token <input required type="password" name="pat" autocomplete="off"></label> <button type="submit">Find my workspaces</button>{{end}}</form></main></html>`))

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
	Expires   time.Time
}

type memberOAuth struct {
	issuer, api string
	box         *secretbox.Box
	mu          sync.Mutex
	codes       map[string]pendingCode
}

// NewMemberConnector wraps the MCP endpoint with per-member OAuth. Only this
// layer handles connector tokens; the upstream REST API receives the member PAT.
func NewMemberConnector(issuer, api, version string, key []byte) (http.Handler, error) {
	upstream, err := newRemoteHandler(api, version)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("connector issuer must be a public HTTPS origin")
	}
	box, err := secretbox.New(key)
	if err != nil {
		return nil, err
	}
	o := &memberOAuth{issuer: issuer, api: api, box: box, codes: make(map[string]pendingCode)}
	mux := http.NewServeMux()
	res := func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, 200, map[string]any{"resource": issuer + "/mcp", "authorization_servers": []string{issuer}, "scopes_supported": []string{scope}, "bearer_methods_supported": []string{"header"}})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", res)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", res)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, 200, map[string]any{"issuer": issuer, "authorization_response_iss_parameter_supported": true, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{scope}, "registration_endpoint": issuer + "/register"})
	})
	mux.HandleFunc("POST /register", register)
	mux.HandleFunc("GET /authorize", o.authorize)
	mux.HandleFunc("POST /authorize", o.authorize)
	mux.HandleFunc("POST /token", o.exchange)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		grant, ok := o.openGrant(token, o.issuer+"/mcp")
		if !strings.EqualFold(scheme, "Bearer") || !ok || grant.Workspace == "" {
			w.Header().Set("WWW-Authenticate", o.challenge())
			http.Error(w, "Connect your Agora account", 401)
			return
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+grant.PAT)
		r.Header.Set("X-Workspace-ID", grant.Workspace)
		upstream.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// same-origin, not no-referrer: under no-referrer browsers send
		// "Origin: null" on the consent form POST, which the origin check rejects.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' https://claude.ai https://chatgpt.com; frame-ancestors 'none'; base-uri 'none'")
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

func (o *memberOAuth) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	clientID := q.Get("client_id")
	app, registered := clients[clientID]
	challenge := q.Get("code_challenge")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	if q.Get("response_type") != "code" || !registered || q.Get("redirect_uri") != app.Callback || q.Get("code_challenge_method") != "S256" || err != nil || len(decoded) != 32 || q.Get("state") == "" || len(q.Get("state")) > 2048 || !o.validResource(q.Get("resource")) || (q.Get("scope") != "" && q.Get("scope") != scope) {
		http.Error(w, "Invalid authorization request. Check the client ID, callback and PKCE settings.", 400)
		return
	}
	const cookieName = "__Host-agora-mcp-consent"
	if r.Method == http.MethodGet {
		nonce := rand.Text()
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: nonce, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 600})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = consent.Execute(w, map[string]string{"Client": app.Name, "Agora": o.api, "CSRF": digest(nonce + " " + q.Encode())})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	cookie, err := r.Cookie(cookieName)
	if err != nil || !o.sameOriginPost(r) || r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(digest(cookie.Value+" "+q.Encode()))) != 1 {
		http.Error(w, "Authorization form expired. Reconnect from your app.", 403)
		return
	}
	csrf := r.PostForm.Get("csrf")
	pat := strings.TrimSpace(r.PostForm.Get("pat"))
	ticketAudience := o.issuer + "/authorize/" + csrf
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	now := time.Now()
	ticket := r.PostForm.Get("ticket")
	if ticket == "" {
		if !strings.HasPrefix(pat, "mul_") || len(pat) > 256 {
			http.Error(w, "Enter your Agora personal access token.", 400)
			return
		}
		var memberships []struct{ ID, Name, Slug string }
		client := memberClient(o.api, "", pat)
		if err := client.GetJSON(ctx, "/api/workspaces", &memberships); err != nil || len(memberships) == 0 {
			http.Error(w, "No accessible Agora workspace found. Check your token and membership.", 403)
			return
		}
		sealed, err := o.sealGrant(memberGrant{ticketAudience, "", pat, now.Add(5 * time.Minute).Unix()})
		if err != nil {
			http.Error(w, "Could not start workspace selection", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = consent.Execute(w, map[string]any{"Client": app.Name, "Agora": o.api, "Ticket": sealed, "CSRF": csrf, "Workspaces": memberships})
		return
	}
	login, ok := o.openGrant(ticket, ticketAudience)
	if !ok {
		http.Error(w, "Workspace selection expired. Reconnect from your app.", 403)
		return
	}
	workspace := r.PostForm.Get("workspace")
	if _, err := util.ParseUUID(workspace); err != nil {
		http.Error(w, "Choose an Agora workspace.", 400)
		return
	}
	client := memberClient(o.api, workspace, login.PAT)
	var membership struct{ ID string }
	if err := client.GetJSON(ctx, "/api/workspaces/"+workspace, &membership); err != nil || membership.ID != workspace {
		http.Error(w, "Workspace access denied.", 403)
		return
	}
	sealed, err := o.sealGrant(memberGrant{o.issuer + "/mcp", workspace, login.PAT, now.Add(grantLifetime).Unix()})
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
	o.codes[digest(code)] = pendingCode{clientID, challenge, sealed, now.Add(2 * time.Minute)}
	o.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: -1})
	values := url.Values{"state": {q.Get("state")}, "code": {code}, "iss": {o.issuer}}
	http.Redirect(w, r, app.Callback+"?"+values.Encode(), http.StatusSeeOther)
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

// sameOriginPost reports whether a form POST came from this origin. Some
// browsers send "Origin: null" (or omit it) under strict referrer policies;
// Sec-Fetch-Site is set by the browser and cannot be forged by a page, so it
// is accepted as proof in that case.
func (o *memberOAuth) sameOriginPost(r *http.Request) bool {
	switch origin := r.Header.Get("Origin"); origin {
	case o.issuer:
		return true
	case "", "null":
		return r.Header.Get("Sec-Fetch-Site") == "same-origin"
	default:
		return false
	}
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
	jsonReply(w, 200, map[string]any{"access_token": entry.Access, "token_type": "Bearer", "expires_in": int(grantLifetime.Seconds()) - 120, "scope": scope})
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
