package evalgo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ToolCheck is a task's deterministic ground truth for HOW the agent used its
// tools, not just which ones. tool_correctness only asks "was calculator in the
// set of names"; a ToolCheck can say "set_reminder with time 20:00, after
// get_weather, using the rate get_fx_rate returned, never send_email, at most
// four calls, and the final answer quotes 32.40".
//
// Every assertion is one check; the metric's score is the fraction that pass
// and it passes only when all do. Each failure carries a reason a human can act
// on ("set_reminder: arg time: got \"19:00\", want regex 20:00").
type ToolCheck struct {
	Calls     []CallExpect `json:"calls,omitempty"`
	Forbidden []string     `json:"forbidden,omitempty"` // tools that must never be called; "*" = no tool call at all
	MinCalls  *int         `json:"min_calls,omitempty"` // bounds on the total number of tool calls
	MaxCalls  *int         `json:"max_calls,omitempty"`
	Answer    *TextCheck   `json:"answer,omitempty"` // assertions on the final answer
	State     []StateCheck `json:"state,omitempty"`  // assertions on the tool environment's final state
}

// CallExpect is one expected tool call (or group of interchangeable calls).
type CallExpect struct {
	ID    string                `json:"id,omitempty"`     // label for after/from references; defaults to Tool
	Tool  string                `json:"tool,omitempty"`   // the tool name
	AnyOf []string              `json:"any_of,omitempty"` // or: any of these names satisfies it
	Args  map[string]ValueCheck `json:"args,omitempty"`   // argument path -> assertion; dots reach nested fields
	Min   *int                  `json:"min,omitempty"`    // matching calls required; default 1
	Max   *int                  `json:"max,omitempty"`    // matching calls allowed; default unbounded
	After []string              `json:"after,omitempty"`  // ids that must have a matching call earlier
	Turn  int                   `json:"turn,omitempty"`   // 1-based turn it must happen in; 0 = any
	// OrAnswer is the "tool or justified no-call" alternative: the expectation
	// is also met when no call was made and the final answer passes this check
	// — e.g. calculator OR the correct sum worked out in the reply.
	OrAnswer *TextCheck `json:"or_answer,omitempty"`
	Why      string     `json:"why,omitempty"` // shown in the failure reason
}

// ValueCheck asserts one value. All set fields must hold. String comparisons
// are case-insensitive and ignore surrounding space; numbers parse from strings.
type ValueCheck struct {
	Equals      any      `json:"equals,omitempty"`
	OneOf       []any    `json:"one_of,omitempty"`
	Num         *float64 `json:"num,omitempty"` // numeric value within Tol (default 0.01)
	Tol         float64  `json:"tol,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	Regex       string   `json:"regex,omitempty"` // Go RE2, matched case-insensitively against the string form
	Contains    strList  `json:"contains,omitempty"`
	ContainsAny strList  `json:"contains_any,omitempty"`
	NotContains strList  `json:"not_contains,omitempty"`
	Absent      bool     `json:"absent,omitempty"`  // the argument must not be set
	Present     bool     `json:"present,omitempty"` // the argument must be set (any value)
	// From ties an argument to an earlier call's result: the value must equal
	// the referenced call's output at Path (numbers within 0.5%). It is how
	// "B uses A's output" is checked rather than assumed.
	From *FromRef `json:"from,omitempty"`
}

// FromRef points at a field of an earlier expected call's JSON output.
type FromRef struct {
	Call string `json:"call"`           // a CallExpect id
	Path string `json:"path,omitempty"` // dot path into its output; empty = the whole output must contain the value
}

// TextCheck asserts a block of text (the final answer). Case-insensitive.
type TextCheck struct {
	Contains    strList `json:"contains,omitempty"`     // every one must appear
	ContainsAny strList `json:"contains_any,omitempty"` // at least one must appear
	NotContains strList `json:"not_contains,omitempty"` // none may appear
	Regex       strList `json:"regex,omitempty"`        // every one must match
	NotRegex    strList `json:"not_regex,omitempty"`    // none may match
}

// StateCheck asserts the final tool state. Path names a value (dot path); when
// it is an array, Where filters its elements and Count bounds how many match
// (default: at least one). For a scalar, Value asserts it.
type StateCheck struct {
	Path  string                `json:"path"`
	Where map[string]ValueCheck `json:"where,omitempty"`
	Count *ValueCheck           `json:"count,omitempty"`
	Value *ValueCheck           `json:"value,omitempty"`
	Why   string                `json:"why,omitempty"`
}

// strList accepts either "x" or ["x","y"] in JSON.
type strList []string

func (l *strList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = strList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("want a string or an array of strings: %w", err)
	}
	*l = many
	return nil
}

func (c CallExpect) id() string {
	if c.ID != "" {
		return c.ID
	}
	if c.Tool != "" {
		return c.Tool
	}
	return strings.Join(c.AnyOf, "|")
}

func (c CallExpect) names() map[string]bool {
	m := map[string]bool{}
	if c.Tool != "" {
		m[c.Tool] = true
	}
	for _, n := range c.AnyOf {
		m[n] = true
	}
	return m
}

func (c CallExpect) label() string {
	if c.Tool != "" && len(c.AnyOf) == 0 {
		return c.Tool
	}
	n := c.AnyOf
	if c.Tool != "" {
		n = append([]string{c.Tool}, n...)
	}
	return strings.Join(n, "|")
}

// Validate reports a malformed check — an unknown reference, a regex that does
// not compile, an expectation naming no tool — so a typo fails at load time
// instead of silently never matching.
func (tc *ToolCheck) Validate() error {
	if tc == nil {
		return nil
	}
	ids := map[string]int{}
	for i, c := range tc.Calls {
		if c.Tool == "" && len(c.AnyOf) == 0 {
			return fmt.Errorf("calls[%d]: needs tool or any_of", i)
		}
		if _, dup := ids[c.id()]; dup {
			return fmt.Errorf("calls[%d]: duplicate id %q (set id to tell them apart)", i, c.id())
		}
		ids[c.id()] = i
	}
	for i, c := range tc.Calls {
		for _, a := range c.After {
			if _, ok := ids[a]; !ok {
				return fmt.Errorf("calls[%d] (%s): after %q: no such call id", i, c.id(), a)
			}
		}
		for path, v := range c.Args {
			if err := v.validate(); err != nil {
				return fmt.Errorf("calls[%d] (%s): arg %s: %w", i, c.id(), path, err)
			}
			if v.From != nil {
				j, ok := ids[v.From.Call]
				if !ok {
					return fmt.Errorf("calls[%d] (%s): arg %s: from %q: no such call id", i, c.id(), path, v.From.Call)
				}
				if j >= i {
					return fmt.Errorf("calls[%d] (%s): arg %s: from %q must name an earlier call", i, c.id(), path, v.From.Call)
				}
			}
		}
		if err := c.OrAnswer.validate(); err != nil {
			return fmt.Errorf("calls[%d] (%s): or_answer: %w", i, c.id(), err)
		}
	}
	if err := tc.Answer.validate(); err != nil {
		return fmt.Errorf("answer: %w", err)
	}
	for i, st := range tc.State {
		if st.Path == "" {
			return fmt.Errorf("state[%d]: missing path", i)
		}
		for k, v := range st.Where {
			if err := v.validate(); err != nil {
				return fmt.Errorf("state[%d] where %s: %w", i, k, err)
			}
		}
	}
	return nil
}

func (v ValueCheck) validate() error {
	if v.Regex != "" {
		if _, err := regexp.Compile("(?i)" + v.Regex); err != nil {
			return err
		}
	}
	return nil
}

func (t *TextCheck) validate() error {
	if t == nil {
		return nil
	}
	for _, r := range append(append(strList{}, t.Regex...), t.NotRegex...) {
		if _, err := regexp.Compile("(?i)" + r); err != nil {
			return err
		}
	}
	return nil
}

// ToolTrajectory scores a run against its Task's ToolCheck. Deterministic: no
// judge, no tokens. Samples without a check pass as skipped.
func ToolTrajectory() Metric {
	return MetricFunc{"tool_trajectory", func(_ context.Context, s Sample) (Result, error) {
		if s.Check == nil {
			return Result{Metric: "tool_trajectory", Passed: true, Score: 1, Reason: "no check (skipped)"}, nil
		}
		passed, total, fails := evaluateCheck(*s.Check, s)
		score := 1.0
		if total > 0 {
			score = float64(passed) / float64(total)
		}
		reason := fmt.Sprintf("%d/%d checks", passed, total)
		if len(fails) > 0 {
			reason += "; " + strings.Join(fails, "; ")
		}
		return Result{Metric: "tool_trajectory", Score: score, Passed: len(fails) == 0, Reason: reason}, nil
	}}
}

func evaluateCheck(tc ToolCheck, s Sample) (passed, total int, fails []string) {
	check := func(ok bool, why string) {
		total++
		if ok {
			passed++
		} else {
			fails = append(fails, why)
		}
	}
	calls := s.ToolCalls

	// matches[id] = indices of recorded calls satisfying that expectation.
	matches := map[string][]int{}
	for _, ce := range tc.Calls {
		names := ce.names()
		var idx []int
		var near []string // why same-named calls did not match, for the reason
		for i, c := range calls {
			if !names[c.Name] {
				continue
			}
			if ce.Turn > 0 && c.Turn != ce.Turn {
				near = append(near, fmt.Sprintf("call %d was in turn %d, want turn %d", i+1, c.Turn, ce.Turn))
				continue
			}
			if why := argsMismatch(ce.Args, c.Args, calls, matches); why != "" {
				near = append(near, why)
				continue
			}
			idx = append(idx, i)
		}
		matches[ce.id()] = idx

		lo, hi := 1, math.MaxInt
		if ce.Min != nil {
			lo = *ce.Min
		}
		if ce.Max != nil {
			hi = *ce.Max
		}
		n := len(idx)
		ok := n >= lo && n <= hi
		if !ok && n == 0 && ce.OrAnswer != nil && textFails(*ce.OrAnswer, s.Output) == "" {
			ok = true
		}
		why := ""
		if !ok {
			switch {
			case n < lo && len(near) > 0:
				why = fmt.Sprintf("%s: %d matching call(s), want >= %d (%s)", ce.label(), n, lo, uniqueJoin(near, 2))
			case n < lo:
				why = fmt.Sprintf("%s: not called", ce.label())
				if ce.OrAnswer != nil {
					why += " and the answer does not show it: " + textFails(*ce.OrAnswer, s.Output)
				}
			default:
				why = fmt.Sprintf("%s: called %d times, at most %d allowed", ce.label(), n, hi)
			}
			if ce.Why != "" {
				why += " [" + ce.Why + "]"
			}
		}
		check(ok, why)

		for _, ref := range ce.After {
			if len(idx) == 0 {
				continue // already failed above (or satisfied by answer); order is moot
			}
			before := matches[ref]
			ok := len(before) > 0 && before[0] < idx[len(idx)-1]
			check(ok, fmt.Sprintf("%s must come after %s", ce.label(), ref))
		}
	}

	for _, f := range tc.Forbidden {
		bad := 0
		for _, c := range calls {
			if f == "*" || c.Name == f {
				bad++
			}
		}
		if f == "*" {
			check(bad == 0, fmt.Sprintf("no tool call allowed, made %d (%s)", bad, strings.Join(uniq(toolNames(calls)), ", ")))
		} else {
			check(bad == 0, fmt.Sprintf("forbidden tool %s called %d time(s)", f, bad))
		}
	}
	if tc.MinCalls != nil {
		check(len(calls) >= *tc.MinCalls, fmt.Sprintf("%d tool calls, want >= %d", len(calls), *tc.MinCalls))
	}
	if tc.MaxCalls != nil {
		check(len(calls) <= *tc.MaxCalls, fmt.Sprintf("%d tool calls, want <= %d", len(calls), *tc.MaxCalls))
	}
	if tc.Answer != nil {
		why := textFails(*tc.Answer, s.Output)
		check(why == "", "answer: "+why)
	}
	if len(tc.State) > 0 {
		var state any
		if len(s.State) == 0 || json.Unmarshal(s.State, &state) != nil {
			for _, st := range tc.State {
				check(false, "state "+st.Path+": target reported no tool state")
			}
		} else {
			for _, st := range tc.State {
				why := stateFails(st, state)
				if why != "" && st.Why != "" {
					why += " [" + st.Why + "]"
				}
				check(why == "", why)
			}
		}
	}
	return passed, total, fails
}

// argsMismatch returns why a call's arguments fail the expectation, or "".
func argsMismatch(want map[string]ValueCheck, args map[string]any, calls []ToolCall, matches map[string][]int) string {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vc := want[k]
		vals, found := lookup(args, k)
		var v any
		if len(vals) > 0 {
			v = vals[0]
		}
		if vc.From != nil {
			ref := matches[vc.From.Call]
			if len(ref) == 0 {
				return fmt.Sprintf("arg %s: needs %s's output but %s made no matching call", k, vc.From.Call, vc.From.Call)
			}
			if !found {
				return fmt.Sprintf("arg %s: missing", k)
			}
			if why := fromMismatch(v, calls[ref[0]].Output, vc.From.Path); why != "" {
				return fmt.Sprintf("arg %s: %s", k, why)
			}
		}
		if why := valueFails(vc, v, found); why != "" {
			return fmt.Sprintf("arg %s: %s", k, why)
		}
	}
	return ""
}

// valueFails returns why v fails vc, or "". found says whether v was present.
func valueFails(vc ValueCheck, v any, found bool) string {
	if vc.Absent {
		if found {
			return fmt.Sprintf("must be absent, got %s", show(v))
		}
		return ""
	}
	needs := vc.Present || vc.Equals != nil || len(vc.OneOf) > 0 || vc.Num != nil || vc.Min != nil ||
		vc.Max != nil || vc.Regex != "" || len(vc.Contains) > 0 || len(vc.ContainsAny) > 0
	if !found {
		if needs {
			return "missing"
		}
		return ""
	}
	if vc.Equals != nil && !looseEqual(v, vc.Equals) {
		return fmt.Sprintf("got %s, want %s", show(v), show(vc.Equals))
	}
	if len(vc.OneOf) > 0 {
		ok := false
		for _, o := range vc.OneOf {
			ok = ok || looseEqual(v, o)
		}
		if !ok {
			return fmt.Sprintf("got %s, want one of %s", show(v), show(vc.OneOf))
		}
	}
	if vc.Num != nil || vc.Min != nil || vc.Max != nil {
		f, ok := asFloat(v)
		if !ok {
			return fmt.Sprintf("got %s, want a number", show(v))
		}
		if vc.Num != nil {
			tol := vc.Tol
			if tol == 0 {
				tol = 0.01
			}
			if math.Abs(f-*vc.Num) > tol {
				return fmt.Sprintf("got %v, want %v±%v", f, *vc.Num, tol)
			}
		}
		if vc.Min != nil && f < *vc.Min {
			return fmt.Sprintf("got %v, want >= %v", f, *vc.Min)
		}
		if vc.Max != nil && f > *vc.Max {
			return fmt.Sprintf("got %v, want <= %v", f, *vc.Max)
		}
	}
	str := strings.ToLower(asString(v))
	if vc.Regex != "" && !regexp.MustCompile("(?i)"+vc.Regex).MatchString(asString(v)) {
		return fmt.Sprintf("got %s, want regex %s", show(v), vc.Regex)
	}
	for _, c := range vc.Contains {
		if !strings.Contains(str, strings.ToLower(c)) {
			return fmt.Sprintf("got %s, want it to contain %q", show(v), c)
		}
	}
	if len(vc.ContainsAny) > 0 {
		ok := false
		for _, c := range vc.ContainsAny {
			ok = ok || strings.Contains(str, strings.ToLower(c))
		}
		if !ok {
			return fmt.Sprintf("got %s, want it to contain one of %s", show(v), show([]string(vc.ContainsAny)))
		}
	}
	for _, c := range vc.NotContains {
		if strings.Contains(str, strings.ToLower(c)) {
			return fmt.Sprintf("got %s, must not contain %q", show(v), c)
		}
	}
	return ""
}

// fromMismatch checks that v carries the value an earlier call returned.
func fromMismatch(v any, output, path string) string {
	if path == "" {
		if !strings.Contains(strings.ToLower(output), strings.ToLower(asString(v))) {
			return fmt.Sprintf("%s does not come from the earlier result", show(v))
		}
		return ""
	}
	var parsed any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return "earlier call's output is not JSON (was it recorded?)"
	}
	vals, ok := lookup(parsed, path)
	if !ok || len(vals) == 0 {
		return fmt.Sprintf("earlier output has no %s", path)
	}
	want := vals[0]
	if wf, ok := asFloat(want); ok {
		if gf, ok := asFloat(v); ok {
			if math.Abs(gf-wf) <= math.Max(0.005*math.Abs(wf), 0.01) {
				return ""
			}
			return fmt.Sprintf("got %v, earlier result %s was %v", gf, path, wf)
		}
	}
	if looseEqual(v, want) || strings.Contains(strings.ToLower(asString(v)), strings.ToLower(asString(want))) {
		return ""
	}
	return fmt.Sprintf("got %s, earlier result %s was %s", show(v), path, show(want))
}

func textFails(tc TextCheck, text string) string {
	low := strings.ToLower(text)
	for _, c := range tc.Contains {
		if !strings.Contains(low, strings.ToLower(c)) {
			return fmt.Sprintf("missing %q", c)
		}
	}
	if len(tc.ContainsAny) > 0 {
		ok := false
		for _, c := range tc.ContainsAny {
			ok = ok || strings.Contains(low, strings.ToLower(c))
		}
		if !ok {
			return fmt.Sprintf("none of %s", show([]string(tc.ContainsAny)))
		}
	}
	for _, c := range tc.NotContains {
		if strings.Contains(low, strings.ToLower(c)) {
			return fmt.Sprintf("must not say %q", c)
		}
	}
	for _, r := range tc.Regex {
		if !regexp.MustCompile("(?i)" + r).MatchString(text) {
			return fmt.Sprintf("no match for /%s/", r)
		}
	}
	for _, r := range tc.NotRegex {
		if regexp.MustCompile("(?i)" + r).MatchString(text) {
			return fmt.Sprintf("must not match /%s/", r)
		}
	}
	return ""
}

func stateFails(st StateCheck, state any) string {
	vals, found := lookup(state, st.Path)
	if st.Value != nil {
		var v any
		if len(vals) > 0 {
			v = vals[0]
		}
		if why := valueFails(*st.Value, v, found); why != "" {
			return fmt.Sprintf("state %s: %s", st.Path, why)
		}
		return ""
	}
	var elems []any
	if found && len(vals) > 0 {
		if arr, ok := vals[0].([]any); ok {
			elems = arr
		} else if m, ok := vals[0].(map[string]any); ok {
			for _, k := range sortedKeys(m) {
				elems = append(elems, m[k])
			}
		}
	}
	n := 0
	var near string
	for _, e := range elems {
		em, _ := e.(map[string]any)
		ok := true
		for _, k := range sortedKeys(st.Where) {
			vs, f := lookup(em, k)
			var v any
			if len(vs) > 0 {
				v = vs[0]
			}
			if why := valueFails(st.Where[k], v, f); why != "" {
				ok = false
				if near == "" {
					near = k + ": " + why
				}
				break
			}
		}
		if ok {
			n++
		}
	}
	count := st.Count
	if count == nil {
		one := 1.0
		count = &ValueCheck{Min: &one}
	}
	if why := valueFails(*count, float64(n), true); why != "" {
		msg := fmt.Sprintf("state %s: %d of %d match, count %s", st.Path, n, len(elems), why)
		if near != "" && n == 0 {
			msg += " (closest miss: " + near + ")"
		}
		return msg
	}
	return ""
}

// lookup resolves a dot path. A segment may be a map key, an array index, "#"
// (the array's length) or "*" (every element; returns all matches).
func lookup(root any, path string) ([]any, bool) {
	if path == "" {
		return []any{root}, true
	}
	cur := []any{root}
	for _, seg := range strings.Split(path, ".") {
		var next []any
		for _, c := range cur {
			switch node := c.(type) {
			case map[string]any:
				if seg == "*" {
					for _, k := range sortedKeys(node) {
						next = append(next, node[k])
					}
				} else if v, ok := node[seg]; ok {
					next = append(next, v)
				}
			case []any:
				switch seg {
				case "#":
					next = append(next, float64(len(node)))
				case "*":
					next = append(next, node...)
				default:
					if i, err := strconv.Atoi(seg); err == nil && i >= 0 && i < len(node) {
						next = append(next, node[i])
					}
				}
			}
		}
		if len(next) == 0 {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func looseEqual(a, b any) bool {
	if af, ok := asFloat(a); ok {
		if bf, ok := asFloat(b); ok {
			return math.Abs(af-bf) < 1e-9
		}
	}
	if ab, ok := a.(bool); ok {
		bb, ok := b.(bool)
		return ok && ab == bb
	}
	return strings.EqualFold(strings.TrimSpace(asString(a)), strings.TrimSpace(asString(b)))
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		t := strings.TrimSpace(strings.ReplaceAll(n, ",", ""))
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

func asString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func show(v any) string {
	s := asString(v)
	if _, isStr := v.(string); isStr || v == nil {
		s = strconv.Quote(s)
	}
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func uniq(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func uniqueJoin(xs []string, n int) string {
	u := uniq(xs)
	if len(u) > n {
		u = append(u[:n], fmt.Sprintf("+%d more", len(u)-n))
	}
	return strings.Join(u, "; ")
}
