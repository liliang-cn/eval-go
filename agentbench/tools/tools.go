// Package tools is AgentBench's benchmark tool environment: ten tools with
// fixed, deterministic behaviour, defined once and served two ways.
//
// The point of a tool-calling benchmark is that every agent under test is
// offered the SAME tools, so "did it pick the right one" is comparable across
// agents. That requires one definition with two front ends:
//
//   - cmd/benchagent — attaches them to an OpenAI-compatible /chat/completions
//     request, for scoring a raw model.
//   - cmd/benchmcp — serves them over MCP stdio, for scoring an agent that
//     brings its own runtime (goose, superai, openclaw, ...).
//
// Keeping the definitions here rather than in each command is not tidiness: two
// copies of the currency table would drift, and the day they disagree the two
// front ends stop measuring the same thing.
//
// Four of the ten are deliberate distractors — unit_convert vs convert_currency,
// get_stock_price vs web_search, set_reminder vs create_calendar_event — so a
// task can test whether an agent picks the *right* tool rather than *a* tool.
//
// Results are simulated but real enough to grade: the calculator actually
// evaluates, the currency table is complete across USD/EUR/GBP/JPY, and
// web_search returns canned facts for the questions the task set asks.
package tools

import (
	"fmt"
	"strconv"
	"strings"
)

// Tool is one benchmark tool: a name, a description the model sees, and a JSON
// Schema for its arguments. Front ends translate this into whatever shape their
// protocol wants.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema (object)
}

func str() map[string]any    { return map[string]any{"type": "string"} }
func number() map[string]any { return map[string]any{"type": "number"} }

func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// All returns the benchmark tool set, in a stable order.
func All() []Tool {
	return []Tool{
		{"get_weather", "Get the current weather for a city.",
			schema(map[string]any{"city": str()}, "city")},
		{"web_search", "Search the web for general facts or current information.",
			schema(map[string]any{"query": str()}, "query")},
		{"calculator", "Evaluate an arithmetic expression and return the number.",
			schema(map[string]any{"expression": str()}, "expression")},
		{"send_email", "Send an email to a recipient.",
			schema(map[string]any{"to": str(), "subject": str(), "body": str()}, "to", "body")},
		{"create_calendar_event", "Create a dated calendar event.",
			schema(map[string]any{"title": str(), "date": str(), "time": str()}, "title", "date")},
		{"convert_currency", "Convert a MONEY amount between currencies (USD, EUR, GBP, JPY).",
			schema(map[string]any{"amount": number(), "from": str(), "to": str()}, "amount", "from", "to")},

		// --- distractors: plausible but wrong for specific tasks ---
		{"unit_convert", "Convert a PHYSICAL unit (length, weight, temperature): km, mi, kg, lb, C, F.",
			schema(map[string]any{"value": number(), "from_unit": str(), "to_unit": str()}, "value", "from_unit", "to_unit")},
		{"get_stock_price", "Get the current stock price for a ticker symbol.",
			schema(map[string]any{"ticker": str()}, "ticker")},
		{"set_reminder", "Set a short-term personal reminder (relative time, no fixed date).",
			schema(map[string]any{"text": str(), "time": str()}, "text")},
		{"translate", "Translate text into another language.",
			schema(map[string]any{"text": str(), "to_lang": str()}, "text", "to_lang")},
	}
}

// OpenAIDefinitions renders the tool set as OpenAI `tools` request entries.
func OpenAIDefinitions() []map[string]any {
	all := All()
	out := make([]map[string]any, 0, len(all))
	for _, t := range all {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": t.Name, "description": t.Description, "parameters": t.Schema,
			},
		})
	}
	return out
}

// rates covers every ordered pair of USD/EUR/GBP/JPY, so a task may convert in
// either direction without silently falling back to a 1:1 rate.
var rates = map[string]float64{
	"USD>EUR": 0.92, "EUR>USD": 1.09, "USD>GBP": 0.79, "GBP>USD": 1.27,
	"USD>JPY": 155, "JPY>USD": 0.0065, "EUR>GBP": 0.86, "GBP>EUR": 1.16,
	"EUR>JPY": 168, "JPY>EUR": 0.0059, "GBP>JPY": 196, "JPY>GBP": 0.0051,
}

var units = map[string]float64{
	"km>mi": 0.621371, "mi>km": 1.60934, "kg>lb": 2.20462, "lb>kg": 0.453592,
}

// Run executes one tool and returns its result. Unknown names return a generic
// ok rather than an error: a benchmark scores the agent's choice, and an
// invented tool name is already captured by tool_correctness.
func Run(name string, args map[string]any) any {
	switch name {
	case "calculator":
		v, err := EvalExpr(fmt.Sprint(args["expression"]))
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return map[string]any{"result": v}

	case "convert_currency":
		amt, _ := toFloat(args["amount"])
		from := strings.ToUpper(strings.TrimSpace(fmt.Sprint(args["from"])))
		to := strings.ToUpper(strings.TrimSpace(fmt.Sprint(args["to"])))
		r, ok := rates[from+">"+to]
		if !ok {
			if from == to {
				r = 1
			} else {
				return map[string]any{"error": fmt.Sprintf("no rate for %s>%s", from, to)}
			}
		}
		return map[string]any{"converted": amt * r, "currency": to, "rate": r}

	case "get_weather":
		return map[string]any{"city": args["city"], "condition": "sunny", "temp_c": 24}

	case "web_search":
		return map[string]any{"results": []string{cannedFact(fmt.Sprint(args["query"]))}}

	case "send_email":
		return map[string]any{"status": "sent", "to": args["to"]}

	case "create_calendar_event":
		return map[string]any{"status": "created", "title": args["title"], "date": args["date"]}

	case "unit_convert":
		v, _ := toFloat(args["value"])
		fu := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["from_unit"])))
		tu := strings.ToLower(strings.TrimSpace(fmt.Sprint(args["to_unit"])))
		switch {
		case fu == "c" && tu == "f":
			return map[string]any{"converted": v*9/5 + 32, "unit": "F"}
		case fu == "f" && tu == "c":
			return map[string]any{"converted": (v - 32) * 5 / 9, "unit": "C"}
		}
		f, ok := units[fu+">"+tu]
		if !ok {
			if fu == tu {
				f = 1
			} else {
				return map[string]any{"error": fmt.Sprintf("no conversion for %s>%s", fu, tu)}
			}
		}
		return map[string]any{"converted": v * f, "unit": tu}

	case "get_stock_price":
		return map[string]any{"ticker": args["ticker"], "price": 231.4, "currency": "USD"}

	case "set_reminder":
		return map[string]any{"status": "reminder set", "text": args["text"]}

	case "translate":
		return map[string]any{"translated": "[" + fmt.Sprint(args["to_lang"]) + "] " + fmt.Sprint(args["text"])}
	}
	return map[string]any{"status": "ok"}
}

// cannedFact answers the lookups the task set actually asks, so a web_search
// task can be graded on its answer rather than on whatever the live web says
// that day. Anything else gets a neutral placeholder.
func cannedFact(q string) string {
	l := strings.ToLower(q)
	switch {
	case strings.Contains(l, "japan") && strings.Contains(l, "popul"):
		return "Japan's population is approximately 124 million (2024)."
	case strings.Contains(l, "iphone"):
		return "The latest iPhone starts at $799 in the US."
	case strings.Contains(l, "vitamin d"):
		return "The recommended daily vitamin D intake is 600-800 IU for most adults."
	case strings.Contains(l, "pride and prejudice"):
		return "'Pride and Prejudice' was written by Jane Austen (1813)."
	case strings.Contains(l, "capital") && strings.Contains(l, "france"):
		return "The capital of France is Paris."
	default:
		return "Relevant result for: " + q
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// ---- arithmetic evaluator: + - * / % , parentheses, decimals ----

// EvalExpr evaluates an arithmetic expression. It exists so the calculator tool
// really computes: a task whose answer is "84" is only gradeable if the tool
// that produced it did the multiplication.
func EvalExpr(s string) (float64, error) {
	p := &parser{s: strings.ReplaceAll(s, " ", "")}
	v, err := p.expr()
	if err != nil {
		return 0, err
	}
	if p.pos != len(p.s) {
		return 0, fmt.Errorf("unexpected %q", p.s[p.pos:])
	}
	return v, nil
}

type parser struct {
	s   string
	pos int
}

func (p *parser) peek() byte {
	if p.pos < len(p.s) {
		return p.s[p.pos]
	}
	return 0
}

func (p *parser) expr() (float64, error) { // + -
	v, err := p.term()
	if err != nil {
		return 0, err
	}
	for p.peek() == '+' || p.peek() == '-' {
		op := p.peek()
		p.pos++
		r, err := p.term()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			v += r
		} else {
			v -= r
		}
	}
	return v, nil
}

func (p *parser) term() (float64, error) { // * / %
	v, err := p.factor()
	if err != nil {
		return 0, err
	}
	for p.peek() == '*' || p.peek() == '/' || p.peek() == '%' {
		op := p.peek()
		p.pos++
		r, err := p.factor()
		if err != nil {
			return 0, err
		}
		switch op {
		case '*':
			v *= r
		case '/':
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			v /= r
		case '%':
			if int(r) == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			v = float64(int(v) % int(r))
		}
	}
	return v, nil
}

func (p *parser) factor() (float64, error) {
	if p.peek() == '(' {
		p.pos++
		v, err := p.expr()
		if err != nil {
			return 0, err
		}
		if p.peek() != ')' {
			return 0, fmt.Errorf("missing )")
		}
		p.pos++
		return v, nil
	}
	if p.peek() == '-' {
		p.pos++
		v, err := p.factor()
		return -v, err
	}
	start := p.pos
	for p.pos < len(p.s) && (p.s[p.pos] >= '0' && p.s[p.pos] <= '9' || p.s[p.pos] == '.') {
		p.pos++
	}
	if start == p.pos {
		return 0, fmt.Errorf("expected number at %q", p.s[p.pos:])
	}
	return strconv.ParseFloat(p.s[start:p.pos], 64)
}
