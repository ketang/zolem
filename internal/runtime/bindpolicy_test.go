package runtimecfg_test

import (
	"strings"
	"testing"

	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

func TestBindPolicyLoopbackOnly(t *testing.T) {
	var p runtimecfg.BindPolicy
	if err := p.ValidateAddr("0.0.0.0:1"); err == nil {
		t.Fatal("loopback-only policy accepted 0.0.0.0:1")
	}
	for _, ok := range []string{"127.0.0.1:1", "localhost:2", "[::1]:3"} {
		if err := p.ValidateAddr(ok); err != nil {
			t.Errorf("loopback-only rejected %q: %v", ok, err)
		}
	}
}

func TestBindPolicyWildcardWithRange(t *testing.T) {
	p := runtimecfg.NewWildcardBindPolicy(18100, 18101)
	for _, ok := range []string{"0.0.0.0:18100", "[::]:18101", "127.0.0.1:18100"} {
		if err := p.ValidateAddr(ok); err != nil {
			t.Errorf("wildcard policy rejected %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"0.0.0.0:18200", "10.0.0.5:18100", "192.168.1.5:18100"} {
		if err := p.ValidateAddr(bad); err == nil {
			t.Errorf("wildcard policy accepted %q", bad)
		}
	}
	err := p.ValidateAddr("0.0.0.0:18200")
	if want := "listener port 18200 outside -listener-port-range 18100-18101"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want message containing %q", err, want)
	}
}

func TestBindPolicyWildcardWithoutRange(t *testing.T) {
	p := runtimecfg.NewWildcardBindPolicy(0, 0)
	if err := p.ValidateAddr("0.0.0.0:8080"); err != nil {
		t.Errorf("wildcard without range rejected 0.0.0.0:8080: %v", err)
	}
	if err := p.ValidateAddr("10.0.0.5:8080"); err == nil {
		t.Error("specific non-loopback IP accepted")
	}
}

func TestParsePortRange(t *testing.T) {
	lo, hi, err := runtimecfg.ParsePortRange("18100-18109")
	if err != nil || lo != 18100 || hi != 18109 {
		t.Fatalf("got %d %d %v", lo, hi, err)
	}
	for _, bad := range []string{"", "18100", "a-b", "18109-18100", "0-10", "1-70000", "1-2-3"} {
		if _, _, err := runtimecfg.ParsePortRange(bad); err == nil {
			t.Errorf("ParsePortRange(%q) succeeded", bad)
		}
	}
}

func TestStoreWithPolicyEnforcesRange(t *testing.T) {
	s := runtimecfg.NewStoreWithBindPolicy(runtimecfg.NewWildcardBindPolicy(18100, 18101))
	spec := runtimecfg.ListenerSpec{Name: "a", Addr: "0.0.0.0:18100", Provider: "openai", Profile: "p"}
	if _, err := s.UpsertListener(spec); err != nil {
		t.Fatal(err)
	}
	spec.Addr = "0.0.0.0:18200"
	if _, err := s.UpsertListener(spec); err == nil {
		t.Fatal("out-of-range listener accepted")
	}
	if _, err := runtimecfg.NewStore().UpsertListener(runtimecfg.ListenerSpec{Name: "a", Addr: "0.0.0.0:1", Provider: "openai", Profile: "p"}); err == nil {
		t.Fatal("default store accepted wildcard")
	}
}
