package typesafe

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math/rand"
)

// answerLorem produces a deterministic, valid answer with no randomness:
// choice and score both put 0.9 on the first option/level and spread the
// remaining 0.1 evenly, and noul is always 0.5. Useful for smoke tests where
// only response shape matters, matching the issue's "lorem: deterministic"
// spec.
func answerLorem(question Question) (Answer, error) {
	switch question.Type {
	case QuestionNoul:
		return Answer{Type: QuestionNoul, Noul: floatPtr(0.5)}, nil
	case QuestionChoice:
		options, err := choiceOptions(question.Criteria)
		if err != nil {
			return Answer{}, err
		}
		probs := skewedDistribution(options)
		return Answer{
			Type:          QuestionChoice,
			Choice:        options[0],
			Probabilities: probs,
			Confidence:    floatPtr(probs[options[0]]),
		}, nil
	case QuestionScore:
		levels, err := scoreLevels(question.Criteria)
		if err != nil {
			return Answer{}, err
		}
		indices := levelIndices(levels)
		probs := skewedDistribution(indices)
		return Answer{
			Type:          QuestionScore,
			Score:         floatPtr(weightedScore(indices, probs)),
			Legend:        legendFor(levels),
			Probabilities: probs,
			Confidence:    floatPtr(probs[indices[0]]),
		}, nil
	default:
		return Answer{}, fmt.Errorf("unknown question type %q", question.Type)
	}
}

// answerFaker produces a seeded pseudo-random answer: the same (request,
// question) pair always yields the same answer, and a different request (a
// different state, in particular) yields a different one. seedBase is a
// stable hash of the whole request body so faker output changes only when
// the request itself changes, not between requests that happen to share a
// question id.
func answerFaker(question Question, questionKey string, seedBase uint64) (Answer, error) {
	seed := int64(hashSeed(seedBase, questionKey)) //nolint:gosec // deterministic PRNG seed, not cryptographic
	rng := rand.New(rand.NewSource(seed))          //nolint:gosec // deterministic synthetic backend, not security-sensitive

	switch question.Type {
	case QuestionNoul:
		return Answer{Type: QuestionNoul, Noul: floatPtr(rng.Float64())}, nil
	case QuestionChoice:
		options, err := choiceOptions(question.Criteria)
		if err != nil {
			return Answer{}, err
		}
		probs := randomDistribution(rng, options)
		choice, _ := argmaxKey(probs)
		return Answer{
			Type:          QuestionChoice,
			Choice:        choice,
			Probabilities: probs,
			Confidence:    floatPtr(probs[choice]),
		}, nil
	case QuestionScore:
		levels, err := scoreLevels(question.Criteria)
		if err != nil {
			return Answer{}, err
		}
		indices := levelIndices(levels)
		probs := randomDistribution(rng, indices)
		best, _ := argmaxKey(probs)
		return Answer{
			Type:          QuestionScore,
			Score:         floatPtr(weightedScore(indices, probs)),
			Legend:        legendFor(levels),
			Probabilities: probs,
			Confidence:    floatPtr(probs[best]),
		}, nil
	default:
		return Answer{}, fmt.Errorf("unknown question type %q", question.Type)
	}
}

// requestSeed hashes the raw request body into a stable uint64 for the faker
// backend, so identical requests (byte for byte) always produce identical
// answers and a changed body (e.g. a different state) produces different
// ones.
func requestSeed(body []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(body)
	return h.Sum64()
}

func hashSeed(base uint64, key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%d|%s", base, key)))
	return h.Sum64()
}

func levelIndices(levels []json.RawMessage) []string {
	indices := make([]string, len(levels))
	for i := range levels {
		indices[i] = fmt.Sprintf("%d", i)
	}
	return indices
}

func legendFor(levels []json.RawMessage) map[string]string {
	legend := make(map[string]string, len(levels))
	for i, level := range levels {
		legend[fmt.Sprintf("%d", i)] = levelLabel(level)
	}
	return legend
}

// skewedDistribution puts 0.9 on keys[0] and spreads the remaining 0.1
// evenly over the rest, per the issue's lorem-backend spec.
func skewedDistribution(keys []string) map[string]float64 {
	probs := make(map[string]float64, len(keys))
	if len(keys) == 1 {
		probs[keys[0]] = 1
		return probs
	}
	rest := 0.1 / float64(len(keys)-1)
	for i, k := range keys {
		if i == 0 {
			probs[k] = 0.9
		} else {
			probs[k] = rest
		}
	}
	return probs
}

// randomDistribution draws a positive weight per key from rng and normalizes
// the weights to sum to 1.
func randomDistribution(rng *rand.Rand, keys []string) map[string]float64 {
	weights := make([]float64, len(keys))
	var sum float64
	for i := range keys {
		// +0.01 keeps every weight strictly positive so no key can be
		// starved to exactly zero.
		weights[i] = rng.Float64() + 0.01
		sum += weights[i]
	}
	probs := make(map[string]float64, len(keys))
	for i, k := range keys {
		probs[k] = weights[i] / sum
	}
	return probs
}

// weightedScore computes the probability-weighted 0-based position over a
// score distribution keyed by level index string, matching the equality the
// answer validator checks. indices must be iterated in a fixed order (its
// slice order, "0", "1", "2", ...) rather than by ranging over the probs map:
// Go's map iteration order is randomized per range statement, and floating
// point addition is not associative, so summing in map order would make the
// same answer serialize to a different score across two calls with an
// identical seed — breaking the faker backend's "identical request, identical
// answer" contract.
func weightedScore(indices []string, probs map[string]float64) float64 {
	var score float64
	for i, idxStr := range indices {
		score += float64(i) * probs[idxStr]
	}
	return score
}
