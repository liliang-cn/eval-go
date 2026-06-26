package evalgo

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// Report diffing compares two runs to catch regressions between versions — the
// CI question "did this change make any eval worse?". LoadReport reads a JSON
// report written by Report.WriteJSON; DiffReports pairs results by (sample,
// metric) and classifies each change.

// LoadReport parses a JSON report produced by Report.WriteJSON. Only the
// per-sample results are needed for diffing (summary/usage are ignored).
func LoadReport(r io.Reader) (Report, error) {
	var rep Report
	if err := json.NewDecoder(r).Decode(&rep); err != nil {
		return Report{}, fmt.Errorf("parse report JSON: %w", err)
	}
	return rep, nil
}

// ChangeStatus classifies one metric result's movement between two reports.
type ChangeStatus string

const (
	Regressed ChangeStatus = "regressed" // was passing, now failing
	Fixed     ChangeStatus = "fixed"     // was failing, now passing
	Declined  ChangeStatus = "declined"  // same pass state, lower score
	Improved  ChangeStatus = "improved"  // same pass state, higher score
	Unchanged ChangeStatus = "unchanged"
	Added     ChangeStatus = "added"   // present only in the new report
	Removed   ChangeStatus = "removed" // present only in the old report
)

// ScoreDelta is one metric's movement between two reports.
type ScoreDelta struct {
	Sample    string       `json:"sample"`
	Metric    string       `json:"metric"`
	Old       float64      `json:"old"`
	New       float64      `json:"new"`
	OldPassed bool         `json:"old_passed"`
	NewPassed bool         `json:"new_passed"`
	Status    ChangeStatus `json:"status"`
}

// Diff is the full comparison of two reports.
type Diff []ScoreDelta

// DiffReports compares old vs new by (sample, metric).
func DiffReports(old, new Report) Diff {
	type rk struct{ sample, metric string }
	oldByKey := map[rk]Result{}
	for _, sr := range old.Samples {
		for _, res := range sr.Results {
			oldByKey[rk{sr.Sample, res.Metric}] = res
		}
	}
	seen := map[rk]bool{}

	var out Diff
	for _, sr := range new.Samples {
		for _, res := range sr.Results {
			key := rk{sr.Sample, res.Metric}
			seen[key] = true
			d := ScoreDelta{Sample: sr.Sample, Metric: res.Metric, New: res.Score, NewPassed: res.Passed}
			if o, ok := oldByKey[key]; ok {
				d.Old, d.OldPassed = o.Score, o.Passed
				d.Status = classify(o, res)
			} else {
				d.Status = Added
			}
			out = append(out, d)
		}
	}
	// results present in old but gone from new
	for _, sr := range old.Samples {
		for _, res := range sr.Results {
			key := rk{sr.Sample, res.Metric}
			if !seen[key] {
				out = append(out, ScoreDelta{Sample: sr.Sample, Metric: res.Metric,
					Old: res.Score, OldPassed: res.Passed, Status: Removed})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i].Status) != rank(out[j].Status) {
			return rank(out[i].Status) < rank(out[j].Status)
		}
		if out[i].Sample != out[j].Sample {
			return out[i].Sample < out[j].Sample
		}
		return out[i].Metric < out[j].Metric
	})
	return out
}

func classify(old, new Result) ChangeStatus {
	switch {
	case old.Passed && !new.Passed:
		return Regressed
	case !old.Passed && new.Passed:
		return Fixed
	case new.Score > old.Score:
		return Improved
	case new.Score < old.Score:
		return Declined
	default:
		return Unchanged
	}
}

// rank orders statuses so regressions surface first in reports.
func rank(s ChangeStatus) int {
	switch s {
	case Regressed:
		return 0
	case Declined:
		return 1
	case Removed:
		return 2
	case Fixed:
		return 3
	case Improved:
		return 4
	case Added:
		return 5
	default:
		return 6
	}
}

// Regressions counts results that started passing and now fail.
func (d Diff) Regressions() int {
	n := 0
	for _, x := range d {
		if x.Status == Regressed {
			n++
		}
	}
	return n
}

// WriteConsole prints the diff, regressions first, ending with a verdict.
func (d Diff) WriteConsole(w io.Writer) {
	fmt.Fprintln(w, "=== Eval-Go diff ===")
	mark := map[ChangeStatus]string{
		Regressed: "✗ REGRESSED", Declined: "↓ declined", Removed: "- removed",
		Fixed: "✓ fixed", Improved: "↑ improved", Added: "+ added", Unchanged: "  unchanged",
	}
	shown := 0
	for _, x := range d {
		if x.Status == Unchanged {
			continue
		}
		shown++
		fmt.Fprintf(w, "  %-12s %s / %s: %.2f → %.2f\n", mark[x.Status], x.Sample, x.Metric, x.Old, x.New)
	}
	if shown == 0 {
		fmt.Fprintln(w, "  (no changes)")
	}
	reg := d.Regressions()
	if reg > 0 {
		fmt.Fprintf(w, "\nVerdict: %d REGRESSION(S) ❌\n", reg)
	} else {
		fmt.Fprintln(w, "\nVerdict: no regressions ✅")
	}
}
