package fixture

// Fixture represents a loaded canned response with its compiled match module.
type Fixture struct {
	ID           string
	Provider     string
	Version      string
	Stream       bool
	Status       int
	ResponseBody []byte
	WASMPath     string          // path to match.wasm; empty if not yet loaded
	Module       *CompiledModule // nil if no match.wasm present
}
