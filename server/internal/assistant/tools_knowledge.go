package assistant

import (
	"encoding/json"

	"github.com/jamshidtulaganov/agora/server/internal/integrations/llm"
)

// Workspace knowledge base tools (docs/workspace-knowledge-plan.md §7). Like
// the Zoho tools they are not in the static catalog: a run carries them only
// when one of the person's workspaces has readable documents
// (Service.RunExtras). All three are reads.
const (
	ToolSearchKnowledge = "search_knowledge"
	ToolReadKnowledge   = "read_knowledge"
	ToolListKnowledge   = "list_knowledge"
)

// KnowledgeToolSpecs are the knowledge tools as model tools.
func KnowledgeToolSpecs() []llm.Tool {
	return []llm.Tool{
		{
			Name: ToolSearchKnowledge,
			Description: "Search the documents a workspace's team uploaded (SOPs, policies, price lists) for sections that answer a question. " +
				"Returns ranked sections, each with a cite value to quote inline as [kb:xxxxxxxx]. Search with the key words of the " +
				"question; if nothing fits, try synonyms or the documents' language before concluding they don't cover it. " +
				"Omit workspace_id to search the workspace the user is in.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "What to look for, in a few key words", "maxLength": 300},
    "workspace_id": {"type": "string", "description": "Workspace UUID from list_workspaces; omit for the current workspace"},
    "limit": {"type": "integer", "minimum": 1, "maximum": 8}
  },
  "required": ["query"],
  "additionalProperties": false
}`),
		},
		{
			Name: ToolReadKnowledge,
			Description: "Read consecutive sections of one knowledge document — use after search_knowledge when an answer needs the " +
				"surrounding steps or the rest of a table. Each section carries a cite value.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "doc_id": {"type": "string", "description": "doc_id from search_knowledge or list_knowledge"},
    "workspace_id": {"type": "string", "description": "Workspace UUID; omit for the current workspace"},
    "from_section": {"type": "integer", "minimum": 0, "description": "Section number to start at (from search results)"},
    "count": {"type": "integer", "minimum": 1, "maximum": 8}
  },
  "required": ["doc_id"],
  "additionalProperties": false
}`),
		},
		{
			Name:        ToolListKnowledge,
			Description: "List a workspace's knowledge documents (title, sections, whether always included). Omit workspace_id for the current workspace.",
			Parameters: json.RawMessage(`{
  "type": "object",
  "properties": {
    "workspace_id": {"type": "string", "description": "Workspace UUID; omit for the current workspace"}
  },
  "additionalProperties": false
}`),
		},
	}
}

// IsKnowledgeTool reports whether name is one of the knowledge tools.
func IsKnowledgeTool(name string) bool {
	switch name {
	case ToolSearchKnowledge, ToolReadKnowledge, ToolListKnowledge:
		return true
	}
	return false
}
