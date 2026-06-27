package evalgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// ErrNoLabels is returned by AlignMetric when no sample in the set carries a
// human label for the metric — so the caller can skip it cleanly.
var ErrNoLabels = errors.New("no samples carry a label for this metric")

// Disagreement is one labeled sample where the judge and the human disagreed
// (on the pass/fail boundary), surfaced for inspection.
type Disagreement struct {
	Sample      string  `json:"sample"`
	Human       float64 `json:"human"`        // human gold score 0..1
	JudgeScore  float64 `json:"judge_score"`  // the judge's raw score 0..1
	JudgePassed bool    `json:"judge_passed"` // the judge's pass verdict
	Reason      string  `json:"reason,omitempty"`
}

// AlignmentResult reports how well a metric's judge agrees with human labels on
// the labeled subset. Binary stats compare judge.Passed against (label >= 0.5);
// continuous stats compare judge.Score against the raw label.
type AlignmentResult struct {
	Metric  string `json:"metric"`
	N       int    `json:"n"`       // labeled samples successfully scored
	Errored int    `json:"errored"` // labeled samples the judge errored on (excluded from N)

	// binary agreement — confusion matrix
	TP int `json:"tp"`
	FP int `json:"fp"`
	TN int `json:"tn"`
	FN int `json:"fn"`

	Accuracy  float64 `json:"accuracy"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
	Kappa     float64 `json:"kappa"` // Cohen's kappa (chance-corrected agreement)

	// continuous agreement (judge.Score vs human score)
	Pearson  float64 `json:"pearson"`
	Spearman float64 `json:"spearman"`
	MAE      float64 `json:"mae"`
	RMSE     float64 `json:"rmse"`

	// BestThreshold is the pass-threshold on judge.Score that best matches the
	// human binary labels — use it to re-calibrate the metric's threshold.
	BestThreshold float64 `json:"best_threshold"`

	Disagreements []Disagreement `json:"disagreements,omitempty"`
}

// AlignMetric runs metric over the samples that carry a human label under
// labelKey (Sample.Labels[labelKey]) and measures judge-vs-human agreement.
// labelKey is the name the user labels by — typically the same name used to
// request the metric (the CLI `-m` name), which can differ from metric.Name().
// It reuses Suite.Run, so the same bounded concurrency and rate-limited judge
// apply; it only post-processes the produced Results against the labels. Returns
// ErrNoLabels when the set has no labeled samples for labelKey.
func AlignMetric(ctx context.Context, samples []Sample, metric Metric, labelKey string) (AlignmentResult, error) {
	labeled := Filter(samples, func(s Sample) bool {
		_, ok := s.Labels[labelKey]
		return ok
	})
	res := AlignmentResult{Metric: labelKey}
	if len(labeled) == 0 {
		return res, ErrNoLabels
	}

	rep := Suite{Samples: labeled, Metrics: []Metric{metric}, Concurrency: 4}.Run(ctx)

	var judgeScores, humanScores []float64
	var humanPos []bool
	mName := metric.Name() // Results are keyed by the metric's own name
	for i, sr := range rep.Samples {
		r, ok := resultFor(sr.Results, mName)
		if !ok || r.Err != "" {
			res.Errored++
			continue
		}
		human := labeled[i].Labels[labelKey]
		hp := human >= 0.5
		switch {
		case hp && r.Passed:
			res.TP++
		case !hp && r.Passed:
			res.FP++
		case !hp && !r.Passed:
			res.TN++
		default:
			res.FN++
		}
		judgeScores = append(judgeScores, r.Score)
		humanScores = append(humanScores, human)
		humanPos = append(humanPos, hp)
		if r.Passed != hp {
			res.Disagreements = append(res.Disagreements, Disagreement{
				Sample: sr.Sample, Human: human, JudgeScore: r.Score,
				JudgePassed: r.Passed, Reason: r.Reason,
			})
		}
	}

	res.N = len(judgeScores)
	if res.N == 0 {
		return res, nil
	}

	n := float64(res.N)
	res.Accuracy = float64(res.TP+res.TN) / n
	res.Precision = ratio(res.TP, res.TP+res.FP)
	res.Recall = ratio(res.TP, res.TP+res.FN)
	if res.Precision+res.Recall > 0 {
		res.F1 = 2 * res.Precision * res.Recall / (res.Precision + res.Recall)
	}
	res.Kappa = cohensKappa(res.TP, res.FP, res.TN, res.FN)

	res.Pearson = pearson(judgeScores, humanScores)
	res.Spearman = pearson(ranks(judgeScores), ranks(humanScores))
	res.MAE, res.RMSE = errStats(judgeScores, humanScores)
	res.BestThreshold = bestThreshold(judgeScores, humanPos)
	return res, nil
}

// AlignmentReport is the alignment of several metrics, ready to write or gate.
type AlignmentReport []AlignmentResult

// Failing returns the metrics whose Kappa < minKappa or F1 < minF1. A floor of
// 0 disables that check. Use as the CI gate on judge quality.
func (a AlignmentReport) Failing(minKappa, minF1 float64) []string {
	var bad []string
	for _, r := range a {
		if (minKappa > 0 && r.Kappa < minKappa) || (minF1 > 0 && r.F1 < minF1) {
			bad = append(bad, r.Metric)
		}
	}
	return bad
}

// WriteJSON emits the machine-readable alignment report.
func (a AlignmentReport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(a)
}

// WriteConsole emits a human-readable per-metric agreement summary, with up to
// maxDisagreements example mismatches per metric (0 = none).
func (a AlignmentReport) WriteConsole(w io.Writer, maxDisagreements int) {
	fmt.Fprintln(w, "=== Eval-Go judge alignment ===")
	for _, r := range a {
		fmt.Fprintf(w, "\n%s  (n=%d", r.Metric, r.N)
		if r.Errored > 0 {
			fmt.Fprintf(w, ", %d errored", r.Errored)
		}
		fmt.Fprintln(w, ")")
		if r.N == 0 {
			fmt.Fprintln(w, "  (no scored samples)")
			continue
		}
		fmt.Fprintf(w, "  confusion: TP=%d FP=%d TN=%d FN=%d\n", r.TP, r.FP, r.TN, r.FN)
		fmt.Fprintf(w, "  accuracy=%.3f precision=%.3f recall=%.3f f1=%.3f kappa=%.3f\n",
			r.Accuracy, r.Precision, r.Recall, r.F1, r.Kappa)
		fmt.Fprintf(w, "  score: pearson=%.3f spearman=%.3f mae=%.3f rmse=%.3f  best_threshold=%.2f\n",
			r.Pearson, r.Spearman, r.MAE, r.RMSE, r.BestThreshold)
		fmt.Fprintf(w, "  agreement: %s\n", kappaLabel(r.Kappa))
		for i, d := range r.Disagreements {
			if i >= maxDisagreements {
				fmt.Fprintf(w, "  ...and %d more disagreements\n", len(r.Disagreements)-i)
				break
			}
			fmt.Fprintf(w, "  ✗ %s: human=%.2f judge=%.2f(%s) — %s\n",
				d.Sample, d.Human, d.JudgeScore, passStr(d.JudgePassed), truncate(d.Reason, 80))
		}
	}
}

// --- small stdlib-only stats helpers ---

func resultFor(rs []Result, name string) (Result, bool) {
	for _, r := range rs {
		if r.Metric == name {
			return r, true
		}
	}
	return Result{}, false
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// cohensKappa is chance-corrected agreement between the judge and human on the
// binary pass/fail call.
func cohensKappa(tp, fp, tn, fn int) float64 {
	n := float64(tp + fp + tn + fn)
	if n == 0 {
		return 0
	}
	po := float64(tp+tn) / n
	pPosJudge := float64(tp+fp) / n
	pPosHuman := float64(tp+fn) / n
	pe := pPosJudge*pPosHuman + (1-pPosJudge)*(1-pPosHuman)
	if 1-pe == 0 { // both always agree on one class
		if po == 1 {
			return 1
		}
		return 0
	}
	return (po - pe) / (1 - pe)
}

// pearson is the Pearson correlation of two equal-length series (0 if either is
// constant or lengths differ).
func pearson(x, y []float64) float64 {
	n := len(x)
	if n == 0 || len(y) != n {
		return 0
	}
	var sx, sy float64
	for i := range x {
		sx += x[i]
		sy += y[i]
	}
	mx, my := sx/float64(n), sy/float64(n)
	var cov, vx, vy float64
	for i := range x {
		dx, dy := x[i]-mx, y[i]-my
		cov += dx * dy
		vx += dx * dx
		vy += dy * dy
	}
	if vx == 0 || vy == 0 {
		return 0
	}
	return cov / math.Sqrt(vx*vy)
}

// ranks returns average (tie-corrected) ranks, so pearson(ranks(x),ranks(y))
// gives Spearman's rho.
func ranks(x []float64) []float64 {
	n := len(x)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return x[idx[a]] < x[idx[b]] })
	r := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && x[idx[j+1]] == x[idx[i]] {
			j++
		}
		avg := float64(i+j)/2.0 + 1.0 // 1-based average position of the tie group
		for k := i; k <= j; k++ {
			r[idx[k]] = avg
		}
		i = j + 1
	}
	return r
}

func errStats(judge, human []float64) (mae, rmse float64) {
	n := len(judge)
	if n == 0 {
		return 0, 0
	}
	var sumAbs, sumSq float64
	for i := range judge {
		d := judge[i] - human[i]
		sumAbs += math.Abs(d)
		sumSq += d * d
	}
	return sumAbs / float64(n), math.Sqrt(sumSq / float64(n))
}

// bestThreshold finds the pass-threshold t (predict pass iff score >= t) that
// maximizes agreement with the human binary labels, tie-broken toward 0.5.
func bestThreshold(scores []float64, humanPos []bool) float64 {
	if len(scores) == 0 {
		return 0.5
	}
	cands := append([]float64{0, 1.0001}, scores...) // include all-pass / all-fail
	sort.Float64s(cands)
	best, bestAcc := 0.5, -1.0
	for _, t := range cands {
		correct := 0
		for i, s := range scores {
			if (s >= t) == humanPos[i] {
				correct++
			}
		}
		acc := float64(correct) / float64(len(scores))
		if acc > bestAcc+1e-12 ||
			(math.Abs(acc-bestAcc) < 1e-12 && math.Abs(t-0.5) < math.Abs(best-0.5)) {
			best, bestAcc = t, acc
		}
	}
	return best
}

func kappaLabel(k float64) string {
	switch {
	case k < 0:
		return "worse than chance — do not trust this judge"
	case k < 0.2:
		return "slight"
	case k < 0.4:
		return "fair"
	case k < 0.6:
		return "moderate"
	case k < 0.8:
		return "substantial"
	default:
		return "almost perfect"
	}
}

func passStr(b bool) string {
	if b {
		return "pass"
	}
	return "fail"
}
