package evalgo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func mustCheck(t *testing.T, js string) *ToolCheck {
	t.Helper()
	var c ToolCheck
	dec := json.NewDecoder(strings.NewReader(js))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	return &c
}

func score(t *testing.T, c *ToolCheck, s Sample) Result {
	t.Helper()
	s.Check = c
	r, err := ToolTrajectory().Score(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestToolTrajectoryArgsOrderAndFrom(t *testing.T) {
	c := mustCheck(t, `{
		"calls": [
			{"id":"rate","tool":"get_fx_rate","args":{"from":{"equals":"usd"},"to":{"one_of":["CHF","chf"]}}},
			{"tool":"calculator","after":["rate"],"args":{"expression":{"contains":"0.88"}}},
			{"tool":"send_email","after":["calculator"],"args":{"to":{"regex":"^me@home\\.com$"},"body":{"contains":["440"]}}}
		],
		"forbidden": ["web_search"],
		"max_calls": 4,
		"answer": {"regex": ["440(\\.00)?"]}
	}`)
	good := Sample{
		Output: "500 USD is 440.00 CHF; emailed you.",
		ToolCalls: []ToolCall{
			{Name: "get_fx_rate", Args: map[string]any{"from": "USD", "to": "CHF"}, Output: `{"rate":0.88}`},
			{Name: "calculator", Args: map[string]any{"expression": "500*0.88"}, Output: `{"result":440}`},
			{Name: "send_email", Args: map[string]any{"to": "me@home.com", "body": "It is 440 CHF"}},
		},
	}
	if r := score(t, c, good); !r.Passed || r.Score != 1 {
		t.Fatalf("good run should pass: %+v", r)
	}

	// Wrong order and an extra forbidden call.
	bad := good
	bad.ToolCalls = []ToolCall{good.ToolCalls[1], good.ToolCalls[0], {Name: "web_search"}, good.ToolCalls[2]}
	r := score(t, c, bad)
	if r.Passed {
		t.Fatalf("out-of-order run with web_search should fail: %+v", r)
	}
	for _, want := range []string{"calculator must come after rate", "forbidden tool web_search"} {
		if !strings.Contains(r.Reason, want) {
			t.Errorf("reason %q missing %q", r.Reason, want)
		}
	}

	// Right tool, wrong argument: the reason names the argument.
	wrong := good
	wrong.ToolCalls = append([]ToolCall(nil), good.ToolCalls...)
	wrong.ToolCalls[2] = ToolCall{Name: "send_email", Args: map[string]any{"to": "boss@work.com", "body": "440"}}
	r = score(t, c, wrong)
	if r.Passed || !strings.Contains(r.Reason, "arg to") {
		t.Fatalf("wrong recipient should fail naming the arg: %+v", r)
	}
}

func TestToolTrajectoryFromOutput(t *testing.T) {
	c := mustCheck(t, `{"calls":[
		{"id":"find","tool":"search_contacts"},
		{"tool":"send_sms","args":{"phone":{"from":{"call":"find","path":"results.0.phone"}}}}
	]}`)
	s := Sample{ToolCalls: []ToolCall{
		{Name: "search_contacts", Output: `{"results":[{"name":"Mia","phone":"+86 138 0000 1111"}]}`},
		{Name: "send_sms", Args: map[string]any{"phone": "+86 138 0000 1111"}},
	}}
	if r := score(t, c, s); !r.Passed {
		t.Fatalf("phone from search result should pass: %+v", r)
	}
	s.ToolCalls[1].Args["phone"] = "+86 139 9999 0000" // invented number
	if r := score(t, c, s); r.Passed || !strings.Contains(r.Reason, "earlier result") {
		t.Fatalf("invented phone should fail: %+v", r)
	}
}

func TestToolTrajectoryOrAnswerAndCounts(t *testing.T) {
	c := mustCheck(t, `{"calls":[{"tool":"calculator","or_answer":{"regex":"32\\.40?"}}]}`)
	if r := score(t, c, Sample{Output: "You have $32.40 left."}); !r.Passed {
		t.Fatalf("correct mental arithmetic should satisfy or_answer: %+v", r)
	}
	if r := score(t, c, Sample{Output: "You have $33 left."}); r.Passed {
		t.Fatalf("wrong mental arithmetic must fail: %+v", r)
	}

	c = mustCheck(t, `{"calls":[{"tool":"get_quote","min":2,"max":3}],"forbidden":["*"]}`)
	r := score(t, c, Sample{ToolCalls: []ToolCall{{Name: "get_quote"}}})
	if r.Passed || !strings.Contains(r.Reason, "not called") && !strings.Contains(r.Reason, "want >= 2") {
		t.Fatalf("one call under min 2 should fail: %+v", r)
	}

	none := mustCheck(t, `{"forbidden":["*"]}`)
	if r := score(t, none, Sample{}); !r.Passed {
		t.Fatalf("no calls under forbidden * should pass: %+v", r)
	}
	if r := score(t, none, Sample{ToolCalls: []ToolCall{{Name: "send_email"}}}); r.Passed {
		t.Fatal("any call under forbidden * should fail")
	}
}

func TestToolTrajectoryStateAndTurns(t *testing.T) {
	c := mustCheck(t, `{
		"calls":[{"tool":"update_reminder","turn":2}],
		"state":[{"path":"reminders","where":{"title":{"contains":"mom"},"time":{"regex":"T20:00"}},"count":{"equals":1}}]
	}`)
	s := Sample{
		ToolCalls: []ToolCall{{Name: "create_reminder", Turn: 1}, {Name: "update_reminder", Turn: 2}},
		State:     json.RawMessage(`{"reminders":[{"id":"r1","title":"Call Mom","time":"2026-10-30T20:00:00+08:00"}]}`),
	}
	if r := score(t, c, s); !r.Passed {
		t.Fatalf("updated reminder should pass: %+v", r)
	}
	s.State = json.RawMessage(`{"reminders":[{"id":"r1","title":"Call Mom","time":"2026-10-30T19:00:00+08:00"},{"id":"r2","title":"call mom","time":"2026-10-30T20:00:00+08:00"}]}`)
	s.ToolCalls[1] = ToolCall{Name: "update_reminder", Turn: 1}
	r := score(t, c, s)
	if r.Passed {
		t.Fatalf("update in the wrong turn should fail: %+v", r)
	}
	if !strings.Contains(r.Reason, "turn 1, want turn 2") {
		t.Errorf("reason should name the turn: %s", r.Reason)
	}

	if r := score(t, c, Sample{ToolCalls: s.ToolCalls}); r.Passed || !strings.Contains(r.Reason, "no tool state") {
		t.Fatalf("missing state must fail, not pass: %+v", r)
	}
}

func TestToolCheckValidate(t *testing.T) {
	for name, js := range map[string]string{
		"no tool":        `{"calls":[{"args":{}}]}`,
		"bad after":      `{"calls":[{"tool":"a","after":["zz"]}]}`,
		"from later":     `{"calls":[{"tool":"a","args":{"x":{"from":{"call":"b"}}}},{"tool":"b"}]}`,
		"bad regex":      `{"answer":{"regex":"("}}`,
		"dup id":         `{"calls":[{"tool":"a"},{"tool":"a"}]}`,
		"state no path":  `{"state":[{"count":{"equals":1}}]}`,
		"arg bad regex":  `{"calls":[{"tool":"a","args":{"x":{"regex":"["}}}]}`,
		"or_answer regx": `{"calls":[{"tool":"a","or_answer":{"regex":"("}}]}`,
	} {
		var c ToolCheck
		if err := json.Unmarshal([]byte(js), &c); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestJudgePromptKeepsSmallStateNextToLargeState(t *testing.T) {
	big := strings.Repeat(`{"name":"someone","phone":"+86 000"},`, 400)
	s := Sample{State: json.RawMessage(`{"contacts":[` + strings.TrimSuffix(big, ",") + `],"reminders":[{"title":"Call Mom","remind_at":"2026-10-30T20:00"}]}`)}
	block := stateBlock(s)
	if !strings.Contains(block, "Call Mom") {
		t.Fatalf("the reminders must survive a long contacts list:\n%s", block[:200])
	}
	calls := make([]ToolCall, 100)
	for i := range calls {
		calls[i] = ToolCall{Name: "web_search", Output: strings.Repeat("x", 5000)}
	}
	if n := len(formatToolCalls(calls)); n > judgeMaxToolText+200 {
		t.Fatalf("tool-call block not bounded: %d", n)
	}
}
