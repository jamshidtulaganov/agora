import { describe, it, expect } from "vitest";
import {
  buildServerEntry,
  listMcpServers,
  readMcpServers,
  upsertMcpServer,
  removeMcpServer,
  isRemoteMcpServer,
  type McpServerEntry,
} from "./types";

describe("buildServerEntry — stdio (back-compat)", () => {
  it("builds a command entry with args + env, omitting empties", () => {
    const entry = buildServerEntry({
      command: "  npx  ",
      argsText: "-y figma-developer-mcp  --stdio",
      env: [
        { key: "FIGMA_API_KEY", value: "tok" },
        { key: "  ", value: "ignored" }, // blank key dropped
      ],
    });
    expect(entry).toEqual({
      command: "npx",
      args: ["-y", "figma-developer-mcp", "--stdio"],
      env: { FIGMA_API_KEY: "tok" },
    });
    // No remote fields leak into a stdio entry.
    expect(entry.type).toBeUndefined();
    expect(entry.url).toBeUndefined();
  });

  it("omits args/env entirely when empty", () => {
    const entry = buildServerEntry({ command: "mcp-server-mysql", argsText: "   ", env: [] });
    expect(entry).toEqual({ command: "mcp-server-mysql" });
  });

  it("treats an explicit stdio transport identically to the default", () => {
    const entry = buildServerEntry({
      transport: "stdio",
      command: "foo",
      argsText: "",
      env: [],
    });
    expect(entry).toEqual({ command: "foo" });
  });
});

describe("buildServerEntry — remote (http/sse)", () => {
  it("builds an http entry with type + url + keyed headers", () => {
    const entry = buildServerEntry({
      transport: "http",
      url: "  https://mcp.example.com/mcp  ",
      headers: [
        { key: "Authorization", value: "Bearer xyz" },
        { key: "  ", value: "dropped" }, // blank key dropped
      ],
    });
    expect(entry).toEqual({
      type: "http",
      url: "https://mcp.example.com/mcp",
      headers: { Authorization: "Bearer xyz" },
    });
    expect(entry.command).toBeUndefined();
  });

  it("keeps a blank-valued header key (sealed-auth placeholder)", () => {
    const entry = buildServerEntry({
      transport: "http",
      url: "https://mcp.example.com/mcp",
      headers: [{ key: "Authorization", value: "" }],
    });
    expect(entry).toEqual({
      type: "http",
      url: "https://mcp.example.com/mcp",
      headers: { Authorization: "" },
    });
  });

  it("omits headers entirely when no keyed row exists", () => {
    const entry = buildServerEntry({
      transport: "sse",
      url: "https://mcp.example.com/sse",
      headers: [{ key: "", value: "" }],
    });
    expect(entry).toEqual({ type: "sse", url: "https://mcp.example.com/sse" });
  });
});

describe("isRemoteMcpServer", () => {
  it("classifies by type", () => {
    expect(isRemoteMcpServer({ type: "http", url: "u" })).toBe(true);
    expect(isRemoteMcpServer({ type: "sse", url: "u" })).toBe(true);
    expect(isRemoteMcpServer({ command: "npx" })).toBe(false);
    expect(isRemoteMcpServer({ type: "stdio", command: "npx" })).toBe(false);
    expect(isRemoteMcpServer({})).toBe(false);
  });
});

describe("upsert/list/read/remove with mixed transports", () => {
  it("round-trips a remote entry alongside a stdio entry", () => {
    const stdio: McpServerEntry = { command: "npx", args: ["-y", "x"] };
    const remote: McpServerEntry = {
      type: "http",
      url: "https://mcp.example.com/mcp",
      headers: { Authorization: "" },
    };
    let cfg = upsertMcpServer(null, "local", stdio);
    cfg = upsertMcpServer(cfg, "linear", remote);

    const servers = readMcpServers(cfg);
    expect(servers.local).toEqual(stdio);
    expect(servers.linear).toEqual(remote);

    const list = listMcpServers(cfg);
    // Sorted by name: linear, local.
    expect(list.map((s) => s.name)).toEqual(["linear", "local"]);
    const linear = list.find((s) => s.name === "linear")!;
    expect(linear.command).toBeUndefined();
    expect(linear.url).toBe("https://mcp.example.com/mcp");
    expect(linear.type).toBe("http");

    // Removing the remote entry preserves the stdio one.
    const pruned = removeMcpServer(cfg, "linear");
    expect(Object.keys(readMcpServers(pruned))).toEqual(["local"]);
  });

  it("reads a legacy stdio-only config unchanged (back-compat)", () => {
    const legacy = { mcpServers: { gh: { command: "npx", args: ["-y", "server-github"] } } };
    const list = listMcpServers(legacy);
    expect(list).toEqual([{ name: "gh", command: "npx", args: ["-y", "server-github"] }]);
    expect(isRemoteMcpServer(list[0]!)).toBe(false);
  });
});
