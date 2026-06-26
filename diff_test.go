package evalgo

import (
	"strings"
	"testing"
)

func mkReport(results map[string][]Result) Report {
	var r Report
	for name, res := range results {
		r.Samples = append(r.Samples, SampleReport{Sample: name, Results: res})
	}
	return r
}

func TestDiffReports(t *testing.T) {
	old := mkReport(map[string][]Result{
		"s1": {{Metric: "faithfulness", Score: 1, Passed: true}},
		"s2": {{Metric: "faithfulness", Score: 0, Passed: false}},
		"s3": {{Metric: "rubric", Score: 0.8, Passed: true}}, // removed in new
	})
	newR := mkReport(map[string][]Result{
		"s1": {{Metric: "faithfulness", Score: 0, Passed: false}}, // regressed
		"s2": {{Metric: "faithfulness", Score: 1, Passed: true}},  // fixed
		"s4": {{Metric: "rubric", Score: 0.9, Passed: true}},      // added
	})

	d := DiffReports(old, newR)
	if d.Regressions() != 1 {
		t.Errorf("want 1 regression, got %d", d.Regressions())
	}
	// regression must sort first
	if d[0].Status != Regressed || d[0].Sample != "s1" {
		t.Errorf("regression should sort first, got %+v", d[0])
	}

	status := map[string]ChangeStatus{}
	for _, x := range d {
		status[x.Sample] = x.Status
	}
	if status["s2"] != Fixed || status["s3"] != Removed || status["s4"] != Added {
		t.Errorf("unexpected statuses: %+v", status)
	}

	var b strings.Builder
	d.WriteConsole(&b)
	if !strings.Contains(b.String(), "REGRESSION") {
		t.Errorf("console should report regression:\n%s", b.String())
	}
}

func TestLoadReportRoundTrip(t *testing.T) {
	rep := Report{Samples: []SampleReport{
		{Sample: "x", Passed: true, Results: []Result{{Metric: "m", Score: 1, Passed: true}}},
	}}
	var b strings.Builder
	if err := rep.WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	got, err := LoadReport(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 1 || got.Samples[0].Results[0].Metric != "m" {
		t.Errorf("round-trip lost data: %+v", got)
	}
}
