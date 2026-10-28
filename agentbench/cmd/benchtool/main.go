// Command benchtool exposes the benchmark tool set to a runner that cannot
// speak MCP — one process per call, JSON in, JSON out.
//
//	benchtool list                      # [{name, description, params:{arg:desc}}]
//	benchtool schema                    # [{name, description, parameters:<JSON Schema>}]
//	benchtool call get_weather '{"city":"Paris"}'
//	benchtool state                     # the tool world's current state (JSON)
//	benchtool info                      # {profile, now, weekday, timezone, user}
//	benchtool validate tasks.json       # every task's tool_script opens, every tool its check names exists
//
// Which tools exist and how they behave comes from the environment (see
// tools.Env): EVAL_TOOL_SCRIPT (the task's tool_script), BENCH_PROFILE, and
// BENCH_STATE (the state file every call of one run shares).
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
	"strings"

	"agentbench/tools"
)

const usage = "usage: benchtool list | schema | call <name> <json-args> | state | info | validate <tasks.json>"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if os.Args[1] == "validate" {
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(2)
		}
		if err := validate(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "benchtool validate:", err)
			os.Exit(1)
		}
		return
	}
	env := tools.MustOpen()

	switch os.Args[1] {
	case "list":
		type spec struct {
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Params      map[string]string `json:"params"`
			Required    []string          `json:"required"`
		}
		var out []spec
		for _, t := range env.Tools() {
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

	case "schema":
		// The exact JSON Schema every front end serves, for a runner that
		// registers tools itself rather than mounting benchmcp. `list` drops the
		// argument types, and a number typed as a string is a different tool.
		type spec struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			Parameters  map[string]any `json:"parameters"`
		}
		var out []spec
		for _, t := range env.Tools() {
			out = append(out, spec{t.Name, t.Description, t.Schema})
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)

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
		out, _ := json.Marshal(env.Call(name, args))
		fmt.Println(string(out))

	case "state":
		_ = json.NewEncoder(os.Stdout).Encode(env.State())

	case "info":
		_ = json.NewEncoder(os.Stdout).Encode(env.Info())

	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

// validate catches the task-file mistakes that would otherwise only show up as
// a silently wrong score: a tool_script that does not parse, a scripted
// response for a tool the profile lacks, and — worst — a check naming a tool
// that does not exist, which a forbidden list would "pass" forever.
func validate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var tasks []struct {
		Name       string          `json:"name"`
		ToolScript json.RawMessage `json:"tool_script"`
		Check      *struct {
			Calls []struct {
				Tool  string   `json:"tool"`
				AnyOf []string `json:"any_of"`
			} `json:"calls"`
			Forbidden []string `json:"forbidden"`
		} `json:"check"`
	}
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}
	var problems []string
	for _, t := range tasks {
		os.Setenv("EVAL_TOOL_SCRIPT", string(t.ToolScript))
		env, err := tools.Open()
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", t.Name, err))
			continue
		}
		have := map[string]bool{}
		for _, tl := range env.Tools() {
			have[tl.Name] = true
		}
		if t.Check == nil {
			continue
		}
		var names []string
		for _, c := range t.Check.Calls {
			if c.Tool != "" {
				names = append(names, c.Tool)
			}
			names = append(names, c.AnyOf...)
		}
		for _, f := range t.Check.Forbidden {
			if f != "*" {
				names = append(names, f)
			}
		}
		for _, n := range names {
			if !have[n] {
				problems = append(problems, fmt.Sprintf("%s: check names tool %q, which profile %s does not have", t.Name, n, env.Profile()))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%d problem(s):\n  %s", len(problems), strings.Join(problems, "\n  "))
	}
	fmt.Printf("%s: %d tasks ok\n", path, len(tasks))
	return nil
}
