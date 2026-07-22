# AgentBench — a tool-calling benchmark for LLMs

A benchmark suite that measures how well a model **uses tools**: does it pick the
right tool, pass the right arguments, orchestrate multi-step chains, follow
conditional logic, and — crucially — know when *not* to call a tool at all.

Runs against any **OpenAI-compatible** `/chat/completions` endpoint — local Ollama
(`http://localhost:11434/v1`) or a hosted gateway — so local and frontier models
are scored on the exact same tasks, side by side, and ranked in a task × model grid.

Built on [Eval-Go](../README.md)'s `bench` engine: deterministic scoring where
possible, LLM-as-judge where needed, with caching and cost tracking.

## Why this exists

Everyone can call *a* tool. What separates models is the hard part:

- **Conditional orchestration** — compute, compare, then take the *right* branch.
- **Distractor resistance** — pick `unit_convert` over `convert_currency`,
  `get_stock_price` over `web_search`, when both look plausible.
- **Restraint** — when there is no suitable tool (book a flight, transfer money,
  diagnose an illness), say so instead of misusing one. In our runs, *every*
  frontier model tested failed the "book a flight" trap by faking it with a tool.
- **Long chains** — 3–5 dependent tool calls in the correct order, no missing steps.

## Task sets

| File | Scenarios | Focus |
|---|---|---|
| `tasks/life-50.json` | **50** | Real-world scenarios across **home, work, health, travel, finance, personal** — the flagship set. |
| `tasks/hard-12.json` | 12 | Concentrated hard cases (conditional / distractor / restraint / long chain). Separates frontier models. |
| `tasks/basic-10.json` | 10 | Everyday single-tool + short chains. Saturates for frontier models; good for separating small/local ones. |

Every task carries **ground truth**: `expected_tools` (the correct tool *set*) and a
`rubric`. Conditional tasks include both branches — some are designed so the correct
action is to call *fewer* tools (e.g. a BMI under the threshold means *no* reminder),
which catches over-acting.

## The 10-tool environment

`get_weather`, `web_search`, `calculator`, `send_email`, `create_calendar_event`,
`convert_currency`, plus four deliberate distractors: `unit_convert`,
`get_stock_price`, `set_reminder`, `translate`. The agent executes tools for real
where it can (a working calculator, a full currency table, unit conversions) so
final answers are gradeable, not hand-waved.

## Run it

```bash
cd agentbench
go build -o agent/benchagent ./agent          # build the runner once

# the JUDGE — any OpenAI-compatible endpoint
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...

# --- local Ollama models (no key needed; agent defaults to localhost:11434/v1) ---
evalgo bench --tasks tasks/life-50.json --targets targets.local.json \
  -m tool_correctness,task_completion,step_efficiency \
  --gate tool_correctness,task_completion --cache .cache

# --- cloud models (the agent reads OPENAI_API_KEY from the env, never the file) ---
export OPENAI_API_KEY=sk-...
cp targets.cloud.example.json targets.cloud.json     # edit OPENAI_BASE_URL + models
evalgo bench --tasks tasks/life-50.json --targets targets.cloud.json \
  -m tool_correctness,task_completion,step_efficiency \
  --gate tool_correctness,task_completion --concurrency 4 --cache .cache -o report.json
```

Output is a task × model PASS/FAIL grid plus a usage/cost block. `--cache` makes
re-runs free.

## Scoring

- **`tool_correctness`** (deterministic, free) — did the model call the expected
  *set* of tools? Catches missing, extra (over-calling), and wrong-tool (distractor)
  choices. Order-independent; both over- and under-calling cost points.
- **`task_completion`** (judge) — reading the full trajectory + tool calls, did it
  accomplish the task? Catches the wrong conditional branch, a missing chain step,
  and restraint violations (a tool call the task forbade).
- **`step_efficiency`** (judge) — did it reach the goal without redundant steps?

**Gate on `tool_correctness,task_completion` — not `rubric`.** `rubric` only sees the
final text answer, which is blind to the tool calls that are the whole point here.

## Adding scenarios

Append objects to a `tasks/*.json` file:

```jsonc
{
  "name": "unique-id",
  "input": "the user request",
  "expected_tools": ["tool_a", "tool_b"],   // the correct SET (empty = should call none)
  "rubric": "what a correct run looks like; state the right branch / forbidden actions",
  "meta": {"domain": "home", "type": "conditional-chain"}
}
```

Filter a run to one domain with Eval-Go's `--where`, e.g. `--where domain=health`.
