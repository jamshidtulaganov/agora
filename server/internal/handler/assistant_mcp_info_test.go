package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// The MCP server introduces itself with Agora's name and logo, so Claude and
// ChatGPT can show them. initialize touches no database.
func TestAssistantMCPServerInfo(t *testing.T) {
	t.Setenv("AGORA_APP_URL", "https://app.example")
	h := (&Handler{}).AssistantMCP("test")
	req := httptest.NewRequest("POST", "/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`))
	req.Header.Set("X-User-ID", "00000000-0000-4000-8000-000000000001")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Result struct {
			ServerInfo struct {
				Title      string `json:"title"`
				WebsiteURL string `json:"websiteUrl"`
				Icons      []struct {
					Src string `json:"src"`
				} `json:"icons"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("initialize: %d %s", rec.Code, rec.Body)
	}
	info := out.Result.ServerInfo
	if info.Title != "Agora" || info.WebsiteURL != "https://app.example" || len(info.Icons) == 0 || info.Icons[0].Src != "https://app.example/icon-192.png" {
		t.Fatalf("server info: %+v", info)
	}
}
