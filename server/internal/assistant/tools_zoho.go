package assistant

import (
	"encoding/json"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
	"github.com/jamshidtulaganov/agora/server/internal/zohoread"
)

// ZohoToolSpecs is the read-only Zoho surface (zohoread — the same tools
// agents get) as model tools. They are not part of ToolSpecs: a run only
// carries them when its person has connected their own Zoho account
// (Service.Integrations), so nobody pays their prompt cost for nothing and
// the model never offers Zoho to someone it can't read Zoho for.
func ZohoToolSpecs() []llm.Tool {
	tools := zohoread.Tools()
	out := make([]llm.Tool, 0, len(tools))
	for _, t := range tools {
		schema := make(map[string]any, len(t.InputSchema)+1)
		for k, v := range t.InputSchema {
			schema[k] = v
		}
		// Closed schemas, like every other assistant tool: the weaker
		// free-tier model must not invent arguments.
		schema["additionalProperties"] = false
		raw, _ := json.Marshal(schema)
		out = append(out, llm.Tool{Name: t.Name, Description: t.Description, Parameters: raw})
	}
	return out
}

// IsZohoTool reports whether name is one of the Zoho read tools.
func IsZohoTool(name string) bool {
	for _, t := range zohoread.Tools() {
		if t.Name == name {
			return true
		}
	}
	return false
}
