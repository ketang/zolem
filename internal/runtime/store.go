package runtimecfg

import (
	"errors"
	"net"
	"path"
	"slices"
	"strings"
	"sync"
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

func ValidateProfile(profile RuntimeProfile) error {
	if profile.Name == "" {
		return errors.New("profile name is required")
	}
	if err := validateFixtureNamespace(profile.FixtureNamespace); err != nil {
		return err
	}
	switch profile.Backend {
	case "", "lorem", "faker", "fixture":
		return nil
	default:
		return errors.New("profile backend must be lorem, faker, or fixture")
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
