package wasmgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/ketang/zolem/internal/fixture"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	MaxResultBytes = 1 << 20
	MaxChunks      = 4096

	memoryLimitPages = 256 // 16 MiB
)

var requiredExports = map[string]byte{
	"alloc":       exportKindFunction,
	"dealloc":     exportKindFunction,
	"generate":    exportKindFunction,
	"result_ptr":  exportKindFunction,
	"result_len":  exportKindFunction,
	"result_free": exportKindFunction,
	"memory":      exportKindMemory,
}

// allowedBoundaryExports lists names that rustc/lld and similar toolchains
// emit as standard linker-defined boundary globals. They mark the end of
// static data and the start of heap space and are not generator entry points,
// so the validator accepts them when they appear with the global kind. Any
// other extra export is still rejected so unintended callable surface is not
// silently treated as supported API.
var allowedBoundaryExports = map[string]byte{
	"__data_end":  exportKindGlobal,
	"__heap_base": exportKindGlobal,
}

// Generator executes a compiled, validated WASM content generator.
type Generator struct {
	rt       wazero.Runtime
	compiled wazero.CompiledModule
	timeout  time.Duration
}

// Compile validates and compiles a freestanding WASM content generator.
func Compile(wasmBytes []byte, timeout time.Duration) (*Generator, error) {
	if timeout <= 0 {
		return nil, errors.New("wasm generate timeout must be positive")
	}
	if err := validateImportExportSurface(wasmBytes); err != nil {
		return nil, err
	}

	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(memoryLimitPages))
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, fmt.Errorf("compile WASM generator: %w", err)
	}
	if err := validateCompiledModule(compiled); err != nil {
		_ = compiled.Close(ctx)
		_ = rt.Close(ctx)
		return nil, err
	}
	return &Generator{rt: rt, compiled: compiled, timeout: timeout}, nil
}

func (g *Generator) Close(ctx context.Context) error {
	return errors.Join(g.compiled.Close(ctx), g.rt.Close(ctx))
}

func (g *Generator) Generate(ctx context.Context, req fixture.MatchRequest) ([]string, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal WASM generator input: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	inst, err := g.rt.InstantiateModule(ctx, g.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions())
	if err != nil {
		return nil, fmt.Errorf("instantiate WASM generator: %w", err)
	}
	defer inst.Close(ctx)

	mem := inst.ExportedMemory("memory")
	if mem == nil {
		return nil, errors.New("WASM generator has no exported memory")
	}

	alloc := inst.ExportedFunction("alloc")
	dealloc := inst.ExportedFunction("dealloc")
	generate := inst.ExportedFunction("generate")
	resultPtr := inst.ExportedFunction("result_ptr")
	resultLen := inst.ExportedFunction("result_len")
	resultFree := inst.ExportedFunction("result_free")

	inputPtr, err := callU32(ctx, alloc, uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("call alloc: %w", err)
	}
	defer func() {
		_, _ = dealloc.Call(ctx, uint64(inputPtr), uint64(len(input)))
	}()

	if !mem.Write(inputPtr, input) {
		return nil, fmt.Errorf("write WASM generator input at %d length %d", inputPtr, len(input))
	}

	handle, err := callU32(ctx, generate, uint64(inputPtr), uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("call generate: %w", err)
	}
	defer func() {
		_, _ = resultFree.Call(ctx, uint64(handle))
	}()

	n, err := callU32(ctx, resultLen, uint64(handle))
	if err != nil {
		return nil, fmt.Errorf("call result_len: %w", err)
	}
	if n > MaxResultBytes {
		return nil, fmt.Errorf("WASM generator result exceeds %d bytes", MaxResultBytes)
	}
	ptr, err := callU32(ctx, resultPtr, uint64(handle))
	if err != nil {
		return nil, fmt.Errorf("call result_ptr: %w", err)
	}

	result, ok := mem.Read(ptr, n)
	if !ok {
		return nil, fmt.Errorf("read WASM generator result at %d length %d", ptr, n)
	}
	result = slices.Clone(result)
	if !utf8.Valid(result) {
		return nil, errors.New("WASM generator result must be valid UTF-8 JSON")
	}

	var chunks []string
	if err := json.Unmarshal(result, &chunks); err != nil {
		return nil, fmt.Errorf("decode WASM generator result: %w", err)
	}
	if len(chunks) > MaxChunks {
		return nil, fmt.Errorf("WASM generator result exceeds %d chunks", MaxChunks)
	}
	return chunks, nil
}

func callU32(ctx context.Context, fn api.Function, args ...uint64) (uint32, error) {
	if fn == nil {
		return 0, errors.New("missing WASM generator function")
	}
	results, err := fn.Call(ctx, args...)
	if err != nil {
		return 0, err
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("expected one result, got %d", len(results))
	}
	return api.DecodeU32(results[0]), nil
}

func validateCompiledModule(compiled wazero.CompiledModule) error {
	if len(compiled.ImportedFunctions()) > 0 || len(compiled.ImportedMemories()) > 0 {
		return errors.New("WASM generator imports are not supported in v1")
	}
	funcs := compiled.ExportedFunctions()
	for name := range funcs {
		if requiredExports[name] != exportKindFunction {
			return fmt.Errorf("unsupported WASM generator function export %q", name)
		}
	}
	for name, kind := range requiredExports {
		if kind != exportKindFunction {
			continue
		}
		fn, ok := funcs[name]
		if !ok {
			return fmt.Errorf("WASM generator missing %q export", name)
		}
		if err := validateSignature(name, fn); err != nil {
			return err
		}
	}

	memories := compiled.ExportedMemories()
	if len(memories) != 1 || memories["memory"] == nil {
		return errors.New("WASM generator must export exactly one memory named memory")
	}
	return nil
}

func validateSignature(name string, fn api.FunctionDefinition) error {
	params := fn.ParamTypes()
	results := fn.ResultTypes()
	switch name {
	case "alloc", "result_ptr", "result_len":
		if sameValueTypes(params, []api.ValueType{api.ValueTypeI32}) && sameValueTypes(results, []api.ValueType{api.ValueTypeI32}) {
			return nil
		}
	case "dealloc":
		if sameValueTypes(params, []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}) && len(results) == 0 {
			return nil
		}
	case "generate":
		if sameValueTypes(params, []api.ValueType{api.ValueTypeI32, api.ValueTypeI32}) && sameValueTypes(results, []api.ValueType{api.ValueTypeI32}) {
			return nil
		}
	case "result_free":
		if sameValueTypes(params, []api.ValueType{api.ValueTypeI32}) && len(results) == 0 {
			return nil
		}
	}
	return fmt.Errorf("WASM generator export %q has wrong signature", name)
}

func sameValueTypes(a, b []api.ValueType) bool {
	return slices.Equal(a, b)
}
