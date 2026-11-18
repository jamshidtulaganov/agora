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

	"github.com/jamshidtulaganov/agora/server/internal/cli"
	"github.com/jamshidtulaganov/agora/server/internal/util/secretbox"
)

// One public OAuth client, pre-registered for the Claude web connector.
// No network lookups or dynamic redirect URIs are needed to identify it.
const claudeClient = "agora-claude"
const claudeCallback = "https://claude.ai/api/mcp/auth_callback"
const grantLifetime = 24 * time.Hour
const scope = "agora.workspace"

var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
var consent = template.Must(template.New("consent").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Connect Agora to Claude</title>
<main><h1>Connect Agora to Claude</h1><p>Allow Claude to read the Agora workspace you have access to and post comments under your name.</p>
<p>Open <a href="{{.Agora}}" target="_blank" rel="noreferrer">Agora</a>, sign in, and create a personal access token in Settings → Personal Access Tokens. Use a separate token for this connection. You can revoke it in Agora at any time.</p>
<form method="post"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Your Agora personal access token <input required type="password" name="pat" autocomplete="off"></label> <button type="submit">Authorize Claude</button></form></main></html>`))

type memberGrant struct {
	Audience  string
	Workspace string
	PAT       string
	Expires   int64
}

type pendingCode struct {
	Challenge string
	Access    string
	Expires   time.Time
}

type memberOAuth struct {
	issuer, api, workspace string
	box                    *secretbox.Box
	mu                     sync.Mutex
	codes                  map[string]pendingCode
}

// NewMemberConnector wraps the MCP endpoint with per-member OAuth. Only this
// layer handles connector tokens; the upstream REST API receives the member PAT.
func NewMemberConnector(issuer, api, workspace, version string, key []byte) (http.Handler, error) {
	upstream, err := NewRemoteHandler(api, workspace, version)
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
	o := &memberOAuth{issuer: issuer, api: api, workspace: workspace, box: box, codes: make(map[string]pendingCode)}
	mux := http.NewServeMux()
	res := func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, 200, map[string]any{"resource": issuer + "/mcp", "authorization_servers": []string{issuer}, "scopes_supported": []string{scope}, "bearer_methods_supported": []string{"header"}})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", res)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", res)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		jsonReply(w, 200, map[string]any{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code"}, "code_challenge_methods_supported": []string{"S256"}, "token_endpoint_auth_methods_supported": []string{"none"}, "scopes_supported": []string{scope}})
	})
	mux.HandleFunc("GET /authorize", o.authorize)
	mux.HandleFunc("POST /authorize", o.authorize)
	mux.HandleFunc("POST /token", o.exchange)
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		pat := o.openGrant(token)
		if !strings.EqualFold(scheme, "Bearer") || pat == "" {
			w.Header().Set("WWW-Authenticate", o.challenge())
			http.Error(w, "Connect your Agora account", 401)
			return
		}
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+pat)
		upstream.ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' https://claude.ai; frame-ancestors 'none'; base-uri 'none'")
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
	challenge := q.Get("code_challenge")
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(challenge)
	if q.Get("response_type") != "code" || q.Get("client_id") != claudeClient || q.Get("redirect_uri") != claudeCallback || q.Get("code_challenge_method") != "S256" || err != nil || len(decoded) != 32 || q.Get("state") == "" || len(q.Get("state")) > 2048 || !o.validResource(q.Get("resource")) || (q.Get("scope") != "" && q.Get("scope") != scope) {
		http.Error(w, "Invalid authorization request. Check the Claude client ID, callback and PKCE settings.", 400)
		return
	}
	const cookieName = "__Host-agora-mcp-consent"
	if r.Method == http.MethodGet {
		nonce := rand.Text()
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: nonce, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: 600})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = consent.Execute(w, map[string]string{"Agora": o.api, "CSRF": digest(nonce + " " + q.Encode())})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	cookie, err := r.Cookie(cookieName)
	if err != nil || r.Header.Get("Origin") != o.issuer || r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("csrf")), []byte(digest(cookie.Value+" "+q.Encode()))) != 1 {
		http.Error(w, "Authorization form expired. Reconnect from Claude.", 403)
		return
	}
	pat := strings.TrimSpace(r.PostForm.Get("pat"))
	if !strings.HasPrefix(pat, "mul_") || len(pat) > 256 {
		http.Error(w, "Enter your Agora personal access token.", 400)
		return
	}
	client := cli.NewAPIClient(o.api, o.workspace, pat)
	client.HTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var workspace struct {
		ID string `json:"id"`
	}
	if err := client.GetJSON(ctx, "/api/workspaces/"+o.workspace, &workspace); err != nil || workspace.ID != o.workspace {
		http.Error(w, "Could not verify access to this Agora workspace. Check your token and membership, then reconnect.", 403)
		return
	}
	now := time.Now()
	payload, err := json.Marshal(memberGrant{o.issuer + "/mcp", o.workspace, pat, now.Add(grantLifetime).Unix()})
	if err != nil {
		http.Error(w, "Could not authorize", 500)
		return
	}
	sealed, err := o.box.Seal(payload)
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
	o.codes[digest(code)] = pendingCode{challenge, base64.RawURLEncoding.EncodeToString(sealed), now.Add(2 * time.Minute)}
	o.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode, Path: "/", MaxAge: -1})
	values := url.Values{"state": {q.Get("state")}, "code": {code}, "iss": {o.issuer}}
	http.Redirect(w, r, claudeCallback+"?"+values.Encode(), http.StatusSeeOther)
}

func (o *memberOAuth) validResource(resource string) bool {
	return resource == "" || resource == o.issuer+"/mcp"
}

func (o *memberOAuth) exchange(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if r.ParseForm() != nil || r.PostForm.Get("grant_type") != "authorization_code" || r.PostForm.Get("client_id") != claudeClient || r.PostForm.Get("redirect_uri") != claudeCallback || !o.validResource(r.PostForm.Get("resource")) {
		jsonReply(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	code := digest(r.PostForm.Get("code"))
	verifier := r.PostForm.Get("code_verifier")
	o.mu.Lock()
	entry, ok := o.codes[code]
	valid := ok && entry.Expires.After(time.Now()) && verifierPattern.MatchString(verifier) && subtle.ConstantTimeCompare([]byte(entry.Challenge), []byte(digest(verifier))) == 1
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

func (o *memberOAuth) openGrant(token string) string {
	if token == "" || len(token) > 2048 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ""
	}
	data, err := o.box.Open(b)
	if err != nil {
		return ""
	}
	var g memberGrant
	if json.Unmarshal(data, &g) != nil || g.Audience != o.issuer+"/mcp" || g.Workspace != o.workspace || g.Expires <= time.Now().Unix() {
		return ""
	}
	return g.PAT
}
