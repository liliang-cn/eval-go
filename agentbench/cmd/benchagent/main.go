// Command benchagent is an eval-go ExecTarget: drive one OpenAI-compatible model
// through a bounded tool-calling loop and print an eval-go RunOutput JSON.
//
// Works against any OpenAI-compatible /chat/completions endpoint — local Ollama
// (http://localhost:11434/v1) or a cloud gateway — selected by env:
//
//	OPENAI_BASE_URL, OPENAI_API_KEY, OPENAI_MODEL, EVAL_INPUT
//
// This scores a raw model. To score an agent that brings its own runtime, serve
// the same tools over MCP with cmd/benchmcp instead.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"agentbench/tools"
)

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func chat(base, key, model string, msgs []message, defs []map[string]any) (message, error) {
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": msgs, "tools": defs, "temperature": 0,
	})
	req, _ := http.NewRequest("POST", strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := (&http.Client{Timeout: 240 * time.Second}).Do(req)
	if err != nil {
		return message{}, err
	}
	defer resp.Body.Close()

	var out struct {
		Choices []struct {
			Message message `json:"message"`
		} `json:"choices"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return message{}, err
	}
	if len(out.Error) > 0 {
		return message{}, fmt.Errorf("api error: %s", string(out.Error))
	}
	if len(out.Choices) == 0 {
		return message{}, fmt.Errorf("no choices in response")
	}
	return out.Choices[0].Message, nil
}

func main() {
	base := env("OPENAI_BASE_URL", "http://localhost:11434/v1")
	key := env("OPENAI_API_KEY", "ollama")
	model := os.Getenv("OPENAI_MODEL")
	const maxSteps = 8

	world := tools.MustOpen()
	var defs []map[string]any
	for _, t := range world.Tools() {
		defs = append(defs, map[string]any{"type": "function", "function": map[string]any{
			"name": t.Name, "description": t.Description, "parameters": t.Schema,
		}})
	}
	sys := "You are a helpful assistant with access to tools. Call the appropriate tool(s) to accomplish the user's request, then give a brief final answer."
	if world.Profile() != "classic" {
		info := world.Info()
		sys += fmt.Sprintf(" The user's current local time is %v (%v, %v).", info["now"], info["weekday"], info["timezone"])
	}

	userTurns := []string{os.Getenv("EVAL_INPUT")}
	var more []string
	_ = json.Unmarshal([]byte(os.Getenv("EVAL_TURNS")), &more)
	userTurns = append(userTurns, more...)

	type tcOut struct {
		Name   string         `json:"name"`
		Args   map[string]any `json:"args"`
		Output string         `json:"output,omitempty"`
		Turn   int            `json:"turn,omitempty"`
	}
	var calls []tcOut
	var trajectory []string
	var turns []map[string]string
	var final string
	modelCalls := 0
	msgs := []message{{Role: "system", Content: sys}}

	for ti, input := range userTurns {
		msgs = append(msgs, message{Role: "user", Content: input})
		turns = append(turns, map[string]string{"role": "user", "content": input})
		final = ""
		for range maxSteps {
			msg, err := chat(base, key, model, msgs, defs)
			modelCalls++
			if err != nil {
				final = "RUN ERROR: " + err.Error()
				break
			}
			msgs = append(msgs, msg)
			if len(msg.ToolCalls) == 0 {
				final = msg.Content
				break
			}
			for _, c := range msg.ToolCalls {
				args := map[string]any{}
				_ = json.Unmarshal([]byte(c.Function.Arguments), &args)
				aj, _ := json.Marshal(args)
				trajectory = append(trajectory, fmt.Sprintf("called %s(%s)", c.Function.Name, aj))
				result, _ := json.Marshal(world.Call(c.Function.Name, args))
				calls = append(calls, tcOut{Name: c.Function.Name, Args: args, Output: string(result), Turn: ti + 1})
				msgs = append(msgs, message{Role: "tool", ToolCallID: c.ID, Content: string(result)})
			}
		}
		turns = append(turns, map[string]string{"role": "assistant", "content": final})
	}

	res := map[string]any{"output": final, "tool_calls": calls, "trajectory": trajectory,
		"usage": map[string]any{"model_calls": modelCalls, "tool_calls": len(calls), "source": "benchagent"}}
	if len(userTurns) > 1 {
		res["turns"] = turns
	}
	if world.Profile() != "classic" {
		res["state"] = world.State()
	}
	out, _ := json.Marshal(res)
	fmt.Println(string(out))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
