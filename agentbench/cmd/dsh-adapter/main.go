// Command dsh-adapter is an eval-go ExecTarget for DeepSeek Harness (`dsh`).
//
// dsh answers one task per invocation in its headless profile and prints the
// final assistant message to stdout — no session, no server:
//
//	dsh --profile headless "<task>"
//
// It reports no machine-readable tool trace, so tool_calls comes back empty and
// only task_completion is meaningful for this target. Pointing dsh at
// agentbench's MCP tool server (cmd/benchmcp) would make tool_correctness
// comparable too; until then this measures the answer, not the tool choice.
//
// Model and credentials are configured in the dsh profile itself
// ($DSH_HOME/profiles/headless/cordis.patch.yml), not here.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	bin := os.Getenv("DSH_BIN")
	if bin == "" {
		bin = "dsh"
	}
	profile := os.Getenv("DSH_PROFILE")
	if profile == "" {
		profile = "headless"
	}
	input := os.Getenv("EVAL_INPUT")

	cmd := exec.Command(bin, "--profile", profile, input)
	cmd.Env = os.Environ()
	cmd.Stderr = os.Stderr // dsh logs to stderr; keep stdout clean for the answer

	out, err := cmd.Output()
	if err != nil {
		emit("RUN ERROR: "+err.Error(), nil)
		return
	}
	emit(strings.TrimSpace(string(out)), []any{})
}

func emit(output string, calls []any) {
	if calls == nil {
		calls = []any{}
	}
	b, _ := json.Marshal(map[string]any{"output": output, "tool_calls": calls})
	fmt.Println(string(b))
}
