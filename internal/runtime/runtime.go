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
	Seed                *int64 `json:"seed,omitempty"`
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
