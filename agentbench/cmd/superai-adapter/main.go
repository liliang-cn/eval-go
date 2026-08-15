// Command superaiadapter is an eval-go ExecTarget that drives the `superai` CLI
// (superai-desktop's own agent, answering one prompt) and reshapes its native
// JSON into an eval-go RunOutput.
//
//	SUPERAI_BIN=/path/to/superai EVAL_INPUT="..." superaiadapter
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// mcpPrefix is how agent-go namespaces a tool that came from an MCP server:
// mcp_<server>_<tool>. The benchmark's expected_tools are the bare names the
// server publishes, so the prefix is stripped before scoring — otherwise
// tool_correctness compares "mcp_agentbench_get_weather" against "get_weather"
// and every correct call reads as a wrong one.
const mcpPrefix = "mcp_"

func normalizeTool(name, server string) string {
	full := mcpPrefix + server + "_"
	return strings.TrimPrefix(name, full)
}

type superaiResult struct {
	OK         bool     `json:"ok"`
	Answer     string   `json:"answer"`
	Emotion    string   `json:"emotion,omitempty"`
	Tools      []string `json:"tools"`
	DurationMS int64    `json:"duration_ms"`
	SessionID  string   `json:"session_id"`
	Error      string   `json:"error,omitempty"`
}

type toolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

func main() {
	bin := os.Getenv("SUPERAI_BIN")
	if bin == "" {
		bin = "superai"
	}
	input := os.Getenv("EVAL_INPUT")

	cmd := exec.Command(bin, "--json", input)
	cmd.Env = os.Environ()
	out, _ := cmd.Output() // superai prints its JSON result even on a run error

	var r superaiResult
	if err := json.Unmarshal(out, &r); err != nil {
		fmt.Printf(`{"output":"RUN ERROR: superai produced no parseable JSON: %s"}`+"\n", jsonEscape(err.Error()))
		return
	}

	output := r.Answer
	if !r.OK {
		output = "RUN ERROR: " + r.Error
	}
	server := os.Getenv("AGENTBENCH_MCP_NAME")
	if server == "" {
		server = "agentbench"
	}
	calls := make([]toolCall, len(r.Tools))
	for i, t := range r.Tools {
		calls[i] = toolCall{Name: normalizeTool(t, server)}
	}
	res, _ := json.Marshal(map[string]any{"output": output, "tool_calls": calls})
	fmt.Println(string(res))
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
