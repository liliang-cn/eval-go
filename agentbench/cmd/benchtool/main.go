// Command benchtool exposes the benchmark tool set to a runner that cannot
// speak MCP — one process per call, JSON in, JSON out.
//
//	benchtool list                      # [{name, description, params:{arg:desc}}]
//	benchtool call get_weather '{"city":"Paris"}'
//
// The Python guest adapters (LangGraph / CrewAI / smolagents) bind plain
// callables, and reimplementing ten tools in Python would put a second currency
// table in the repo — the day the two disagree, the frameworks stop being
// compared on the same environment. Shelling out keeps tools/ the only source.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"agentbench/tools"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: benchtool list | benchtool call <name> <json-args>")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "list":
		type spec struct {
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Params      map[string]string `json:"params"`
			Required    []string          `json:"required"`
		}
		var out []spec
		for _, t := range tools.All() {
			s := spec{Name: t.Name, Description: t.Description, Params: map[string]string{}}
			if props, ok := t.Schema["properties"].(map[string]any); ok {
				for arg, raw := range props {
					desc := ""
					if m, ok := raw.(map[string]any); ok {
						if d, ok := m["description"].(string); ok {
							desc = d
						}
						if desc == "" {
							if ty, ok := m["type"].(string); ok {
								desc = ty
							}
						}
					}
					s.Params[arg] = desc
				}
			}
			if req, ok := t.Schema["required"].([]string); ok {
				s.Required = req
			}
			out = append(out, s)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)

	case "call":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "benchtool call <name> <json-args>")
			os.Exit(2)
		}
		name := os.Args[2]
		args := map[string]any{}
		if len(os.Args) > 3 && os.Args[3] != "" {
			if err := json.Unmarshal([]byte(os.Args[3]), &args); err != nil {
				fmt.Fprintf(os.Stderr, "benchtool: bad args JSON: %v\n", err)
				os.Exit(2)
			}
		}
		out, _ := json.Marshal(tools.Run(name, args))
		fmt.Println(string(out))

	default:
		fmt.Fprintln(os.Stderr, "usage: benchtool list | benchtool call <name> <json-args>")
		os.Exit(2)
	}
}
