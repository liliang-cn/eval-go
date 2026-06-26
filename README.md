# Eval-Go

A small, native-Go framework for evaluating LLM and RAG outputs.

While Python dominates LLM evaluation with heavy frameworks (DeepEval, RAGAS),
Eval-Go takes the opposite approach: lean on Go's own strengths — **concurrency,
determinism, zero-cost heuristics, and `go test`** — instead of importing a large
framework. Metrics split into two families:

- **Deterministic** (code-based): JSON validity, regex, forbidden-pattern, exact
  match, citation presence. Fast, free, no LLM. Run them first as cheap gates.
- **Semantic** (LLM-as-a-judge): rubric/G-Eval, faithfulness, answer relevancy,
  contextual precision — capture intent via a pluggable judge.

The core package has **zero third-party dependencies** (stdlib only). The judge
adapter for [agent-go](https://github.com/liliang-cn/agent-go) lives in a separate
package (`./llmjudge`) so deterministic-only users pay no dependency cost.

Eval-Go is **both a library and a CLI**: import the `evalgo` package to embed
evaluation in your own Go code and tests, or run the `evalgo` binary against a
JSON golden dataset in CI.

## Install

```bash
# as a library
go get github.com/liliang-cn/eval-go

# as a CLI
go install github.com/liliang-cn/eval-go/cmd/evalgo@latest
```

## CLI

```bash
# deterministic metrics — free, offline
evalgo -dataset examples/golden.json -metrics nonempty,citation,valid_json

# add LLM-as-a-judge (any OpenAI-compatible endpoint)
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...
evalgo -dataset examples/golden.json -judge env \
  -metrics faithfulness,answer_relevancy,context_precision,rubric \
  -format json -out report.json -fail-under 0.8
```

The dataset is a JSON array of samples (`name`, `input`, `output`, `context`,
`expected`, `rubric`). Exit code is `1` when the CI gate fails (`-fail-under`, or
any failure by default) and `2` on a config error — drop it straight into a
pipeline.

| Flag | Purpose |
|---|---|
| `-dataset` | golden dataset JSON (`-` for stdin) |
| `-metrics` | comma list (see table below) |
| `-judge` | `env` (LLM via `LLM_*`) or `none` |
| `-format` | `console` or `json` |
| `-out` | also write a JSON report file |
| `--concurrency` / `--rps` | parallel samples / judge rate limit |
| `--threshold` / `--fail-under` | metric pass threshold / suite CI gate |
| `--cache` | dir to cache judge responses (re-runs spend no tokens) |
| `--cost-in` / `--cost-out` | USD per 1M prompt / completion tokens — adds a cost estimate to the report |

The report ends with a `judge usage` block — calls, estimated tokens, cumulative
judge time, and (with `--cost-*`) an estimated cost. Cache hits aren't counted,
so the number reflects only real provider calls.

## Library quick start

```go
import (
    evalgo "github.com/liliang-cn/eval-go"
    "github.com/liliang-cn/eval-go/llmjudge"
)

judge, _ := llmjudge.FromEnv()          // LLM_BASE_URL / LLM_API_KEY / LLM_MODEL
judge = evalgo.RateLimit(judge, 4, 2)   // token-bucket; avoid 429s
judge = evalgo.Cache(judge, ".eval-cache") // disk cache; re-runs spend no tokens

suite := evalgo.Suite{
    Concurrency: 4,
    Metrics: []evalgo.Metric{
        evalgo.CitationPresent(),               // deterministic
        evalgo.Faithfulness(judge),             // semantic (RAG)
        evalgo.AnswerRelevancy(judge, 0.5),
        evalgo.ContextualPrecision(judge, 0.5),
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
if report.Failed() { os.Exit(1) }       // CI gate
```

## Native `go test` integration

`RunT` turns a suite into table-driven, parallel subtests so `go test` exit codes
and CI reporting work for free. Gate semantic evals behind an env flag so plain
`go test` spends no tokens:

```go
func TestRAG(t *testing.T) {
    if os.Getenv("RUN_EVALS") != "true" { t.Skip("set RUN_EVALS=true") }
    judge, _ := llmjudge.FromEnv()
    evalgo.RunT(t, evalgo.Suite{Judge stuff..., Metrics: ..., Samples: ...})
}
```

```bash
go test ./...                                   # deterministic only, free & offline
RUN_EVALS=true LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=... go test ./... -v
```

## Synthesize a dataset

Instead of hand-writing goldens, generate them from your sources with an LLM —
the output is a normal samples array you can pipe straight into `evalgo -d`:

```bash
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...
evalgo gen --docs handbook.txt -n 3 -o golden.json          # chunk a document, 3 goldens/chunk
evalgo gen --contexts groups.json --evolutions 1 -o hard.json   # from context groups, + complexity evolution
```

As a library, `Synthesizer{Judge: ...}.FromDocuments(ctx, docs, chunkSize)` (or
`FromContexts`) returns `[]Sample` with `Input` / `Expected` / `Context` set —
ready for the RAG and faithfulness metrics that score against context.

## Datasets & regression gating

Load goldens from JSON, JSONL, or CSV (the CLI picks by extension), filter by
metadata, and diff two runs to catch regressions between versions:

```bash
evalgo -d goldens.csv -m faithfulness --judge env        # CSV in, columns → fields
evalgo -d attacks.json -m refusal --where attack=jailbreak  # only matching samples

evalgo -d goldens.json --judge env -f json -o new.json   # save a report
evalgo diff old.json new.json                            # exit 1 if any metric regressed
```

`diff` pairs results by sample+metric and flags each as regressed / fixed /
improved / added / removed — regressions first — so a PR that quietly makes an
eval worse fails CI.

## Red-teaming (safety probes)

Generate an adversarial dataset to check your own system upholds its safety
boundaries — authorized defensive testing. Generation is offline; scoring uses
the safety metrics:

```bash
evalgo redteam -o attacks.json                      # offline: injection / jailbreak / PII / harmful
evalgo redteam --kinds prompt_injection,jailbreak --enhance 1 --judge env  # LLM-obfuscated probes
# run attacks.json through your system, fill each "output", then:
evalgo -d attacks.json --judge env -m attack_resistance,pii_leakage,toxicity,refusal
```

`refusal` is a free deterministic gate (does the output decline?); `attack_resistance`
is the judge call that decides whether the attack actually succeeded.

## Metrics

| Metric | Type | What it checks |
|---|---|---|
| `ValidJSON`, `JSONHasFields` | deterministic | output is JSON / has required keys |
| `MatchesRegex`, `ForbidsRegex` | deterministic | required format / safety boundary (no leaked secrets) |
| `Contains`, `ExactMatch`, `NonEmpty` | deterministic | substring / reference / non-empty |
| `CitationPresent` | deterministic | output carries `[SOURCE]` attribution |
| `RubricJudge` | semantic | G-Eval-style pass/score against a rubric |
| `Faithfulness` | semantic (RAG) | claims extracted from the answer are grounded in context (catches hallucination) |
| `AnswerRelevancy` | semantic (RAG) | answer statements actually address the question |
| `ContextualPrecision` | semantic (RAG) | retrieved context chunks are relevant to the question |
| `ToolCorrectness` | deterministic (agent) | tools the agent invoked match the expected set |
| `ArgumentCorrectness` | semantic (agent) | each tool call's arguments are correct for the task |
| `TaskCompletion` | semantic (agent) | the agent actually accomplished the task |
| `StepEfficiency` | semantic (agent) | goal reached without redundant / repeated steps |
| `PlanQuality` | semantic (agent) | the agent's plan is logical, complete, efficient |
| `PlanAdherence` | semantic (agent) | the agent's execution followed its own plan |
| `ContextualRecall` | semantic (RAG) | retrieved context covers everything the reference answer needs |
| `ContextualRelevancy` | semantic (RAG) | statements in the context are relevant to the question |
| `Hallucination` | semantic (safety) | output does not contradict the trusted context |
| `Bias`, `Toxicity` | semantic (safety) | output statements are unbiased / non-toxic |
| `PIILeakage` | semantic (safety) | output leaks no personally identifiable information |
| `Summarization` | semantic | summary is faithful to and covers the source text |
| `ConversationCompleteness` | semantic (multi-turn) | the assistant fulfilled the user's intentions across turns |
| `KnowledgeRetention` | semantic (multi-turn) | the assistant remembered facts given earlier |
| `ConversationRelevancy` | semantic (multi-turn) | each assistant turn is relevant in context |
| `RoleAdherence` | semantic (multi-turn) | the assistant stayed in its assigned persona |

RAG metrics use the DeepEval/RAGAS-style decomposition (extract atomic units →
verify each) rather than one vague 0–1 score — more reliable and explainable.

## Agent evaluation

Agent metrics evaluate a **recorded** agent run carried on the `Sample` — Eval-Go
scores the trajectory the agent already produced rather than instrumenting a live
runtime, so any framework (or none) can emit these fields:

```json
{
  "name": "weather-agent",
  "input": "What's the weather in Tokyo tomorrow?",
  "plan": "1) geocode Tokyo, 2) fetch forecast, 3) report",
  "trajectory": ["geocoded Tokyo", "fetched tomorrow's forecast", "reported"],
  "tool_calls": [
    {"name": "geocode", "args": {"query": "Tokyo"}},
    {"name": "weather", "args": {"lat": 35.68, "lon": 139.69}}
  ],
  "expected_tools": ["geocode", "weather"],
  "output": "Tokyo tomorrow: sunny, high around 24C."
}
```

```bash
evalgo -dataset examples/agent.json -metrics tool_correctness          # free, offline
evalgo -dataset examples/agent.json -judge env \
  -metrics tool_correctness,argument_correctness,task_completion,step_efficiency,plan_quality,plan_adherence
```

This mirrors DeepEval's agent metrics across the same three layers — action
(`tool_correctness`, `argument_correctness`), overall execution (`task_completion`,
`step_efficiency`), and reasoning (`plan_quality`, `plan_adherence`) — using the
record-then-evaluate model instead of `@observe`-style tracing. A metric whose
inputs are absent (no plan, no tool calls) skips and passes, so partial datasets
score cleanly.

`examples/golden.json` deliberately includes a hallucinated answer; `faithfulness`
scores it `0.00` even though it carries a citation that fools the deterministic
checks.

## License

MIT
