# Feature spec: Judge Alignment (judge meta-evaluation)

## Problem

Eval-Go has LLM-as-a-judge metrics (`faithfulness`, `rubric`, `task_completion`, …)
but no way to know whether the judge **itself** is trustworthy. If the judge
disagrees with humans, every score it produces — and every `--fail-under` gate
built on it — is noise. Today the judge is an unmeasured black box.

This adds a mode that measures **judge ↔ human agreement** on a small labeled set,
gates on it in CI, and compares judge configs — so a green eval run actually means
something.

## Concept

Give a small subset of samples a **human gold judgment** for a metric. Run the
judge on that subset. Compare the judge's verdict/score against the human label →
agreement statistics. Validate (and tune) the judge *before* trusting it on the
unlabeled bulk.

## Data model (one minimal, backward-compatible addition)

Add to `Sample`:

```go
// human gold score per metric name, normalized 0..1 (binary: 1=pass, 0=fail).
Labels map[string]float64 `json:"labels,omitempty"`
```

A sample joins the alignment set for metric `M` iff `Labels[M]` exists. `omitempty`
keeps every existing dataset and code path untouched. Labels live in the same
JSON/JSONL/CSV the loaders already parse (CSV: `label.<metric>` columns).

## API

```go
// AlignMetric runs `metric` over the labeled subset and compares each judge
// Result to Sample.Labels[labelKey]. labelKey is the name the user labels by —
// normally the same name used to request the metric (the CLI `-m` name), which
// can differ from metric.Name() (e.g. `nonempty` vs the metric's `non_empty`).
// Reuses Suite.Run mechanics (bounded concurrency, the shared rate-limited
// JudgeFunc); it only post-processes the produced Results against the labels.
func AlignMetric(ctx context.Context, samples []Sample, metric Metric, labelKey string) (AlignmentResult, error)

type AlignmentResult struct {
    Metric string `json:"metric"`
    N      int    `json:"n"` // labeled samples actually scored

    // Binary agreement: judge Result.Passed  vs  (Labels[metric] >= 0.5)
    TP, FP, TN, FN int     `json:"tp","fp","tn","fn"`
    Accuracy       float64 `json:"accuracy"`
    Precision      float64 `json:"precision"`
    Recall         float64 `json:"recall"`
    F1             float64 `json:"f1"`
    Kappa          float64 `json:"kappa"` // Cohen's kappa (chance-corrected)

    // Continuous agreement: judge Result.Score (0..1)  vs  human Labels[metric]
    Pearson, Spearman float64 `json:"pearson","spearman"`
    MAE, RMSE         float64 `json:"mae","rmse"`

    // Pass-threshold on Result.Score that maximizes accuracy vs the labels.
    // Lets you re-calibrate a metric's threshold to its human ground truth.
    BestThreshold float64 `json:"best_threshold"`

    Disagreements []Disagreement `json:"disagreements"` // judge != human, for inspection
}

type Disagreement struct {
    Sample      string  `json:"sample"`
    Human       float64 `json:"human"`
    JudgeScore  float64 `json:"judge_score"`
    JudgePassed bool    `json:"judge_passed"`
    Reason      string  `json:"reason"` // the judge's own Result.Reason
}
```

All stats are plain stdlib math — **keeps the core dependency-free**, consistent
with the rest of eval-go. Lives in a new `align.go` (+ `align_test.go`).

## CLI

```bash
evalgo -d labeled.json -m faithfulness,rubric --judge env --align \
       --align-min-kappa 0.6 -f json -o align.json
```

- `--align` — switch from *scoring* mode to *alignment* mode. Only metrics with at
  least one `Labels` entry in the dataset are aligned; the rest are skipped (warned).
- `--align-min-kappa F` / `--align-min-f1 F` — CI gate. Exit `1` if any aligned
  metric falls below the floor (same exit-code contract as `--fail-under`: `1` =
  gate fail, `2` = config error). This gates on **judge quality**, not system quality.
- Console output: per-metric confusion matrix, kappa/F1, best threshold, and the
  top-K disagreements with the judge's reasons.

## Judge comparison (reuse `diff.go`)

Run `--align` twice with different `--judge` endpoints or rubric prompts, then:

```bash
evalgo align-diff old-judge.json new-judge.json
```

reuses the `DiffReports` pattern to rank which judge config agrees better with the
humans. **Pick the judge model/prompt by measured alignment, not by vibes.**

## Why this is the missing piece

Everything around it already exists in eval-go — 27 metrics, RAGAS-style
decomposition, synthetic data, red-teaming, cost metering, and cross-version
regression gating. The one thing missing is making the **judge itself** a
first-class, measurable, gateable artifact. This closes that loop.

## Scope / non-goals (v1)

- Post-hoc over a labeled JSON/JSONL/CSV subset. **No human-labeling UI** — label in
  the dataset directly, or import from a sheet. (A web labeler is a separate, later
  concern; record-then-evaluate already fits this.)
- Stats are exact stdlib math; no new third-party deps in core.
- Future: suggest *which* unlabeled samples to label next (max judge uncertainty /
  near-threshold) to grow the gold set efficiently.
