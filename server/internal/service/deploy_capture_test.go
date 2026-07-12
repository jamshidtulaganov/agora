package service

import "testing"

// parseDeployResultBlock mirrors parseQAResultBlock's contract: extract the
// fenced JSON, tolerate prose around it, and fail CLOSED (ok=false, never a
// panic) on a missing block, malformed JSON, or an unknown status.
func TestParseDeployResultBlock(t *testing.T) {
	t.Run("valid block amid prose", func(t *testing.T) {
		content := "## Deploy report\n\nPipeline green in 3m04s.\n\n" +
			"```deploy-result\n" +
			`{"environment":"staging","ref":"staging","status":"success","summary":"pipeline green","pipeline_url":"https://gitlab.sdteam.uz/g/p/-/pipelines/123","duration_s":184}` +
			"\n```\n"
		p, ok := parseDeployResultBlock(content)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if p.Environment != "staging" || p.Ref != "staging" || p.Status != "success" {
			t.Errorf("unexpected payload: %+v", p)
		}
		if p.Summary != "pipeline green" || p.PipelineURL == "" {
			t.Errorf("summary/pipeline_url not lifted: %+v", p)
		}
	})

	t.Run("failed and timeout statuses parse", func(t *testing.T) {
		for _, status := range []string{"failed", "timeout"} {
			content := "```deploy-result\n" + `{"environment":"production","ref":"main","status":"` + status + `"}` + "\n```"
			p, ok := parseDeployResultBlock(content)
			if !ok || p.Status != status {
				t.Errorf("status %q: ok=%v payload=%+v", status, ok, p)
			}
		}
	})

	t.Run("no block", func(t *testing.T) {
		if _, ok := parseDeployResultBlock("just a normal comment"); ok {
			t.Error("expected ok=false without a deploy-result block")
		}
	})

	t.Run("malformed json is skipped, not fatal", func(t *testing.T) {
		if _, ok := parseDeployResultBlock("```deploy-result\n{not valid json}\n```"); ok {
			t.Error("expected ok=false on malformed JSON")
		}
	})

	t.Run("unknown status is skipped", func(t *testing.T) {
		if _, ok := parseDeployResultBlock("```deploy-result\n" + `{"environment":"staging","status":"pending"}` + "\n```"); ok {
			t.Error("expected ok=false on a status outside success/failed/timeout")
		}
	})

	t.Run("target tolerated as an environment alias", func(t *testing.T) {
		p, ok := parseDeployResultBlock("```deploy-result\n" + `{"target":"staging","ref":"main","status":"success"}` + "\n```")
		if !ok || p.Target != "staging" {
			t.Errorf("expected the target alias to parse, got ok=%v %+v", ok, p)
		}
	})

	t.Run("a qa-result block is not a deploy result", func(t *testing.T) {
		if _, ok := parseDeployResultBlock("```qa-result\n" + `{"verdict":"pass","summary":"x"}` + "\n```"); ok {
			t.Error("qa-result must not parse as deploy-result")
		}
	})
}
