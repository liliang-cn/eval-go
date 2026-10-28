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

## Original task set

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

## Suites

| file | tasks | tools | what it tests |
|---|---|---|---|
| `tasks/core-30.json` | 30 | classic (10) | the original suite; saturated by frontier models |
| `tasks/hard-15.json` | 15 | classic (10) | restraint, recovery, boundaries; also near saturation |
| `tasks/tools-hard.json` | 51 | life (100) | picking among near-duplicates, argument formats, retries, stale/partial data, pagination, multi-turn corrections, long-horizon clean-ups, prompt injection and phishing, acting on the user's behalf, restraint — English |
| `tasks/zh-life.json` | 63 | life (100) | the same, in Chinese, shaped like real personal-assistant requests (日程、提醒、记账、换算、翻译、查询、条件链、该拒绝的、该追问的) |

The two new suites run in the **life** tool world (below) at a fixed time —
Thursday 2026-10-29 09:00, Asia/Shanghai — so "后天上午十点" has one right answer.
Every name, number and address in them is invented.

## Tool worlds

`tools/` defines the tools once; `benchtool`, `benchmcp` and `benchagent` all
serve them through `tools.Env`, so every runner sees the same world.

- **classic** — the original ten tools, unchanged, so old results stay comparable.
- **life** — 100 tools of the shape a real assistant mounts on every request:
  notes, to-dos, calendar, reminders, alarms, timers, contacts, email, SMS/chat,
  files, quotes, FX, expenses and budgets, units (including 斤/两/亩), translation,
  web search, fetch, a small browser, maps, memory, settings, health logs,
  shopping list, flights/hotels (search only), smart home, parcels. It is crowded
  with near-duplicates on purpose: `create_reminder` / `create_alarm` /
  `create_todo` / `start_timer` / `create_event`, `send_email` / `draft_email` /
  `reply_email`, `get_fx_rate` / `convert_currency`, `get_weather` /
  `get_weather_forecast`, `memory_save` / `create_note`, `control_device` /
  `set_thermostat`. Arguments are validated like a real API (required fields,
  enums, ISO 8601 times, email and phone formats, a reminder in the past), and a
  bad call comes back as `{"error":{"code":"INVALID_ARGUMENT",...}}` for the
  agent to read and fix.

The world is stateful. A run's calls share one state file (`BENCH_STATE`), so a
reminder created in turn one can be moved in turn two, and the final state is
reported back and checked.

### Per-task tool scripts

A task's `tool_script` seeds the world and scripts what tools say back:

```jsonc
"tool_script": {
  "profile": "life",
  "now": "2026-10-29T09:00:00+08:00",            // optional; this is the default
  "seed": {                                     // replaces these collections
    "events":   [{"id": "evt_3", "title": "Hiking with Sam", "start": "...", "end": "..."}],
    "expenses": [...], "inbox": [...], "memories": [...], "packages": [...],
    "pages":    {"https://...": {"title": "...", "text": "...", "links": {"Branches": "https://..."}}},
    "search":   [{"keywords": ["museum"], "results": [{"title": "...", "url": "...", "snippet": "..."}]}]
  },
  "responses": [                                // first matching rule wins
    {"tool": "get_weather_forecast", "times": 1,
     "error": {"code": "RATE_LIMITED", "message": "retry after 1s", "retryable": true, "retry_after_ms": 1000}},
    {"tool": "get_stock_quote", "match": {"symbol": "0700"},
     "return": {"price": 405.2, "as_of": "2026-10-27T16:08:00+08:00"}}
  ]
}
```

`times` makes a rule run out, which is how "rate limited once, then fine" is
scripted. Timeouts, non-retryable errors (`PERMISSION_DENIED`,
`SERVICE_UNAVAILABLE`), stale timestamps, missing fields and pagination
(`page_size` / `next_page_token` on list tools) are all just data.

`benchtool validate tasks/x.json` checks every task before you spend tokens: the
script parses, scripted tools exist, and every tool a check names exists — a
`forbidden` entry with a typo would otherwise pass forever.

## Task schema

```jsonc
{
  "name": "th-mt-dentist",
  "input": "Set a reminder for my dentist appointment on Monday at 3pm — remind me an hour before.",
  "turns": ["Sorry — it's Tuesday, not Monday.", "And put it in my calendar too; 45 minutes."],
  "tool_script": {"profile": "life"},
  "check": {                                   // deterministic, scored by tool_trajectory
    "calls": [
      {"id": "rate", "tool": "get_fx_rate", "args": {"quote": {"equals": "CHF"}}},
      {"tool": "calculator", "after": ["rate"],
       "args": {"expression": {"contains": "0.86"}},
       "or_answer": {"regex": "430"}},          // tool OR the right number shown in the reply
      {"tool": "send_sms", "args": {"phone": {"from": {"call": "find", "path": "results.0.phone"}}}},
      {"tool": "send_email", "turn": 1, "min": 0, "max": 0}   // nothing sent before the user clarified
    ],
    "forbidden": ["create_alarm"],              // "*" = no tool call at all
    "min_calls": 1, "max_calls": 6,            // an efficiency budget: the minimal calls + 2
    "answer": {"contains": ["280"], "regex": ["(10-27|Oct 27)"], "not_contains": ["sold"]},
    "state": [{"path": "reminders",
               "where": {"title": {"contains": "dentist"}, "remind_at": {"regex": "^2026-11-03T14:00"}},
               "count": {"equals": 1}}]
  },
  "rubric": "What a careful human would accept, with the right branch and the named failures.",
  "gate": ["rubric_judge"],                     // optional per-task override of the auto gate
  "meta": {"lang": "en", "domain": "time", "type": "multi-turn-correction"}
}
```

Argument / value assertions: `equals`, `one_of`, `num` (+`tol`, default 0.01),
`min`, `max`, `regex`, `contains`, `contains_any`, `not_contains`, `absent`,
`present`, and `from` (the value must equal a field of an earlier call's
result — "B uses A's output"). Strings compare case-insensitively. Dots reach
nested arguments; in a state `where`, the empty key `""` matches against the
whole element (e.g. "this address appears in to or cc"). `expected_tools` still works and still feeds
`tool_correctness`.

Unknown fields anywhere in a task file are an error, not ignored.

## Scoring and the gate

- **`tool_trajectory`** (deterministic, free) — every assertion in `check`, with a
  reason per failure (`send_sms: arg phone: got "+86 139…", earlier result results.0.phone was "+86 138…"`).
- **`rubric_judge`** — the judge reads the input, the whole conversation, the
  rubric, the final answer, every tool call **with its arguments and result**
  (bounded) and the final tool state. Recorded calls are ground truth; what the
  agent claims it did is not.
- **`task_completion`** — reported, but see the gate.
- **`tool_correctness`** — set membership of `expected_tools`, kept for old suites.

**The default gate is `auto`:** a task is gated by `rubric_judge` when it has a
rubric and by `tool_trajectory` when it has a check (both when it has both);
`task_completion` gates only a task with neither, or one that lists it in its own
`gate`. Rationale: `task_completion` asks "did it do what was asked", and for a
request that must be refused the correct run does not do what was asked — the
2026-10-28 run failed every correct refusal on it. The rubric says what correct
means for each task, so the rubric decides.

Every name is checked up front: an unknown metric in `-m` or `--gate`, a gate
metric that is not scored, an auto gate that needs `rubric_judge` or
`tool_trajectory` without it in `-m`, an unknown `--where` key, an unknown
`--only` target — all are errors that list the valid names.

## Judge calibration

Before trusting the judge, measure it against people:

```bash
evalgo bench calibrate --set calibration/rubric-gold.json   # exit 1 below 85% agreement or kappa 0.6
```

`calibration/rubric-gold.json` holds 42 real transcripts, 24 pass / 18 fail: 28
from the classic suites (opus, sonnet, haiku, agent-go, harness-rs, neumann) and
14 from the life suites (multi-turn conversations, recorded tool results, final
tool state). Each carries a gold verdict derived from the task's rubric and the
reasoning in `meta.gold_reason`. The command prints the confusion matrix, accuracy, Cohen's
kappa, and every disagreement next to the human reasoning. Re-run it whenever
the judge model or the judge prompt changes.

## Run it

```bash
cd agentbench
go build -o bin/benchtool ./cmd/benchtool
(cd adapters/agentgo-lib && go build -o ../../bin/agentgo-lib .)
(cd .. && go build -o agentbench/bin/evalgo ./cmd/evalgo)

export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...      # the judge: not one of the candidates

./bin/evalgo bench calibrate --set calibration/rubric-gold.json
./bin/evalgo bench --tasks tasks/tools-hard.json --targets targets.claude.json \
  --runs 3 --concurrency 4 --cache .judge-cache --per-task -o results/run-tools-hard.json
```

Use `--runs 3` to rank anything. One run cannot separate two agents: a flaky
task looks like a win or a loss.

Output: a task × agent PASS/FAIL grid; per target pass@1 (the mean per-task pass
rate) with a 95% interval computed over tasks, pass@k, pass^k, and the flaky
tasks; a paired comparison of every two targets (mean per-task difference with
its interval, and a sign-flip permutation p-value); and what each attempt cost —
wall time, model calls (auxiliary ones counted separately), tool calls, tokens.
`--per-task` adds the same per task.

Targets run separately can be compared afterwards:

```bash
./bin/evalgo bench stats results/a.json results/b.json --a sonnet --b opus --per-task
```

## Adapter protocol

A target is any program. It gets the task as JSON on stdin and as environment:
`EVAL_NAME`, `EVAL_INPUT`, `EVAL_RUBRIC`, `EVAL_EXPECTED`, `EVAL_EXPECTED_TOOLS`,
`EVAL_CONTEXT`, `EVAL_FILES`, and (new, all ignorable) `EVAL_TURNS` (JSON array of
follow-up user messages), `EVAL_TOOL_SCRIPT`, `EVAL_META`. It prints one JSON
object:

```jsonc
{
  "output": "final assistant message",
  "tool_calls": [{"name": "...", "args": {...}, "output": "raw result", "turn": 1}],
  "turns": [{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}],  // multi-turn
  "state": {...},                                  // benchtool state, for state checks
  "usage": {"model_calls": 6, "aux_model_calls": 2, "tool_calls": 2,
            "prompt_tokens": 71850, "completion_tokens": 382, "cached_tokens": 53520,
            "model_ms": 27244, "calls": [{"kind": "loop", "ms": 5129, "prompt_tokens": 17620}], "source": "adapter"}
}
```

An old adapter keeps working on single-turn tasks. On a multi-turn task, an
output whose `turns` does not show every user turn is a run error — never a
conversation graded as if it had happened. The harness measures wall time itself.

`adapters/agentgo-lib` implements all of it on agent-go v3.76.0: each turn is a
`Run` with the same `WithSessionID`; a metering wrapper around the LLM client
counts every model request, auxiliary ones included; tool results are recorded;
the final state comes from `benchtool state`. `runEndSummary` reads agent-go's
forthcoming `run_end` summary from `ExecutionResult.Metadata` when a release
provides it, without depending on it.

## Adding scenarios

Append objects to a `tasks/*.json` file (schema above), run
`benchtool validate`, then run the suite once against a null target to see which
tasks a do-nothing agent already passes deterministically.

Filter a run with `--where`, e.g. `--where lang=zh` or `--where type=restraint`.

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
- `langgraph_mcp_agent.py` — the same LangGraph shape on the **benchmcp** tool set
  over stdio (`langchain-mcp-adapters`), so LangGraph can be scored on exactly the ten
  tools superai and superleo mount. Set `BENCHMCP=./bin/benchmcp` in the target env.
  Use this one when the question is "which loop is better"; the six-tool guests
  answer "how does a framework cope with a thinner tool set".

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
