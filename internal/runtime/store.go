package runtimecfg

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand"
	"net"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	ErrProfileNotFound  = errors.New("profile not found")
	ErrListenerNotFound = errors.New("listener not found")
	ErrProfileInUse     = errors.New("profile in use")
)

// Store keeps local runtime profiles and listener specs in memory.
type Store struct {
	mu        sync.RWMutex
	profiles  map[string]RuntimeProfile
	listeners map[string]ListenerSpec
}

func NewStore() *Store {
	return &Store{
		profiles:  make(map[string]RuntimeProfile),
		listeners: make(map[string]ListenerSpec),
	}
}

func (s *Store) UpsertProfile(profile RuntimeProfile) (RuntimeProfile, error) {
	if err := ValidateProfile(profile); err != nil {
		return RuntimeProfile{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.profiles[profile.Name] = profile
	return profile, nil
}

func (s *Store) GetProfile(name string) (RuntimeProfile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	profile, ok := s.profiles[name]
	return profile, ok
}

func (s *Store) ListProfiles() []RuntimeProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	profiles := make([]RuntimeProfile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		profiles = append(profiles, profile)
	}
	slices.SortFunc(profiles, func(a, b RuntimeProfile) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	})
	return profiles
}

func (s *Store) DeleteProfile(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, listener := range s.listeners {
		if listener.Profile == name {
			return ErrProfileInUse
		}
	}
	if _, ok := s.profiles[name]; !ok {
		return ErrProfileNotFound
	}
	delete(s.profiles, name)
	return nil
}

func (s *Store) UpsertListener(spec ListenerSpec) (ListenerSpec, error) {
	if err := ValidateListenerSpec(spec); err != nil {
		return ListenerSpec{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.listeners[spec.Name] = spec
	return spec, nil
}

func (s *Store) DeleteListener(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.listeners[name]; !ok {
		return ErrListenerNotFound
	}
	delete(s.listeners, name)
	return nil
}

func (s *Store) ListListeners() []ListenerSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()

	listeners := make([]ListenerSpec, 0, len(s.listeners))
	for _, listener := range s.listeners {
		listeners = append(listeners, listener)
	}
	slices.SortFunc(listeners, func(a, b ListenerSpec) int {
		switch {
		case a.Name < b.Name:
			return -1
		case a.Name > b.Name:
			return 1
		default:
			return 0
		}
	})
	return listeners
}

func validateLoopbackAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listener addr must bind to localhost or a loopback IP")
	}
	return nil
}

func ValidateLoopbackAddr(addr string) error {
	return validateLoopbackAddr(addr)
}

// HostHeaderAllowed reports whether an HTTP Host header may target a
// loopback-only listener. It permits loopback IP literals, "localhost", and any
// host in the explicit allowlist; everything else is rejected. This guards the
// auth-less local listeners against DNS-rebinding attacks, where a browser
// resolves an attacker-controlled name to 127.0.0.1 and drives the API with the
// attacker's hostname in the Host header.
func HostHeaderAllowed(hostHeader string, allow []string) bool {
	host := hostHeader
	if h, _, err := net.SplitHostPort(hostHeader); err == nil {
		host = h
	} else {
		// No "host:port" split: a bare IPv6 literal still carries brackets.
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if host == "" {
		return false
	}
	for _, a := range allow {
		if strings.EqualFold(host, a) {
			return true
		}
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

func ValidateProfile(profile RuntimeProfile) error {
	if profile.Name == "" {
		return errors.New("profile name is required")
	}
	if err := validateFixtureNamespace(profile.FixtureNamespace); err != nil {
		return err
	}
	if err := validateErrorProfile(profile); err != nil {
		return err
	}
	if err := validateResponseModelPolicy(profile); err != nil {
		return err
	}
	if err := validateOllamaUpstream(profile); err != nil {
		return err
	}
	if err := validateWASMProfile(profile); err != nil {
		return err
	}
	if err := validateStreamDelay(profile.StreamDelay); err != nil {
		return err
	}
	switch profile.Backend {
	case "", "lorem", "faker", "fixture", "ollama", "error", BackendWASM:
		return nil
	default:
		return errors.New("profile backend must be lorem, faker, fixture, ollama, error, or wasm")
	}
}

func ValidateListenerSpec(spec ListenerSpec) error {
	if spec.Name == "" {
		return errors.New("listener name is required")
	}
	if spec.Profile == "" {
		return errors.New("listener profile is required")
	}
	if spec.Provider != "anthropic" && spec.Provider != "openai" && spec.Provider != "gemini" {
		return errors.New("listener provider must be anthropic, openai, or gemini")
	}
	if err := validateLoopbackAddr(spec.Addr); err != nil {
		return err
	}
	return nil
}

func validateFixtureNamespace(namespace string) error {
	if namespace == "" {
		return nil
	}
	if strings.Contains(namespace, "\\") {
		return errors.New("fixture namespace must use forward slashes")
	}
	clean := path.Clean(namespace)
	switch {
	case strings.HasPrefix(namespace, "/"):
		return errors.New("fixture namespace must be relative")
	case clean == "." || clean == "..":
		return errors.New("fixture namespace must be a relative subdirectory")
	case strings.HasPrefix(clean, "../"):
		return errors.New("fixture namespace must not escape the fixtures root")
	case clean != namespace:
		return errors.New("fixture namespace must be normalized")
	default:
		return nil
	}
}

func validateResponseModelPolicy(profile RuntimeProfile) error {
	switch profile.ResponseModelPolicy {
	case "", ResponseModelEchoRequest, ResponseModelForceLiteral, ResponseModelForceBackend:
		if profile.ResponseModelPolicy == ResponseModelForceLiteral && profile.ResponseModel == "" {
			return errors.New("response model is required when response_model_policy is force_literal")
		}
		return nil
	default:
		return errors.New("response_model_policy must be echo_request, force_literal, or force_backend")
	}
}

func validateOllamaUpstream(profile RuntimeProfile) error {
	if profile.OllamaUpstream == "" {
		return nil
	}
	u, err := url.Parse(profile.OllamaUpstream)
	if err != nil {
		return errors.New("ollama_upstream must be a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("ollama_upstream must use http or https scheme")
	}
	if u.Host == "" {
		return errors.New("ollama_upstream must include a host")
	}
	if profile.AllowExternalOllamaUpstream {
		return nil
	}
	if !ollamaUpstreamHostIsPrivate(u.Hostname()) {
		return errors.New("ollama_upstream host must be loopback or a private (RFC1918/RFC4193) address; set allow_external_ollama_upstream to forward to an external host")
	}
	return nil
}

// ollamaUpstreamHostIsPrivate reports whether an ollama_upstream host stays
// inside the no-egress posture: loopback or a private (RFC1918/RFC4193) address.
// A non-literal hostname can resolve anywhere — and is itself a rebinding vector
// — so only the "localhost" name is accepted without the opt-out flag.
func ollamaUpstreamHostIsPrivate(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate()
}

func validateErrorProfile(profile RuntimeProfile) error {
	switch profile.Backend {
	case BackendError:
		if err := validateErrorType(profile.ErrorType); err != nil {
			return err
		}
	default:
		if profile.ErrorType != "" {
			return errors.New("error_type is only allowed when backend is error")
		}
	}
	return nil
}

func validateErrorType(value string) error {
	switch value {
	case ErrorTypeAuthentication, ErrorTypePermission, ErrorTypeInvalidRequest, ErrorTypeRateLimit, ErrorTypeServerError:
		return nil
	case "":
		return errors.New("error_type is required when backend is error")
	default:
		return errors.New("error_type must be authentication, permission, invalid_request, rate_limit, or server_error")
	}
}

func validateWASMProfile(profile RuntimeProfile) error {
	if profile.Backend == BackendWASM {
		if profile.WASMModuleBase64 == "" {
			return errors.New("wasm_module_base64 is required when backend is wasm")
		}
		if profile.WASMGenerateTimeoutMS < 0 {
			return errors.New("wasm_generate_timeout_ms must be non-negative")
		}
		if profile.WASMGenerateTimeoutMS != 0 && (profile.WASMGenerateTimeoutMS < 1 || profile.WASMGenerateTimeoutMS > 5000) {
			return errors.New("wasm_generate_timeout_ms must be between 1 and 5000")
		}
		return nil
	}
	if profile.WASMModuleBase64 != "" {
		return errors.New("wasm_module_base64 is only allowed when backend is wasm")
	}
	if profile.WASMGenerateTimeoutMS != 0 {
		return errors.New("wasm_generate_timeout_ms is only allowed when backend is wasm")
	}
	return nil
}

func validateStreamDelay(delay StreamDelay) error {
	switch delay.Mode {
	case "":
		if delay.MS != 0 || delay.MinMS != 0 || delay.MaxMS != 0 || delay.Seed != nil {
			return errors.New("stream_delay mode is required when stream_delay fields are set")
		}
		return nil
	case "fixed":
		if delay.MS < 0 {
			return errors.New("stream_delay.ms must be non-negative")
		}
		if delay.MinMS != 0 || delay.MaxMS != 0 || delay.Seed != nil {
			return errors.New("fixed stream_delay only allows ms")
		}
		return nil
	case "random":
		if delay.MinMS < 0 || delay.MaxMS < 0 {
			return errors.New("stream_delay min_ms and max_ms must be non-negative")
		}
		if delay.MaxMS < delay.MinMS {
			return errors.New("stream_delay max_ms must be greater than or equal to min_ms")
		}
		if delay.MS != 0 {
			return errors.New("random stream_delay does not allow ms")
		}
		return nil
	default:
		return errors.New("stream_delay mode must be fixed or random")
	}
}

type StreamDelayFunc func(context.Context) error

func StreamDelayForRequest(ctx context.Context) StreamDelayFunc {
	rt, ok := ListenerRuntimeFromContext(ctx)
	if !ok {
		return nil
	}
	delay := rt.Profile.StreamDelay
	switch delay.Mode {
	case "fixed":
		d := time.Duration(delay.MS) * time.Millisecond
		return func(ctx context.Context) error {
			return sleepContext(ctx, d)
		}
	case "random":
		ordinal := ProfileRequestSequenceFromContext(ctx)
		seed := int64(0)
		if delay.Seed != nil {
			seed = *delay.Seed
		}
		rng := rand.New(rand.NewSource(streamSeed(rt.Profile.Name, seed, ordinal)))
		minMS := delay.MinMS
		maxMS := delay.MaxMS
		return func(ctx context.Context) error {
			ms := minMS
			if maxMS > minMS {
				ms += rng.Intn(maxMS - minMS + 1)
			}
			return sleepContext(ctx, time.Duration(ms)*time.Millisecond)
		}
	default:
		return nil
	}
}

func streamSeed(profileName string, seed int64, ordinal uint64) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(profileName))
	_, _ = h.Write([]byte{0})
	for i := 0; i < 8; i++ {
		_, _ = h.Write([]byte{byte(uint64(seed) >> (8 * i))})
	}
	for i := 0; i < 8; i++ {
		_, _ = h.Write([]byte{byte(ordinal >> (8 * i))})
	}
	return int64(h.Sum64())
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
