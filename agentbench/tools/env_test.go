package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openEnv(t *testing.T, script, statePath string) *Env {
	t.Helper()
	t.Setenv("EVAL_TOOL_SCRIPT", script)
	t.Setenv("BENCH_STATE", statePath)
	t.Setenv("BENCH_PROFILE", "")
	e, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

func errCode(v any) string {
	m, _ := v.(map[string]any)
	if m == nil {
		return ""
	}
	switch e := m["error"].(type) {
	case ToolError:
		return e.Code
	case *ToolError:
		return e.Code
	case map[string]any:
		return fmt.Sprint(e["code"])
	}
	return ""
}

func TestClassicUnchanged(t *testing.T) {
	e := openEnv(t, "", "")
	if e.Profile() != "classic" || len(e.Tools()) != 10 {
		t.Fatalf("default profile should be the original ten tools: %s %d", e.Profile(), len(e.Tools()))
	}
	r := asMap(t, e.Call("convert_currency", map[string]any{"amount": 100.0, "from": "USD", "to": "EUR"}))
	if r["converted"] != 92.0 {
		t.Fatalf("classic convert_currency changed: %v", r)
	}
}

func TestOpenRejectsBadScripts(t *testing.T) {
	for name, script := range map[string]string{
		"unknown profile": `{"profile":"nope"}`,
		"misspelt field":  `{"profile":"life","respones":[]}`,
		"rule unknown":    `{"profile":"life","responses":[{"tool":"book_flight","return":{}}]}`,
		"rule empty":      `{"profile":"life","responses":[{"tool":"get_weather"}]}`,
		"bad now":         `{"profile":"life","now":"tomorrow"}`,
	} {
		t.Setenv("EVAL_TOOL_SCRIPT", script)
		if _, err := Open(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLifeProfileSize(t *testing.T) {
	e := openEnv(t, `{"profile":"life"}`, "")
	n := len(e.Tools())
	if n < 80 || n > 100 {
		t.Fatalf("life profile should have 80-100 tools, has %d", n)
	}
	seen := map[string]bool{}
	for _, tl := range e.Tools() {
		if seen[tl.Name] {
			t.Fatalf("duplicate tool %s", tl.Name)
		}
		seen[tl.Name] = true
		if !strings.Contains(fmt.Sprint(runLife(&call{env: e, st: map[string]any{}, name: tl.Name, args: map[string]any{}})), "UNIMPLEMENTED") {
			continue
		}
		t.Errorf("%s has no implementation", tl.Name)
	}
}

func TestScriptedRetryThenSuccess(t *testing.T) {
	e := openEnv(t, `{"profile":"life","responses":[
		{"tool":"get_fx_rate","match":{"quote":"CHF"},"times":1,"error":{"code":"RATE_LIMITED","message":"slow down","retryable":true,"retry_after_ms":500}}
	]}`, "")
	first := e.Call("get_fx_rate", map[string]any{"base": "USD", "quote": "CHF"})
	if errCode(first) != "RATE_LIMITED" {
		t.Fatalf("first call should be rate limited: %v", first)
	}
	second := asMap(t, e.Call("get_fx_rate", map[string]any{"base": "USD", "quote": "CHF"}))
	if second["rate"] != 0.86 {
		t.Fatalf("retry should succeed with the real rate: %v", second)
	}
}

func TestValidationErrorsAreResults(t *testing.T) {
	e := openEnv(t, `{"profile":"life"}`, "")
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"create_reminder", map[string]any{"title": "x"}},                                                      // missing remind_at
		{"create_reminder", map[string]any{"title": "x", "remind_at": "tomorrow 7pm"}},                         // bad format
		{"create_reminder", map[string]any{"title": "x", "remind_at": "2026-10-28T09:00:00+08:00"}},            // past
		{"log_expense", map[string]any{"amount": 30.0, "currency": "RMB", "category": "food"}},                 // bad enum
		{"send_email", map[string]any{"to": []any{"mia"}, "subject": "s", "body": "b"}},                        // not an address
		{"create_alarm", map[string]any{"time": "7am"}},                                                        // bad time
		{"get_weather", map[string]any{"city": "Paris", "date": "2026-10-30"}},                                 // unknown field
		{"unit_convert", map[string]any{"value": 1.0, "from_unit": "kg", "to_unit": "km"}},                     // wrong dimension
		{"set_thermostat", map[string]any{"device_id": "dev_1", "target_c": 22.0}},                             // a light
		{"log_health_metric", map[string]any{"metric": "blood_pressure", "value": 120.0}},                      // needs value2
		{"create_event", map[string]any{"title": "x", "start": "2026-10-30T10:00", "end": "2026-10-30T09:00"}}, // end before start
	} {
		if code := errCode(e.Call(tc.tool, tc.args)); code != "INVALID_ARGUMENT" {
			t.Errorf("%s %v: want INVALID_ARGUMENT, got %q", tc.tool, tc.args, code)
		}
	}
}

func TestStatePersistsAcrossProcessesAndParallelCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	script := `{"profile":"life","seed":{"reminders":[{"id":"rem_7","title":"old","remind_at":"2026-11-01T08:00:00+08:00","status":"pending"}]}}`
	a := openEnv(t, script, path)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e := openEnv(t, script, path) // a separate "process"
			e.Call("create_todo", map[string]any{"title": fmt.Sprintf("t%d", i)})
		}()
	}
	wg.Wait()
	r := asMap(t, a.Call("create_reminder", map[string]any{"title": "Call Mom", "remind_at": "2026-10-30T19:00:00+08:00"}))
	id := r["reminder"].(map[string]any)["id"]
	if id != "rem_8" {
		t.Fatalf("new id must not collide with the seeded rem_7: %v", id)
	}
	b := openEnv(t, script, path)
	b.Call("update_reminder", map[string]any{"reminder_id": id, "remind_at": "2026-10-30T20:00:00+08:00"})
	st := asMap(t, b.State())
	if n := len(st["todos"].([]any)); n != 8 {
		t.Fatalf("parallel calls lost writes: %d todos", n)
	}
	rems := st["reminders"].([]any)
	if len(rems) != 2 || rems[1].(map[string]any)["remind_at"] != "2026-10-30T20:00:00+08:00" {
		t.Fatalf("update not persisted: %v", rems)
	}
	if _, leaked := st["_calls"]; leaked {
		t.Fatal("internal bookkeeping must not appear in State()")
	}
}

func TestPagination(t *testing.T) {
	var exp []map[string]any
	for i := 1; i <= 23; i++ {
		exp = append(exp, map[string]any{"id": fmt.Sprintf("exp_%d", i), "amount": float64(i), "currency": "CNY", "category": "food", "date": fmt.Sprintf("2026-10-%02d", i)})
	}
	seed, _ := json.Marshal(map[string]any{"profile": "life", "seed": map[string]any{"expenses": exp}})
	e := openEnv(t, string(seed), "")
	total, pages, tok := 0, 0, ""
	for {
		args := map[string]any{"from": "2026-10-01", "to": "2026-10-31"}
		if tok != "" {
			args["page_token"] = tok
		}
		r := asMap(t, e.Call("list_expenses", args))
		pages++
		for _, x := range r["expenses"].([]any) {
			a, _ := toFloat(x.(map[string]any)["amount"])
			total += int(a)
		}
		next, _ := r["next_page_token"].(string)
		if next == "" {
			break
		}
		tok = next
	}
	if pages != 3 || total != 276 {
		t.Fatalf("want 3 pages summing to 276, got %d pages, %d", pages, total)
	}
}

func TestUnitsAndTimezones(t *testing.T) {
	e := openEnv(t, `{"profile":"life"}`, "")
	r := asMap(t, e.Call("unit_convert", map[string]any{"value": 3.0, "from_unit": "斤", "to_unit": "kg"}))
	if r["result"] != 1.5 {
		t.Fatalf("3 斤 = 1.5 kg: %v", r)
	}
	r = asMap(t, e.Call("convert_timezone", map[string]any{"time": "2026-10-30T15:00", "from_tz": "Europe/London", "to_tz": "Asia/Shanghai"}))
	if r["to"] != "2026-10-30T23:00:00+08:00" { // London is back on GMT after 2026-10-25
		t.Fatalf("15:00 London -> Shanghai: %v", r)
	}
}
