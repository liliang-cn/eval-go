"""agentbench exec-target: LangGraph's prebuilt ReAct agent on the benchmcp tool set.

    EVAL_INPUT="..." BENCHMCP=./bin/benchmcp .venv/bin/python langgraph_mcp_agent.py

Same framework shape as langgraph_agent.py -- create_react_agent over ChatOpenAI,
the same plain system prompt -- but the tools come from the benchmark's own MCP
server (cmd/benchmcp) over stdio, exactly as superai and superleo mount them.
That makes this the apples-to-apples run: the guest tool suite in tools.py has no
get_weather / get_stock_price / convert_currency, so on the six-tool surface a
weather task is lost to the tool set, not to the loop.

Tool calls are recorded from the final message state (every AIMessage.tool_calls,
in order) rather than by wrapping the tools, so what the judge sees is what the
model actually emitted.
"""

from __future__ import annotations

import asyncio
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import tools as T  # noqa: E402
from langgraph_agent import _build_agent, _final_text, _model  # noqa: E402

MAX_STEPS = int(os.environ.get("GUEST_MAX_STEPS", "24"))


def _benchmcp() -> str:
    path = os.environ.get("BENCHMCP") or os.path.join(os.getcwd(), "bin", "benchmcp")
    if not os.path.exists(path):
        raise RuntimeError(f"benchmcp not found at {path}; set BENCHMCP")
    return os.path.abspath(path)


def _record_from_state(state) -> None:
    messages = state.get("messages", []) if isinstance(state, dict) else []
    for msg in messages:
        for call in getattr(msg, "tool_calls", None) or []:
            name = call.get("name") if isinstance(call, dict) else getattr(call, "name", "")
            args = call.get("args") if isinstance(call, dict) else getattr(call, "args", {})
            if name:
                T.record(name, dict(args or {}))


async def _run_async(user_input: str) -> str:
    from langchain_mcp_adapters.client import MultiServerMCPClient

    client = MultiServerMCPClient(
        {"agentbench": {"command": _benchmcp(), "args": [], "transport": "stdio"}}
    )
    tool_list = await client.get_tools()
    if not tool_list:
        raise RuntimeError("benchmcp served no tools")
    agent = _build_agent(_model(), tool_list)
    state = await agent.ainvoke(
        {"messages": [{"role": "user", "content": user_input}]},
        config={"recursion_limit": MAX_STEPS},
    )
    _record_from_state(state)
    return _final_text(state)


def run(user_input: str) -> str:
    return asyncio.run(_run_async(user_input))


if __name__ == "__main__":
    T.run_adapter(run)
