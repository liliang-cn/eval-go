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
| `-concurrency` / `-rps` | parallel samples / judge rate limit |
| `-threshold` / `-fail-under` | metric pass threshold / suite CI gate |

## Library quick start

```go
import (
    evalgo "github.com/liliang-cn/eval-go"
    "github.com/liliang-cn/eval-go/llmjudge"
)

judge, _ := llmjudge.FromEnv()          // LLM_BASE_URL / LLM_API_KEY / LLM_MODEL
judge = evalgo.RateLimit(judge, 4, 2)   // token-bucket; avoid 429s

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

RAG metrics use the DeepEval/RAGAS-style decomposition (extract atomic units →
verify each) rather than one vague 0–1 score — more reliable and explainable.

`examples/golden.json` deliberately includes a hallucinated answer; `faithfulness`
scores it `0.00` even though it carries a citation that fools the deterministic
checks.

## License

MIT
