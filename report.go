package evalgo

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// SampleReport holds every metric Result for one sample.
type SampleReport struct {
	Sample  string            `json:"sample"`
	Meta    map[string]string `json:"meta,omitempty"`
	Results []Result          `json:"results"`
	Passed  bool              `json:"passed"`
}

// Report is the full outcome of a Suite run.
type Report struct {
	Samples []SampleReport `json:"samples"`
	Usage   *Usage         `json:"usage,omitempty"` // judge usage, when metered (set by the caller)
}

// Failed reports whether any sample failed any metric — use as the CI exit gate.
func (r Report) Failed() bool {
	for _, s := range r.Samples {
		if !s.Passed {
			return true
		}
	}
	return false
}

// WriteJSON emits the machine-readable report (for CI artifacts / dashboards).
func (r Report) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Samples []SampleReport  `json:"samples"`
		Summary []MetricSummary `json:"summary"`
		Usage   *Usage          `json:"usage,omitempty"`
		Failed  bool            `json:"failed"`
	}{r.Samples, r.Summary(), r.Usage, r.Failed()})
}

// WriteConsole emits a human-readable report: per-sample metric grid + per-metric
// aggregates + overall verdict.
func (r Report) WriteConsole(w io.Writer) {
	fmt.Fprintln(w, "=== Eval-Go report ===")
	for _, s := range r.Samples {
		status := "PASS"
		if !s.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(w, "\n[%s] %s\n", status, s.Sample)
		for _, res := range s.Results {
			mark := "✓"
			if !res.Passed {
				mark = "✗"
			}
			line := fmt.Sprintf("    %s %-22s score=%.2f", mark, res.Metric, res.Score)
			if res.Err != "" {
				line += "  err=" + res.Err
			} else if res.Reason != "" {
				line += "  " + truncate(res.Reason, 80)
			}
			fmt.Fprintln(w, line)
		}
	}

	fmt.Fprintln(w, "\n--- metric summary ---")
	fmt.Fprintf(w, "%-24s %-12s %s\n", "METRIC", "PASS_RATE", "MEAN_SCORE")
	for _, ms := range r.Summary() {
		fmt.Fprintf(w, "%-24s %-12s %.2f\n", ms.Metric,
			fmt.Sprintf("%d/%d (%.0f%%)", ms.Passed, ms.Total, ms.PassRate*100), ms.MeanScore)
	}
	if u := r.Usage; u != nil {
		fmt.Fprintln(w, "\n--- judge usage ---")
		fmt.Fprintf(w, "calls         : %d\n", u.Calls)
		fmt.Fprintf(w, "est. tokens   : %d prompt + %d completion = %d\n", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
		fmt.Fprintf(w, "judge time    : %.1fs (cumulative)\n", u.JudgeSeconds)
		if u.Cost > 0 {
			fmt.Fprintf(w, "est. cost     : $%.4f\n", u.Cost)
		}
	}

	overall := "ALL PASSED ✅"
	if r.Failed() {
		overall = "FAILURES PRESENT ❌"
	}
	fmt.Fprintf(w, "\nOverall: %s\n", overall)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}
