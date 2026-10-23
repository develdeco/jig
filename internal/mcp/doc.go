// Package mcp serves jig verbs to an agent harness as a Model Context Protocol
// server over stdio. It speaks newline-delimited JSON-RPC 2.0 by hand
// (initialize, tools/list, tools/call, ping, and the notification that cancels
// a request) and makes one request of the client, elicitation/create, to put a
// question to the person. It holds no jig logic: each tool is a verb with the
// arguments a model may give it, run through a Runner the program supplies,
// and what the verb prints is the tool result. It backs `jig mcp`.
package mcp
