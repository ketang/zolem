# Shatter tractability changelog

Log of zolem changes made to improve Shatter coverage, written for the
`shatter-advise` skill: each entry records what changed, the general pattern
it represents, and why it should (or should not) generalize to other target
projects. Newest entries first.

Coverage goal context: ≥90% line coverage of zolem's feature code (non-test
`*.go` under `cmd/` and `internal/`; no generated code exists in that set).
Baseline 2026-07-04 (shatter main-build `2f3cb598`, random explorer, whole
repo warm-cache scan): 27.5% lines (1691/6152 across 377/413 completed
functions).

## 2026-07-05 — zolem-cf4: provider request-body + routing hints

`.shatter/config.yaml` gains per-function `defaults:` for the three provider
HTTP handler methods. Shatter ≥ str-e41w plans a direct `*http.Request`
parameter as a symbolic string body; a schema-valid hint payload lets each
handler pass its decode/validation guards so exploration reaches post-parse
branches. Gemini's `handleGenerate` additionally needed its routing arguments
(`version`, `model`) hinted, because schema lookup is keyed on them.

Scratch-copy validation (str-e41w binary): `handleMessages` 7%→34% lines
(14/14 branches), `handleChatCompletions` 6%→38% (13/13),
`handleGenerate` 7%→15% (7/12 — remainder gated on a `*specs.Validator`
receiver with loaded schemas; that is a constructor-depth lever, not a
body-hint lever).

**Pattern:** API-schema payloads belong in target config hints, not in the
engine — the engine seeds only schema-agnostic JSON (`{}`, small object,
`[]`). Any JSON-API project should hint one valid body per handler; hints
rank above engine seeds. For handlers with routing/dispatch string params,
hint those too or the body never reaches its parser.

**Caution:** when switching a project to a post-str-e41w shatter binary,
clear `.shatter-cache/analysis` — analysis-cache entries are not keyed on
analyzer version (str-2cihu) and stale entries silently keep the old
fixed-empty-body behavior.

## 2026-07-04 — zolem-eyq: sandboxed focused-scan entry point

`make shatter-focused INCLUDE='<glob> [<glob>...]'` runs shatter against
just the matching files with the same Docker sandbox and host-write guards
as the full scan (`scripts/shatter-focused-scan.sh`, shared setup in
`scripts/shatter-scan-lib.sh`, reports under `shatter-report/focused/`).
A one-file focused run completes in seconds–minutes vs the whole-repo
budget.

**Pattern:** target repos that gate shatter behind a sandboxed full-scan
wrapper need a focused variant for iteration, or every coverage experiment
costs a full-repo run. Make-level wrappers must disable recipe-shell glob
expansion (`set -f`) so include globs reach shatter literally — otherwise
`*_test.go` files sneak into scope and `**` breaks.

**Related engine finding (str-tbk9e):** the first whole-repo scan after a
cache clear launches all per-function harness builds concurrently on a cold
Go build cache; every build hits the 60s cap and the scan completes zero
functions. Until fixed engine-side, warm the cache (`go build ./...` or a
prior run) before full scans.
