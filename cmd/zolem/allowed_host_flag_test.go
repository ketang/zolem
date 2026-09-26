package main

import (
	"reflect"
	"testing"
)

func TestStringListSet_AllowedHost(t *testing.T) {
	var l stringList
	for _, v := range []string{" zolem.test ", "Zolem.Test:8090", "other.test"} {
		if err := l.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	if want := []string{"zolem.test", "other.test"}; !reflect.DeepEqual([]string(l), want) {
		t.Fatalf("got %v, want %v (trimmed, port stripped, deduped)", l, want)
	}

	for _, bad := range []string{"", "   ", "http://zolem.test", "zolem.test/path", "a/b"} {
		var l stringList
		if err := l.Set(bad); err == nil {
			t.Errorf("Set(%q) succeeded, want error", bad)
		}
	}
}
