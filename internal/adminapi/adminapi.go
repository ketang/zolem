// Package adminapi holds the wire types shared between the zolem admin server
// and zolemc. Keeping them here prevents the server and CLI from silently
// drifting apart as new profile or listener fields are added.
package adminapi

import runtimecfg "github.com/ketang/zolem/internal/runtime"

// ProfilePayload is the request body for PUT /_zolem/profiles/<name>.
type ProfilePayload struct {
	Backend                     string                 `json:"backend,omitempty"`
	BackendModel                string                 `json:"backend_model,omitempty"`
	ErrorType                   string                 `json:"error_type,omitempty"`
	ResponseModelPolicy         string                 `json:"response_model_policy,omitempty"`
	ResponseModel               string                 `json:"response_model,omitempty"`
	FixtureNamespace            string                 `json:"fixture_namespace,omitempty"`
	Seed                        *int64                 `json:"seed,omitempty"`
	OllamaUpstream              string                 `json:"ollama_upstream,omitempty"`
	AllowExternalOllamaUpstream bool                   `json:"allow_external_ollama_upstream,omitempty"`
	WASMModuleBase64            string                 `json:"wasm_module_base64,omitempty"`
	WASMGenerateTimeoutMS       *int                   `json:"wasm_generate_timeout_ms,omitempty"`
	StreamDelay                 runtimecfg.StreamDelay `json:"stream_delay,omitempty"`
}

// ListenerPayload is the request body for PUT /_zolem/listeners/<name>.
type ListenerPayload struct {
	Addr                       string `json:"addr"`
	Provider                   string `json:"provider"`
	Profile                    string `json:"profile"`
	TLS                        bool   `json:"tls,omitempty"`
	RecordRequestBodyCapBytes  *int   `json:"record_request_body_cap_bytes,omitempty"`
	RecordResponseBodyCapBytes *int   `json:"record_response_body_cap_bytes,omitempty"`
	RecordStreamEventCap       *int   `json:"record_stream_event_cap,omitempty"`
}

// ListenerView is returned by listener create, get, and list operations.
type ListenerView struct {
	Name                       string `json:"name"`
	Addr                       string `json:"addr"`
	Provider                   string `json:"provider"`
	Profile                    string `json:"profile"`
	Backend                    string `json:"backend"`
	TLS                        bool   `json:"tls,omitempty"`
	BaseURL                    string `json:"base_url"`
	RecordRequestBodyCapBytes  int    `json:"record_request_body_cap_bytes"`
	RecordResponseBodyCapBytes int    `json:"record_response_body_cap_bytes"`
	RecordStreamEventCap       int    `json:"record_stream_event_cap"`
}

// ListenerStateView is returned by GET /_zolem/state on the data-plane listener.
type ListenerStateView struct {
	Provider string `json:"provider"`
	Profile  string `json:"profile"`
	Backend  string `json:"backend"`
	Listener string `json:"listener"`
	TLS      bool   `json:"tls"`
}
