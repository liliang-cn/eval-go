package evalgo

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Dataset helpers load Samples from common formats and slice them by metadata,
// so a golden set can live as JSON, JSONL, or a spreadsheet export and still
// feed a Suite. All loaders are stdlib-only.

// LoadJSON reads a JSON array of Samples.
func LoadJSON(r io.Reader) ([]Sample, error) {
	var samples []Sample
	if err := json.NewDecoder(r).Decode(&samples); err != nil {
		return nil, fmt.Errorf("parse JSON (want an array of samples): %w", err)
	}
	return samples, nil
}

// LoadJSONL reads one Sample per line (JSON Lines) — the convenient append-only
// format for large or streamed datasets. Blank lines are skipped.
func LoadJSONL(r io.Reader) ([]Sample, error) {
	var samples []Sample
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // allow long rows
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var s Sample
		if err := json.Unmarshal([]byte(text), &s); err != nil {
			return nil, fmt.Errorf("JSONL line %d: %w", line, err)
		}
		samples = append(samples, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return samples, nil
}

// LoadCSV reads Samples from a CSV with a header row. Recognized columns
// (case-insensitive) map to Sample fields: name, input, output, expected,
// rubric, plan, persona. "context" splits on '|' into chunks; "expected_tools"
// splits on ','. Any other column becomes a Meta entry — so a "tag" or
// "category" column is filterable via FilterMeta.
func LoadCSV(r io.Reader) ([]Sample, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse CSV: %w", err)
	}
	if len(rows) < 1 {
		return nil, fmt.Errorf("CSV has no header row")
	}
	header := make([]string, len(rows[0]))
	for i, h := range rows[0] {
		header[i] = strings.ToLower(strings.TrimSpace(h))
	}

	var samples []Sample
	for _, row := range rows[1:] {
		var s Sample
		for i, col := range header {
			if i >= len(row) {
				break
			}
			val := strings.TrimSpace(row[i])
			if val == "" {
				continue
			}
			switch col {
			case "name":
				s.Name = val
			case "input":
				s.Input = val
			case "output":
				s.Output = val
			case "expected":
				s.Expected = val
			case "rubric":
				s.Rubric = val
			case "plan":
				s.Plan = val
			case "persona":
				s.Persona = val
			case "context":
				s.Context = splitTrim(val, "|")
			case "expected_tools":
				s.ExpectedTools = splitTrim(val, ",")
			default:
				if s.Meta == nil {
					s.Meta = map[string]string{}
				}
				s.Meta[col] = val
			}
		}
		samples = append(samples, s)
	}
	return samples, nil
}

// FilterMeta returns the samples whose Meta[key] equals value — e.g. select one
// red-team attack kind with FilterMeta(samples, "attack", "jailbreak").
func FilterMeta(samples []Sample, key, value string) []Sample {
	return Filter(samples, func(s Sample) bool { return s.Meta[key] == value })
}

// Filter returns the samples for which keep returns true.
func Filter(samples []Sample, keep func(Sample) bool) []Sample {
	out := make([]Sample, 0, len(samples))
	for _, s := range samples {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

func splitTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
