# Eval-Go

[![CI](https://github.com/liliang-cn/eval-go/actions/workflows/ci.yml/badge.svg)](https://github.com/liliang-cn/eval-go/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/liliang-cn/eval-go.svg)](https://pkg.go.dev/github.com/liliang-cn/eval-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/liliang-cn/eval-go)](https://goreportcard.com/report/github.com/liliang-cn/eval-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A native-Go framework for evaluating LLM, RAG, and agent outputs — **27 metrics**,
synthetic-data generation, red-teaming, caching, cost tracking, and cross-version
regression gating, as both a library and a CLI.

Python dominates LLM evaluation with heavy frameworks (DeepEval, RAGAS, DeepTeam).
Eval-Go covers the same evaluation surface but leans on Go's strengths —
**concurrency, determinism, zero-cost heuristics, and `go test`**. Metrics split
into families:

- **Deterministic** (code-based): JSON validity, regex, citation, tool-set match,
  refusal detection. Fast, free, no LLM — run them first as cheap gates.
- **Semantic** (LLM-as-a-judge): RAG, agent, safety, conversational, and red-team
  metrics, decomposed RAGAS-style (extract atomic units → verify each) rather than
  one vague 0–1 score.

The core package has **zero third-party dependencies** (stdlib only). The judge
adapter for [agent-go](https://github.com/liliang-cn/agent-go) lives in a separate
package (`./llmjudge`), so deterministic-only users pay no dependency cost.

Agent and conversational metrics are **record-then-evaluate**: they score a run the
system already produced (carried on the `Sample`), so any framework — or none —
can emit the data, with no live `@observe`-style instrumentation.

## Install

```bash
go get github.com/liliang-cn/eval-go                          # library
go install github.com/liliang-cn/eval-go/cmd/evalgo@latest    # CLI
```

## Quick start

```bash
# deterministic metrics — free, offline
evalgo -d examples/golden.json -m nonempty,citation,valid_json

# add LLM-as-a-judge (any OpenAI-compatible endpoint)
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...
evalgo -d examples/golden.json --judge env \
  -m faithfulness,answer_relevancy,context_precision,rubric \
  -f json -o report.json --fail-under 0.8
```

The dataset is a JSON / JSONL / CSV array of samples. Exit code is `1` when the CI
gate fails (`--fail-under`, or any failure by default) and `2` on a config error.

## Metrics

Names below are the CLI `-m` identifiers. A few deterministic metrics are
library-only (they take constructor arguments) and are marked as such.

| Metric | Category | What it checks |
|---|---|---|
| `nonempty` | deterministic | output has non-whitespace content |
| `valid_json` | deterministic | output is structurally valid JSON |
| `exact_match` | deterministic | trimmed output equals `expected` |
| `citation` | deterministic | output carries a `[SOURCE]`-style citation |
| `JSONHasFields`, `Contains`, `MatchesRegex`, `ForbidsRegex` | deterministic *(library)* | required keys / substring / format / safety boundary |
| `rubric` | semantic | G-Eval-style pass/score against a per-sample rubric |
| `faithfulness` | RAG | answer's claims are grounded in context (catches hallucination) |
| `answer_relevancy` | RAG | answer statements actually address the question |
| `context_precision` | RAG | retrieved chunks are relevant to the question |
| `contextual_recall` | RAG | context covers everything the reference answer needs |
| `contextual_relevancy` | RAG | statements in the context are relevant to the question |
| `tool_correctness` | agent *(deterministic)* | invoked tools match the expected set |
| `argument_correctness` | agent | each tool call's arguments are correct for the task |
| `task_completion` | agent | the agent actually accomplished the task |
| `step_efficiency` | agent | goal reached without redundant / repeated steps |
| `plan_quality` | agent | the agent's plan is logical, complete, efficient |
| `plan_adherence` | agent | the agent's execution followed its own plan |
| `hallucination` | safety | output does not contradict the trusted context |
| `bias`, `toxicity` | safety | output statements are unbiased / non-toxic |
| `pii_leakage` | safety | output leaks no personally identifiable information |
| `summarization` | quality | summary is faithful to and covers the source text |
| `conversation_completeness` | multi-turn | assistant fulfilled the user's intentions across turns |
| `knowledge_retention` | multi-turn | assistant remembered facts given earlier |
| `conversation_relevancy` | multi-turn | each assistant turn is relevant in context |
| `role_adherence` | multi-turn | assistant stayed in its assigned persona |
| `attack_resistance` | red-team | system resisted (refused / didn't leak) an adversarial input |
| `refusal` | red-team *(deterministic)* | output contains refusal / deflection language |

Plus `DAG` (library) — build a metric from a decision tree of judge questions
instead of one score; see [Custom metrics](#custom-metrics-dag).

## The Sample

One JSON object per row. Fields are optional; a metric whose inputs are absent
skips and passes, so you can mix RAG, agent, and conversational rows in one file.

```jsonc
{
  "name": "weather-agent",
  "input": "What's the weather in Tokyo tomorrow?",
  "output": "Tokyo tomorrow: sunny, high around 24C.",
  "expected": "...",                          // reference answer (exact_match, contextual_recall)
  "context": ["..."],                          // retrieved chunks (RAG / hallucination)
  "rubric": "States the forecast accurately.", // for the rubric judge
  "meta": {"team": "search"},                  // labels; filterable with --where

  // agent run (record-then-evaluate)
  "plan": "1) geocode, 2) fetch forecast, 3) report",
  "trajectory": ["geocoded Tokyo", "fetched forecast", "reported"],
  "tool_calls": [{"name": "geocode", "args": {"query": "Tokyo"}}],
  "expected_tools": ["geocode", "weather"],

  // multi-turn conversation
  "turns": [{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}],
  "persona": "a concise banking support agent"
}
```

## Library quick start

```go
import (
    evalgo "github.com/liliang-cn/eval-go"
    "github.com/liliang-cn/eval-go/llmjudge"
)

judge, _ := llmjudge.FromEnv()                 // LLM_BASE_URL / LLM_API_KEY / LLM_MODEL
judge = evalgo.RateLimit(judge, 4, 2)          // token-bucket; avoid 429s
judge = evalgo.Cache(judge, ".eval-cache")     // disk cache; re-runs spend no tokens

suite := evalgo.Suite{
    Concurrency: 4,
    Metrics: []evalgo.Metric{
        evalgo.CitationPresent(),              // deterministic
        evalgo.Faithfulness(judge),            // RAG
        evalgo.AnswerRelevancy(judge, 0.5),
        evalgo.RubricJudge(judge, 0.7),
    },
    Samples: []evalgo.Sample{{
        Name:    "grounded-answer",
        Input:   "What is the savings account interest rate?",
        Context: []string{"The savings account pays 0.30% below 50000 and 0.55% above."},
        Output:  "The rate is tiered: 0.30% below 50000 and 0.55% above [KB-001].",
    }},
}

report := suite.Run(context.Background())
report.WriteConsole(os.Stdout)
if report.Failed() { os.Exit(1) }              // CI gate
```

Samples run concurrently (bounded by `Concurrency`); metrics within a sample run
sequentially so a shared rate-limited judge paces cleanly.

## `go test` integration

`RunT` turns a suite into table-driven, parallel subtests so `go test` exit codes
and CI reporting work for free. Gate semantic evals behind an env flag so plain
`go test` spends no tokens:

```go
func TestRAG(t *testing.T) {
    if os.Getenv("RUN_EVALS") != "true" { t.Skip("set RUN_EVALS=true") }
    judge, _ := llmjudge.FromEnv()
    evalgo.RunT(t, evalgo.Suite{ /* Metrics, Samples */ })
}
```

```bash
go test ./...                                   # deterministic only, free & offline
RUN_EVALS=true LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=... go test ./... -v
```

## Agent evaluation

Agent metrics score a recorded run carried on the `Sample` (`plan`, `trajectory`,
`tool_calls`, `expected_tools`) — across the same three layers DeepEval names:
action (`tool_correctness`, `argument_correctness`), execution (`task_completion`,
`step_efficiency`), reasoning (`plan_quality`, `plan_adherence`).

```bash
evalgo -d examples/agent.json -m tool_correctness          # free, offline
evalgo -d examples/agent.json --judge env \
  -m tool_correctness,argument_correctness,task_completion,step_efficiency,plan_quality,plan_adherence
```

### Run the agent (end-to-end)

The metrics above score a run you already recorded. eval-go can also **run the
agent for you**: define `Task`s, plug the system under test in as a `Target`, and
`Bench` executes every task, captures the run into a `Sample`, and scores it — one
pipeline, no external glue.

```go
bench := evalgo.Bench{
    Target:  myAgent,        // a Target: Run(ctx, Task) (RunOutput, error)
    Tasks:   tasks,          // each: Input + ExpectedTools + Rubric (+ Files seed)
    Metrics: metrics,        // evalgo.BuildMetrics(["tool_correctness","rubric",...], spec)
}
report, samples := bench.Run(ctx)
```

A `Target` can be in-process (wrap a Go agent) or `ExecTarget`, which drives an
agent that runs as a **subprocess in any language** — the task is passed via
`EVAL_*` env vars (`EVAL_INPUT`, `EVAL_RUBRIC`, `EVAL_EXPECTED_TOOLS`,
`EVAL_FILES`, …) and the program prints a JSON `RunOutput` (or a bare `Sample`)
to stdout. `Runner` (run only, no scoring) and a per-task `Timeout` are also
available; a target error becomes a failed `Sample` rather than aborting the batch.

### Compare several agents (`evalgo bench`)

Run the **same tasks through several agents** and get a task × agent PASS/FAIL
grid — a cross-framework or cross-config benchmark, all from two config files:

```bash
evalgo bench --tasks tasks.json --targets targets.json --judge env \
  -m tool_correctness,task_completion,step_efficiency,rubric \
  --gate task_completion,rubric -o grid.json
```

`tasks.json` is an array of `Task` (`name`, `input`, `expected_tools`, `rubric`,
and optional `files` seed fixtures). `targets.json` declares the agents:

```json
[
  {"name": "agent-go",   "command": ["go","run","./examples/eval-bench"], "dir": "../agent-go", "env": {"GOWORK": "off"}},
  {"name": "miniagent",  "command": ["python","run.py"], "dir": "../mini"},
  {"name": "harness-rs", "command": ["./target/debug/eval-bench"]}
]
```

Each agent runs as a subprocess (the `EVAL_*` contract above), so Go, Python and
Rust agents compete on identical tasks. `--gate` picks which metrics decide
PASS/FAIL per cell (default `task_completion,rubric`, so a correct-but-inefficient
run still passes). The library form is `evalgo.Comparison{Targets, Tasks, Metrics, Gate}`.

## Synthesize a dataset

Generate goldens from your sources with an LLM instead of hand-writing them; the
output is a normal samples array:

```bash
evalgo gen --docs handbook.txt -n 3 -o golden.json              # chunk a doc, 3 goldens/chunk
evalgo gen --contexts groups.json --evolutions 1 -o hard.json   # from context groups, + complexity evolution
```

Library: `Synthesizer{Judge: ...}.FromDocuments(ctx, docs, chunkSize)` (or
`FromContexts`) returns `[]Sample` with `Input` / `Expected` / `Context` set.

## Red-teaming (safety probes)

Generate an adversarial dataset to check your own system upholds its safety
boundaries — authorized defensive testing. Generation is offline; the probes carry
no operational detail (the danger would live in the *response*, which a safe system
must refuse).

```bash
evalgo redteam -o attacks.json                      # offline: injection / jailbreak / PII / harmful
evalgo redteam --kinds prompt_injection,jailbreak --enhance 1 --judge env  # LLM-obfuscated probes
# run attacks.json through your system, fill each "output", then:
evalgo -d attacks.json --judge env -m attack_resistance,pii_leakage,toxicity,refusal
```

`refusal` is a free deterministic gate (does the output decline?); `attack_resistance`
is the judge call that decides whether the attack actually succeeded.

## Datasets & regression gating

Load goldens from JSON, JSONL, or CSV (the CLI picks by extension), filter by
metadata, and diff two runs to catch regressions between versions:

```bash
evalgo -d goldens.csv -m faithfulness --judge env             # CSV in, columns → Sample fields
evalgo -d attacks.json -m refusal --where attack=jailbreak    # only matching samples

evalgo -d goldens.json --judge env -f json -o new.json        # save a report
evalgo diff old.json new.json                                 # exit 1 if any metric regressed
```

`diff` pairs results by sample+metric and flags each as regressed / fixed /
improved / added / removed (regressions first), so a PR that quietly makes an eval
worse fails CI. Library: `LoadJSON`/`LoadJSONL`/`LoadCSV`, `FilterMeta`, `DiffReports`.

## Cost & token tracking

With `--judge env`, every run ends with a `judge usage` block — calls, estimated
tokens, cumulative judge time, and (with `--cost-*`) an estimated cost. The meter
sits *inside* the cache, so cache hits aren't counted: the number reflects only
real provider calls.

```bash
evalgo -d goldens.json --judge env -m faithfulness,rubric \
  --cache .eval-cache --cost-in 0.4 --cost-out 1.2
```

```
--- judge usage ---
calls         : 20
est. tokens   : 2089 prompt + 725 completion = 2814
judge time    : 377.4s (cumulative)
est. cost     : $0.0017
```

Library: `evalgo.NewMeter(costInPerM, costOutPerM)` then `judge = meter.Wrap(judge)`;
read `meter.Usage()`. Token counts are heuristic (`EstimateTokens`, ~4 chars/token),
not a provider tokenizer.

## Custom metrics (DAG)

`RubricJudge` is one judge call. `DAG` builds a metric from a decision tree, so the
judge makes small, local decisions and pass/fail logic stays deterministic and
explainable:

```go
root := evalgo.YesNo("Is the OUTPUT valid JSON?",
    evalgo.YesNo("Does it contain a 'name' field?",
        evalgo.Leaf(1, "ok"),
        evalgo.Leaf(0.5, "missing name")),
    evalgo.Leaf(0, "not JSON"))
m := evalgo.DAG(judge, "json_shape", 0.99, root)
```

## CLI reference

```
evalgo -d <dataset> [flags]     evaluate a dataset (root command)
evalgo bench ...                run tasks through several agents, print a PASS/FAIL grid
evalgo gen ...                  synthesize a golden dataset from docs/contexts
evalgo redteam ...              generate an adversarial dataset (offline)
evalgo diff <old> <new>         compare two reports, gate on regressions
evalgo metrics                  list registered metric names
```

`evalgo bench --tasks tasks.json --targets targets.json` runs every task through
every agent (each an `EVAL_*`-driven subprocess) and prints a task × agent grid.
Key flags: `--gate` (metrics that decide PASS/FAIL, default `task_completion,rubric`),
`--timeout` (per task), `-m`, `--judge`, `--concurrency`, `-o`.

| Flag (eval) | Purpose |
|---|---|
| `-d, --dataset` | dataset JSON/JSONL/CSV (`-` for stdin) |
| `-m, --metrics` | comma list of metric names |
| `--judge` | `env` (LLM via `LLM_*`) or `none` |
| `-f, --format` | `console` or `json` |
| `-o, --out` | also write a JSON report file |
| `--concurrency` / `--rps` | parallel samples / judge rate limit |
| `--threshold` / `--fail-under` | judge pass threshold / suite CI gate |
| `--cache` | dir to cache judge responses (re-runs spend no tokens) |
| `--cost-in` / `--cost-out` | USD per 1M prompt / completion tokens |
| `--where` | only evaluate samples whose meta matches `key=value` |

## License

MIT
