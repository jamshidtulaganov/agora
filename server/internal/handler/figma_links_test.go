package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/figma"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func issueWith(description, figmaLinksStamp string) db.Issue {
	issue := db.Issue{Metadata: []byte(`{}`)}
	if description != "" {
		issue.Description = pgtype.Text{String: description, Valid: true}
	}
	if figmaLinksStamp != "" {
		meta, _ := json.Marshal(map[string]string{"figma_links": figmaLinksStamp})
		issue.Metadata = meta
	}
	return issue
}

func TestIssueFigmaRefs_UnionOfStampAndDescription(t *testing.T) {
	// Stamp carries node 1:1; the description was edited later and now also
	// references node 2:2. The union must surface BOTH — a stale stamp must
	// never hide a newer link.
	stamp := figma.LinksMetadataValue([]figma.Ref{
		{URL: "https://www.figma.com/design/AbCdEf123456/X?node-id=1-1", FileKey: "AbCdEf123456", NodeID: "1:1"},
	})
	issue := issueWith(
		"see https://www.figma.com/design/AbCdEf123456/X?node-id=1-1 and https://www.figma.com/design/AbCdEf123456/X?node-id=2-2",
		stamp,
	)
	refs := issueFigmaRefs(issue)
	if len(refs) != 2 {
		t.Fatalf("got %d refs (%+v), want 2 (stamp ∪ description)", len(refs), refs)
	}
}

func TestIssueFigmaRefs_NoRefs(t *testing.T) {
	if refs := issueFigmaRefs(issueWith("plain text", "")); refs != nil {
		t.Errorf("expected nil, got %+v", refs)
	}
	if refs := issueFigmaRefs(db.Issue{}); refs != nil {
		t.Errorf("nil description/metadata must yield nil, got %+v", refs)
	}
}

func TestIssueFigmaRefs_MalformedStampFallsBackToDescription(t *testing.T) {
	issue := issueWith("https://www.figma.com/design/AbCdEf123456/X?node-id=3-3", "not json")
	refs := issueFigmaRefs(issue)
	if len(refs) != 1 || refs[0].NodeID != "3:3" {
		t.Fatalf("got %+v, want the live-extracted ref", refs)
	}
}

func TestFigmaContextForIssue(t *testing.T) {
	if figmaContextForIssue(nil) != "" {
		t.Error("no refs → empty note")
	}
	refs := []figma.Ref{
		{URL: "https://www.figma.com/design/cF4PFq3P5NOyZvp01JSHnE/SD?node-id=208-5147", FileKey: "cF4PFq3P5NOyZvp01JSHnE", NodeID: "208:5147"},
	}
	note := figmaContextForIssue(refs)
	for _, want := range []string{
		`get_figma_data(fileKey="cF4PFq3P5NOyZvp01JSHnE", nodeId="208:5147")`,
		"never fetch a whole file",
		"download_figma_images",
		"comment attachments",
		"Retry-After",
		"agora-figma",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("claim-time note missing %q:\n%s", want, note)
		}
	}
}

func TestFigmaDesignInputContext(t *testing.T) {
	if figmaDesignInputContext(nil) != "" {
		t.Error("no refs → empty block (draft_code must not append anything for a design-less issue)")
	}
	refs := []figma.Ref{
		{URL: "https://www.figma.com/design/cF4PFq3P5NOyZvp01JSHnE/SD?node-id=208-5147", FileKey: "cF4PFq3P5NOyZvp01JSHnE", NodeID: "208:5147"},
	}
	note := figmaDesignInputContext(refs)
	for _, want := range []string{
		"DESIGN INPUT",
		"visual contract",
		"not a separate review stage",
		`get_figma_data(fileKey="cF4PFq3P5NOyZvp01JSHnE", nodeId="208:5147")`,
		"download_figma_images",
		"Match the BUILT UI to the design",
		"empty/loading/error",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("draft_code design-input block missing %q:\n%s", want, note)
		}
	}
}

func TestProbeFigmaToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/me" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("X-Figma-Token") == "good" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	orig := figmaAPIBase
	figmaAPIBase = srv.URL
	defer func() { figmaAPIBase = orig }()

	if status, reachable := probeFigmaToken(t.Context(), "good"); status != http.StatusOK || !reachable {
		t.Errorf("good token: status=%d reachable=%v, want 200/true", status, reachable)
	}
	if status, reachable := probeFigmaToken(t.Context(), "bad"); status != http.StatusForbidden || !reachable {
		t.Errorf("bad token: status=%d reachable=%v, want 403/true", status, reachable)
	}

	figmaAPIBase = "http://127.0.0.1:1" // nothing listens here
	if _, reachable := probeFigmaToken(t.Context(), "any"); reachable {
		t.Error("connection failure must report unreachable, not invalid")
	}
}

func TestProbeFigmaSeat(t *testing.T) {
	monthly := false
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/files/") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if monthly {
			w.Header().Set("X-Figma-Rate-Limit-Type", "monthly")
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	orig := figmaAPIBase
	figmaAPIBase = srv.URL
	defer func() { figmaAPIBase = orig }()

	if got := probeFigmaSeat(t.Context(), "tok", "AbCdEf123456"); got != "ok" {
		t.Errorf("200 → %q, want ok", got)
	}
	status = http.StatusTooManyRequests
	monthly = true
	if got := probeFigmaSeat(t.Context(), "tok", "AbCdEf123456"); got != "low_seat" {
		t.Errorf("monthly 429 → %q, want low_seat", got)
	}
	monthly = false
	if got := probeFigmaSeat(t.Context(), "tok", "AbCdEf123456"); got != "unknown" {
		t.Errorf("ambiguous 429 → %q, want unknown", got)
	}
}

func TestProbeFileKeyValidation(t *testing.T) {
	// The probe file key is interpolated into a server-side URL with the
	// workspace token attached — path traversal must be rejected up front.
	for _, bad := range []string{"x/../../v1/teams/1", "abc?x=1", "short", "AbCdEf 1234567"} {
		if probeFileKeyRe.MatchString(bad) {
			t.Errorf("probeFileKeyRe must reject %q", bad)
		}
	}
	for _, good := range []string{"cF4PFq3P5NOyZvp01JSHnE", "AbCdEf123456"} {
		if !probeFileKeyRe.MatchString(good) {
			t.Errorf("probeFileKeyRe must accept %q", good)
		}
	}
}
