# Agora workspace MCP — source map

The inbound workspace server in `mcp-servers.mdx` is separate from outbound runtime injection in `mcp-injection-source-map.md`.

| Contract | Source |
| --- | --- |
| `agora mcp serve`, explicit write opt-in; QA preserved | `server/cmd/agora/cmd_mcp.go`: `mcpServeCmd`, `mcpQACmd`, `init` |
| CLI flag/env/profile authentication and actor attribution | `server/cmd/agora/cmd_agent.go`: `newAPIClient`, `resolveWorkspaceID`, `resolveServerURL`; `cmd_auth.go`: `resolveToken` |
| HTTP(S), workspace UUID, authentication validation | `server/internal/agoramcp/server.go`: `New` |
| MCP stdio lifecycle, version negotiation, 45s timeout, 8-call concurrency, cancellation | `server/internal/agoramcp/server.go`: `Serve` |
| Default 14 reads, opt-in 3 writes, no deletes or arbitrary requests | `server/internal/agoramcp/tools.go`: `tools`, `Server.call` |
| Input validation and fixed workspace routing | `server/internal/agoramcp/tools.go`: `validateArguments`, `Server.call` |
| Agent secret minimization and malformed-response handling | `server/internal/agoramcp/tools.go`: `agentSummary`, `Server.call` |
| Server permission enforcement | `server/cmd/server/router.go`: workspace membership middleware; domain handlers |
| Protocol, write denial, routing, auth headers, malformed arguments/responses | `server/internal/agoramcp/server_test.go`; `server/cmd/agora/cmd_mcp_test.go` |

No client configuration is installed automatically and no token is created by `mcp serve`. Write annotations are hints; the server's tool allowlist is the read-only enforcement boundary.
