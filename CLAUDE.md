# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Eval-Go is a native-Go framework for evaluating LLM/RAG outputs — both an importable library (`evalgo` package) and a CLI (`cmd/evalgo`). It is the Go-idiomatic counterpart to Python's DeepEval/RAGAS: lean on concurrency, determinism, and `go test` instead of a heavy framework.

## Build / test / run

```bash
go build ./...
go test ./...                 # deterministic metrics only — free & offline, no LLM calls
go test -run TestExtractJSON ./...   # single test by name
go vet ./...

# CLI (deterministic, offline)
go run ./cmd/evalgo -dataset examples/golden.json -metrics nonempty,citation,valid_json

# CLI with LLM judge (any OpenAI-compatible endpoint)
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...
go run ./cmd/evalgo -dataset examples/golden.json -judge env \
  -metrics faithfulness,answer_relevancy,context_precision,rubric -fail-under 0.8
```

Semantic-metric tests are gated behind `RUN_EVALS=true` (plus the `LLM_*` env vars) so plain `go test` never spends tokens. To run them: `RUN_EVALS=true LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=... go test ./... -v`.

## Architecture

**The dependency boundary is the central design constraint.** The root `evalgo` package is **stdlib-only — zero third-party imports.** All LLM-provider coupling lives in the sibling `./llmjudge` package, which is the *only* thing that imports `agent-go`. Deterministic-only library users never pull in agent-go. **When adding code to the root package, do not introduce any third-party import** — if you need provider/LLM functionality, it belongs in `llmjudge` (or another sibling package).

Core abstractions (all in the root package):

- **`Metric`** interface (`eval.go`) — `Score(ctx, Sample) (Result, error)`. Every metric is a `MetricFunc` closure. Two families:
  - **Deterministic** (`metrics_det.go`): ValidJSON, JSONHasFields, MatchesRegex, ForbidsRegex, Contains, ExactMatch, NonEmpty, CitationPresent. No LLM, ignore `ctx`. Run as cheap gates first.
  - **Semantic** (`metrics_judge.go`, `metrics_rag.go`): RubricJudge (G-Eval), Faithfulness, AnswerRelevancy, ContextualPrecision. All take a `JudgeFunc`.
  - **Tool trajectory** (`check.go`): ToolTrajectory scores a Task's `ToolCheck` deterministically — expected calls with argument assertions (equals / one_of / num±tol / regex / contains / from an earlier call's output), `after` ordering, per-turn calls, min/max counts, forbidden tools, "tool or correct answer" alternatives (`or_answer`), final-answer assertions, and assertions on the final tool `State`. `ToolCheck.Validate` runs at `LoadTasks` time so a dangling reference or bad regex fails before any tokens are spent.
  - **Agent** (`metrics_agent.go`): ToolCorrectness (deterministic), ArgumentCorrectness, TaskCompletion, StepEfficiency, PlanQuality, PlanAdherence. These score a **recorded** agent run carried on the `Sample` (`Plan`, `Trajectory`, `ToolCalls`, `ExpectedTools`) — Eval-Go is record-then-evaluate, **not** live `@observe`-style tracing (that would need runtime instrumentation, incompatible with the offline dataset model). A metric whose inputs are absent skips and passes, so partial datasets score cleanly.
  - **RAG retrieval** (`metrics_context.go`): ContextualRecall (needs `Expected`), ContextualRelevancy. Complete the precision/recall/relevancy trio with `metrics_rag.go`.
  - **Safety / quality** (`metrics_safety.go`): Hallucination, Bias, Toxicity, PIILeakage, Summarization.
  - **Conversational** (`metrics_conversation.go`): ConversationCompleteness, KnowledgeRetention, ConversationRelevancy, RoleAdherence (needs `Persona`). These score `Sample.Turns` (`[]Turn{Role, Content}`), the multi-turn fields on `Sample`.
- **`JudgeFunc`** (`metrics_judge.go`) — `func(ctx, prompt) (string, error)`. The single seam between metrics and any LLM. `llmjudge.New`/`FromEnv` produce one from agent-go; it composes with three stdlib decorators: `RateLimit` (`ratelimit.go`, token bucket, avoids 429s), `Cache` (`cache.go`, SHA-256-keyed on-disk cache so re-running an unchanged dataset spends no tokens; errors never cached, empty dir disables), and `Meter` (`meter.go`, counts calls / estimated tokens / time / cost). **Wrap order matters**: `Cache(meter.Wrap(RateLimit(base)), dir)` (the CLI uses `CacheSalted` keyed by the judge model, so switching `LLM_MODEL` never reuses another judge's verdicts) — the meter sits *inside* the cache so cache hits aren't counted as billed calls. The CLI wires all three via `--rps` / `--cache` / `--cost-in` / `--cost-out`, and attaches the `Usage` snapshot to `Report.Usage` (rendered in both console and JSON). Token counts are heuristic (`EstimateTokens`, ~4 chars/token), not a provider tokenizer.
- **`DAG`** (`dag.go`) — builds a `Metric` from a decision tree of judge questions (`Branch`/`YesNo`/`Leaf` nodes) instead of one vague score, making complex pass/fail logic deterministic and explainable. Library-only (the tree is Go code), so it is **not** in the CLI `registry`.
- **Gate** (`gate.go`) — `ResolveGate` validates gate names against the scored metrics up front (unknown name = error listing valid ones; registry keys like `rubric` resolve to reported names like `rubric_judge`, derived from the registry). The default bench gate is `auto` (`AutoGate`): per task, `rubric_judge` if it has a rubric, `tool_trajectory` if it has a check, else `task_completion`; a task's own `gate` overrides. task_completion must not gate a rubric task: it fails every correct refusal.
- **Stats** (`stats.go`) — pass@1 is the mean per-task pass rate with a 95% interval computed over tasks (Wilson fallback when variance is zero), flaky tasks, `Paired` (per-task differences, CI, sign-flip permutation p — exact ≤20 differing tasks, seeded Monte Carlo above), and per-target `Usage` from `RunUsage`. Tasks, not attempts, are the unit: repeated runs of one task are not independent.
- **Multi-turn + usage** (`runner.go`) — `Task.Turns` (follow-up user messages), `Task.ToolScript` (opaque JSON handed to the target as `EVAL_TOOL_SCRIPT`), `RunOutput.Turns/State/Usage`. The Runner measures wall time and fails a multi-turn task whose output does not show every user turn (an adapter that ignores `EVAL_TURNS` must not be graded on a conversation it never had).
- **`Suite.Run`** (`eval.go`) — runs samples concurrently (bounded by `Concurrency`, default 4) but metrics *within* a sample sequentially, so a shared rate-limited judge paces cleanly. A metric error becomes a failed `Result`, never aborts the run.
- **`Report`** (`report.go`) — `WriteConsole` / `WriteJSON` / `Summary()` (per-metric aggregates) / `Failed()` (CI gate).
- **`Synthesizer`** (`synth.go`) — generates golden `Sample`s with an LLM (`FromContexts` / `FromDocuments`), optionally complexity-"evolving" each question. Uses only a `JudgeFunc`, so the stdlib-only core holds. Surfaced as the `evalgo gen` CLI subcommand.
- **`RedTeam`** (`redteam.go`) — generates adversarial attack `Sample`s (prompt injection / jailbreak / PII extraction / harmful request) from built-in templates — **fully offline**; a `Judge` + `Enhance>0` additionally LLM-obfuscates each probe. Authorized defensive testing: probes are abstract inputs a safe system must refuse. Scored by `AttackResistance` (judge) and `RefusalPresent` (deterministic, `metrics_redteam.go`) plus the safety metrics. Surfaced as `evalgo redteam`; example dataset at `examples/attacks.json`.
- **Dataset I/O** (`dataset.go`) — `LoadJSON` / `LoadJSONL` / `LoadCSV` bring goldens in from any of those formats (CSV maps known columns to fields, `context` splits on `|`, unknown columns become `Meta`); `FilterMeta` / `Filter` slice by metadata (e.g. one red-team attack kind). The CLI picks the loader by extension and exposes `--where key=value`.
- **Report diff** (`diff.go`) — `LoadReport` reads back a JSON report; `DiffReports(old, new)` pairs results by (sample, metric) and classifies each as regressed/fixed/declined/improved/added/removed, regressions sorted first. Surfaced as `evalgo diff old.json new.json` (exit 1 on any regression) — the cross-version CI gate.
- **`RunT`** (`testing.go`) — adapts a Suite into parallel `go test` subtests (one per sample) so CI exit codes work for free.
- **`evalgo bench`** (`cmd/evalgo/bench.go`) — `bench` (grid, stats, paired comparisons, cost; `--runs`, `--where`, `--only`, `--per-task`), `bench calibrate` (judge vs gold labels; exits 1 below `--min-accuracy`/`--min-kappa`), `bench stats` (re-analyse / merge saved reports). The benchmark itself (tool worlds, task suites, adapters, calibration set) lives in the separate `agentbench/` module — see `agentbench/README.md`.
- **`BuildMetrics` + `registry`** (`registry.go`) — string-name → Metric resolution used by the CLI. Semantic factories are flagged `needsJudge`; requesting one without a judge fails fast. **Adding a new metric that should be CLI-accessible requires registering it here.**

### RAG metric convention

RAG metrics use DeepEval/RAGAS-style **decomposition**, not a single vague 0–1 score: first ask the judge to extract atomic units (claims / statements / chunks), then verify each one, then score = good units / total. Follow this pattern for new RAG metrics. Shared judge helpers (`extractClaims`, `extractStatements`, `callJSON`, `callVerdict`, `extractJSON`) live in `metrics_judge.go` / `metrics_rag.go`. `extractJSON` tolerates ```json fences and surrounding prose from non-strict judges — reuse it rather than `json.Unmarshal`-ing raw judge output.

### Data shapes

`Sample` (input/output/expected/context/rubric/meta) is the golden-dataset row; the CLI loads a JSON array of these. `Result` carries a normalized `Score` (0..1), `Passed`, and a human `Reason`. Exit codes: CLI returns `1` on a failed CI gate (`-fail-under`, or any failure by default), `2` on config/runtime error.
