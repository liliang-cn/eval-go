# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Note: early history was squashed, so pre-0.4.0 entries are reconstructed from
tag messages and feature areas rather than individual commits.

## [Unreleased]

## [0.5.0] - 2026-08-15

### Added

- **AgentBench** (`agentbench/`) — a tool-calling benchmark built on `evalgo bench`,
  promoted from an example to a first-class part of the repo. A curated 30-task
  set (`tasks/core-30.json`) across home / work / health / travel / finance /
  personal, weighted toward the failure modes that actually separate agents:
  restraint (8), conditional orchestration (10), long chains (6).
- A ten-tool benchmark environment defined once in `agentbench/tools/`, including
  four deliberate distractors (`unit_convert` vs `convert_currency`,
  `get_stock_price` vs `web_search`, `set_reminder` vs `create_calendar_event`).
- `cmd/benchmcp` — serves those tools over **MCP stdio**, so an agent that brings
  its own runtime can be scored on the same tool set as a raw model. Without it
  `tool_correctness` cannot tell "picked the wrong tool" from "was offered a
  different one". Verified with superai (agent-go) and superleo (harness-rs).
- Adapters for agents under test: `superai`, `superleo`, `dsh` (DeepSeek
  Harness), plus guest-framework adapters for LangGraph, CrewAI, smolagents and
  qi-harness.
- `docs/overview.md` — the two-layer model (score an output you bring vs. drive
  an agent to produce one) and the limits of each.

### Fixed

- Guest tool suite survives a busy shared gateway; `web_search` no longer comes
  back empty on it.

### Changed

- `llmjudge` migrated to agent-go v3.6.0.

## [0.4.0] - 2026-06-28

### Added

- Benchmarking: `Bench`/`Target` API to run agents and compare them end-to-end.
- `evalgo bench` CLI command.

## [0.3.0] - 2026-06-27

### Added

- Judge alignment mode (judge meta-evaluation): score the judge itself against
  human-labeled samples.

## [0.2.0] - 2026-06-26

### Added

- Agent, safety, conversational, and red-team metric families.
- Synthetic dataset generation (`synth`).
- Judge-call caching and cost tracking.
- Dataset tooling (JSON / JSONL / CSV loading and manipulation).

### Changed

- README rewritten to cover the full v0.2.0 capabilities.

## [0.1.1] - 2026-06-25

### Changed

- Pinned agent-go to v2.92.0 and added `go.sum` for reproducible standalone
  builds. (Tagged from the same commit as v0.1.0.)

## [0.1.0] - 2026-06-25

### Added

- Initial release: native-Go LLM/RAG evaluation framework, as a library and a
  CLI (`evalgo`).
- Deterministic (code-based) metrics and LLM-as-a-judge semantic metrics.
- Judge adapter for agent-go in a separate `./llmjudge` package, keeping the
  core package stdlib-only.

[Unreleased]: https://github.com/liliang-cn/eval-go/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/liliang-cn/eval-go/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/liliang-cn/eval-go/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/liliang-cn/eval-go/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/liliang-cn/eval-go/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/liliang-cn/eval-go/releases/tag/v0.1.0
