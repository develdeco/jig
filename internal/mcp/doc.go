// Package mcp serves jig's verbs to an agent harness as an MCP server over
// stdio: newline-delimited JSON-RPC 2.0 with initialize, tools/list,
// tools/call and ping, and the server-to-client elicitation request. It backs
// `jig mcp`.
//
// The package holds no code yet; its directory and its place in
// ARCHITECTURE.md are reserved so the code that fills it lands in files of
// its own.
package mcp
