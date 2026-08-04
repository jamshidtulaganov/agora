package handler

import (
	"strings"

	"github.com/jamshidtulaganov/agora/server/internal/figma"
	db "github.com/jamshidtulaganov/agora/server/pkg/db/generated"
)

// Figma link extraction is the platform primitive design-as-a-build-input
// builds on: an issue that references a Figma design (Bitrix TZ, hand-written
// description) is detected here, and everything downstream — the draft_code
// DESIGN INPUT block (figmaDesignInputContext, below), claim-time MCP
// injection, the optional design_proposal action, decomposition — keys off
// these refs. The pure primitives live in internal/figma (the service layer
// stamps at create time and cannot import this package); this file holds the
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

// figmaDesignInputContext renders the compact "DESIGN INPUT" block appended
// to a draft_code instruction when the issue references a Figma design. This
// is the ICP's core ask (Agora targets small vibe-coding dev teams, usually
// without a dedicated designer): a Figma link pasted on an issue becomes
// context the BUILDING agent reads directly, not a separate design-review
// stage with its own ceremony — design is an INPUT to the build, not a
// co-equal SDLC stage. Empty when the issue references nothing. Reuses
// figmaContextForIssue for the "how to read it" mechanics (fileKey/nodeId
// calls, quota, attachments) and adds the build-specific framing: match the
// implementation to the design, treating it as the visual contract. Kept
// short (one link list + two sentences) so it doesn't blow the slice-action
// instruction's size budget alongside the other draft_code context blocks.
func figmaDesignInputContext(refs []figma.Ref) string {
	howTo := figmaContextForIssue(refs)
	if howTo == "" {
		return ""
	}
	return "\n\nDESIGN INPUT (this issue references a Figma design — it is the visual contract for what you build, " +
		"not a separate review stage): " + howTo + " Match the BUILT UI to the design — layout, spacing, copy, and " +
		"states (empty/loading/error), not just the happy path. Deviate only where the project's existing " +
		"components/tokens (if this brief carries a design-system context) make a different implementation the better fit."
}
