package fixture

import (
	"context"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type CompiledModule struct {
	compiled wazero.CompiledModule
}

type Runner struct {
	rt wazero.Runtime
}

func NewRunner() *Runner {
	return &Runner{rt: wazero.NewRuntime(context.Background())}
}

func (r *Runner) Close() {
	r.rt.Close(context.Background())
}

// CompileWAT compiles a WebAssembly Text format module.
func (r *Runner) CompileWAT(ctx context.Context, wat []byte) (CompiledModule, error) {
	compiled, err := r.rt.CompileModule(ctx, wat)
	if err != nil {
		return CompiledModule{}, fmt.Errorf("compile WAT: %w", err)
	}
	return CompiledModule{compiled: compiled}, nil
}

// CompileWASM compiles a binary WASM module.
func (r *Runner) CompileWASM(ctx context.Context, wasmBytes []byte) (CompiledModule, error) {
	compiled, err := r.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return CompiledModule{}, fmt.Errorf("compile WASM: %w", err)
	}
	return CompiledModule{compiled: compiled}, nil
}

// Score instantiates the module, writes input to memory at offset 0,
// calls match(0, len(input)), returns f32 score. Negative = no match.
func (r *Runner) Score(ctx context.Context, mod CompiledModule, input []byte) (float32, error) {
	cfg := wazero.NewModuleConfig().WithName("")
	inst, err := r.rt.InstantiateModule(ctx, mod.compiled, cfg)
	if err != nil {
		return -1, fmt.Errorf("instantiate: %w", err)
	}
	defer inst.Close(ctx)

	// Fixtures are expected to export memory under the standard "memory" name;
	// using any memory instance would miss the error path the loader relies on.
	mem := inst.ExportedMemory("memory")
	if mem == nil {
		return -1, fmt.Errorf("module has no exported memory")
	}
	if !mem.Write(0, input) {
		return -1, fmt.Errorf("failed to write input to WASM memory (size %d)", len(input))
	}

	matchFn := inst.ExportedFunction("match")
	if matchFn == nil {
		return -1, fmt.Errorf("module does not export 'match' function")
	}

	results, err := matchFn.Call(ctx, uint64(0), uint64(len(input)))
	if err != nil {
		return -1, fmt.Errorf("call match: %w", err)
	}

	score := api.DecodeF32(results[0])
	return score, nil
}
