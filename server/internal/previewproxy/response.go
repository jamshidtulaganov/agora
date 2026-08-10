package previewproxy

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// rootAssetRefs matches absolute-root references emitted by common frontend
// dev servers and production asset manifests. Preview apps are mounted below
// /editor/local/{port}, so these references must retain that mount prefix.
var rootAssetRefs = regexp.MustCompile(
	`([\"'` + "`" + `(=\s])/((?:@vite/|@react-refresh|@id/|@fs/|src/|node_modules/|assets/|@vite-plugin|__vite))`,
)

// PrepareResponse makes an application response functional inside Agora's
// isolated preview iframe. The app stays cross-origin and sandboxed; only
// frame-ancestors and X-Frame-Options are removed. Every other CSP directive
// continues to constrain the preview application.
func PrepareResponse(resp *http.Response, mountPrefix string) error {
	removeCSPDirective(resp.Header, "Content-Security-Policy", "frame-ancestors")
	removeCSPDirective(resp.Header, "Content-Security-Policy-Report-Only", "frame-ancestors")
	resp.Header.Del("X-Frame-Options")

	if !IsRewritableContentType(resp.Header.Get("Content-Type")) {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return err
	}
	out := RewriteBody(body, mountPrefix)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.Header.Del("ETag")
	return nil
}

func removeCSPDirective(header http.Header, key, directiveName string) {
	values := header.Values(key)
	if len(values) == 0 {
		return
	}
	header.Del(key)
	for _, value := range values {
		kept := make([]string, 0)
		for _, directive := range strings.Split(value, ";") {
			directive = strings.TrimSpace(directive)
			name, _, _ := strings.Cut(directive, " ")
			if directive == "" || strings.EqualFold(name, directiveName) {
				continue
			}
			kept = append(kept, directive)
		}
		if len(kept) > 0 {
			header.Add(key, strings.Join(kept, "; "))
		}
	}
}

// IsRewritableContentType reports whether a response can contain asset URLs.
func IsRewritableContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "text/html") ||
		strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "text/css")
}

// RewriteBody prefixes absolute-root frontend asset references.
func RewriteBody(body []byte, mountPrefix string) []byte {
	return rootAssetRefs.ReplaceAll(body, []byte("${1}"+mountPrefix+"/${2}"))
}
