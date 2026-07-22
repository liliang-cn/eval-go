# agent-bench — tool-calling benchmark harness

Compare LLMs on **tool calling** (BFCL / τ-bench style) with `evalgo bench`: run the
same tasks through several models and score each run with a task × model PASS/FAIL grid.

Works against any **OpenAI-compatible** `/chat/completions` endpoint — local Ollama
(`http://localhost:11434/v1`) or a cloud gateway — so the same harness benchmarks
local and hosted models side by side.

## Pieces

| File | What |
|---|---|
| `agent/` | a tiny Go program (`benchagent`) — an eval-go `ExecTarget`. Reads the task from `EVAL_INPUT`, offers a fixed tool set to the model, runs a bounded tool-calling loop, and prints a JSON `RunOutput` (tool calls + final answer). |
| `tasks-basic.json` | 10 everyday tool tasks (single-tool + short chains). Saturates for frontier models; good for separating small/local models. |
| `tasks-hard.json` | 12 hard tasks that separate frontier models: **conditional orchestration**, **distractor tools** (right tool among confusable ones), **restraint traps** (must NOT call a tool), and **long tool chains**. |
| `targets.local.json` | 3 local Ollama models. |
| `targets.cloud.example.json` | template for cloud models — fill in `OPENAI_BASE_URL`; the API key comes from the environment (never commit keys). |

The tool set includes deliberate distractors (`unit_convert` vs `convert_currency`,
`get_stock_price` vs `web_search`, `set_reminder` vs `create_calendar_event`) so a
task can test whether a model picks the *right* tool, not just *a* tool.

## Run

```bash
cd examples/agent-bench
go build -o agent/benchagent ./agent          # build the target program once

# 1) local Ollama models (no judge key needed for the models; judge still uses LLM_*)
export LLM_BASE_URL=... LLM_API_KEY=... LLM_MODEL=...   # the JUDGE (any OpenAI-compatible)
evalgo bench --tasks tasks-hard.json --targets targets.local.json \
  -m tool_correctness,argument_correctness,task_completion,step_efficiency,rubric \
  --gate tool_correctness,task_completion --cache .cache

# 2) cloud models — the agent reads OPENAI_API_KEY from the env (kept out of the JSON)
export OPENAI_API_KEY=sk-...                            # the models under test
cp targets.cloud.example.json targets.cloud.json        # edit OPENAI_BASE_URL / models
evalgo bench --tasks tasks-hard.json --targets targets.cloud.json \
  -m tool_correctness,argument_correctness,task_completion,step_efficiency,rubric \
  --gate tool_correctness,task_completion --concurrency 4 --cache .cache -o report.json
```

## Scoring — why this gate

- **`tool_correctness`** (deterministic, free) — did the model call the expected *set*
  of tools? Catches missing, extra (over-calling), and wrong-tool (distractor) choices.
- **`task_completion`** (judge) — reading the trajectory + tool calls, did it actually
  accomplish the task? Catches wrong conditional branch, missing chain steps, and
  restraint violations (a tool call the task forbade).
- `argument_correctness` / `step_efficiency` / `rubric` are reported too but not gated.

**Gate on `tool_correctness,task_completion`, not `rubric`** — `rubric` only sees the
final text answer, which is blind to the tool calls that are the whole point here.

Token spend is tracked in the report's usage block; `--cache` makes re-runs free.
