// Command superleoadapter is an eval-go ExecTarget that drives `superleo-server
// chat --json` (SuperLeo's own agent, answering one prompt through the real
// run_chat_turn pipeline) and reshapes its native JSON into an eval-go RunOutput.
//
//	SUPERLEO_BIN=/path/to/superleo-server EVAL_INPUT="..." superleoadapter
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type superleoResult struct {
	Reply     string     `json:"reply"`
	ToolCalls []toolCall `json:"tool_calls"`
	SessionID string     `json:"session_id"`
	Model     string     `json:"model"`
	MS        int64      `json:"ms"`
}

type toolCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

func main() {
	bin := os.Getenv("SUPERLEO_BIN")
	if bin == "" {
		bin = "superleo-server"
	}
	input := os.Getenv("EVAL_INPUT")

	// Flags that must precede the subcommand — e.g. "--local --db <path>", which
	// a benchmark needs to run against an isolated database with its own MCP
	// servers rather than the user's real ~/.superleo.
	args := append(strings.Fields(os.Getenv("SUPERLEO_ARGS")), "chat", "--json", "--text", input)
	cmd := exec.Command(bin, args...)

	// SUPERLEO_MASTER_KEY and friends live in the project's .env. Reading them
	// here keeps them out of the targets file, which is checked in.
	cmd.Env = append(os.Environ(), envFile(os.Getenv("SUPERLEO_ENV_FILE"))...)

	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 300 {
			tail = tail[len(tail)-300:]
		}
		fmt.Printf(`{"output":"RUN ERROR: %s: %s"}`+"\n", jsonEscape(err.Error()), jsonEscape(tail))
		return
	}

	var r superleoResult
	if uerr := json.Unmarshal(out, &r); uerr != nil {
		fmt.Printf(`{"output":"RUN ERROR: superleo-server produced no parseable JSON: %s"}`+"\n", jsonEscape(uerr.Error()))
		return
	}

	res, _ := json.Marshal(map[string]any{"output": r.Reply, "tool_calls": r.ToolCalls})
	fmt.Println(string(res))
}

// envFile reads KEY=VALUE lines from a dotenv file into exec-ready strings.
// Anything already in the environment still wins, because Env entries later in
// the slice override earlier ones and these are appended after os.Environ()
// — so a target may override one value without editing the file. Missing file:
// no entries, and the callee reports the resulting failure itself.
func envFile(path string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); set {
			continue // an explicit target env wins over the file
		}
		out = append(out, k+"="+v)
	}
	return out
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}
