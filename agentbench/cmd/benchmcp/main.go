// Command benchmcp serves AgentBench's ten benchmark tools over MCP stdio.
//
// It exists so an agent that brings its own runtime — goose, superai, openclaw,
// Claude Code — can be scored on the same tool set as a raw model. Without it,
// each agent answers with whatever tools it happens to have, and
// `tool_correctness` compares nothing: a benchmark cannot tell "picked the
// wrong tool" from "was offered a different tool".
//
// Point any MCP client at this binary over stdio:
//
//	goose run --with-extension "./bin/benchmcp" --text "..."
//
// The tool behaviour is identical to cmd/benchagent's, because both call
// tools.Run — same currency table, same calculator, same canned facts.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"agentbench/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agentbench-tools", Version: "1.0.0"},
		&mcp.ServerOptions{HasTools: true},
	)

	for _, t := range tools.All() {
		name := t.Name // capture per iteration; the handler outlives this loop
		server.AddTool(
			&mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := map[string]any{}
				if len(req.Params.Arguments) > 0 {
					if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
						return nil, fmt.Errorf("%s: bad arguments: %w", name, err)
					}
				}
				// JSON, not prose: the caller is a model deciding what to do
				// next, and a stable shape is easier to chain than a sentence.
				out, err := json.Marshal(tools.Run(name, args))
				if err != nil {
					return nil, err
				}
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{Text: string(out)}},
				}, nil
			},
		)
	}

	// Logs go to stderr: stdout is the MCP transport and anything else on it
	// corrupts the protocol.
	log.SetOutput(os.Stderr)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("agentbench-tools: %v", err)
	}
}
