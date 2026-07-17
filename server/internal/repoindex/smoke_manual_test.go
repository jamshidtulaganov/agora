package repoindex

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestManualSmoke renders a real pack against a real repo so a human can
// eyeball retrieval quality. Skipped unless REPOINDEX_SMOKE_DIR is set.
func TestManualSmoke(t *testing.T) {
	dir := os.Getenv("REPOINDEX_SMOKE_DIR")
	if dir == "" {
		t.Skip("set REPOINDEX_SMOKE_DIR to run")
	}
	query := os.Getenv("REPOINDEX_SMOKE_QUERY")
	if query == "" {
		query = "QA MCP server injection into agent mcp_config"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()
	pack, stats, err := Pack(ctx, dir, query, DefaultTokenBudget)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	t.Logf("query=%q elapsed=%s", query, time.Since(start))
	t.Logf("stats: %+v", stats)
	t.Logf("\n%s", pack)
}
