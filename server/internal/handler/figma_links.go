package handler

import (
	"strings"

	"github.com/multica-ai/multica/server/internal/figma"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Figma link extraction is the platform primitive the design stage builds on:
// an issue that references a Figma design (Bitrix TZ, hand-written
// description) is detected here, and everything downstream — claim-time MCP
// injection, the design_proposal action, decomposition — keys off these refs.
// The pure primitives live in internal/figma (the service layer stamps at
// create time and cannot import this package); this file holds the
// issue-shaped wrappers.

// issueFigmaRefs returns the issue's design references: the union of the
// stamped `figma_links` metadata key (when present) and a live extraction
// from the description. The union — not stamp-first — matters: a stamp
// written at create time must never hide a link added to the description
// later. Deduplicated by (file key, node id).
func issueFigmaRefs(issue db.Issue) []figma.Ref {
	var out []figma.Ref
	seen := map[string]bool{}
	add := func(refs []figma.Ref) {
		for _, r := range refs {
			key := r.FileKey + "|" + r.NodeID
			if r.FileKey == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, r)
		}
	}
	add(figma.ParseLinksMetadataValue(metaString(issue.Metadata, "figma_links")))
	if issue.Description.Valid {
		add(figma.RefsFrom(issue.Description.String))
	}
	return out
}

// figmaContextForIssue renders the claim-time note that teaches the agent how
// to actually open the referenced designs through its injected figma MCP
// server. Empty when the issue references nothing.
func figmaContextForIssue(refs []figma.Ref) string {
	if len(refs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("FIGMA DESIGNS REFERENCED BY THIS ISSUE — read them with your `figma` MCP tools:")
	for _, r := range refs {
		b.WriteString("\n- " + r.URL + " → get_figma_data(fileKey=\"" + r.FileKey + "\"")
		if r.NodeID != "" {
			b.WriteString(", nodeId=\"" + r.NodeID + "\"")
		}
		b.WriteString(")")
	}
	b.WriteString("\nRead ONLY the referenced node(s), scoped by nodeId — never fetch a whole file. ")
	b.WriteString("Download frames you need with download_figma_images (pngScale=2) into your workdir. ")
	b.WriteString("Figma render URLs expire — if you post images, upload them as comment attachments. ")
	b.WriteString("Quota is limited (~10-20 req/min): batch node ids, honor Retry-After on 429. See the agora-figma skill.")
	return b.String()
}
