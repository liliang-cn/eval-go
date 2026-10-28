package tools

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A profile is one tool world. "classic" is the original ten tools, kept
// byte-for-byte so old results stay comparable; "life" is a personal-assistant
// world of ~100 tools shaped like the ones a real assistant (SuperLeo) offers
// on every request, near-duplicates included.
type profile struct {
	tools    func() []Tool
	run      func(*call) any
	defaults func(*Env) map[string]any
	validate bool // check args against the schema before running
}

func (p profile) find(name string) (Tool, bool) {
	for _, t := range p.tools() {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

var profiles = map[string]profile{
	"classic": {
		tools: All,
		run:   func(c *call) any { return Run(c.name, c.args) },
	},
	"life": {
		tools:    lifeTools,
		run:      runLife,
		defaults: lifeDefaults,
		validate: true,
	},
}

var shanghai = func() *time.Location {
	l, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return l
}()

// call is one tool invocation with access to the environment's state.
type call struct {
	env  *Env
	st   map[string]any
	name string
	args map[string]any
}

func (c *call) str(k string) string {
	v, ok := c.args[k]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return fmt.Sprint(v)
}

func (c *call) num(k string) (float64, bool) {
	v, ok := c.args[k]
	if !ok {
		return 0, false
	}
	return toFloat(v)
}

func (c *call) has(k string) bool { _, ok := c.args[k]; return ok && c.args[k] != nil }

func (c *call) list(coll string) []any {
	l, _ := c.st[coll].([]any)
	return l
}

func (c *call) items(coll string) []map[string]any {
	var out []map[string]any
	for _, v := range c.list(coll) {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// nextID returns prefix_N, past every id already in the collection.
func (c *call) nextID(coll, prefix string) string {
	hi := 0
	for _, it := range c.items(coll) {
		id, _ := it["id"].(string)
		if n, err := strconv.Atoi(strings.TrimPrefix(id, prefix+"_")); err == nil && n > hi {
			hi = n
		}
	}
	return fmt.Sprintf("%s_%d", prefix, hi+1)
}

// create stores a copy of the args (plus extra) under a fresh id.
func (c *call) create(coll, prefix string, extra map[string]any) map[string]any {
	item := map[string]any{"id": c.nextID(coll, prefix)}
	for k, v := range c.args {
		item[k] = v
	}
	for k, v := range extra {
		item[k] = v
	}
	item["created_at"] = c.env.now().Format(time.RFC3339)
	appendList(c.st, coll, item)
	return item
}

func (c *call) byID(coll, idArg string) (map[string]any, map[string]any) {
	id := c.str(idArg)
	for _, it := range c.items(coll) {
		if it["id"] == id {
			return it, nil
		}
	}
	return nil, errResult("NOT_FOUND", false, "no %s with %s %q", strings.TrimSuffix(coll, "s"), idArg, id)
}

func (c *call) remove(coll, id string) {
	var kept []any
	for _, v := range c.list(coll) {
		if m, ok := v.(map[string]any); ok && m["id"] == id {
			continue
		}
		kept = append(kept, v)
	}
	if kept == nil {
		kept = []any{}
	}
	c.st[coll] = kept
}

// update copies the given fields (skipping the id argument) onto item.
func (c *call) update(item map[string]any, idArg string) {
	for k, v := range c.args {
		if k != idArg {
			item[k] = v
		}
	}
	item["updated_at"] = c.env.now().Format(time.RFC3339)
}

// page applies page_size / page_token pagination to a list result.
func (c *call) page(key string, items []map[string]any, defSize int) map[string]any {
	size := defSize
	if n, ok := c.num("page_size"); ok && n > 0 {
		size = int(n)
	}
	start := 0
	if tok := c.str("page_token"); tok != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(tok, "p"))
		if err != nil || n < 0 || n > len(items) {
			return errResult("INVALID_ARGUMENT", false, "invalid page_token %q", tok)
		}
		start = n
	}
	end := min(start+size, len(items))
	out := map[string]any{key: items[start:end], "total": len(items)}
	if items[start:end] == nil {
		out[key] = []map[string]any{}
	}
	if end < len(items) {
		out["next_page_token"] = fmt.Sprintf("p%d", end)
	}
	return out
}

func (c *call) timeArg(k string) (time.Time, bool) {
	s := c.str(k)
	if s == "" {
		return time.Time{}, false
	}
	t, err := parseTime(s, shanghai)
	return t, err == nil
}

func itemTime(it map[string]any, k string) (time.Time, bool) {
	s, _ := it[k].(string)
	if s == "" {
		return time.Time{}, false
	}
	t, err := parseTime(s, shanghai)
	return t, err == nil
}

// within filters items whose field k falls in [from, to] (either bound optional).
func (c *call) within(items []map[string]any, k string) []map[string]any {
	from, hasFrom := c.timeArg("from")
	to, hasTo := c.timeArg("to")
	if !hasFrom {
		if d := c.str("from"); d != "" {
			if t, err := time.ParseInLocation("2006-01-02", d, shanghai); err == nil {
				from, hasFrom = t, true
			}
		}
	}
	if !hasTo {
		if d := c.str("to"); d != "" {
			if t, err := time.ParseInLocation("2006-01-02", d, shanghai); err == nil {
				to, hasTo = t.Add(24*time.Hour-time.Second), true
			}
		}
	}
	var out []map[string]any
	for _, it := range items {
		t, ok := itemTime(it, k)
		if !ok {
			if d, _ := it[k].(string); d != "" {
				if tt, err := time.ParseInLocation("2006-01-02", d, shanghai); err == nil {
					t, ok = tt, true
				}
			}
		}
		if (hasFrom || hasTo) && !ok {
			continue
		}
		if hasFrom && t.Before(from) || hasTo && t.After(to) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func matchesQuery(it map[string]any, q string, fields ...string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	var hay strings.Builder
	for _, f := range fields {
		hay.WriteString(strings.ToLower(fmt.Sprint(it[f])))
		hay.WriteByte(' ')
	}
	h := hay.String()
	if strings.Contains(h, q) {
		return true
	}
	// A Chinese query has no spaces to split on: "王医生" should find "王丽医生"
	// the way a real address book does, so every Han character must appear.
	if hasHan(q) && !strings.Contains(q, " ") {
		for _, r := range q {
			if unicode.Is(unicode.Han, r) && !strings.ContainsRune(h, r) {
				return false
			}
		}
		return true
	}
	// every word of the query somewhere in the fields
	words := strings.Fields(q)
	if len(words) < 2 {
		return false
	}
	for _, w := range words {
		if !strings.Contains(h, w) {
			return false
		}
	}
	return true
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func hashOf(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func sortByTime(items []map[string]any, k string) {
	sort.SliceStable(items, func(i, j int) bool {
		a, _ := itemTime(items[i], k)
		b, _ := itemTime(items[j], k)
		return a.Before(b)
	})
}
