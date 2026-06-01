package wasmgen

import (
	"context"
	"slices"
	"testing"

	"github.com/tetratelabs/wazero/api"
)

func TestShatterWazeroRuntimeValues(t *testing.T) {
	definition := newShatterFunctionDefinition()
	if definition == nil {
		t.Fatalf("newShatterFunctionDefinition returned nil")
	}
	if !slices.Equal(definition.ParamTypes(), []api.ValueType{api.ValueTypeI32}) {
		t.Fatalf("definition params = %v, want [i32]", definition.ParamTypes())
	}
	if !slices.Equal(definition.ResultTypes(), []api.ValueType{api.ValueTypeI32}) {
		t.Fatalf("definition results = %v, want [i32]", definition.ResultTypes())
	}

	fn := newShatterFunction()
	if fn == nil {
		t.Fatalf("newShatterFunction returned nil")
	}
	results, err := fn.Call(context.Background(), uint64(7))
	if err != nil {
		t.Fatalf("call shatter function: %v", err)
	}
	if got := api.DecodeU32(results[0]); got != 7 {
		t.Fatalf("call result = %d, want 7", got)
	}
}
