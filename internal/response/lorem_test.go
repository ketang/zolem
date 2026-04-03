// internal/response/lorem_test.go
package response_test

import (
	"strings"
	"testing"

	"zolem.dev/zolem/internal/response"
)

func TestLoremGenerate_ReturnsTokens(t *testing.T) {
	g := response.NewLoremGenerator()
	tokens := g.Generate(10)
	if len(tokens) == 0 {
		t.Fatal("expected non-empty tokens")
	}
}

func TestLoremGenerate_ApproximateWordCount(t *testing.T) {
	g := response.NewLoremGenerator()
	tokens := g.Generate(20)
	if len(tokens) < 15 || len(tokens) > 30 {
		t.Errorf("expected ~20 tokens, got %d", len(tokens))
	}
}

func TestLoremGenerate_NonEmpty(t *testing.T) {
	g := response.NewLoremGenerator()
	tokens := g.Generate(5)
	for i, tok := range tokens {
		if strings.TrimSpace(tok) == "" {
			t.Errorf("token[%d] is empty", i)
		}
	}
}
