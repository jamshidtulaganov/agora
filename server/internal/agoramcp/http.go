package agoramcp

import (
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// connectorClientHeader matches handler.ConnectorClientHeader: it labels the
// member's connector conversation in Agora (where confirmations wait).
const connectorClientHeader = "X-Agora-Connector-Client"

// newRemoteHandler forwards MCP requests to the Agora backend's /api/mcp,
// which serves the Agora Assistant's full tool catalog as the member. The
// caller has already replaced Authorization with the member's token. Nothing
// browser-ambient (cookies, Origin, Referer) is forwarded: the backend must
// authenticate this request by its bearer token alone.
func newRemoteHandler(apiURL string) (http.Handler, error) {
	u, err := url.Parse(apiURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("AGORA_API_URL must be an HTTPS URL without credentials, query or fragment")
	}
	target := *u
	target.Path = strings.TrimRight(u.Path, "/") + "/api/mcp"
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = &url.URL{Scheme: target.Scheme, Host: target.Host, Path: target.Path}
			pr.Out.Host = target.Host
			for _, h := range []string{"Cookie", "Origin", "Referer", "X-Workspace-ID", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
				pr.Out.Header.Del(h)
			}
		},
		ModifyResponse: func(res *http.Response) error {
			if res.StatusCode >= 400 {
				log.Printf("mcp: upstream refused: status=%d", res.StatusCode)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("mcp: upstream unreachable: %v", err)
			http.Error(w, "Agora is not reachable right now", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(token, "mul_") {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Agora"`)
			http.Error(w, "An Agora personal access token is required", http.StatusUnauthorized)
			return
		}
		proxy.ServeHTTP(w, r)
	}), nil
}
