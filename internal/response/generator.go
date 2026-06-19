package response

// Generator produces deterministic token slices for synthetic responses.
type Generator interface {
	Generate(n int) []string
}

func CountNonEmpty(chunks []string) int {
	n := 0
	for _, chunk := range chunks {
		if chunk != "" {
			n++
		}
	}
	return n
}
