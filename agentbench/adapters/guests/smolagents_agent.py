"""agentbench exec-target: smolagents ToolCallingAgent.

    EVAL_INPUT="..." .venv/bin/python smolagents_agent.py

ToolCallingAgent, not CodeAgent: the benchmark compares JSON tool calling
against superai / superleo / LangGraph / CrewAI, not a python code sandbox.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import tools as T  # noqa: E402

os.environ.setdefault("HF_HUB_DISABLE_TELEMETRY", "1")
os.environ.setdefault("SMOLAGENTS_TELEMETRY", "0")

MAX_STEPS = int(os.environ.get("GUEST_MAX_STEPS", "12"))


def _model():
    from smolagents import OpenAIServerModel

    base, key, model = T._gateway()
    return OpenAIServerModel(model_id=model, api_base=base, api_key=key, temperature=0)


def _tools():
    from smolagents import Tool

    built = []
    for spec in T.TOOL_SPECS:
        fn = spec.tracked()
        attrs = {
            "name": spec.name,
            "description": spec.description,
            "inputs": {
                arg: {"type": "string", "description": desc} for arg, desc in spec.params.items()
            },
            "output_type": "string",
            "forward": (lambda self, call=fn, **kwargs: call(**kwargs)),
            "skip_forward_signature_validation": True,
        }
        built.append(type(f"{spec.name}_tool", (Tool,), attrs)())
    return built


def _build_agent(model, tool_list):
    from smolagents import ToolCallingAgent

    for kwargs in (
        {"instructions": T.SYSTEM_PROMPT},
        {"description": T.SYSTEM_PROMPT},
        {},
    ):
        try:
            return ToolCallingAgent(
                tools=tool_list, model=model, max_steps=MAX_STEPS, verbosity_level=0, **kwargs
            )
        except TypeError:
            continue
    raise RuntimeError("could not construct a smolagents ToolCallingAgent with any known signature")


def run(user_input: str) -> str:
    agent = _build_agent(_model(), _tools())
    prompt = user_input
    if not getattr(agent, "instructions", None):
        # older smolagents has no `instructions`; prepend the shared system prompt
        prompt = f"{T.SYSTEM_PROMPT}\n\n{user_input}"
    result = agent.run(prompt)
    if isinstance(result, str):
        return result.strip()
    return str(result).strip()


if __name__ == "__main__":
    T.run_adapter(run)
