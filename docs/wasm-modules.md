# Building WASM Modules

Zolem does not require C headers, generated bindings, or any source files from
the Zolem repository to build a WebAssembly module. Compatibility is defined by:

- the exported WASM symbols
- the function signatures for those exports
- the JSON bytes passed through linear memory

Use a freestanding binary `.wasm` target. Do not compile modules for WASI, the
browser, or `env` host functions unless you have verified that the final module
has no imports.

## WASM Surfaces

Zolem has three WASM surfaces. They are similar at the transport level but have
different exports and different jobs.

| Surface | File or profile field | Purpose | Status |
| --- | --- | --- | --- |
| Generator backend | `wasm_module_base64` or `zolemc -wasm-module-file` | Generate assistant content for a `backend=wasm` profile | Recommended for generated responses |
| Namespace selector | `<local-fixtures-dir>/<namespace>/selector.wasm` | Pick one fixture from a namespace | Recommended when CEL is not expressive enough |
| Per-fixture matcher | `<local-fixtures-dir>/<namespace>/<fixture>/match.wasm` | Score one fixture | Deprecated; prefer `fixtures.yaml`, CEL, or `selector.wasm` |

For fixture selection, use CEL in `fixtures.yaml` when the predicate is simple.
Use `selector.wasm` only when the selector needs custom logic or state.

## Generator Backend ABI

The generator backend receives request context JSON in linear memory and returns
UTF-8 JSON that decodes to `[]string`. Zolem joins the strings for
non-streaming responses and emits one provider-native streaming delta per
string for streaming responses.

Input shape:

```json
{
  "provider": "openai",
  "version": "v1",
  "labels": {},
  "body": {}
}
```

Required exports:

```text
memory
alloc(len: i32) -> i32
dealloc(ptr: i32, len: i32)
generate(input_ptr: i32, input_len: i32) -> i32
result_ptr(handle: i32) -> i32
result_len(handle: i32) -> i32
result_free(handle: i32)
```

The value returned by `generate` is an opaque handle. Zolem passes that handle
to `result_ptr`, `result_len`, and `result_free`. The handle can be a pointer,
an index into module-managed state, or any other `i32` value the module
understands.

Generator constraints:

- the module must be binary WASM, not WAT text
- the module must not import anything, including WASI or `env`
- the module must export the exact required ABI, plus only accepted linker
  boundary globals such as `__data_end` and `__heap_base`
- each request gets a fresh WASM instance
- host memory is limited to 16 MiB
- result bytes are capped at 1 MiB
- decoded result arrays are capped at 4096 strings

## Rust Generator Example

This example is intentionally small: it ignores the input request and returns a
fixed two-chunk response. Because Zolem creates a fresh instance per request,
the bump allocator does not need to reclaim memory for this smoke module.

Save as `generator.rs`:

```rust
#![no_std]
#![no_main]

static RESULT: &[u8] = b"[\"Hello \",\"from WASM.\"]";
static mut NEXT: usize = 4096;

#[panic_handler]
fn panic(_: &core::panic::PanicInfo) -> ! {
    loop {}
}

#[no_mangle]
pub extern "C" fn alloc(len: i32) -> i32 {
    unsafe {
        let aligned = (NEXT + 7) & !7;
        NEXT = aligned + len as usize;
        aligned as i32
    }
}

#[no_mangle]
pub extern "C" fn dealloc(_ptr: i32, _len: i32) {}

#[no_mangle]
pub extern "C" fn generate(_input_ptr: i32, _input_len: i32) -> i32 {
    RESULT.as_ptr() as i32
}

#[no_mangle]
pub extern "C" fn result_ptr(handle: i32) -> i32 {
    handle
}

#[no_mangle]
pub extern "C" fn result_len(_handle: i32) -> i32 {
    RESULT.len() as i32
}

#[no_mangle]
pub extern "C" fn result_free(_handle: i32) {}
```

Build it:

```bash
rustup target add wasm32-unknown-unknown
rustc --target wasm32-unknown-unknown \
  -O \
  --crate-type=cdylib \
  generator.rs \
  -o generator.wasm
```

Use `wasm32-unknown-unknown`, not `wasm32-wasip1` or another WASI target.

## C Generator Example

This example uses clang and wasm-ld directly. It has the same fixed response as
the Rust example.

Save as `generator.c`:

```c
typedef unsigned int u32;

static u32 heap = 4096;
static const char result[] = "[\"Hello \",\"from WASM.\"]";

u32 alloc(u32 len) {
  u32 ptr = (heap + 7) & ~7u;
  heap = ptr + len;
  return ptr;
}

void dealloc(u32 ptr, u32 len) {
  (void)ptr;
  (void)len;
}

u32 generate(u32 input_ptr, u32 input_len) {
  (void)input_ptr;
  (void)input_len;
  return (u32)result;
}

u32 result_ptr(u32 handle) {
  return handle;
}

u32 result_len(u32 handle) {
  (void)handle;
  return sizeof(result) - 1;
}

void result_free(u32 handle) {
  (void)handle;
}
```

Build it:

```bash
clang --target=wasm32 \
  -O2 \
  -nostdlib \
  -Wl,--no-entry \
  -Wl,--export=memory \
  -Wl,--export=alloc \
  -Wl,--export=dealloc \
  -Wl,--export=generate \
  -Wl,--export=result_ptr \
  -Wl,--export=result_len \
  -Wl,--export=result_free \
  generator.c \
  -o generator.wasm
```

If your clang or wasm linker emits imports, do not load that module into Zolem.
Adjust the target and linker flags until the final module is freestanding.

## TinyGo And Managed Runtimes

TinyGo, AssemblyScript, and other managed runtimes can be useful, but their
default targets often add host imports, runtime exports, start functions, or
browser/WASI assumptions. Zolem generator modules reject those extra surfaces.

Before relying on one of these toolchains, inspect the final module:

```bash
wasm-objdump -x generator.wasm
```

The import section must be absent or empty. The export section for a generator
must contain only the required ABI exports plus accepted linker boundary
globals. In particular, avoid TinyGo's WASI targets for generator modules.

## Loading A Generator Profile

Start the admin server:

```bash
go run ./cmd/zolem -local-admin-addr 127.0.0.1:18090
```

Create the profile:

```bash
go run ./cmd/zolemc -admin-url http://127.0.0.1:18090 \
  profiles create wasm-demo \
  -wasm-module-file ./generator.wasm \
  -wasm-timeout-ms 100
```

Create an OpenAI-shaped listener:

```bash
go run ./cmd/zolemc -admin-url http://127.0.0.1:18090 \
  listeners create openai-wasm \
  -addr 127.0.0.1:0 \
  -provider openai \
  -profile wasm-demo
```

Use the listener `base_url` from the response, then call the normal provider
endpoint:

```bash
go run ./cmd/zolemc -base-url http://127.0.0.1:19001 \
  request -method POST \
  -path /v1/chat/completions \
  -H 'Authorization: Bearer sk-test' \
  -json-body '{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}'
```

The Rust and C examples above return:

```text
Hello from WASM.
```

For streaming requests, the same module emits two chunks: `Hello ` and
`from WASM.`.

## Namespace selector.wasm ABI

A namespace selector lives at:

```text
<local-fixtures-dir>/<namespace>/selector.wasm
```

Required exports:

```text
memory
select(input_ptr: i32, input_len: i32) -> (output_ptr: i32, output_len: i32)
```

Zolem writes selector input JSON at offset `0` in linear memory and calls
`select(0, len)`. The module returns a pointer and length for output JSON in
the same memory.

Input shape:

```json
{
  "request": {
    "provider": "anthropic",
    "version": "v1",
    "labels": {},
    "body": {}
  },
  "fixtures": [
    {"id": "anthropic-demo", "tags": {}}
  ]
}
```

Output shape:

```json
{"id": "anthropic-demo"}
```

Return `{"id": null}` or an empty output buffer to decline selection and fall
through to the configured backend.

Do not put `selector.wasm` beside `fixtures.yaml`; the two namespace-level
selection mechanisms are mutually exclusive. When `selector.wasm` is present,
per-fixture `match.cel` and `match.wasm` are not allowed.

## Deprecated match.wasm ABI

Legacy per-fixture matchers live inside a single fixture directory:

```text
<local-fixtures-dir>/<namespace>/<fixture>/match.wasm
```

Required exports:

```text
memory
match(input_ptr: i32, input_len: i32) -> f32
```

Zolem writes the request context JSON at offset `0` in linear memory and calls
`match(0, len)`. A negative score means no match. A non-negative score makes
the fixture a candidate, and the highest score wins.

New fixture configurations should prefer `fixtures.yaml` CEL rules for common
predicates or namespace-level `selector.wasm` for custom logic.

## Common Validation Failures

`invalid WASM magic header`
: The file is not binary WASM. Compile WAT text to `.wasm` first.

`WASM generator imports are not supported in v1`
: The module imports WASI, `env`, or another host surface. Use a freestanding
target and remove runtime dependencies.

`WASM generator missing "<name>" export`
: The module did not export one of the required ABI functions or `memory`.

`WASM generator export "<name>" has wrong signature`
: The export exists, but its parameter or result types do not match the ABI.

`unsupported WASM generator export "<name>"`
: The module exports extra functions, memories, tables, or globals. Hide or
remove those exports, or use a lower-level build that exports only the required
ABI.

`WASM generator result must be valid UTF-8 JSON`
: `result_ptr` and `result_len` did not point to UTF-8 JSON bytes.

`decode WASM generator result`
: The result was valid UTF-8 but did not decode to a JSON array of strings.
