package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // convert_timezone must not depend on the host's zoneinfo
)

// Env is one task's tool environment: a profile (which tools exist), a script
// (what this task's tools say back, including scripted failures) and state
// (what the agent has created, changed or deleted so far).
//
// State lives in a JSON file when BENCH_STATE names one, so a runner that
// executes every call as a separate `benchtool call` process — and a
// multi-turn task whose second turn edits what the first created — sees one
// consistent world. Calls take an exclusive lock on the file: an agent that
// runs two tool calls in parallel must not lose one of them.
//
// Environment:
//
//	EVAL_TOOL_SCRIPT  the task's tool_script JSON (profile, now, seed, responses)
//	BENCH_PROFILE     profile when the script names none ("classic" by default)
//	BENCH_STATE       path of the state file (in-memory when unset)
type Env struct {
	Script    Script
	profile   string
	statePath string
	state     map[string]any // nil until loaded
}

// Script is the per-task tool script. Every field is optional.
type Script struct {
	Profile string `json:"profile,omitempty"`
	// Now is the user's current local time, RFC 3339. Fixed so that
	// "tomorrow at 8" has one right answer.
	Now string `json:"now,omitempty"`
	// Seed is the initial state: collections ("contacts", "events", "inbox",
	// "notes", ...) as arrays of objects, "settings" as an object, "pages" (URL
	// -> {title,text,links}) for fetch_url/browser, "search" (query keyword ->
	// results) for web_search.
	Seed map[string]any `json:"seed,omitempty"`
	// Responses override what a tool returns, first matching rule wins. A rule
	// with Times > 0 is used up after that many matches — the way a rate limit
	// fails once and then succeeds on retry.
	Responses []Rule `json:"responses,omitempty"`
}

// Rule scripts one tool response.
type Rule struct {
	Tool  string         `json:"tool"`
	Match map[string]any `json:"match,omitempty"` // arg -> value; strings match case-insensitively as substrings
	Times int            `json:"times,omitempty"` // 0 = every time
	// Return is the result to give back verbatim. Error is shorthand for
	// {"error": {...}}. DelayMs sleeps first (capped at 5s) to make a slow
	// upstream actually slow.
	Return  json.RawMessage `json:"return,omitempty"`
	Error   *ToolError      `json:"error,omitempty"`
	DelayMs int             `json:"delay_ms,omitempty"`
}

// ToolError is the error shape every tool uses.
type ToolError struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	RetryAfterMs int    `json:"retry_after_ms,omitempty"`
}

func errResult(code string, retryable bool, format string, a ...any) map[string]any {
	return map[string]any{"error": ToolError{Code: code, Message: fmt.Sprintf(format, a...), Retryable: retryable}}
}

const defaultNow = "2026-10-29T09:00:00+08:00" // a Thursday

// Open builds the Env described by the process environment.
func Open() (*Env, error) {
	e := &Env{statePath: os.Getenv("BENCH_STATE")}
	if raw := strings.TrimSpace(os.Getenv("EVAL_TOOL_SCRIPT")); raw != "" {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.DisallowUnknownFields() // a misspelt "respones" must not silently script nothing
		if err := dec.Decode(&e.Script); err != nil {
			return nil, fmt.Errorf("EVAL_TOOL_SCRIPT: %w", err)
		}
	}
	e.profile = e.Script.Profile
	if e.profile == "" {
		e.profile = os.Getenv("BENCH_PROFILE")
	}
	if e.profile == "" {
		e.profile = "classic"
	}
	if _, ok := profiles[e.profile]; !ok {
		return nil, fmt.Errorf("unknown tool profile %q (known: %s)", e.profile, strings.Join(ProfileNames(), ", "))
	}
	for i, r := range e.Script.Responses {
		if r.Tool == "" {
			return nil, fmt.Errorf("tool_script.responses[%d]: missing tool", i)
		}
		if !e.hasTool(r.Tool) {
			return nil, fmt.Errorf("tool_script.responses[%d]: profile %s has no tool %q", i, e.profile, r.Tool)
		}
		if len(r.Return) == 0 && r.Error == nil {
			return nil, fmt.Errorf("tool_script.responses[%d] (%s): needs return or error", i, r.Tool)
		}
	}
	if _, err := e.Now(); err != nil {
		return nil, err
	}
	return e, nil
}

// MustOpen is Open for commands that cannot continue without an Env.
func MustOpen() *Env {
	e, err := Open()
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchtool:", err)
		os.Exit(2)
	}
	return e
}

// Profile is the active profile name.
func (e *Env) Profile() string { return e.profile }

// Now is the scripted current time.
func (e *Env) Now() (time.Time, error) {
	s := e.Script.Now
	if s == "" {
		s = defaultNow
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("tool_script.now %q: want RFC 3339", s)
	}
	return t, nil
}

func (e *Env) now() time.Time { t, _ := e.Now(); return t }

// Tools is the profile's tool set, in a stable order.
func (e *Env) Tools() []Tool { return profiles[e.profile].tools() }

func (e *Env) hasTool(name string) bool {
	for _, t := range e.Tools() {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Info is what a runner may tell the agent about its situation up front, the
// way a real assistant app puts the date and the user's name in its prompt.
func (e *Env) Info() map[string]any {
	now := e.now()
	info := map[string]any{
		"profile": e.profile,
		"now":     now.Format(time.RFC3339),
		"weekday": now.Weekday().String(),
	}
	if e.profile == "life" {
		info["timezone"] = "Asia/Shanghai"
		info["user"] = defaultUser
	}
	return info
}

// Call runs one tool call against the environment and returns its result
// (never a Go error: a failure is a result the agent must read and handle).
func (e *Env) Call(name string, args map[string]any) any {
	if args == nil {
		args = map[string]any{}
	}
	var out any
	err := e.withState(func(st map[string]any) {
		appendList(st, "_calls", map[string]any{"tool": name, "args": args})
		if r, ok := e.matchRule(st, name, args); ok {
			if r.DelayMs > 0 {
				time.Sleep(time.Duration(min(r.DelayMs, 5000)) * time.Millisecond)
			}
			if r.Error != nil {
				out = map[string]any{"error": r.Error}
				return
			}
			var v any
			if json.Unmarshal(r.Return, &v) != nil {
				v = string(r.Return)
			}
			out = v
			return
		}
		p := profiles[e.profile]
		t, ok := p.find(name)
		if !ok {
			out = errResult("UNKNOWN_TOOL", false, "no tool named %q", name)
			return
		}
		if bad := validateArgs(t.Schema, args); p.validate && bad != "" {
			out = errResult("INVALID_ARGUMENT", false, "%s", bad)
			return
		}
		out = p.run(&call{env: e, st: st, name: name, args: args})
	})
	if err != nil {
		return errResult("INTERNAL", true, "tool state: %v", err)
	}
	return out
}

// State is the environment's current state, without internal bookkeeping
// (keys starting with "_").
func (e *Env) State() map[string]any {
	var out map[string]any
	_ = e.withState(func(st map[string]any) {
		out = map[string]any{}
		for k, v := range st {
			if !strings.HasPrefix(k, "_") {
				out[k] = v
			}
		}
	})
	return out
}

func (e *Env) matchRule(st map[string]any, name string, args map[string]any) (Rule, bool) {
	uses, _ := st["_rule_uses"].(map[string]any)
	if uses == nil {
		uses = map[string]any{}
		st["_rule_uses"] = uses
	}
	for i, r := range e.Script.Responses {
		if r.Tool != name || !argsMatch(r.Match, args) {
			continue
		}
		key := fmt.Sprint(i)
		used, _ := uses[key].(float64)
		if r.Times > 0 && int(used) >= r.Times {
			continue
		}
		uses[key] = used + 1
		return r, true
	}
	return Rule{}, false
}

func argsMatch(want, got map[string]any) bool {
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return false
		}
		ws, wIsStr := w.(string)
		if wIsStr {
			if !strings.Contains(strings.ToLower(fmt.Sprint(g)), strings.ToLower(ws)) {
				return false
			}
			continue
		}
		wf, wok := toFloat(w)
		gf, gok := toFloat(g)
		if wok && gok {
			if wf != gf {
				return false
			}
			continue
		}
		if fmt.Sprint(w) != fmt.Sprint(g) {
			return false
		}
	}
	return true
}

// withState loads the state (seeding it on first use), runs fn, and saves.
func (e *Env) withState(fn func(map[string]any)) error {
	if e.statePath == "" {
		if e.state == nil {
			e.state = e.seed()
		}
		fn(e.state)
		return nil
	}
	lock, err := os.OpenFile(e.statePath+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st := map[string]any{}
	data, err := os.ReadFile(e.statePath)
	switch {
	case os.IsNotExist(err) || len(data) == 0:
		st = e.seed()
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &st); err != nil {
			return fmt.Errorf("parse %s: %w", e.statePath, err)
		}
	}
	fn(st)
	out, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := e.statePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, e.statePath)
}

// seed deep-copies the script's seed through JSON so numbers are float64 and
// maps are map[string]any, exactly as they will be after a save and reload.
func (e *Env) seed() map[string]any {
	st := map[string]any{}
	if p, ok := profiles[e.profile]; ok && p.defaults != nil {
		for k, v := range p.defaults(e) {
			st[k] = v
		}
	}
	for k, v := range e.Script.Seed {
		st[k] = v
	}
	b, _ := json.Marshal(st)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func appendList(st map[string]any, key string, item any) []any {
	l, _ := st[key].([]any)
	l = append(l, item)
	st[key] = l
	return l
}

// ProfileNames lists the known profiles.
func ProfileNames() []string {
	var n []string
	for k := range profiles {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// validateArgs checks args against a tool's JSON Schema the way a real API
// would: required fields, enums, types, date-time formats, one level of
// nesting. A wrong argument must come back as an error the agent can read,
// not be accepted and silently do the wrong thing.
func validateArgs(schema map[string]any, args map[string]any) string {
	props, _ := schema["properties"].(map[string]any)
	if req, ok := schema["required"].([]string); ok {
		for _, r := range req {
			if v, ok := args[r]; !ok || v == nil || v == "" {
				return fmt.Sprintf("missing required field %q", r)
			}
		}
	}
	for k, v := range args {
		ps, ok := props[k].(map[string]any)
		if !ok {
			if len(props) > 0 {
				return fmt.Sprintf("unknown field %q (allowed: %s)", k, strings.Join(sortedKeys(props), ", "))
			}
			continue
		}
		if bad := validateValue(k, ps, v); bad != "" {
			return bad
		}
	}
	return ""
}

func validateValue(k string, ps map[string]any, v any) string {
	if v == nil {
		return ""
	}
	switch ps["type"] {
	case "number", "integer":
		f, ok := toFloat(v)
		if !ok {
			return fmt.Sprintf("field %q must be a number, got %v", k, v)
		}
		if ps["type"] == "integer" && f != float64(int64(f)) {
			return fmt.Sprintf("field %q must be an integer, got %v", k, v)
		}
		if mn, ok := ps["minimum"].(float64); ok && f < mn {
			return fmt.Sprintf("field %q must be >= %v", k, mn)
		}
		if mx, ok := ps["maximum"].(float64); ok && f > mx {
			return fmt.Sprintf("field %q must be <= %v", k, mx)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("field %q must be true or false, got %v", k, v)
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return fmt.Sprintf("field %q must be a string, got %v", k, v)
		}
		if enum, ok := ps["enum"].([]string); ok {
			found := false
			for _, e := range enum {
				found = found || e == s
			}
			if !found {
				return fmt.Sprintf("field %q must be one of %s, got %q", k, strings.Join(enum, ", "), s)
			}
		}
		switch ps["format"] {
		case "date-time":
			if _, err := parseTime(s, nil); err != nil {
				return fmt.Sprintf("field %q must be an ISO 8601 date-time like 2026-10-29T19:00:00+08:00, got %q", k, s)
			}
		case "date":
			if _, err := time.Parse("2006-01-02", s); err != nil {
				return fmt.Sprintf("field %q must be a date like 2026-10-29, got %q", k, s)
			}
		case "time":
			if _, err := time.Parse("15:04", s); err != nil {
				return fmt.Sprintf("field %q must be a 24h time like 07:30, got %q", k, s)
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Sprintf("field %q must be an array", k)
		}
		if items, ok := ps["items"].(map[string]any); ok {
			for i, it := range arr {
				if bad := validateValue(fmt.Sprintf("%s[%d]", k, i), items, it); bad != "" {
					return bad
				}
			}
		}
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Sprintf("field %q must be an object", k)
		}
		if bad := validateArgs(ps, m); bad != "" {
			return k + ": " + bad
		}
	}
	return ""
}

// parseTime accepts RFC 3339 and the zone-less forms models commonly send,
// reading a zone-less time in loc (Asia/Shanghai when nil).
func parseTime(s string, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = shanghai
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, strings.TrimSpace(s), loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

func sortedKeys(m map[string]any) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
