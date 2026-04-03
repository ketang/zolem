// internal/response/lorem.go
package response

var loremWords = []string{
	"lorem", "ipsum", "dolor", "sit", "amet", "consectetur",
	"adipiscing", "elit", "sed", "do", "eiusmod", "tempor",
	"incididunt", "ut", "labore", "et", "dolore", "magna",
	"aliqua", "enim", "ad", "minim", "veniam", "quis",
	"nostrud", "exercitation", "ullamco", "laboris", "nisi",
	"aliquip", "ex", "ea", "commodo", "consequat", "duis",
	"aute", "irure", "in", "reprehenderit", "voluptate",
	"velit", "esse", "cillum", "fugiat", "nulla", "pariatur",
}

// LoremGenerator produces deterministic lorem ipsum token slices.
type LoremGenerator struct{}

func NewLoremGenerator() *LoremGenerator { return &LoremGenerator{} }

// Generate returns approximately n words as a slice of string tokens.
func (g *LoremGenerator) Generate(n int) []string {
	tokens := make([]string, n)
	for i := range tokens {
		word := loremWords[i%len(loremWords)]
		if i < n-1 {
			tokens[i] = word + " "
		} else {
			tokens[i] = word + "."
		}
	}
	return tokens
}
