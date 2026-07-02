package service

import (
	"testing"
)

func TestParseDesignProposalBlock(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		wantState DesignProposalState
		check     func(t *testing.T, p DesignProposal)
	}{
		{
			name:      "no block",
			content:   "just a plain comment with no fenced block",
			wantState: DesignProposalStateNone,
		},
		{
			name: "valid ok proposal",
			content: "Here is my analysis.\n\n```design-proposal\n" +
				`{"status":"ok","screens":[{"name":"List","figma_node_id":"208:5147","summary":"table","render":"figma-208-5147.png"}],` +
				`"components":[{"name":"DataTable","verdict":"reuse","code_ref":"src/DataTable.vue"}],` +
				`"sub_issues":[{"title":"List view","description":"...","depends_on":[]}]}` +
				"\n```",
			wantState: DesignProposalStateOK,
			check: func(t *testing.T, p DesignProposal) {
				if len(p.Screens) != 1 || p.Screens[0].Render != "figma-208-5147.png" {
					t.Errorf("screens not parsed: %+v", p.Screens)
				}
				if len(p.Components) != 1 || p.Components[0].Verdict != "reuse" {
					t.Errorf("components not parsed: %+v", p.Components)
				}
				if len(p.SubIssues) != 1 {
					t.Errorf("sub_issues not parsed: %+v", p.SubIssues)
				}
			},
		},
		{
			name:      "status omitted defaults to ok",
			content:   "```design-proposal\n{\"screens\":[]}\n```",
			wantState: DesignProposalStateOK,
			check: func(t *testing.T, p DesignProposal) {
				if p.Status != "ok" {
					t.Errorf("empty status must normalize to ok, got %q", p.Status)
				}
			},
		},
		{
			name:      "blocked proposal",
			content:   "```design-proposal\n{\"status\":\"blocked\",\"reason\":\"figma_forbidden\",\"reason_detail\":\"private file\"}\n```",
			wantState: DesignProposalStateBlocked,
			check: func(t *testing.T, p DesignProposal) {
				if p.Reason != "figma_forbidden" {
					t.Errorf("reason = %q, want figma_forbidden", p.Reason)
				}
			},
		},
		{
			name:      "unknown status fails closed to blocked",
			content:   "```design-proposal\n{\"status\":\"weird\"}\n```",
			wantState: DesignProposalStateBlocked,
		},
		{
			name:      "malformed json is invalid, not dropped",
			content:   "```design-proposal\n{not valid json\n```",
			wantState: DesignProposalStateInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, p, state := ParseDesignProposalBlock(tt.content)
			if state != tt.wantState {
				t.Fatalf("state = %q, want %q", state, tt.wantState)
			}
			if tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

// TestParseDesignProposalBlock_NewestBlockShape guards the schema field names the
// frontend zod schema mirrors — a rename here without updating the TS twin would
// silently drop data.
func TestParseDesignProposalBlock_DeviationsAndQuestions(t *testing.T) {
	content := "```design-proposal\n" +
		`{"status":"ok","deviations":[{"aspect":"color","figma_value":"#123","project_value":"#2563EB","question":"which?"}],` +
		`"open_questions":["q1","q2"]}` + "\n```"
	_, p, state := ParseDesignProposalBlock(content)
	if state != DesignProposalStateOK {
		t.Fatalf("state = %q", state)
	}
	if len(p.Deviations) != 1 || p.Deviations[0].Aspect != "color" {
		t.Errorf("deviations not parsed: %+v", p.Deviations)
	}
	if len(p.OpenQuestions) != 2 {
		t.Errorf("open_questions not parsed: %+v", p.OpenQuestions)
	}
}
