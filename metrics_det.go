package evalgo

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

// Deterministic metrics are code-based heuristics: fast, free, no LLM. Run these
// first as cheap gates before spending tokens on a judge.

// ValidJSON passes when Output is structurally valid JSON.
func ValidJSON() Metric {
	return MetricFunc{"valid_json", func(_ context.Context, s Sample) (Result, error) {
		var js json.RawMessage
		ok := json.Unmarshal([]byte(strings.TrimSpace(s.Output)), &js) == nil
		return pass("valid_json", ok, jsonReason(ok)), nil
	}}
}

// JSONHasFields passes when Output is a JSON object containing all given keys.
func JSONHasFields(fields ...string) Metric {
	return MetricFunc{"json_has_fields", func(_ context.Context, s Sample) (Result, error) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(s.Output)), &obj); err != nil {
			return pass("json_has_fields", false, "not a JSON object"), nil
		}
		var missing []string
		for _, f := range fields {
			if _, ok := obj[f]; !ok {
				missing = append(missing, f)
			}
		}
		ok := len(missing) == 0
		reason := "all fields present"
		if !ok {
			reason = "missing: " + strings.Join(missing, ", ")
		}
		return pass("json_has_fields", ok, reason), nil
	}}
}

// MatchesRegex passes when Output matches the pattern (e.g. a required format).
func MatchesRegex(pattern string) Metric {
	re := regexp.MustCompile(pattern)
	return MetricFunc{"matches_regex", func(_ context.Context, s Sample) (Result, error) {
		ok := re.MatchString(s.Output)
		return pass("matches_regex", ok, "/"+pattern+"/"), nil
	}}
}

// ForbidsRegex passes when Output does NOT match the pattern — a safety boundary
// (no secrets, no banned phrases, no leaked PII).
func ForbidsRegex(pattern string) Metric {
	re := regexp.MustCompile(pattern)
	return MetricFunc{"forbids_regex", func(_ context.Context, s Sample) (Result, error) {
		ok := !re.MatchString(s.Output)
		reason := "boundary respected"
		if !ok {
			reason = "matched forbidden /" + pattern + "/"
		}
		return pass("forbids_regex", ok, reason), nil
	}}
}

// Contains passes when Output contains substr (case-insensitive).
func Contains(substr string) Metric {
	return MetricFunc{"contains", func(_ context.Context, s Sample) (Result, error) {
		ok := strings.Contains(strings.ToLower(s.Output), strings.ToLower(substr))
		return pass("contains", ok, "want substring "+strconvQuote(substr)), nil
	}}
}

// ExactMatch passes when trimmed Output equals trimmed Expected.
func ExactMatch() Metric {
	return MetricFunc{"exact_match", func(_ context.Context, s Sample) (Result, error) {
		ok := strings.TrimSpace(s.Output) == strings.TrimSpace(s.Expected)
		return pass("exact_match", ok, ""), nil
	}}
}

// NonEmpty passes when Output has non-whitespace content.
func NonEmpty() Metric {
	return MetricFunc{"non_empty", func(_ context.Context, s Sample) (Result, error) {
		ok := strings.TrimSpace(s.Output) != ""
		return pass("non_empty", ok, ""), nil
	}}
}

var reCitation = regexp.MustCompile(`\[[A-Za-z0-9][\w\-]*\]`)

// CitationPresent passes when Output contains at least one [SOURCE]-style
// citation — a deterministic proxy for grounded, attributable RAG answers.
func CitationPresent() Metric {
	return MetricFunc{"citation_present", func(_ context.Context, s Sample) (Result, error) {
		hits := reCitation.FindAllString(s.Output, -1)
		ok := len(hits) > 0
		reason := "no [SOURCE] citation found"
		if ok {
			reason = "citations: " + strings.Join(hits, " ")
		}
		return pass("citation_present", ok, reason), nil
	}}
}

func jsonReason(ok bool) string {
	if ok {
		return "valid JSON"
	}
	return "invalid JSON"
}

func strconvQuote(s string) string { return `"` + s + `"` }
