package typesafe

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"

	ollamaclient "github.com/ketang/zolem/internal/ollama"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

// upstreamError marks a failure of the Ollama upstream the ollama-logprob
// backend depends on. The handler reports it as 502: zolem is acting as a
// gateway to that upstream, unlike the other backends.
type upstreamError struct{ err error }

func (e *upstreamError) Error() string { return e.err.Error() }
func (e *upstreamError) Unwrap() error { return e.err }

const defaultUpstream = "http://localhost:11434"

// answersFromOllamaLogprob answers every question by asking a local Ollama
// model for one greedy token and reading that position's log probabilities as
// the answer distribution. Each question is one upstream call; the model sees
// the instructions, the state, and the options rendered as short labels, and
// the probability mass on each label's token becomes that option's probability.
func answersFromOllamaLogprob(ctx context.Context, req Request) (map[string]Answer, error) {
	rt, _ := runtimecfg.ListenerRuntimeFromContext(ctx)
	upstream := rt.Profile.OllamaUpstream
	if upstream == "" {
		upstream = defaultUpstream
	}
	temperature := 1.0
	if t := rt.Profile.CalibrationTemperature; t != nil {
		temperature = *t
	}
	model := rt.Profile.BackendModel

	pick := func(prompt string, labels []string) ([]float64, error) {
		cands, err := ollamaclient.FirstTokenLogprobs(ctx, upstream, model, prompt)
		if err != nil {
			return nil, &upstreamError{err: err}
		}
		return labelDistribution(cands, labels, temperature), nil
	}

	return answersWith(req, func(q Question, _ string) (Answer, error) {
		switch q.Type {
		case QuestionChoice:
			return logprobChoice(req, q, pick)
		case QuestionScore:
			return logprobScore(req, q, pick)
		case QuestionNoul:
			return logprobNoul(req, q, pick)
		default:
			return Answer{}, fmt.Errorf("unknown question type %q", q.Type)
		}
	})
}

type labelPicker func(prompt string, labels []string) ([]float64, error)

func logprobChoice(req Request, q Question, pick labelPicker) (Answer, error) {
	options, err := choiceOptions(q.Criteria)
	if err != nil {
		return Answer{}, err
	}
	var descriptions map[string]string
	_ = json.Unmarshal(q.Criteria, &descriptions)

	labels := optionLabels(len(options))
	lines := make([]string, len(options))
	for i, o := range options {
		lines[i] = optionLine(labels[i], o, descriptions[o])
	}
	probs, err := pick(buildPrompt(req.State, q.Instructions, lines,
		"Reply with only the label of the best option."), labels)
	if err != nil {
		return Answer{}, err
	}

	answer := Answer{Type: QuestionChoice, Probabilities: make(map[string]float64, len(options))}
	best := 0
	for i, o := range options {
		answer.Probabilities[o] = probs[i]
		if probs[i] > probs[best] {
			best = i
		}
	}
	answer.Choice = options[best]
	answer.Confidence = floatPtr(probs[best])
	return answer, nil
}

func logprobScore(req Request, q Question, pick labelPicker) (Answer, error) {
	levels, err := scoreLevels(q.Criteria)
	if err != nil {
		return Answer{}, err
	}
	labels := optionLabels(len(levels))
	lines := make([]string, len(levels))
	legend := make(map[string]string, len(levels))
	for i, l := range levels {
		text := levelLabel(l)
		lines[i] = optionLine(labels[i], text, "")
		legend[fmt.Sprintf("%d", i)] = text
	}
	probs, err := pick(buildPrompt(req.State, q.Instructions, lines,
		"The levels are ordered from lowest to highest. Reply with only the label of the level that fits best."), labels)
	if err != nil {
		return Answer{}, err
	}

	answer := Answer{Type: QuestionScore, Legend: legend, Probabilities: make(map[string]float64, len(levels))}
	var weighted float64
	best := 0
	for i := range levels {
		answer.Probabilities[fmt.Sprintf("%d", i)] = probs[i]
		weighted += float64(i) * probs[i]
		if probs[i] > probs[best] {
			best = i
		}
	}
	answer.Score = floatPtr(weighted)
	answer.Confidence = floatPtr(probs[best])
	return answer, nil
}

func logprobNoul(req Request, q Question, pick labelPicker) (Answer, error) {
	var criteria struct {
		True  string `json:"true"`
		False string `json:"false"`
	}
	if len(q.Criteria) > 0 {
		_ = json.Unmarshal(q.Criteria, &criteria)
	}
	labels := optionLabels(2)
	lines := []string{
		optionLine(labels[0], "yes", criteria.True),
		optionLine(labels[1], "no", criteria.False),
	}
	probs, err := pick(buildPrompt(req.State, q.Instructions, lines,
		"Reply with only the label of the correct answer."), labels)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Type: QuestionNoul, Noul: floatPtr(probs[0])}, nil
}

// optionLabels returns one short label per option. Digits 1-9 are single
// tokens on every tokenizer, but "10" commonly splits into "1" and "0" and
// would be indistinguishable from option 1 at the first token, so past nine
// options letters are used instead (A-Z, then AA, AB, ...). Labels beyond 26
// may also split; the docs list that limitation.
func optionLabels(n int) []string {
	labels := make([]string, n)
	for i := range labels {
		switch {
		case n <= 9:
			labels[i] = fmt.Sprintf("%d", i+1)
		case i < 26:
			labels[i] = string(rune('A' + i))
		default:
			j := i - 26
			labels[i] = string(rune('A'+j/26%26)) + string(rune('A'+j%26))
		}
	}
	return labels
}

func optionLine(label, name, description string) string {
	if description == "" {
		return fmt.Sprintf("%s. %s", label, name)
	}
	return fmt.Sprintf("%s. %s: %s", label, name, description)
}

// buildPrompt renders the question for a raw /api/generate call.
func buildPrompt(state, instructions json.RawMessage, optionLines []string, closing string) string {
	var b strings.Builder
	b.WriteString(renderJSONText(instructions))
	b.WriteString("\n\nState:\n")
	b.WriteString(renderJSONText(state))
	b.WriteString("\n\nOptions:\n")
	b.WriteString(strings.Join(optionLines, "\n"))
	b.WriteString("\n\n")
	b.WriteString(closing)
	b.WriteString("\nLabel:")
	return b.String()
}

// renderJSONText unquotes a JSON string and pretty-prints anything else.
func renderJSONText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// labelDistribution turns first-token candidates into a probability per label:
// tokens are trimmed and matched to labels, each label's weight is the sum of
// exp(logprob/temperature) over its tokens, and the weights are renormalized
// over the labels. Labels that did not appear get 0. When no candidate is a
// label, the distribution is uniform.
func labelDistribution(cands []ollamaclient.TokenLogprob, labels []string, temperature float64) []float64 {
	index := make(map[string]int, len(labels))
	for i, l := range labels {
		index[l] = i
	}

	type scaled struct {
		label int
		z     float64
	}
	var matched []scaled
	maxZ := math.Inf(-1)
	for _, c := range cands {
		token := strings.ToUpper(strings.TrimRight(strings.TrimSpace(c.Token), ".):"))
		i, ok := index[token]
		if !ok {
			continue
		}
		z := c.Logprob / temperature
		matched = append(matched, scaled{i, z})
		maxZ = math.Max(maxZ, z)
	}

	probs := make([]float64, len(labels))
	if len(matched) == 0 {
		log.Printf("typesafe ollama-logprob: no candidate token matched an option label; answering uniformly")
		for i := range probs {
			probs[i] = 1 / float64(len(probs))
		}
		return probs
	}

	var total float64
	for _, m := range matched {
		w := math.Exp(m.z - maxZ)
		probs[m.label] += w
		total += w
	}
	for i := range probs {
		probs[i] /= total
	}
	return probs
}
