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

func chat(base, key, model string, msgs []message) (message, error) {
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": msgs, "tools": tools.OpenAIDefinitions(), "temperature": 0,
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
	input := os.Getenv("EVAL_INPUT")
	const maxSteps = 5

	msgs := []message{
		{Role: "system", Content: "You are a helpful assistant with access to tools. Call the appropriate tool(s) to accomplish the user's request, then give a brief final answer."},
		{Role: "user", Content: input},
	}

	type tcOut struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	var calls []tcOut
	var trajectory []string
	var final string

	for range maxSteps {
		msg, err := chat(base, key, model, msgs)
		if err != nil {
			final = "RUN ERROR: " + err.Error()
			break
		}
		if len(msg.ToolCalls) == 0 {
			final = msg.Content
			break
		}
		msgs = append(msgs, msg)
		for _, c := range msg.ToolCalls {
			args := map[string]any{}
			_ = json.Unmarshal([]byte(c.Function.Arguments), &args)
			calls = append(calls, tcOut{Name: c.Function.Name, Args: args})
			aj, _ := json.Marshal(args)
			trajectory = append(trajectory, fmt.Sprintf("called %s(%s)", c.Function.Name, aj))
			result, _ := json.Marshal(tools.Run(c.Function.Name, args))
			msgs = append(msgs, message{Role: "tool", ToolCallID: c.ID, Content: string(result)})
		}
	}

	out, _ := json.Marshal(map[string]any{"output": final, "tool_calls": calls, "trajectory": trajectory})
	fmt.Println(string(out))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
