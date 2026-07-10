# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Note: early history was squashed, so pre-0.4.0 entries are reconstructed from
tag messages and feature areas rather than individual commits.

## [Unreleased]

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
