package evalgo

import (
	"context"
	"errors"
	"math"
	"testing"
)

// fakeMetric returns a preset (score, passed) per sample name, so alignment math
// is tested without an LLM. out[name] = {score, passFlag(>=0.5 means pass)}.
func fakeMetric(name string, out map[string][2]float64) Metric {
	return MetricFunc{MetricName: name, Fn: func(_ context.Context, s Sample) (Result, error) {
		v := out[s.Name]
		return Result{Metric: name, Score: v[0], Passed: v[1] >= 0.5, Reason: "fake"}, nil
	}}
}

func lbl(name string, v float64) Sample {
	return Sample{Name: name, Labels: map[string]float64{"m": v}}
}

func approx(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("got %.6f, want %.6f", got, want)
	}
}

func TestAlignPerfectAgreement(t *testing.T) {
	samples := []Sample{lbl("A", 1), lbl("B", 0), lbl("C", 1), lbl("D", 0)}
	metric := fakeMetric("m", map[string][2]float64{
		"A": {0.9, 1}, "B": {0.1, 0}, "C": {0.8, 1}, "D": {0.2, 0},
	})
	r, err := AlignMetric(context.Background(), samples, metric, "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.N != 4 || r.TP != 2 || r.TN != 2 || r.FP != 0 || r.FN != 0 {
		t.Fatalf("confusion wrong: %+v", r)
	}
	approx(t, r.Accuracy, 1)
	approx(t, r.Precision, 1)
	approx(t, r.Recall, 1)
	approx(t, r.F1, 1)
	approx(t, r.Kappa, 1)
	if len(r.Disagreements) != 0 {
		t.Errorf("expected no disagreements, got %d", len(r.Disagreements))
	}
}

func TestAlignHalfAgreement(t *testing.T) {
	// TP, FN, FP, TN — one of each → accuracy .5, kappa 0.
	samples := []Sample{lbl("A", 1), lbl("B", 1), lbl("C", 0), lbl("D", 0)}
	metric := fakeMetric("m", map[string][2]float64{
		"A": {0.9, 1}, // TP
		"B": {0.4, 0}, // FN
		"C": {0.6, 1}, // FP
		"D": {0.1, 0}, // TN
	})
	r, err := AlignMetric(context.Background(), samples, metric, "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.TP != 1 || r.FN != 1 || r.FP != 1 || r.TN != 1 {
		t.Fatalf("confusion wrong: %+v", r)
	}
	approx(t, r.Accuracy, 0.5)
	approx(t, r.Precision, 0.5)
	approx(t, r.Recall, 0.5)
	approx(t, r.F1, 0.5)
	approx(t, r.Kappa, 0)
	if len(r.Disagreements) != 2 { // B and C
		t.Errorf("expected 2 disagreements, got %d", len(r.Disagreements))
	}
}

func TestAlignNoLabels(t *testing.T) {
	samples := []Sample{{Name: "A"}, {Name: "B"}} // no Labels at all
	_, err := AlignMetric(context.Background(), samples, fakeMetric("m", nil), "m")
	if !errors.Is(err, ErrNoLabels) {
		t.Fatalf("want ErrNoLabels, got %v", err)
	}
}

func TestAlignOnlyLabeledSubsetCounts(t *testing.T) {
	// Only A and C carry a label for "m"; B is unlabeled and must be ignored.
	samples := []Sample{
		lbl("A", 1),
		{Name: "B"}, // unlabeled → excluded
		lbl("C", 0),
	}
	metric := fakeMetric("m", map[string][2]float64{"A": {0.9, 1}, "B": {0.5, 1}, "C": {0.1, 0}})
	r, err := AlignMetric(context.Background(), samples, metric, "m")
	if err != nil {
		t.Fatal(err)
	}
	if r.N != 2 {
		t.Fatalf("expected N=2 (only labeled), got %d", r.N)
	}
}

func TestBestThresholdSeparates(t *testing.T) {
	got := bestThreshold([]float64{0.9, 0.8, 0.3, 0.2}, []bool{true, true, false, false})
	approx(t, got, 0.8) // lowest positive score that perfectly separates
}

func TestPearsonAndRanks(t *testing.T) {
	approx(t, pearson([]float64{1, 2, 3}, []float64{2, 4, 6}), 1)  // perfect positive
	approx(t, pearson([]float64{1, 2, 3}, []float64{6, 4, 2}), -1) // perfect negative
	approx(t, pearson([]float64{1, 1, 1}, []float64{1, 2, 3}), 0)  // constant → 0
	// Spearman of a monotonic-but-nonlinear pair is 1.
	approx(t, pearson(ranks([]float64{1, 2, 3}), ranks([]float64{1, 4, 9})), 1)
}

func TestKappaEdgeCases(t *testing.T) {
	approx(t, cohensKappa(0, 0, 0, 0), 0) // empty
	approx(t, cohensKappa(4, 0, 0, 0), 1) // all TP, all agree on one class
	approx(t, cohensKappa(2, 0, 2, 0), 1) // perfect, both classes
	approx(t, cohensKappa(1, 1, 1, 1), 0) // chance-level
}

func TestAlignmentReportFailing(t *testing.T) {
	rep := AlignmentReport{
		{Metric: "good", Kappa: 0.8, F1: 0.9},
		{Metric: "weak", Kappa: 0.3, F1: 0.5},
	}
	bad := rep.Failing(0.6, 0) // kappa floor only
	if len(bad) != 1 || bad[0] != "weak" {
		t.Fatalf("expected [weak], got %v", bad)
	}
	if len(rep.Failing(0, 0)) != 0 { // floors off → nothing fails
		t.Errorf("expected no failures with floors off")
	}
}
