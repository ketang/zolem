package fixture

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"text/template"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	runtimecfg "github.com/ketang/zolem/internal/runtime"
)

type TemplateRuntimeContext struct {
	ListenerName     string
	ListenerProvider string
	ProfileName      string
	BackendModel     string
	FixtureNamespace string
	TLS              bool
}

type TemplateFixtureContext struct {
	ID       string
	Provider string
	Version  string
	Stream   bool
	Status   int
}

type TemplateSequenceContext struct {
	ProfileRequest uint64
	TemplateRender uint64
}

type TemplateMetaContext struct {
	Seed uint64
}

type TemplateContext struct {
	Runtime  TemplateRuntimeContext
	Fixture  TemplateFixtureContext
	Template TemplateMetaContext
	Sequence TemplateSequenceContext
	Now      time.Time
	Faker    *gofakeit.Faker
}

type RenderInput struct {
	Runtime  TemplateRuntimeContext
	Sequence TemplateSequenceContext
	Now      time.Time
}

type ValidationInput struct {
	Runtime TemplateRuntimeContext
}

const validationSeed uint64 = 1

func parseResponseTemplate(body string) (*template.Template, error) {
	tmpl, err := template.New("response.json.tmpl").
		Option("missingkey=error").
		Funcs(template.FuncMap{"json": templateJSON}).
		Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse response.json.tmpl: %w", err)
	}
	return tmpl, nil
}

func templateJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func RenderBody(f Fixture, input RenderInput) ([]byte, error) {
	if !f.Templated {
		return f.ResponseBody, nil
	}
	if f.templateBody == nil {
		return nil, fmt.Errorf("fixture %q has no parsed response template", f.ID)
	}

	seed, err := renderSeed(f)
	if err != nil {
		return nil, err
	}
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	}

	var buf bytes.Buffer
	if err := f.templateBody.Execute(&buf, templateContext(f, input, seed)); err != nil {
		return nil, fmt.Errorf("execute response template for fixture %q: %w", f.ID, err)
	}
	if !json.Valid(buf.Bytes()) {
		return nil, fmt.Errorf("rendered template is not valid JSON for fixture %q", f.ID)
	}
	return buf.Bytes(), nil
}

func RuntimeContext(listenerRuntime runtimecfg.ListenerRuntime) TemplateRuntimeContext {
	return TemplateRuntimeContext{
		ListenerName:     listenerRuntime.Spec.Name,
		ListenerProvider: listenerRuntime.Spec.Provider,
		ProfileName:      listenerRuntime.Profile.Name,
		BackendModel:     listenerRuntime.Profile.BackendModel,
		FixtureNamespace: listenerRuntime.Profile.FixtureNamespace,
		TLS:              listenerRuntime.Spec.TLS,
	}
}

func ValidateTemplate(f Fixture, input ValidationInput) error {
	if !f.Templated {
		return nil
	}
	if f.templateBody == nil {
		return fmt.Errorf("fixture %q has no parsed response template", f.ID)
	}

	now := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	renderInput := RenderInput{
		Runtime: input.Runtime,
		Now:     now,
		Sequence: TemplateSequenceContext{
			ProfileRequest: 1,
			TemplateRender: 1,
		},
	}
	if err := f.templateBody.Execute(&buf, templateContext(f, renderInput, validationSeed)); err != nil {
		return fmt.Errorf("validate response template for fixture %q: %w", f.ID, err)
	}
	if !json.Valid(buf.Bytes()) {
		return fmt.Errorf("rendered template is not valid JSON for fixture %q", f.ID)
	}
	return nil
}

func templateContext(f Fixture, input RenderInput, seed uint64) TemplateContext {
	return TemplateContext{
		Runtime: input.Runtime,
		Fixture: TemplateFixtureContext{
			ID:       f.ID,
			Provider: f.Provider,
			Version:  f.Version,
			Stream:   f.Stream,
			Status:   f.Status,
		},
		Template: TemplateMetaContext{Seed: seed},
		Sequence: input.Sequence,
		Now:      input.Now,
		Faker:    gofakeit.New(seed),
	}
}

func renderSeed(f Fixture) (uint64, error) {
	if f.TemplateSeed != nil {
		return *f.TemplateSeed, nil
	}
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return 0, fmt.Errorf("generate template seed for fixture %q: %w", f.ID, err)
	}
	return binary.LittleEndian.Uint64(data[:]), nil
}
