"""agentbench exec-target: CrewAI, single agent / single task.

    EVAL_INPUT="..." .venv/bin/python crewai_agent.py

CrewAI's LLM layer is LiteLLM, so the shared gateway is addressed as
``openai/<model>`` with an explicit base_url. Role/goal/backstory are kept as
plain as CrewAI allows -- the point is to measure the framework's default agent
loop, not a hand-tuned persona.
"""

from __future__ import annotations

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import tools as T  # noqa: E402

# Module-level so pydantic can resolve the `type[BaseModel]` annotation on the
# dynamically-built tool classes (it looks names up in the class's module globals).
from pydantic import BaseModel, Field, create_model  # noqa: E402

# CrewAI phones home and starts an OTel exporter by default; both add latency and
# noise to a benchmark subprocess.
os.environ.setdefault("CREWAI_TELEMETRY_OPT_OUT", "true")
os.environ.setdefault("CREWAI_DISABLE_TELEMETRY", "true")
os.environ.setdefault("OTEL_SDK_DISABLED", "true")
os.environ.setdefault("ANONYMIZED_TELEMETRY", "False")

_BASE, _KEY, _MODEL = T._gateway()
# LiteLLM reads these directly for the openai/ provider.
os.environ.setdefault("OPENAI_API_KEY", _KEY)
os.environ.setdefault("OPENAI_API_BASE", _BASE)
os.environ.setdefault("OPENAI_BASE_URL", _BASE)

MAX_ITER = int(os.environ.get("GUEST_MAX_STEPS", "24"))


def _llm():
    from crewai import LLM

    model = _MODEL if "/" in _MODEL else f"openai/{_MODEL}"
    return LLM(model=model, base_url=_BASE, api_key=_KEY, temperature=0)


def _tools():
    from crewai.tools import BaseTool

    built = []
    for spec in T.TOOL_SPECS:
        schema = create_model(
            f"{spec.name}_args",
            **{arg: (str, Field(description=desc)) for arg, desc in spec.params.items()},
        )
        fn = spec.tracked()

        def _make(tool_name: str, tool_desc: str, tool_schema: type[BaseModel], call=fn):
            # NB: the class body must not shadow the closure variables it reads,
            # so the parameters are named differently from the pydantic fields.
            class _GuestTool(BaseTool):
                name: str = tool_name
                description: str = tool_desc
                args_schema: type[BaseModel] = tool_schema

                def _run(self, **kwargs) -> str:
                    return call(**kwargs)

            return _GuestTool()

        built.append(_make(spec.name, spec.description, schema))
    return built


def run(user_input: str) -> str:
    from crewai import Agent, Crew, Process, Task

    agent = Agent(
        role="Life assistant",
        goal=T.SYSTEM_PROMPT,
        backstory="You help with everyday tasks: reminders, schedules, notes, arithmetic and lookups.",
        tools=_tools(),
        llm=_llm(),
        max_iter=MAX_ITER,
        verbose=False,
        allow_delegation=False,
    )
    task = Task(
        description=user_input,
        expected_output="The answer to the user's request, in the user's language.",
        agent=agent,
    )
    try:
        crew = Crew(agents=[agent], tasks=[task], process=Process.sequential,
                    verbose=False, telemetry=False, memory=False)
    except TypeError:  # `telemetry` is not a field on every CrewAI version
        crew = Crew(agents=[agent], tasks=[task], process=Process.sequential, verbose=False)
    result = crew.kickoff()
    text = getattr(result, "raw", None) or str(result)
    return text.strip()


if __name__ == "__main__":
    T.run_adapter(run)
