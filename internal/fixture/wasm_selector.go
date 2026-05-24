package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// wasmSelector implements Selector by delegating fixture selection to a WASM
// module loaded from <namespace>/selector.wasm.
//
// ABI:
//
//	exports: memory, select(inputPtr i32, inputLen i32) -> (outputPtr i32, outputLen i32)
//
// Host writes input JSON at offset 0 in linear memory and calls select(0, len).
// The module returns a (ptr, len) pair pointing at output JSON inside the same
// linear memory. Output of `{"id": "<fixture-id>"}` selects a fixture; output
// of `{"id": null}` or an empty buffer falls through to the configured
// backend.
//
// Locking discipline (read carefully — this is intentional, not copied from
// wasm.go's per-fixture matcher):
//
//   - The per-fixture matcher in wasm.go avoids locking because it instantiates
//     a fresh module per call (stateless). selector.wasm is the opposite:
//     a single instance is reused across requests so it can implement
//     stateful behaviors (round-robin counters, protocol simulations,
//     request-body-driven state machines). That statefulness REQUIRES
//     shared linear memory across requests.
//
//   - Consequently, the critical section spans the entire write-call-copy
//     round trip: write input → call select → copy output bytes out of WASM
//     memory. The lock CANNOT be released between any of those steps, because
//     a concurrent caller would overwrite linear memory before the first
//     caller finished reading its output.
//
//   - This serialization is load-bearing for state coherence: any
//     implementation that allowed concurrent calls on a shared instance would
//     produce inconsistent state mutations even on otherwise-correct WASM
//     code.
//
//   - Trade-off: when the WASM selector is active, all requests for the
//     namespace are serialized through one mutex. Under concurrent load this
//     is a per-namespace bottleneck. That is the cost of statefulness; if
//     parallelism matters more than shared state, use fixtures.yaml instead.
type wasmSelector struct {
	mu       sync.Mutex
	instance api.Module
	mem      api.Memory
	selectFn api.Function
}

type wasmSelectorInput struct {
	Request  MatchRequest          `json:"request"`
	Fixtures []wasmSelectorFixture `json:"fixtures"`
}

type wasmSelectorFixture struct {
	ID   string            `json:"id"`
	Tags map[string]string `json:"tags"`
}

type wasmSelectorOutput struct {
	ID *string `json:"id"`
}

// newWASMSelector instantiates a compiled selector.wasm module exactly once
// and returns a Selector that reuses the instance across all calls. The
// instance is owned by the runner's underlying wazero runtime and is released
// when the runtime is closed.
func newWASMSelector(ctx context.Context, runner *Runner, mod CompiledModule) (*wasmSelector, error) {
	cfg := wazero.NewModuleConfig().WithName("")
	inst, err := runner.rt.InstantiateModule(ctx, mod.compiled, cfg)
	if err != nil {
		return nil, fmt.Errorf("instantiate selector.wasm: %w", err)
	}
	mem := inst.ExportedMemory("memory")
	if mem == nil {
		_ = inst.Close(ctx)
		return nil, fmt.Errorf("selector.wasm does not export 'memory'")
	}
	selFn := inst.ExportedFunction("select")
	if selFn == nil {
		_ = inst.Close(ctx)
		return nil, fmt.Errorf("selector.wasm does not export 'select' function")
	}
	return &wasmSelector{instance: inst, mem: mem, selectFn: selFn}, nil
}

// Select serializes the request and candidate fixtures to JSON, hands the
// buffer to the WASM `select` function under a mutex, and looks up the
// returned fixture ID in the candidate list. Returns (nil, nil) when the
// module declines to pick (`{"id": null}` or empty output) so the framework
// falls through to the configured backend. Returns an error if the WASM
// returns an ID that is not in the candidate slice.
func (s *wasmSelector) Select(ctx context.Context, req MatchRequest, candidates []Fixture) (*Fixture, error) {
	fixturesIn := make([]wasmSelectorFixture, 0, len(candidates))
	for _, f := range candidates {
		tags := f.Tags
		if tags == nil {
			tags = map[string]string{}
		}
		fixturesIn = append(fixturesIn, wasmSelectorFixture{ID: f.ID, Tags: tags})
	}
	inputBytes, err := json.Marshal(wasmSelectorInput{Request: req, Fixtures: fixturesIn})
	if err != nil {
		return nil, fmt.Errorf("marshal selector input: %w", err)
	}

	// Critical section: write → call → copy. See the wasmSelector doc comment
	// for why this must not be split.
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.mem.Write(0, inputBytes) {
		return nil, fmt.Errorf("failed to write %d input bytes to selector.wasm memory", len(inputBytes))
	}
	results, err := s.selectFn.Call(ctx, uint64(0), uint64(len(inputBytes)))
	if err != nil {
		return nil, fmt.Errorf("call select: %w", err)
	}
	if len(results) != 2 {
		return nil, fmt.Errorf("select returned %d values, want 2", len(results))
	}
	outPtr := api.DecodeU32(results[0])
	outLen := api.DecodeU32(results[1])
	if outLen == 0 {
		return nil, nil
	}
	raw, ok := s.mem.Read(outPtr, outLen)
	if !ok {
		return nil, fmt.Errorf("failed to read selector output (ptr=%d len=%d)", outPtr, outLen)
	}
	// Copy out before parsing — guards against the WASM module reusing the
	// same memory region on its next call, even though the mutex prevents
	// concurrent overlap.
	outBytes := make([]byte, len(raw))
	copy(outBytes, raw)

	var out wasmSelectorOutput
	if err := json.Unmarshal(outBytes, &out); err != nil {
		return nil, fmt.Errorf("parse selector output %q: %w", string(outBytes), err)
	}
	if out.ID == nil || *out.ID == "" {
		return nil, nil
	}
	for i := range candidates {
		if candidates[i].ID == *out.ID {
			return &candidates[i], nil
		}
	}
	return nil, fmt.Errorf("selector.wasm returned unknown fixture id %q", *out.ID)
}
