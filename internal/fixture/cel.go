package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
)

type CompiledCELMatcher struct {
	program cel.Program
	score   float32
}

func CompileCELMatcher(expr string, score float64) (*CompiledCELMatcher, error) {
	if score < 0 || math.IsNaN(score) || math.IsInf(score, 0) || score > math.MaxFloat32 {
		return nil, fmt.Errorf("match.score must be a finite non-negative float32 number")
	}

	env, err := cel.NewEnv(
		cel.Variable("provider", cel.StringType),
		cel.Variable("version", cel.StringType),
		cel.Variable("labels", cel.MapType(cel.StringType, cel.StringType)),
		cel.Variable("body", cel.DynType),
	)
	if err != nil {
		return nil, err
	}

	ast, issues := env.Compile(expr)
	if err := issues.Err(); err != nil {
		return nil, fmt.Errorf("compile CEL matcher: %w", err)
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return nil, fmt.Errorf("CEL matcher must return bool, got %s", ast.OutputType())
	}

	program, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("build CEL matcher program: %w", err)
	}
	return &CompiledCELMatcher{program: program, score: float32(score)}, nil
}

func (m *CompiledCELMatcher) Score(ctx context.Context, req MatchRequest) (float32, error) {
	var body any
	if len(req.Body) == 0 {
		body = map[string]any{}
	} else if err := json.Unmarshal(req.Body, &body); err != nil {
		return -1, fmt.Errorf("decode CEL body: %w", err)
	}

	result, _, err := m.program.ContextEval(ctx, map[string]any{
		"provider": req.Provider,
		"version":  req.Version,
		"labels":   req.Labels,
		"body":     body,
	})
	if err != nil {
		return -1, err
	}

	matched, ok := result.Value().(bool)
	if !ok {
		if celBool, ok := result.(types.Bool); ok {
			matched = bool(celBool)
		} else {
			return -1, fmt.Errorf("CEL matcher returned %T, want bool", result.Value())
		}
	}
	if !matched {
		return -1, nil
	}
	return m.score, nil
}
