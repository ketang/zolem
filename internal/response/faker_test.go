package response_test

import (
	"strings"
	"testing"

	"zolem.dev/zolem/internal/response"
)

func TestFakerGenerate_ReturnsRequestedTokenCount(t *testing.T) {
	g := response.NewFakerGenerator()
	tokens := g.Generate(12)
	if len(tokens) != 12 {
		t.Fatalf("token count: got %d, want 12", len(tokens))
	}
}

func TestFakerGenerate_IsDeterministic(t *testing.T) {
	g := response.NewFakerGenerator()
	first := strings.Join(g.Generate(16), "")
	second := strings.Join(g.Generate(16), "")
	if first != second {
		t.Fatalf("faker output should be deterministic:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestFakerGenerate_UsesNonLoremVocabulary(t *testing.T) {
	g := response.NewFakerGenerator()
	text := strings.Join(g.Generate(20), "")
	if strings.Contains(text, "lorem ipsum") {
		t.Fatalf("faker output should not look like lorem: %q", text)
	}
	if !strings.Contains(text, "rollout") {
		t.Fatalf("faker output should contain fake business-style text: %q", text)
	}
}
