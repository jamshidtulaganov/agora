package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/jamshidtulaganov/agora/server/internal/agoramcp"
	"github.com/jamshidtulaganov/agora/server/internal/qamcp"
)

// `agora mcp qa` — Agora's own QA MCP server over stdio. The daemon injects
// this command into every agent task's mcp_config (internal/daemon/qamcp.go),
// giving agents deterministic test-runner tools: detect_tests / run_tests /
// run_case_script / write_test_file. Every verdict traces to a real process
// exit code — tests run the way they did before LLMs: as scripts.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Agora MCP servers (stdio)",
}

var mcpQACmd = &cobra.Command{
	Use:   "qa",
	Short: "QA test-runner MCP server (stdio) — detect_tests, run_tests, run_case_script, write_test_file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// stdout is the protocol channel — all logging goes to stderr.
		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		return qamcp.New(version, logger).Serve(os.Stdin, os.Stdout)
	},
}

var mcpServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Expose Agora tasks and knowledge to Claude Code and other MCP clients (stdio)",
	Long:  "Serve the configured Agora workspace over MCP stdio. Read-only by default.\nUse --allow-writes to expose task creation, updates, and comments.\nAuthenticate with 'agora login' before connecting; stdout is reserved for MCP.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient(cmd)
		if err != nil {
			return err
		}
		writes, _ := cmd.Flags().GetBool("allow-writes")
		server, err := agoramcp.New(client, version, writes)
		if err != nil {
			return err
		}
		return server.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout())
	},
}

func init() {
	mcpServeCmd.Flags().Bool("allow-writes", false, "Enable create_issue, update_issue, and comment_issue (may trigger automations and notifications)")
	mcpCmd.AddCommand(mcpServeCmd)
	mcpCmd.AddCommand(mcpQACmd)
	rootCmd.AddCommand(mcpCmd)
}
