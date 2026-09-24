package fixture_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ketang/zolem/internal/fixture"
)

func TestRenderBody_TemplateUsesFixtureRuntimeTimeAndSequences(t *testing.T) {
	seed := uint64(99)
	f := fixture.Fixture{
		ID:           "templated-openai",
		Provider:     "openai",
		Version:      "v1",
		Status:       200,
		TemplateSeed: &seed,
	}
	if err := f.SetResponseTemplate([]byte(`{
		"id": {{ json .Faker.UUID }},
		"created_at": {{ json (.Now.Format "2006-01-02T15:04:05Z07:00") }},
		"request_sequence": {{ json .Sequence.ProfileRequest }},
		"render_sequence": {{ json .Sequence.TemplateRender }},
		"listener": {{ json .Runtime.ListenerName }},
		"profile": {{ json .Runtime.ProfileName }},
		"model": {{ json .Runtime.BackendModel }},
		"fixture": {{ json .Fixture.ID }}
	}`)); err != nil {
		t.Fatalf("set template: %v", err)
	}

	input := fixture.RenderInput{
		Runtime: fixture.TemplateRuntimeContext{
			ListenerName:     "openai-demo",
			ListenerProvider: "openai",
			ProfileName:      "fixture-profile",
			BackendModel:     "mock-model",
			FixtureNamespace: "team-a",
			TLS:              true,
		},
		Now: time.Date(2026, 4, 28, 14, 30, 0, 0, time.UTC),
		Sequence: fixture.TemplateSequenceContext{
			ProfileRequest: 7,
			TemplateRender: 3,
		},
	}

	first, err := fixture.RenderBody(f, input)
	if err != nil {
		t.Fatalf("render first: %v", err)
	}
	second, err := fixture.RenderBody(f, input)
	if err != nil {
		t.Fatalf("render second: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("seeded template should be deterministic:\nfirst:  %s\nsecond: %s", first, second)
	}

	var payload map[string]any
	if err := json.Unmarshal(first, &payload); err != nil {
		t.Fatalf("rendered template is not JSON: %v\n%s", err, first)
	}
	if payload["created_at"] != "2026-04-28T14:30:00Z" {
		t.Fatalf("created_at: got %#v", payload["created_at"])
	}
	if payload["request_sequence"] != float64(7) || payload["render_sequence"] != float64(3) {
		t.Fatalf("sequence: got request=%#v render=%#v", payload["request_sequence"], payload["render_sequence"])
	}
	if payload["listener"] != "openai-demo" || payload["profile"] != "fixture-profile" || payload["model"] != "mock-model" || payload["fixture"] != "templated-openai" {
		t.Fatalf("metadata payload: got %#v", payload)
	}
}

func TestRenderBody_TemplateDoesNotExposeRequestData(t *testing.T) {
	f := fixture.Fixture{ID: "no-request", Provider: "openai", Version: "v1", Status: 200}
	if err := f.SetResponseTemplate([]byte(`{"value": {{ json .Request.Body.model }}}`)); err != nil {
		t.Fatalf("set template: %v", err)
	}

	_, err := fixture.RenderBody(f, fixture.RenderInput{})
	if err == nil {
		t.Fatal("expected missing request context error")
	}
	if !strings.Contains(err.Error(), "Request") {
		t.Fatalf("error %q does not mention missing request data", err)
	}
}

func TestValidateTemplate_CatchesInvalidRenderedJSONAtSetup(t *testing.T) {
	f := fixture.Fixture{ID: "invalid-json", Provider: "openai", Version: "v1", Status: 200}
	if err := f.SetResponseTemplate([]byte(`{"id": {{ .Faker.UUID }}}`)); err != nil {
		t.Fatalf("set template: %v", err)
	}

	err := fixture.ValidateTemplate(f, fixture.ValidationInput{})
	if err == nil {
		t.Fatal("expected setup validation error")
	}
	if !strings.Contains(err.Error(), "rendered template is not valid JSON") {
		t.Fatalf("error %q does not mention invalid JSON", err)
	}
}

func TestValidateTemplate_DynamicRequestChecksJSONWhenRendered(t *testing.T) {
	f := fixture.Fixture{ID: "literal-request", Provider: "typesafe", Version: "v1", Status: 200}
	if err := f.SetResponseTemplate([]byte(`{"text":".Request", "bad": }`)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.ValidateTemplate(f, fixture.ValidationInput{DynamicRequest: true}); err != nil {
		t.Fatalf("request-capable template should wait for a real request: %v", err)
	}
	if _, err := fixture.RenderBody(f, fixture.RenderInput{Request: map[string]any{"state": true}}); err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("expected render-time JSON validation, got %v", err)
	}
}

func TestValidateTemplate_DefersRootVariableRequestReference(t *testing.T) {
	f := fixture.Fixture{ID: "root-request", Provider: "typesafe", Version: "v1", Status: 200}
	if err := f.SetResponseTemplate([]byte(`{"value": {{ $.Request.state }}}`)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.ValidateTemplate(f, fixture.ValidationInput{DynamicRequest: true}); err != nil {
		t.Fatalf("request-dependent template should validate at render time: %v", err)
	}
	body, err := fixture.RenderBody(f, fixture.RenderInput{Request: map[string]any{"state": true}})
	if err != nil || string(body) != `{"value": true}` {
		t.Fatalf("render request: body=%s error=%v", body, err)
	}
}

func TestValidateTemplate_DefersAliasedRequestReference(t *testing.T) {
	f := fixture.Fixture{ID: "aliased-request", Provider: "typesafe", Version: "v1", Status: 200}
	if err := f.SetResponseTemplate([]byte(`{"value": {{ $r := . }}{{ $r.Request.state }}}`)); err != nil {
		t.Fatal(err)
	}
	if err := fixture.ValidateTemplate(f, fixture.ValidationInput{DynamicRequest: true}); err != nil {
		t.Fatalf("aliased request-dependent template should validate at render time: %v", err)
	}
	body, err := fixture.RenderBody(f, fixture.RenderInput{Request: map[string]any{"state": true}})
	if err != nil || string(body) != `{"value": true}` {
		t.Fatalf("render request: body=%s error=%v", body, err)
	}
}
