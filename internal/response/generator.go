package response

// Generator produces deterministic token slices for synthetic responses.
type Generator interface {
	Generate(n int) []string
}
