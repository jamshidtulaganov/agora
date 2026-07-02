// Package figma holds the pure Figma-link primitives the design stage builds
// on: extracting design references (file key + node id) from free text and
// rendering them as the `figma_links` issue-metadata stamp. It lives outside
// the handler package because both the handler layer (claim-time injection,
// slice actions) and the service layer (create-time stamping) need it, and
// service → handler imports are forbidden.
package figma

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// urlRe matches file/design/proto URLs and captures the file key. The
// trailing class deliberately stops at whitespace and common punctuation so a
// URL pasted mid-sentence or inside markdown doesn't swallow the delimiter.
var urlRe = regexp.MustCompile(`https://(?:www\.)?figma\.com/(?:file|design|proto)/([A-Za-z0-9]{10,})[^\s)\]"'<>]*`)

// Ref is one referenced design node: the original URL, the file key, and the
// normalized node id ("208:5147" — Figma URLs carry it as "208-5147").
type Ref struct {
	URL     string `json:"url"`
	FileKey string `json:"file_key"`
	NodeID  string `json:"node_id"`
}

// RefsFrom extracts every Figma design reference from free text,
// deduplicated by (file key, node id). Pure — exhaustively table-tested.
func RefsFrom(text string) []Ref {
	if !strings.Contains(text, "figma.com/") {
		return nil
	}
	matches := urlRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	var out []Ref
	seen := map[string]bool{}
	for _, m := range matches {
		ref := Ref{URL: m[0], FileKey: m[1], NodeID: nodeIDFromURL(m[0])}
		key := ref.FileKey + "|" + ref.NodeID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ref)
	}
	return out
}

// nodeIDFromURL pulls the node-id query param and normalizes the URL form
// ("208-5147", possibly %3A-encoded) to the API form ("208:5147").
func nodeIDFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	nodeID := u.Query().Get("node-id")
	if nodeID == "" {
		return ""
	}
	return strings.ReplaceAll(nodeID, "-", ":")
}

// LinksMetadataValue renders refs as the string value stamped under the
// `figma_links` issue-metadata key. The issue-metadata V1 contract allows
// primitive scalar values only, so the array is JSON-encoded into a string —
// the exact idiom the Bitrix sync uses for bitrix_task_id. Capped at 5 links;
// returns "" when there is nothing to stamp or the encoded value would risk
// the 8KB metadata CHECK.
func LinksMetadataValue(refs []Ref) string {
	if len(refs) == 0 {
		return ""
	}
	if len(refs) > 5 {
		refs = refs[:5]
	}
	buf, err := json.Marshal(refs)
	if err != nil {
		return ""
	}
	if len(buf) > 4096 {
		return ""
	}
	return string(buf)
}

// ParseLinksMetadataValue decodes a stamped `figma_links` value back into
// refs. Malformed input degrades to nil — the stamp is an optimization, live
// extraction from the description is the fallback everywhere.
func ParseLinksMetadataValue(stamped string) []Ref {
	if stamped == "" {
		return nil
	}
	var refs []Ref
	if json.Unmarshal([]byte(stamped), &refs) != nil {
		return nil
	}
	return refs
}
