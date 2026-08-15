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

## Task set

`tasks/core-30.json` — **30 scenarios**, 5 each across **home, work, health,
travel, finance, personal**. Curated (not generated) for diagnostic value: heavy
on the types that actually separate agents — **restraint** (8, the single biggest
differentiator we've found), **conditional** / **conditional-chain** (10), **chains**
(6) — and light on plain distractor-tool picking (1), which every model we've
tested already saturates near 100%.

Every task carries **ground truth**: `expected_tools` (the correct tool *set*) and a
`rubric`. Conditional tasks include both branches — some are designed so the correct
action is to call *fewer* tools (e.g. a BMI under the threshold means *no* reminder),
which catches over-acting.

## The 10-tool environment

`get_weather`, `web_search`, `calculator`, `send_email`, `create_calendar_event`,
`convert_currency`, plus four deliberate distractors: `unit_convert`,
`get_stock_price`, `set_reminder`, `translate`. Tools execute for real where they
can (a working calculator, a full USD/EUR/GBP/JPY table, unit conversions) so
final answers are gradeable, not hand-waved.

They are defined once in `tools/` and served two ways, so every agent under test
is offered the *same* tools and `tool_correctness` is comparable:

| Front end | For | How |
|---|---|---|
| `cmd/benchagent` | a raw model | attaches them to an OpenAI-compatible request |
| `cmd/benchmcp` | an agent with its own runtime | serves them over **MCP stdio** |

Without the MCP server, an agent like goose answers with whatever tools it
happens to have, and `tool_correctness` compares nothing — it cannot tell
"picked the wrong tool" from "was offered a different tool".

```bash
go build -o bin/benchmcp ./cmd/benchmcp

# goose: load the benchmark tools for this run
goose run --with-extension "$PWD/bin/benchmcp" --text "..."

# any other MCP client: point it at ./bin/benchmcp over stdio
```

## Run it

```bash
cd agentbench
go build -o bin/benchagent ./cmd/benchagent   # build the runner once

# the JUDGE — any OpenAI-compatible endpoint
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...

# --- local Ollama models (no key needed; agent defaults to localhost:11434/v1) ---
evalgo bench --tasks tasks/core-30.json --targets targets.local.json \
  -m tool_correctness,task_completion,step_efficiency \
  --gate tool_correctness,task_completion --cache .cache

# --- cloud models (the agent reads OPENAI_API_KEY from the env, never the file) ---
export OPENAI_API_KEY=sk-...
cp targets.cloud.example.json targets.cloud.json     # edit OPENAI_BASE_URL + models
evalgo bench --tasks tasks/core-30.json --targets targets.cloud.json \
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

## Guest frameworks (LangGraph / CrewAI / smolagents)

`adapters/guests/` puts three third-party agent frameworks on the same task set as
the in-house agents, so the numbers compare *agent loops* rather than tool surfaces:

- `tools.py` — one shared tool suite (`resolve_datetime`, `set_reminder`,
  `add_schedule`, `save_note`, `calculator`, `web_search`) bound identically by all
  three guests. There is deliberately no email/phone/booking tool, so the restraint
  tasks still test whether the agent admits a missing capability. State goes to
  `./.guest-home/<target>/store.json`; tool calls are recorded by the suite itself,
  not by per-framework callbacks.
- `langgraph_agent.py` / `crewai_agent.py` / `smolagents_agent.py` — default
  framework shapes (prebuilt ReAct graph, single Agent+Task crew, `ToolCallingAgent`),
  one plain shared system prompt, no defensive prompt engineering.

```bash
python3.12 -m venv adapters/guests/.venv
./adapters/guests/.venv/bin/pip install -r adapters/guests/requirements.txt
cp targets.guests.example.json targets.guests.json    # then fill in the gateway env

# one framework, one round
TARGETS_FILE=targets.guests.json ./run-with-target-env.sh langgraph \
  ./bin/evalgo bench --tasks tasks/products-50.json --targets targets.guest-langgraph.json \
  --gate task_completion --cache .judge-cache --concurrency 2 --timeout 8m \
  -o results/report-guest-langgraph-run1.json
```

Run the frameworks **serially** with a pause between them: the guests, the in-house
agents and the judge all share one gateway pool, and a parallel run trips
`429 model_cooldown`. Before trusting a round, count samples with an empty `output`
in the report JSON — more than a handful means the pool collapsed, not that the
framework lost.

## Guest framework: qi-harness (Qi language)

`adapters/qiharness/` puts [qi-harness](https://github.com/qilang-project) — an agent
framework written in **Qi (奇语)**, a compiled language with Chinese keywords — on the
same task set. Same rules as the Python guests: the six-tool surface, no email/phone
tool, and the one plain system prompt from `tools.py`.

- `qiharness_agent.qi` — the whole adapter: `创建代理` + six `添加工具` + `运行`
  (qi-harness's automatic tool-dispatch loop), plus Qi reimplementations of the guest
  tools (a hand-written recursive-descent evaluator for `calculator`, a relative-time
  resolver for `resolve_datetime`, `HTTP.请求` against the same gateway for
  `web_search`). State goes to `./.qi-home/store.json`; tool calls are recorded by the
  handlers themselves, so the `tool_calls` channel is the real dispatch order.
- `run.sh` — keeps stdout clean (the Qi compiler and qi-harness's legacy trace sink
  both write there, so the run goes to stderr and the protocol line is read back from
  a file), compiles the agent once instead of `qi run`-ing it 50 times, and creates the
  `Harness` package symlink the Qi import resolver needs.

Requires the `qi` toolchain (`qi run` / `qi compile`) and a qi-harness checkout.
Point `QIHARNESS_DIR` at it; the default is the sibling layout
`<repos>/dev/lang/qilang/qi-harness`.

```bash
cp targets.qiharness.example.json targets.qiharness.json   # then fill in the gateway env

TARGETS_FILE=targets.qiharness.json ./run-with-target-env.sh qiharness \
  ./bin/evalgo bench --tasks tasks/products-50.json --targets targets.qiharness.json \
  --gate task_completion --cache .judge-cache --concurrency 2 --timeout 8m \
  -o results/report-guest-qiharness-run1.json
```
