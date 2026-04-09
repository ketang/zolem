# Local Runtime Mode

Local runtime mode lets you create mock behavior at runtime through a localhost
control plane instead of editing `zolem.yaml`.

This mode is designed for local development only right now:

- profiles are stored in memory
- listeners are stored in memory
- all addresses must bind to loopback
- there is no auth or TTL enforcement yet
- the current local runtime backends are `lorem` and `faker`
- the current local runtime listeners are HTTP-only

## Concepts

There are two resources:

- profile: describes the response behavior, such as `lorem` or `faker`
- listener: binds one local address to one provider and one profile

Each listener exposes:

- provider-compatible endpoints such as `/v1/chat/completions` or `/v1/messages`
- a local introspection endpoint at `/_zolem/state`

## Start The Admin Server

Run:

```bash
go run ./cmd/zolem -local-admin-addr 127.0.0.1:18090
```

Health check:

```bash
curl http://127.0.0.1:18090/_zolem/health
```

Expected response:

```json
{"status":"ok"}
```

## Manage Profiles

Create a `lorem` profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"lorem"}' \
  http://127.0.0.1:18090/_zolem/profiles/demo
```

Create a `faker` profile:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"backend":"faker"}' \
  http://127.0.0.1:18090/_zolem/profiles/faker-demo
```

List profiles:

```bash
curl http://127.0.0.1:18090/_zolem/profiles
```

Delete a profile:

```bash
curl -X DELETE http://127.0.0.1:18090/_zolem/profiles/demo
```

Notes:

- a profile cannot be deleted while a listener is still using it
- unsupported backends are rejected at profile creation time

## Manage Listeners

Create a listener on an automatically assigned port:

```bash
curl -X PUT \
  -H 'Content-Type: application/json' \
  -d '{"addr":"127.0.0.1:0","provider":"openai","profile":"demo"}' \
  http://127.0.0.1:18090/_zolem/listeners/openai-demo
```

Example response:

```json
{
  "name": "openai-demo",
  "addr": "127.0.0.1:19001",
  "provider": "openai",
  "profile": "demo",
  "backend": "lorem",
  "base_url": "http://127.0.0.1:19001"
}
```

List listeners:

```bash
curl http://127.0.0.1:18090/_zolem/listeners
```

Delete a listener:

```bash
curl -X DELETE http://127.0.0.1:18090/_zolem/listeners/openai-demo
```

Listener rules:

- the address must be loopback-only
- the provider must currently be `openai`, `anthropic`, or `gemini`
- the referenced profile must already exist

## Use A Listener

After listener creation, use the returned `base_url` as the client base URL.
Everything else should remain provider-shaped.

OpenAI example:

```bash
curl -X POST \
  -H 'Authorization: Bearer sk-test' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19001/v1/chat/completions
```

Anthropic example:

```bash
curl -X POST \
  -H 'x-api-key: test-key' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":32,"messages":[{"role":"user","content":"hi"}]}' \
  http://127.0.0.1:19101/v1/messages
```

## Introspection

Each listener exposes:

```bash
curl http://127.0.0.1:19001/_zolem/state
```

Example response:

```json
{
  "provider": "openai",
  "profile": "demo",
  "backend": "lorem",
  "listener": "127.0.0.1:19001"
}
```

## End-To-End Verification

Run the repo script:

```bash
./scripts/test-local-runtime.sh
```

This script:

- runs the package tests
- starts the local admin server
- creates a demo profile
- creates an OpenAI listener
- verifies `/_zolem/state`
- calls `/v1/chat/completions`
- deletes the listener and profile

## TLS Status

Local runtime TLS is not wired up yet in the current branch.

The repo already includes [scripts/generate-certs.sh](/home/ketan/.codex/memories/worktrees/zolem-local-runtime-config-design/scripts/generate-certs.sh) for generating local certificates with `mkcert`, and that flow will be used when the next slice adds TLS to the local admin and data-plane listeners.
