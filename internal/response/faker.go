package response

import (
	"fmt"
	"strings"
)

var fakerFirstNames = []string{
	"Ava", "Milo", "Nora", "Theo", "Ivy", "Ezra", "Lena", "Kai",
}

var fakerLastNames = []string{
	"Brooks", "Patel", "Kim", "Nguyen", "Alvarez", "Singh", "Chen", "Morris",
}

var fakerCompanies = []string{
	"Northwind", "Blue Mesa", "Summit Labs", "Rivet Cloud",
	"Harbor Systems", "Cinder Works", "Juniper AI", "Atlas Forge",
}

var fakerProducts = []string{
	"assistant", "workflow", "dashboard", "integration",
	"prototype", "dataset", "migration", "release",
}

var fakerVerbs = []string{
	"shipped", "reviewed", "validated", "scheduled",
	"improved", "drafted", "tested", "documented",
}

var fakerPlaces = []string{
	"Austin", "Chicago", "Denver", "Portland", "Seattle", "Boston", "Miami", "Phoenix",
}

// FakerGenerator produces deterministic fake business-style text.
type FakerGenerator struct{}

func NewFakerGenerator() *FakerGenerator { return &FakerGenerator{} }

func (g *FakerGenerator) Generate(n int) []string {
	if n <= 0 {
		return nil
	}

	sentences := make([]string, 0, n)
	for i := 0; len(sentences) < n; i++ {
		first := fakerFirstNames[i%len(fakerFirstNames)]
		last := fakerLastNames[(i*3+1)%len(fakerLastNames)]
		company := fakerCompanies[(i*5+2)%len(fakerCompanies)]
		product := fakerProducts[(i*7+3)%len(fakerProducts)]
		verb := fakerVerbs[(i*11+4)%len(fakerVerbs)]
		place := fakerPlaces[(i*13+5)%len(fakerPlaces)]

		sentence := fmt.Sprintf("%s %s from %s %s the %s rollout for %s.", first, last, company, verb, product, place)
		sentences = append(sentences, tokenizeSentence(sentence)...)
	}

	return sentences[:n]
}

func tokenizeSentence(text string) []string {
	return tokenizeWords(text)
}

func tokenizeWords(text string) []string {
	words := strings.Fields(text)
	tokens := make([]string, len(words))
	for i, w := range words {
		if i < len(words)-1 {
			tokens[i] = w + " "
		} else {
			tokens[i] = w
		}
	}
	return tokens
}
