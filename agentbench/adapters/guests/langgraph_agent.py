"""agentbench exec-target: LangGraph's prebuilt ReAct agent.

    EVAL_INPUT="..." .venv/bin/python langgraph_agent.py

Default framework shape on purpose: langgraph.prebuilt.create_react_agent over
langchain-openai's ChatOpenAI, the shared guest tool suite, and the same plain
system prompt every guest gets.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import tools as T  # noqa: E402

MAX_STEPS = int(os.environ.get("GUEST_MAX_STEPS", "24"))


def _model():
    from langchain_openai import ChatOpenAI

    base, key, model = T._gateway()
    return ChatOpenAI(model=model, base_url=base, api_key=key, temperature=0, timeout=180, max_retries=2)


def _tools():
    from langchain_core.tools import StructuredTool
    from pydantic import Field, create_model

    built = []
    for spec in T.TOOL_SPECS:
        schema = create_model(
            f"{spec.name}_args",
            **{arg: (str, Field(description=desc)) for arg, desc in spec.params.items()},
        )
        built.append(
            StructuredTool.from_function(
                func=spec.tracked(),
                name=spec.name,
                description=spec.description,
                args_schema=schema,
            )
        )
    return built


def _build_agent(model, tool_list):
    """create_react_agent moved around between langgraph 0.2/0.6 and langchain 1.x;
    try the current name first and fall back."""
    try:
        from langgraph.prebuilt import create_react_agent
    except ImportError:
        from langchain.agents import create_agent as create_react_agent  # type: ignore

    for kwargs in (
        {"prompt": T.SYSTEM_PROMPT},
        {"system_prompt": T.SYSTEM_PROMPT},
        {"state_modifier": T.SYSTEM_PROMPT},
        {},
    ):
        try:
            return create_react_agent(model, tool_list, **kwargs)
        except TypeError:
            continue
    raise RuntimeError("could not construct a LangGraph react agent with any known signature")


def _final_text(state) -> str:
    messages = state.get("messages", []) if isinstance(state, dict) else []
    for msg in reversed(messages):
        content = getattr(msg, "content", None)
        if content is None and isinstance(msg, dict):
            content = msg.get("content")
        if isinstance(content, list):
            content = "".join(
                p.get("text", "") for p in content if isinstance(p, dict) and p.get("type") == "text"
            )
        if isinstance(content, str) and content.strip() and not getattr(msg, "tool_calls", None):
            if type(msg).__name__ in ("ToolMessage", "HumanMessage"):
                continue
            return content.strip()
    return ""


def run(user_input: str) -> str:
    agent = _build_agent(_model(), _tools())
    state = agent.invoke(
        {"messages": [{"role": "user", "content": user_input}]},
        config={"recursion_limit": MAX_STEPS},
    )
    return _final_text(state)


if __name__ == "__main__":
    T.run_adapter(run)
