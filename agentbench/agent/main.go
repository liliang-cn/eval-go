// Command benchagent is an eval-go ExecTarget: drive one OpenAI-compatible model
// through a bounded tool-calling loop and print an eval-go RunOutput JSON.
//
// Works against any OpenAI-compatible /chat/completions endpoint — local Ollama
// (http://localhost:11434/v1) or a cloud gateway — selected by env:
//
//	OPENAI_BASE_URL, OPENAI_API_KEY, OPENAI_MODEL, EVAL_INPUT
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---- tool schema offered to the model ----

func tool(name, desc string, props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "function", "function": map[string]any{
		"name": name, "description": desc,
		"parameters": map[string]any{"type": "object", "properties": props, "required": required},
	}}
}

func str() map[string]any { return map[string]any{"type": "string"} }

var tools = []map[string]any{
	tool("get_weather", "Get the current weather for a city.", map[string]any{"city": str()}, "city"),
	tool("web_search", "Search the web for general facts or current information.", map[string]any{"query": str()}, "query"),
	tool("calculator", "Evaluate an arithmetic expression and return the number.", map[string]any{"expression": str()}, "expression"),
	tool("send_email", "Send an email to a recipient.", map[string]any{"to": str(), "subject": str(), "body": str()}, "to", "body"),
	tool("create_calendar_event", "Create a dated calendar event.", map[string]any{"title": str(), "date": str(), "time": str()}, "title", "date"),
	tool("convert_currency", "Convert a MONEY amount between currencies (USD, EUR, GBP, JPY).", map[string]any{"amount": map[string]any{"type": "number"}, "from": str(), "to": str()}, "amount", "from", "to"),
	// --- distractor tools: plausible but wrong for specific tasks ---
	tool("unit_convert", "Convert a PHYSICAL unit (length, weight, temperature): km, mi, kg, lb, C, F.", map[string]any{"value": map[string]any{"type": "number"}, "from_unit": str(), "to_unit": str()}, "value", "from_unit", "to_unit"),
	tool("get_stock_price", "Get the current stock price for a ticker symbol.", map[string]any{"ticker": str()}, "ticker"),
	tool("set_reminder", "Set a short-term personal reminder (relative time, no fixed date).", map[string]any{"text": str(), "time": str()}, "text"),
	tool("translate", "Translate text into another language.", map[string]any{"text": str(), "to_lang": str()}, "text", "to_lang"),
}

// ---- simulated tool execution (real enough that answers are gradeable) ----

var rates = map[string]float64{
	"USD>EUR": 0.92, "EUR>USD": 1.09, "USD>GBP": 0.79, "GBP>USD": 1.27,
	"USD>JPY": 155, "JPY>USD": 0.0065, "EUR>GBP": 0.86, "GBP>EUR": 1.16,
	"EUR>JPY": 168, "JPY>EUR": 0.0059, "GBP>JPY": 196, "JPY>GBP": 0.0051,
}

func runTool(name string, args map[string]any) any {
	switch name {
	case "calculator":
		v, err := evalExpr(fmt.Sprint(args["expression"]))
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		return map[string]any{"result": v}
	case "convert_currency":
		amt, _ := toFloat(args["amount"])
		from := strings.ToUpper(fmt.Sprint(args["from"]))
		to := strings.ToUpper(fmt.Sprint(args["to"]))
		r := rates[from+">"+to]
		if r == 0 {
			r = 1
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
		fu := strings.ToLower(fmt.Sprint(args["from_unit"]))
		tu := strings.ToLower(fmt.Sprint(args["to_unit"]))
		f := map[string]float64{"km>mi": 0.621371, "mi>km": 1.60934, "kg>lb": 2.20462, "lb>kg": 0.453592}[fu+">"+tu]
		if fu == "c" && tu == "f" {
			return map[string]any{"converted": v*9/5 + 32, "unit": "F"}
		}
		if fu == "f" && tu == "c" {
			return map[string]any{"converted": (v - 32) * 5 / 9, "unit": "C"}
		}
		if f == 0 {
			f = 1
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

// cannedFact returns a plausible factual snippet so web_search tasks are gradeable.
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

// ---- OpenAI-compatible chat ----

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
		"model": model, "messages": msgs, "tools": tools, "temperature": 0,
	})
	req, _ := http.NewRequest("POST", strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	client := &http.Client{Timeout: 240 * time.Second}
	resp, err := client.Do(req)
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
	maxSteps := 5

	msgs := []message{
		{Role: "system", Content: "You are a helpful assistant with access to tools. Call the appropriate tool(s) to accomplish the user's request, then give a brief final answer."},
		{Role: "user", Content: input},
	}

	type tcOut struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	var toolCalls []tcOut
	var trajectory []string
	var final string

	for step := 0; step < maxSteps; step++ {
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
			toolCalls = append(toolCalls, tcOut{Name: c.Function.Name, Args: args})
			aj, _ := json.Marshal(args)
			trajectory = append(trajectory, fmt.Sprintf("called %s(%s)", c.Function.Name, aj))
			result, _ := json.Marshal(runTool(c.Function.Name, args))
			msgs = append(msgs, message{Role: "tool", ToolCallID: c.ID, Content: string(result)})
		}
	}

	out, _ := json.Marshal(map[string]any{"output": final, "tool_calls": toolCalls, "trajectory": trajectory})
	fmt.Println(string(out))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		f, err := strconv.ParseFloat(n, 64)
		return f, err == nil
	}
	return 0, false
}

// ---- tiny arithmetic evaluator (+ - * / % and parentheses, decimals) ----

func evalExpr(s string) (float64, error) {
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
			v /= r
		case '%':
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
