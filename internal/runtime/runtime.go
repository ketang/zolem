package runtimecfg

// RuntimeProfile describes the configured response behavior for a listener.
type RuntimeProfile struct {
	Name                string `json:"name"`
	Backend             string `json:"backend"`
	BackendModel        string `json:"backend_model"`
	ErrorType           string `json:"error_type"`
	ResponseModelPolicy string `json:"response_model_policy"`
	ResponseModel       string `json:"response_model"`
	FixtureNamespace    string `json:"fixture_namespace"`
	OllamaUpstream      string `json:"ollama_upstream,omitempty"`
	// AllowExternalOllamaUpstream opts a profile out of the default
	// loopback/RFC1918 restriction on OllamaUpstream, permitting forwarding to an
	// arbitrary external host. Off by default to preserve the no-egress posture.
	AllowExternalOllamaUpstream bool        `json:"allow_external_ollama_upstream,omitempty"`
	WASMModuleBase64            string      `json:"wasm_module_base64,omitempty"`
	WASMGenerateTimeoutMS       int         `json:"wasm_generate_timeout_ms,omitempty"`
	StreamDelay                 StreamDelay `json:"stream_delay,omitempty"`
}

// StreamDelay describes per-profile streaming pacing.
type StreamDelay struct {
	Mode  string `json:"mode,omitempty"`
	MS    int    `json:"ms,omitempty"`
	MinMS int    `json:"min_ms,omitempty"`
	MaxMS int    `json:"max_ms,omitempty"`
	Seed  *int64 `json:"seed,omitempty"`
}

// ListenerSpec binds a provider/profile pair to one listening address.
type ListenerSpec struct {
	Name     string `json:"name"`
	Addr     string `json:"addr"`
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	TLS      bool   `json:"tls,omitempty"`
}

// ListenerRuntime is the fixed execution context attached to requests arriving
// on a local data-plane listener.
type ListenerRuntime struct {
	Spec    ListenerSpec
	Profile RuntimeProfile
}
