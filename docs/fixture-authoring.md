# Fixture Authoring

The `fixture` backend loads responses from a fixtures directory. Fixtures are
organized in namespaces (subdirectories). Selection is configured at the
namespace level via a `fixtures.yaml` file or a `selector.wasm` module.

## Namespace Layout

A namespace is a subdirectory under the fixtures root. Each fixture lives in
its own subdirectory inside the namespace and must contain:

- `meta.yaml` — fixture metadata
- `response.json` or `response.json.tmpl` — the response body

```text
my-fixtures/
└── team-a/
    ├── fixtures.yaml
    └── anthropic-demo/
        ├── meta.yaml
        └── response.json
```

## Selection With fixtures.yaml (Recommended)

Selection is configured at the namespace level via `fixtures.yaml`. The
historical per-fixture `match.cel` and `match.wasm` mechanisms are deprecated
(see [Appendix: Deprecated Per-Fixture Matchers](#appendix-deprecated-per-fixture-matchers)).

Example `fixtures.yaml` (the namespace-level selector that routes requests to
fixtures):

```yaml
provider: anthropic
version: v1
fixtures:
  - expression: 'body["model"] == "claude-3-5-sonnet-20241022"'
    fixture: anthropic-demo
```

Example `meta.yaml` (no per-fixture matcher; selection lives in
`fixtures.yaml`):

```yaml
id: anthropic-demo
provider: anthropic
version: v1
status: 200
```

CEL is the recommended expression language for common request predicates.
Each expression must evaluate to a boolean; `fixtures.yaml` entries are
evaluated in declared order and the first entry whose expression returns `true`
selects its fixture.

CEL expressions can read:

- `provider` as a string
- `version` as a string
- `labels` as `map(string, string)`
- `body` as the validated request JSON

Use bracket access for request JSON and labels:

```cel
body["model"] == "gpt-4o-mini" &&
labels["tenant"] == "acme" &&
body["messages"][0]["content"] == "refund"
```

## WebSocket Responses Fixtures

For OpenAI Responses WebSocket sequences, use `version: v1-responses` in
`fixtures.yaml` and a `sequence` entry:

```yaml
provider: openai
version: v1-responses
fixtures:
  - expression: 'true'
    sequence:
      id: conversation
      on_exhaust: last
      steps: [turn-tool, turn-end]
```

Each step directory uses `meta.yaml` with `version: v1-responses`; its
`response.json` is an array of Responses API event objects:

```json
[
  {"type": "response.created", "sequence_number": 0, "response": {"id": "resp_01", "status": "in_progress", "output": []}},
  {"type": "response.output_text.delta", "sequence_number": 1, "item_id": "msg_01", "output_index": 0, "content_index": 0, "delta": "Done."},
  {"type": "response.completed", "sequence_number": 2, "response": {"id": "resp_01", "status": "completed", "output": []}}
]
```

Zolem sends each array element as one WebSocket text frame.

## Templated Fixtures

Replace `response.json` with `response.json.tmpl` to use Go `text/template`
for dynamic responses. Zolem parses, executes, and validates the rendered JSON
when the fixture-backed listener is created. Bad template syntax or invalid
rendered JSON fails startup before the fixture can serve traffic.

Template example:

```json
{
  "id": {{ json .Faker.UUID }},
  "object": "chat.completion",
  "created": 1,
  "model": {{ json .Runtime.BackendModel }},
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": {{ json (printf "fixture %s request %d render %d" .Fixture.ID .Sequence.ProfileRequest .Sequence.TemplateRender) }}
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
}
```

Templated fixture rules:

- templates use Go `text/template`
- use the `json` helper for dynamic values so the rendered response stays valid JSON
- templates can call the full `gofakeit/v7` faker surface through `.Faker`
- templates cannot read request body, query parameters, path parameters, or headers
- Zolem provides the current UTC time as `.Now`
- `.Sequence.ProfileRequest` increments once per request handled by the profile
- `.Sequence.TemplateRender` increments once per templated fixture render for the profile

Template context fields:

- `.Runtime.ListenerName`
- `.Runtime.ListenerProvider`
- `.Runtime.ProfileName`
- `.Runtime.BackendModel`
- `.Runtime.FixtureNamespace`
- `.Runtime.TLS`
- `.Fixture.ID`
- `.Fixture.Provider`
- `.Fixture.Version`
- `.Fixture.Stream`
- `.Fixture.Status`
- `.Template.Seed`

To make faker output deterministic, set `template_seed` in `meta.yaml`:

```yaml
id: openai-templated
provider: openai
version: v1
status: 200
template_seed: 42
```

When `template_seed` is absent, Zolem chooses a fresh seed for each template
render. Setup-time validation uses a fixed validation seed and does not advance
live profile counters.

## Fixture Listener Setup

In local runtime mode, create a `fixture` profile scoped to a namespace:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"fixture","fixture_namespace":"team-a"}' \
  http://127.0.0.1:18090/_zolem/profiles/fixture-demo

curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"anthropic","profile":"fixture-demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/anthropic-fixture
```

With that profile, Zolem loads fixtures from:

```text
<local-fixtures-dir>/team-a
```

Then call the normal provider endpoint on the returned `base_url`:

```bash
curl -X POST \
  -H 'x-api-key: test-key' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19101/v1/messages
```

If the request matches a fixture, Zolem returns `response.json` or the rendered
`response.json.tmpl`. If no fixture matches, provider behavior falls back to
generated output.

## Appendix: Deprecated Per-Fixture Matchers

Per-fixture `match.cel` (inside `meta.yaml`) and `match.wasm` are deprecated.
They still load and serve traffic, but Zolem prints a startup warning for each
fixture that uses them when the namespace has no `fixtures.yaml` and no
`selector.wasm`. New fixtures should use a namespace-level `fixtures.yaml`
entry; complex routing logic that does not fit CEL should use a namespace-level
`selector.wasm`.

Migration example. A fixture directory whose `meta.yaml` used to embed a CEL
matcher:

```yaml
# my-fixtures/team-a/anthropic-demo/meta.yaml (deprecated)
id: anthropic-demo
provider: anthropic
version: v1
status: 200
match:
  cel: 'body["model"] == "claude-3-5-sonnet-20241022"'
  score: 1
```

becomes a fixture with no per-fixture matcher plus a namespace-level
`fixtures.yaml`:

```yaml
# my-fixtures/team-a/anthropic-demo/meta.yaml
id: anthropic-demo
provider: anthropic
version: v1
status: 200
```

```yaml
# my-fixtures/team-a/fixtures.yaml
provider: anthropic
version: v1
fixtures:
  - expression: 'body["model"] == "claude-3-5-sonnet-20241022"'
    fixture: anthropic-demo
```

Per-fixture `match.wasm` migrates the same way: remove `match.wasm` from the
fixture directory, then either add the routing expression to `fixtures.yaml`
or, for logic that requires WASM, drop a namespace-level `selector.wasm` next
to the fixture subdirectories.

(The deprecated per-fixture `match.cel` uses a different selection model: a
`true` result makes the fixture a candidate with `match.score`, which defaults
to `1`, must be finite and non-negative, and participates in highest-score
selection with ties broken by fixture load order. New configurations should use
`fixtures.yaml` instead.)

For custom scoring or logic that does not fit CEL, the deprecated path is a
per-fixture `match.wasm` file. A fixture cannot define both `match.cel` and
`match.wasm`; Zolem rejects that fixture at load time. A fixture with neither
matcher loads but will never match. New configurations should prefer a
namespace-level `selector.wasm` instead. Selector and legacy matcher ABIs are
documented in [wasm-modules.md](wasm-modules.md).
