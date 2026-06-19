package response_test

import (
	"testing"

	"github.com/ketang/zolem/internal/response"
)

func TestCountNonEmpty(t *testing.T) {
	if got := response.CountNonEmpty([]string{"a", "", "b", " "}); got != 3 {
		t.Fatalf("CountNonEmpty = %d, want 3", got)
	}
}
