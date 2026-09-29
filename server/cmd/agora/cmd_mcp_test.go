package main

import "testing"

func TestWorkspaceMCPCommand(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"mcp", "serve"})
	if err != nil || cmd != mcpServeCmd {
		t.Fatalf("MCP command not registered: %v", err)
	}
	flag := cmd.Flags().Lookup("allow-writes")
	if flag == nil || flag.DefValue != "false" {
		t.Fatal("MCP must default to read-only")
	}
	qa, _, err := rootCmd.Find([]string{"mcp", "qa"})
	if err != nil || qa != mcpQACmd {
		t.Fatal("existing QA MCP command was changed")
	}
}
