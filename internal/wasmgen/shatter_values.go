package wasmgen

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type shatterWazeroValueSet struct {
	runtime    wazero.Runtime
	compiled   wazero.CompiledModule
	module     api.Module
	function   api.Function
	definition api.FunctionDefinition
}

var (
	shatterWazeroValuesOnce sync.Once
	shatterWazeroValues     *shatterWazeroValueSet
	shatterWazeroValuesErr  error
)

// shatterFunctionWASM exports `value(i32) i32`, which is enough for Shatter to
// exercise api.Function and api.FunctionDefinition call paths without depending
// on a production generator module.
var shatterFunctionWASM = []byte{
	0x00, 0x61, 0x73, 0x6d,
	0x01, 0x00, 0x00, 0x00,
	0x01, 0x06, 0x01, 0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x09, 0x01, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x20, 0x00, 0x0b,
}

func newShatterFunctionDefinition() api.FunctionDefinition {
	values, err := shatterWazeroRuntimeValues()
	if err != nil {
		panic(err)
	}
	return values.definition
}

func newShatterFunction() api.Function {
	values, err := shatterWazeroRuntimeValues()
	if err != nil {
		panic(err)
	}
	return values.function
}

func shatterWazeroRuntimeValues() (*shatterWazeroValueSet, error) {
	shatterWazeroValuesOnce.Do(func() {
		ctx := context.Background()
		rt := wazero.NewRuntime(ctx)
		compiled, err := rt.CompileModule(ctx, shatterFunctionWASM)
		if err != nil {
			_ = rt.Close(ctx)
			shatterWazeroValuesErr = fmt.Errorf("compile Shatter WASM function: %w", err)
			return
		}
		module, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			_ = compiled.Close(ctx)
			_ = rt.Close(ctx)
			shatterWazeroValuesErr = fmt.Errorf("instantiate Shatter WASM function: %w", err)
			return
		}
		function := module.ExportedFunction("value")
		if function == nil {
			_ = module.Close(ctx)
			_ = compiled.Close(ctx)
			_ = rt.Close(ctx)
			shatterWazeroValuesErr = fmt.Errorf("Shatter WASM function missing value export")
			return
		}
		definition := compiled.ExportedFunctions()["value"]
		if definition == nil {
			_ = module.Close(ctx)
			_ = compiled.Close(ctx)
			_ = rt.Close(ctx)
			shatterWazeroValuesErr = fmt.Errorf("Shatter WASM function missing value definition")
			return
		}
		shatterWazeroValues = &shatterWazeroValueSet{
			runtime:    rt,
			compiled:   compiled,
			module:     module,
			function:   function,
			definition: definition,
		}
	})
	return shatterWazeroValues, shatterWazeroValuesErr
}
