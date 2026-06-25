# Zolem Documentation

Start with the [project README](../README.md) for what Zolem is and quick-start
examples, and [INSTALL.md](../INSTALL.md) for installation.

## Guides

- [local-runtime.md](local-runtime.md) — overview of the two local execution modes
- [admin-mode.md](admin-mode.md) — local runtime mode (admin server): profiles, listeners, backends, call history
- [fixed-listener.md](fixed-listener.md) — fixed-listener mode: single-listener startup, call recording
- [fixture-authoring.md](fixture-authoring.md) — fixture authoring: namespace layout, fixtures.yaml, templated fixtures
- [recipes/bubblewrap.md](recipes/bubblewrap.md) — no-egress client smoke tests with Bubblewrap
- [wasm-modules.md](wasm-modules.md) — building freestanding WASM content
  generators and selector/matcher modules.
- [testing-e2e.md](testing-e2e.md) — the end-to-end testing standard for the
  repo.
- [source-verification.md](source-verification.md) — verifying the OpenAPI /
  discovery specs Zolem validates requests against.
- [anthropic-spec-snapshot.md](anthropic-spec-snapshot.md) — how the Anthropic
  spec snapshot is captured and refreshed.

## Intent stories

- [stories/](stories/INDEX.md) — storystore intent stories describing
  user-facing CLI and runtime behavior.

## Design specs

- [specs/2026-05-22-call-history-design.md](specs/2026-05-22-call-history-design.md)
  — call-history capture and admin API design.
- [specs/2026-05-23-ci-releases-docker-design.md](specs/2026-05-23-ci-releases-docker-design.md)
  — CI, releases, and Docker design.
- [specs/2026-04-09-ollama-backend-mode.md](specs/2026-04-09-ollama-backend-mode.md)
  — Ollama backend mode spec.
- [2026-04-02-llm-mock-service-design.md](2026-04-02-llm-mock-service-design.md)
  — original LLM mock service design spec.

## Implementation plans

Historical plans kept for context (not current task lists):

- [plans/2026-04-02-zolem-implementation.md](plans/2026-04-02-zolem-implementation.md)
- [plans/2026-04-05-zolem-spec-and-smoke-execution-plan.md](plans/2026-04-05-zolem-spec-and-smoke-execution-plan.md)
- [plans/2026-04-08-local-runtime-configuration-plan.md](plans/2026-04-08-local-runtime-configuration-plan.md)
- [plans/2026-04-09-ollama-backend-mode.md](plans/2026-04-09-ollama-backend-mode.md)

## Contributing

Contributor and agent tooling (refute, Shatter, the verification gate, branch
discipline) lives in [AGENTS.md](../AGENTS.md).
