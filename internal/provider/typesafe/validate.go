package typesafe

import (
	"fmt"
	"math"
	"sort"
)

// probabilitySumEpsilon is the tolerance the issue specifies for probability
// sums and score/probability-weighted-position equality checks.
const probabilitySumEpsilon = 1e-6

// AnswerValidationError reports that a backend-produced answer violates the
// TypeSafe response contract for one question. It names the offending
// question key and the specific rule violated, per the issue's requirement
// that a contract violation is a clear 500, never a silently wrong answer.
type AnswerValidationError struct {
	Key  string
	Rule string
}

func (e *AnswerValidationError) Error() string {
	return fmt.Sprintf("question %q: %s", e.Key, e.Rule)
}

// ValidateAnswers checks every answer in resp against the questions in req.
// This runs for every backend (lorem, faker, fixture) before a response is
// written: this validator, not the backend, is what makes the mock
// "cannot hallucinate" the way the real TypeSafe API cannot.
func ValidateAnswers(req Request, answers map[string]Answer) error {
	for key := range req.Questions {
		if _, ok := answers[key]; !ok {
			return &AnswerValidationError{Key: key, Rule: "missing answer for question"}
		}
	}
	for key := range answers {
		if _, ok := req.Questions[key]; !ok {
			return &AnswerValidationError{Key: key, Rule: "answer has no corresponding question in the request"}
		}
	}

	for key, question := range req.Questions {
		answer := answers[key]
		if answer.Type != question.Type {
			return &AnswerValidationError{Key: key, Rule: fmt.Sprintf("answer type %q does not match question type %q", answer.Type, question.Type)}
		}
		var err error
		switch question.Type {
		case QuestionNoul:
			err = validateNoulAnswer(answer)
		case QuestionChoice:
			err = validateChoiceAnswer(question, answer)
		case QuestionScore:
			err = validateScoreAnswer(question, answer)
		default:
			err = fmt.Errorf("unknown question type %q", question.Type)
		}
		if err != nil {
			return &AnswerValidationError{Key: key, Rule: err.Error()}
		}
	}
	return nil
}

func validateNoulAnswer(answer Answer) error {
	if answer.Noul == nil {
		return fmt.Errorf("noul answer is missing the noul field")
	}
	if *answer.Noul < 0 || *answer.Noul > 1 {
		return fmt.Errorf("noul value %v is outside [0, 1]", *answer.Noul)
	}
	return nil
}

func validateChoiceAnswer(question Question, answer Answer) error {
	options, err := choiceOptions(question.Criteria)
	if err != nil {
		return err
	}
	optionSet := make(map[string]bool, len(options))
	for _, o := range options {
		optionSet[o] = true
	}
	if !optionSet[answer.Choice] {
		return fmt.Errorf("choice %q is not one of the request's options", answer.Choice)
	}
	if err := validateProbabilityKeys(answer.Probabilities, options); err != nil {
		return err
	}
	if err := validateProbabilitySum(answer.Probabilities); err != nil {
		return err
	}
	argmax, err := argmaxKey(answer.Probabilities)
	if err != nil {
		return err
	}
	if argmax != answer.Choice {
		return fmt.Errorf("choice %q is not the argmax of probabilities (argmax is %q)", answer.Choice, argmax)
	}
	return nil
}

func validateScoreAnswer(question Question, answer Answer) error {
	levels, err := scoreLevels(question.Criteria)
	if err != nil {
		return err
	}
	indices := make([]string, len(levels))
	for i := range levels {
		indices[i] = fmt.Sprintf("%d", i)
	}
	if err := validateProbabilityKeys(answer.Probabilities, indices); err != nil {
		return err
	}
	if err := validateProbabilitySum(answer.Probabilities); err != nil {
		return err
	}
	if answer.Score == nil {
		return fmt.Errorf("score answer is missing the score field")
	}
	var weighted float64
	for idxStr, p := range answer.Probabilities {
		var idx int
		if _, err := fmt.Sscanf(idxStr, "%d", &idx); err != nil {
			return fmt.Errorf("probabilities key %q is not a level index", idxStr)
		}
		weighted += float64(idx) * p
	}
	if math.Abs(weighted-*answer.Score) > probabilitySumEpsilon {
		return fmt.Errorf("score %v does not equal the probability-weighted position %v", *answer.Score, weighted)
	}
	return nil
}

func validateProbabilityKeys(probabilities map[string]float64, want []string) error {
	if len(probabilities) != len(want) {
		return fmt.Errorf("probabilities has %d keys, want %d", len(probabilities), len(want))
	}
	for _, k := range want {
		if _, ok := probabilities[k]; !ok {
			return fmt.Errorf("probabilities is missing key %q", k)
		}
	}
	return nil
}

func validateProbabilitySum(probabilities map[string]float64) error {
	var sum float64
	for _, p := range probabilities {
		sum += p
	}
	if math.Abs(sum-1) > probabilitySumEpsilon {
		return fmt.Errorf("probabilities sum to %v, want 1", sum)
	}
	return nil
}

// argmaxKey returns the probabilities key with the largest value, breaking
// ties by lexical order so the result is deterministic.
func argmaxKey(probabilities map[string]float64) (string, error) {
	if len(probabilities) == 0 {
		return "", fmt.Errorf("probabilities is empty")
	}
	keys := make([]string, 0, len(probabilities))
	for k := range probabilities {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	best := keys[0]
	for _, k := range keys[1:] {
		if probabilities[k] > probabilities[best] {
			best = k
		}
	}
	return best, nil
}
