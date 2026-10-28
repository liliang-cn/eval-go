// Command agentgo-lib is an eval-go ExecTarget for the agent-go library itself:
// no superai, no app prompt, no app tools. One agent built with library
// defaults, the benchmark's tools, and the plain system prompt every guest
// framework gets.
//
//	EVAL_INPUT="..." OPENAI_BASE_URL=... OPENAI_API_KEY=... OPENAI_MODEL=... BENCHTOOL=./bin/benchtool agentgo-lib
//
// Tools come from benchtool (schema and execution) so this runner and the
// harness-rs one are offered byte-identical tools, and the task's
// EVAL_TOOL_SCRIPT (profile, seed state, scripted failures) reaches every call
// through the environment. One state file per run (BENCH_STATE) is shared by
// every benchtool call, so turn two can edit what turn one created.
//
// Multi-turn: EVAL_TURNS is a JSON array of follow-up user messages. Each one
// is sent with agent-go's session API (WithSessionID) in the same session, and
// the output reports every turn.
//
// Usage: every model request goes through a metering wrapper around the LLM
// client, so the count includes auxiliary calls (synthesis, compaction,
// extraction) and not just the tool loop's rounds. BENCH_USAGE_LOG, when set,
// also gets one JSON line per run.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/pool"
)

const systemPrompt = "You are a helpful life assistant. Use the available tools to actually complete the user's " +
	"task. If you cannot do something because no tool for it exists, say so plainly and explain " +
	"why, instead of pretending you did it."

type toolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	Name   string         `json:"name"`
	Args   map[string]any `json:"args,omitempty"`
	Output string         `json:"output,omitempty"`
	Turn   int            `json:"turn,omitempty"`
}

type turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// modelCall mirrors evalgo.ModelCall (the adapter is its own module and does
// not import eval-go).
type modelCall struct {
	Kind             string `json:"kind"`
	Model            string `json:"model,omitempty"`
	Ms               int64  `json:"ms"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	CachedTokens     int    `json:"cached_tokens,omitempty"`
	ToolCalls        int    `json:"tool_calls,omitempty"`
	Error            string `json:"error,omitempty"`
}

// meteredLLM counts and times every request agent-go makes. It embeds the pool
// client so every other method (and every optional interface agent-go probes
// for) still reaches the real client.
type meteredLLM struct {
	*pool.Client
	model string
	mu    sync.Mutex
	calls []modelCall
}

func (m *meteredLLM) record(kind string, start time.Time, usage *domain.TokenUsage, toolCalls int, err error) {
	c := modelCall{Kind: kind, Model: m.model, Ms: time.Since(start).Milliseconds(), ToolCalls: toolCalls}
	if usage != nil {
		c.PromptTokens, c.CompletionTokens, c.CachedTokens = usage.PromptTokens, usage.CompletionTokens, usage.CachedPromptTokens
	}
	if err != nil {
		c.Error = err.Error()
	}
	m.mu.Lock()
	m.calls = append(m.calls, c)
	m.mu.Unlock()
}

func loopKind(tools []domain.ToolDefinition) string {
	if len(tools) > 0 {
		return "loop"
	}
	return "aux:no-tools"
}

func (m *meteredLLM) GenerateWithTools(ctx context.Context, msgs []domain.Message, tools []domain.ToolDefinition, opts *domain.GenerationOptions) (*domain.GenerationResult, error) {
	start := time.Now()
	r, err := m.Client.GenerateWithTools(ctx, msgs, tools, opts)
	var u *domain.TokenUsage
	n := 0
	if r != nil {
		u, n = r.Usage, len(r.ToolCalls)
	}
	m.record(loopKind(tools), start, u, n, err)
	return r, err
}

func (m *meteredLLM) StreamWithTools(ctx context.Context, msgs []domain.Message, tools []domain.ToolDefinition, opts *domain.GenerationOptions, cb domain.ToolCallCallback) error {
	start := time.Now()
	var u *domain.TokenUsage
	n := 0
	err := m.Client.StreamWithTools(ctx, msgs, tools, opts, func(d *domain.GenerationResult) error {
		if d != nil {
			if d.Usage != nil {
				u = d.Usage
			}
			n = max(n, len(d.ToolCalls))
		}
		return cb(d)
	})
	m.record(loopKind(tools), start, u, n, err)
	return err
}

func (m *meteredLLM) Generate(ctx context.Context, prompt string, opts *domain.GenerationOptions) (string, error) {
	start := time.Now()
	s, err := m.Client.Generate(ctx, prompt, opts)
	m.record("aux:generate", start, nil, 0, err)
	return s, err
}

func (m *meteredLLM) Stream(ctx context.Context, prompt string, opts *domain.GenerationOptions, cb func(string)) error {
	start := time.Now()
	err := m.Client.Stream(ctx, prompt, opts, cb)
	m.record("aux:stream", start, nil, 0, err)
	return err
}

func (m *meteredLLM) GenerateStructured(ctx context.Context, prompt string, schema interface{}, opts *domain.GenerationOptions) (*domain.StructuredResult, error) {
	start := time.Now()
	r, err := m.Client.GenerateStructured(ctx, prompt, schema, opts)
	m.record("aux:structured", start, nil, 0, err)
	return r, err
}

func (m *meteredLLM) RecognizeIntent(ctx context.Context, request string) (*domain.IntentResult, error) {
	start := time.Now()
	r, err := m.Client.RecognizeIntent(ctx, request)
	m.record("aux:intent", start, nil, 0, err)
	return r, err
}

func main() {
	out, usage := run()
	b, _ := json.Marshal(out)
	fmt.Println(string(b))
	if path := os.Getenv("BENCH_USAGE_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			line, _ := json.Marshal(usage)
			f.Write(append(line, '\n'))
			f.Close()
		}
	}
}

func run() (map[string]any, map[string]any) {
	start := time.Now()
	logLine := map[string]any{"framework": "agent-go", "task": os.Getenv("EVAL_NAME"), "model": os.Getenv("OPENAI_MODEL")}
	fail := func(err error) (map[string]any, map[string]any) {
		logLine["error"] = err.Error()
		return map[string]any{"output": "RUN ERROR: " + err.Error()}, logLine
	}

	// A fresh home and tool state per task: memory, history and reminders from
	// one task must not reach the next, or the run is no longer one task.
	home, _ := os.MkdirTemp("", "agentgo-lib-")
	defer os.RemoveAll(home)
	os.Setenv("AGENTGO_HOME", home)
	os.Setenv("BENCH_STATE", filepath.Join(home, "tool-state.json"))

	benchtool := os.Getenv("BENCHTOOL")
	raw, err := exec.Command(benchtool, "schema").Output()
	if err != nil {
		return fail(fmt.Errorf("benchtool schema: %w", benchtoolErr(err)))
	}
	var specs []toolSpec
	if err := json.Unmarshal(raw, &specs); err != nil {
		return fail(err)
	}
	var info map[string]any
	if raw, err := exec.Command(benchtool, "info").Output(); err == nil {
		_ = json.Unmarshal(raw, &info)
	}
	lifeWorld := info["profile"] != nil && info["profile"] != "classic"
	prompt := systemPrompt
	if lifeWorld {
		// Who the assistant is talking to, as a real assistant app says it.
		if u, ok := info["user"].(map[string]any); ok {
			prompt += fmt.Sprintf("\n\nThe user is %v (%v), living in %v.", u["name"], u["name_zh"], u["home_city"])
		}
	}
	// The app's clock and zone, not the host's: the task's world is pinned to
	// one moment (tool_script.now, default 2026-10-29 09:00 Asia/Shanghai) so
	// that "明早9点" has one right answer whenever the bench runs. agent-go
	// states the time in its prompt itself; WithClock and WithTimezone make
	// it state this one, advancing from the pinned moment as the run goes.
	// The classic world keeps the host clock, so its old results stay
	// comparable.
	var appNow time.Time
	var appLoc *time.Location
	if lifeWorld {
		appNow, appLoc = appClock(info)
	}

	client, err := pool.NewClient("bench", os.Getenv("OPENAI_BASE_URL"), os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_MODEL"))
	if err != nil {
		return fail(err)
	}
	llm := &meteredLLM{Client: client, model: os.Getenv("OPENAI_MODEL")}
	b := agent.New("bench").WithLLM(llm).WithSystemPrompt(prompt)
	if !appNow.IsZero() {
		began := time.Now()
		b = b.WithClock(func() time.Time { return appNow.Add(time.Since(began)) })
	}
	if appLoc != nil {
		b = b.WithTimezone(appLoc)
	}
	svc, err := b.Build()
	if err != nil {
		return fail(err)
	}
	defer svc.Close()

	var mu sync.Mutex
	var calls []toolCall
	currentTurn := 1
	for _, s := range specs {
		name := s.Name
		svc.AddTool(name, s.Description, s.Parameters, func(ctx context.Context, args map[string]any) (any, error) {
			argJSON, _ := json.Marshal(args)
			res, err := exec.CommandContext(ctx, benchtool, "call", name, string(argJSON)).Output()
			mu.Lock()
			rec := toolCall{Name: name, Args: args, Output: string(res), Turn: currentTurn}
			if err != nil {
				rec.Output = "benchtool error: " + benchtoolErr(err).Error()
			}
			calls = append(calls, rec)
			mu.Unlock()
			if err != nil {
				return nil, benchtoolErr(err)
			}
			var v any
			if json.Unmarshal(res, &v) != nil {
				return string(res), nil
			}
			return v, nil
		})
	}

	inputs := []string{os.Getenv("EVAL_INPUT")}
	var more []string
	if t := os.Getenv("EVAL_TURNS"); t != "" {
		if err := json.Unmarshal([]byte(t), &more); err != nil {
			return fail(fmt.Errorf("EVAL_TURNS: %w", err))
		}
	}
	inputs = append(inputs, more...)

	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	sessionID := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	var turns []turn
	var final string
	var last *agent.ExecutionResult
	var promptTok, complTok, cachedTok int
	var stops []string
	for i, in := range inputs {
		mu.Lock()
		currentTurn = i + 1
		mu.Unlock()
		res, err := svc.Run(ctx, in, agent.WithSessionID(sessionID))
		if err != nil {
			return fail(fmt.Errorf("turn %d: %w", i+1, err))
		}
		last = res
		final = res.Text()
		turns = append(turns, turn{"user", in}, turn{"assistant", final})
		stops = append(stops, string(res.StopReason))
		if res.Usage != nil {
			promptTok += res.Usage.PromptTokens
			complTok += res.Usage.CompletionTokens
			cachedTok += res.Usage.CachedPromptTokens
		}
	}

	mu.Lock()
	recorded := append([]toolCall(nil), calls...)
	mu.Unlock()
	llm.mu.Lock()
	mcalls := append([]modelCall(nil), llm.calls...)
	llm.mu.Unlock()

	aux := 0
	var modelMs int64
	for _, c := range mcalls {
		if c.Kind != "loop" {
			aux++
		}
		modelMs += c.Ms
	}
	usage := map[string]any{
		"model_calls": len(mcalls), "aux_model_calls": aux, "tool_calls": len(recorded), "model_ms": modelMs,
		"prompt_tokens": promptTok, "completion_tokens": complTok, "cached_tokens": cachedTok,
		"calls": mcalls, "source": "adapter",
	}
	if rs := runEndSummary(last); rs != nil {
		// Prefer the runtime's own account once agent-go reports one.
		for k, v := range rs {
			usage[k] = v
		}
		usage["source"] = "run_end"
	}

	out := map[string]any{"output": final, "tool_calls": recorded, "usage": usage}
	if len(inputs) > 1 {
		out["turns"] = turns
	}
	if lifeWorld {
		if raw, err := exec.Command(benchtool, "state").Output(); err == nil {
			out["state"] = json.RawMessage(raw)
		}
	}

	logLine["ms"] = time.Since(start).Milliseconds()
	logLine["tool_calls"] = len(recorded)
	logLine["model_calls"] = len(mcalls)
	logLine["aux_model_calls"] = aux
	logLine["prompt_tokens"], logLine["completion_tokens"], logLine["cached_tokens"] = promptTok, complTok, cachedTok
	logLine["stop_reason"] = stops
	if last != nil {
		logLine["blocked"] = last.Blocked
	}
	return out, logLine
}

// runEndSummary is the hook for agent-go's forthcoming run_end summary: a
// per-run account of every model call, auxiliary ones included. It reads the
// summary generically from the result's Metadata, so this adapter builds
// against v3.76.0 (which has none) and picks the summary up, unchanged, from
// whichever release adds it. Expected shape, any subset:
//
//	{"model_calls": n, "aux_model_calls": n, "prompt_tokens": n, "completion_tokens": n,
//	 "cached_tokens": n, "calls": [{"kind", "model", "ms", "prompt_tokens", ...}]}
//
// For multi-turn runs this reads the last turn's summary only; when agent-go
// ships it, sum the per-turn summaries here instead.
func runEndSummary(res *agent.ExecutionResult) map[string]any {
	if res == nil || res.Metadata == nil {
		return nil
	}
	raw, ok := res.Metadata["run_end"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

// appClock reads the world's pinned moment and zone from `benchtool info`.
// A zero time means the world has none, and the host clock stays.
func appClock(info map[string]any) (time.Time, *time.Location) {
	var loc *time.Location
	if tz, ok := info["timezone"].(string); ok && tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	now, _ := info["now"].(string)
	t, err := time.Parse(time.RFC3339, now)
	if err != nil {
		return time.Time{}, loc
	}
	if loc != nil {
		t = t.In(loc)
	}
	return t, loc
}

func benchtoolErr(err error) error {
	if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
		return fmt.Errorf("%v: %s", err, ee.Stderr)
	}
	return err
}
