package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	evalgo "github.com/liliang-cn/eval-go"
	"github.com/liliang-cn/eval-go/llmjudge"

	"github.com/spf13/cobra"
)

// judgeSalt keys the judge cache by the judge model, so switching LLM_MODEL
// never reuses another judge's verdicts.
func judgeSalt() string {
	return os.Getenv("LLM_MODEL") + "|" + os.Getenv("LLM_BASE_URL")
}

// judgeFromEnv builds the metered, rate-limited, cached judge every bench
// subcommand uses.
func judgeFromEnv(rps float64, cache string, costIn, costOut float64) (evalgo.JudgeFunc, *evalgo.Meter, error) {
	judge, err := llmjudge.FromEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("judge: %w", err)
	}
	if rps > 0 {
		judge = evalgo.RateLimit(judge, rps, 2)
	}
	meter := evalgo.NewMeter(costIn, costOut)
	judge = meter.Wrap(judge)
	return evalgo.CacheSalted(judge, cache, judgeSalt()), meter, nil
}

// benchReport is the JSON a bench run writes: every pass of every target, plus
// the statistics derived from them, so a reader need not recompute them.
type benchReport struct {
	evalgo.ComparisonReport
	Stats []evalgo.TargetStats      `json:"stats,omitempty"`
	Pairs []evalgo.PairedComparison `json:"pairs,omitempty"`
	Usage []evalgo.UsageStats       `json:"usage,omitempty"`
}

func withStats(cr evalgo.ComparisonReport) benchReport {
	br := benchReport{ComparisonReport: cr, Stats: cr.Stats(), Usage: cr.Usage()}
	for i := 0; i < len(br.Stats); i++ {
		for j := i + 1; j < len(br.Stats); j++ {
			if p, err := cr.Paired(br.Stats[i].Name, br.Stats[j].Name); err == nil {
				br.Pairs = append(br.Pairs, p)
			}
		}
	}
	return br
}

func splitCSV(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

// filterTasks keeps tasks whose meta matches every key=value in where. An
// unknown key or an empty result is an error: "--where domian=health" must not
// quietly run nothing (or everything).
func filterTasks(tasks []evalgo.Task, where string) ([]evalgo.Task, error) {
	if where == "" {
		return tasks, nil
	}
	out := tasks
	for _, cond := range splitCSV(where) {
		k, v, ok := strings.Cut(cond, "=")
		if !ok {
			return nil, fmt.Errorf("--where must be key=value[,key=value], got %q", cond)
		}
		known := false
		var kept []evalgo.Task
		for _, t := range out {
			if _, has := t.Meta[k]; has {
				known = true
			}
			if t.Meta[k] == v {
				kept = append(kept, t)
			}
		}
		if !known {
			return nil, fmt.Errorf("--where: no task has meta key %q", k)
		}
		out = kept
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--where %s matches no task", where)
	}
	return out, nil
}

// newBenchCmd runs the SAME tasks through SEVERAL agents and prints a task x
// agent PASS/FAIL grid — a cross-framework / cross-config agent benchmark, all
// from config files.
func newBenchCmd() *cobra.Command {
	var (
		tasksPath   string
		targetsPath string
		metricNames string
		gate        string
		judgeMode   string
		out         string
		concurrency int
		rps         float64
		threshold   float64
		timeout     time.Duration
		cache       string
		costIn      float64
		costOut     float64
		runs        int
		where       string
		only        string
		perTask     bool
	)
	cmd := &cobra.Command{
		Use:   "bench",
		Short: "Run tasks through several agents and compare them (PASS/FAIL grid)",
		Long: "bench loads a tasks JSON (array of {name,input,turns,check,rubric,tool_script,...})\n" +
			"and a targets JSON (array of {name,command,dir,env}), runs every task through\n" +
			"every agent as a subprocess, scores each run, and prints a task x agent grid,\n" +
			"pass@1 with a confidence interval, flaky tasks, a paired comparison of every\n" +
			"two targets, and what each attempt cost.\n\n" +
			"Each agent program receives the task via EVAL_* env vars (and as JSON on stdin)\n" +
			"and prints a JSON RunOutput to stdout. The judge uses LLM_BASE_URL/LLM_API_KEY/LLM_MODEL.\n\n" +
			"The default gate is auto: a task is gated by rubric_judge when it has a rubric\n" +
			"and by tool_trajectory when it has a check; task_completion gates only tasks\n" +
			"with neither, or tasks that list it in their own \"gate\".",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tasksRaw, err := readInput(tasksPath)
			if err != nil {
				return fmt.Errorf("read tasks: %w", err)
			}
			tasks, err := evalgo.LoadTasks(strings.NewReader(string(tasksRaw)))
			if err != nil {
				return err
			}
			if tasks, err = filterTasks(tasks, where); err != nil {
				return err
			}
			targetsRaw, err := readInput(targetsPath)
			if err != nil {
				return fmt.Errorf("read targets: %w", err)
			}
			targets, err := evalgo.LoadTargets(strings.NewReader(string(targetsRaw)))
			if err != nil {
				return err
			}
			if only != "" {
				var kept []evalgo.NamedTarget
				want := splitCSV(only)
				for _, w := range want {
					found := false
					for _, t := range targets {
						if t.Name == w {
							kept = append(kept, t)
							found = true
						}
					}
					if !found {
						var have []string
						for _, t := range targets {
							have = append(have, t.Name)
						}
						return fmt.Errorf("--only: no target %q (have: %s)", w, strings.Join(have, ", "))
					}
				}
				targets = kept
			}
			if runs < 1 {
				return fmt.Errorf("--runs must be >= 1, got %d", runs)
			}

			var judge evalgo.JudgeFunc
			var meter *evalgo.Meter
			switch judgeMode {
			case "env":
				if judge, meter, err = judgeFromEnv(rps, cache, costIn, costOut); err != nil {
					return err
				}
			case "none":
			default:
				return fmt.Errorf("unknown --judge mode %q (use env|none)", judgeMode)
			}

			metrics, err := evalgo.BuildMetrics(splitCSV(metricNames), evalgo.MetricSpec{Judge: judge, Threshold: threshold})
			if err != nil {
				return err
			}
			gateNames, err := evalgo.ResolveGate(splitCSV(gate), metrics, tasks)
			if err != nil {
				return err
			}

			cmp := evalgo.Comparison{
				Targets:     targets,
				Tasks:       tasks,
				Metrics:     metrics,
				Gate:        gateNames,
				Concurrency: concurrency,
				Timeout:     timeout,
				Runs:        runs,
				OnResult: func(target string, task evalgo.Task, s evalgo.Sample, err error) {
					status := "ok"
					if err != nil {
						status = "ERR: " + err.Error()
					}
					wall := 0.0
					if s.Usage != nil {
						wall = float64(s.Usage.WallMs) / 1000
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "  %-12s %-34s %s (%d tool calls, %.1fs)\n", target, task.Name, status, len(s.ToolCalls), wall)
				},
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "running %d tasks x %d agents x %d run(s) ...\n", len(tasks), len(targets), runs)
			report := cmp.Run(cmd.Context())

			w := cmd.OutOrStdout()
			fmt.Fprintln(w, "\n=== AGENT COMPARISON ===")
			report.RenderGrid(w)
			report.WriteStats(w)
			report.WriteUsage(w)
			if perTask {
				report.WritePerTask(w)
			}
			if meter != nil {
				u := meter.Usage()
				fmt.Fprintf(cmd.ErrOrStderr(), "\njudge: %d calls, ~%d tokens, %.1fs", u.Calls, u.TotalTokens, u.JudgeSeconds)
				if u.Cost > 0 {
					fmt.Fprintf(cmd.ErrOrStderr(), ", ~$%.4f", u.Cost)
				}
				fmt.Fprintln(cmd.ErrOrStderr())
			}
			if out != "" {
				data, err := json.MarshalIndent(withStats(report), "", "  ")
				if err != nil {
					return err
				}
				return os.WriteFile(out, data, 0o644)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&tasksPath, "tasks", "", "JSON file of tasks; '-' for stdin (required)")
	f.StringVar(&targetsPath, "targets", "", "JSON file of agents (array of {name,command,dir,env}); '-' for stdin (required)")
	f.StringVarP(&metricNames, "metrics", "m", "tool_correctness,tool_trajectory,task_completion,rubric_judge", "comma-separated metrics to score each run ("+strings.Join(evalgo.RegisteredMetrics(), ",")+")")
	f.StringVar(&gate, "gate", evalgo.GateAuto, "metrics that must pass for a cell to PASS: auto (per task, see above), a comma list, or empty = all metrics")
	f.StringVar(&judgeMode, "judge", "env", `judge mode: "env" (OpenAI-compatible via LLM_* env) or "none"`)
	f.StringVarP(&out, "out", "o", "", "write the full comparison report (JSON) here")
	f.IntVar(&concurrency, "concurrency", 1, "tasks run in parallel per agent")
	f.IntVar(&runs, "runs", 1, "repeat the whole comparison N times (use 3 to rank agents): pass@1 with a CI, pass@k / pass^k, flaky tasks")
	f.Float64Var(&rps, "rps", 4, "judge rate limit (requests/sec); <=0 disables")
	f.Float64Var(&threshold, "threshold", 0.5, "pass threshold for judge metrics")
	f.DurationVar(&timeout, "timeout", 8*time.Minute, "per-task wall-clock timeout")
	f.StringVar(&cache, "cache", "", "directory to cache judge responses in (keyed by prompt and judge model)")
	f.Float64Var(&costIn, "cost-in", 0, "USD per 1M prompt tokens (for the judge cost estimate)")
	f.Float64Var(&costOut, "cost-out", 0, "USD per 1M completion tokens (for the judge cost estimate)")
	f.StringVar(&where, "where", "", "only run tasks whose meta matches key=value[,key=value] (e.g. lang=zh)")
	f.StringVar(&only, "only", "", "only run these targets (comma list of names from --targets)")
	f.BoolVar(&perTask, "per-task", false, "also print a per-task table: pass/runs, wall time, model calls, tool calls, tokens")
	_ = cmd.MarkFlagRequired("tasks")
	_ = cmd.MarkFlagRequired("targets")

	cmd.AddCommand(newBenchCalibrateCmd())
	cmd.AddCommand(newBenchStatsCmd())
	return cmd
}

// newBenchCalibrateCmd measures the judge against human gold verdicts before
// anyone trusts its PASS/FAIL. A judge that agrees with a careful human on
// fewer than ~85% of real transcripts is noise with a confident tone.
func newBenchCalibrateCmd() *cobra.Command {
	var (
		set         string
		metric      string
		threshold   float64
		rps         float64
		cache       string
		minAccuracy float64
		minKappa    float64
		out         string
	)
	cmd := &cobra.Command{
		Use:   "calibrate",
		Short: "Run the judge over a gold-labelled transcript set and report agreement (exit 1 below threshold)",
		Long: "calibrate loads a JSON array of recorded runs (Samples) that each carry a human\n" +
			"verdict in labels[<metric>] (1 = pass, 0 = fail) and the reasoning in\n" +
			"meta.gold_reason, runs the judge metric over them, and prints the confusion\n" +
			"matrix, accuracy, Cohen's kappa and every disagreement next to the human's\n" +
			"reasoning. Exits 1 when accuracy or kappa is below the floor, or any item errored.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			samples, err := loadDataset(set)
			if err != nil {
				return err
			}
			judge, meter, err := judgeFromEnv(rps, cache, 0, 0)
			if err != nil {
				return err
			}
			metrics, err := evalgo.BuildMetrics([]string{metric}, evalgo.MetricSpec{Judge: judge, Threshold: threshold})
			if err != nil {
				return err
			}
			res, err := evalgo.AlignMetric(cmd.Context(), samples, metrics[0], metric)
			if err != nil {
				return fmt.Errorf("%s: %w", set, err)
			}
			gold := map[string]string{}
			for _, s := range samples {
				gold[s.Name] = s.Meta["gold_reason"]
			}

			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "judge %s on %s: %d items (%d errored)\n", os.Getenv("LLM_MODEL"), metric, res.N, res.Errored)
			fmt.Fprintln(w, "\n                 judge PASS   judge FAIL")
			fmt.Fprintf(w, "  human PASS     %-12d %d\n", res.TP, res.FN)
			fmt.Fprintf(w, "  human FAIL     %-12d %d\n", res.FP, res.TN)
			fmt.Fprintf(w, "\naccuracy %.1f%%   kappa %.2f   precision %.2f   recall %.2f   F1 %.2f\n",
				res.Accuracy*100, res.Kappa, res.Precision, res.Recall, res.F1)
			if len(res.Disagreements) > 0 {
				fmt.Fprintln(w, "\ndisagreements:")
				for _, d := range res.Disagreements {
					human := "FAIL"
					if d.Human >= 0.5 {
						human = "PASS"
					}
					fmt.Fprintf(w, "  %s  human %s, judge %s (%.2f)\n", d.Sample, human, map[bool]string{true: "PASS", false: "FAIL"}[d.JudgePassed], d.JudgeScore)
					fmt.Fprintf(w, "    human: %s\n    judge: %s\n", gold[d.Sample], d.Reason)
				}
			}
			u := meter.Usage()
			fmt.Fprintf(cmd.ErrOrStderr(), "\njudge: %d calls, ~%d tokens, %.1fs\n", u.Calls, u.TotalTokens, u.JudgeSeconds)
			if out != "" {
				data, _ := json.MarshalIndent(res, "", "  ")
				if err := os.WriteFile(out, data, 0o644); err != nil {
					return err
				}
			}
			var bad []string
			if res.Errored > 0 {
				bad = append(bad, fmt.Sprintf("%d item(s) errored", res.Errored))
			}
			if res.Accuracy < minAccuracy {
				bad = append(bad, fmt.Sprintf("accuracy %.1f%% < %.1f%%", res.Accuracy*100, minAccuracy*100))
			}
			if res.Kappa < minKappa {
				bad = append(bad, fmt.Sprintf("kappa %.2f < %.2f", res.Kappa, minKappa))
			}
			if len(bad) > 0 {
				fmt.Fprintf(cmd.ErrOrStderr(), "\nCALIBRATION FAILED: %s — do not trust this judge's verdicts\n", strings.Join(bad, "; "))
				os.Exit(1)
			}
			fmt.Fprintln(w, "\ncalibration ok")
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&set, "set", "calibration/rubric-gold.json", "gold-labelled transcripts (JSON array of samples with labels[<metric>])")
	f.StringVar(&metric, "metric", "rubric_judge", "judge metric to calibrate (the labels key)")
	f.Float64Var(&threshold, "threshold", 0.5, "pass threshold for the judge metric")
	f.Float64Var(&rps, "rps", 4, "judge rate limit (requests/sec); <=0 disables")
	f.StringVar(&cache, "cache", "", "directory to cache judge responses in")
	f.Float64Var(&minAccuracy, "min-accuracy", 0.85, "fail below this agreement with the human verdicts")
	f.Float64Var(&minKappa, "min-kappa", 0.6, "fail below this Cohen's kappa")
	f.StringVarP(&out, "out", "o", "", "write the alignment result (JSON) here")
	return cmd
}

// newBenchStatsCmd re-reads saved bench reports — one run, or several runs of
// different targets — and prints the statistics without re-running anything.
func newBenchStatsCmd() *cobra.Command {
	var (
		a, b    string
		perTask bool
		out     string
	)
	cmd := &cobra.Command{
		Use:   "stats <report.json> [report.json ...]",
		Short: "Merge saved bench reports and print pass@1 with CI, flaky tasks, paired comparisons and cost",
		Long: "stats merges the targets of every report given (they must share a gate) and\n" +
			"prints the same statistics bench prints. --a/--b limit the paired comparison\n" +
			"to two targets. Use it to compare targets that were run separately.",
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var merged evalgo.ComparisonReport
			for i, path := range args {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				var cr evalgo.ComparisonReport
				if err := json.Unmarshal(data, &cr); err != nil {
					return fmt.Errorf("%s: %w", path, err)
				}
				if len(cr.Targets) == 0 {
					return fmt.Errorf("%s: no targets (is it a bench report?)", path)
				}
				if i == 0 {
					merged.Gate = cr.Gate
				} else if !slices.Equal(merged.Gate, cr.Gate) {
					return fmt.Errorf("%s: gate %v differs from %v in %s; pass@1 would not mean the same thing", path, cr.Gate, merged.Gate, args[0])
				}
				merged.Targets = append(merged.Targets, cr.Targets...)
				merged.Runs = max(merged.Runs, cr.Runs)
			}
			w := cmd.OutOrStdout()
			merged.RenderGrid(w)
			merged.WriteStats(w)
			merged.WriteUsage(w)
			if perTask {
				merged.WritePerTask(w)
			}
			if a != "" || b != "" {
				if a == "" || b == "" {
					return fmt.Errorf("--a and --b go together")
				}
				p, err := merged.Paired(a, b)
				if err != nil {
					return err
				}
				fmt.Fprintln(w, "\n"+p.String())
			}
			if out != "" {
				data, err := json.MarshalIndent(withStats(merged), "", "  ")
				if err != nil {
					return err
				}
				return os.WriteFile(out, data, 0o644)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&a, "a", "", "first target of a paired comparison")
	cmd.Flags().StringVar(&b, "b", "", "second target of a paired comparison")
	cmd.Flags().BoolVar(&perTask, "per-task", false, "also print the per-task table")
	cmd.Flags().StringVarP(&out, "out", "o", "", "write the merged report with statistics (JSON) here")
	return cmd
}
