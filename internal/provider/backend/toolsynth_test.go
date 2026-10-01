package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var toolSeq atomic.Int64

func uniqueTool() string { return fmt.Sprintf("tool-%d", toolSeq.Add(1)) }

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// oracle compiles schema (format assertions on, no external loading) and
// validates out against it.
func oracle(t testing.TB, schema, out []byte) error {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		t.Fatalf("schema parse: %v", err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.UseLoader(rejectLoader{})
	if err := c.AddResource("mem://oracle.json", doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("mem://oracle.json")
	if err != nil {
		t.Fatalf("schema compile: %v", err)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("output parse: %v (%s)", err, out)
	}
	return s.Validate(v)
}

var supportedCases = []struct{ name, schema string }{
	{"enum", `{"type":"object","required":["u"],"properties":{"u":{"type":"string","enum":["c","f"]}}}`},
	{"const", `{"type":"object","required":["k"],"properties":{"k":{"const":"fixed"}}}`},
	{"nullable_type_array", `{"type":"object","required":["n"],"properties":{"n":{"type":["integer","null"]}}}`},
	{"anyOf", `{"type":"object","required":["u"],"properties":{"u":{"anyOf":[{"type":"null"},{"type":"integer"}]}}}`},
	{"oneOf_int_number", `{"type":"object","required":["x"],"properties":{"x":{"oneOf":[{"type":"integer"},{"type":"number"}]}}}`},
	{"minItems", `{"type":"object","required":["t"],"properties":{"t":{"type":"array","items":{"type":"string"},"minItems":3}}}`},
	{"maxItems_zero", `{"type":"object","required":["t"],"properties":{"t":{"type":"array","items":{"type":"string"},"maxItems":0}}}`},
	{"formats", `{"type":"object","required":["a","b","c","d","e"],"properties":{"a":{"type":"string","format":"email"},"b":{"type":"string","format":"date-time"},"c":{"type":"string","format":"date"},"d":{"type":"string","format":"uri"},"e":{"type":"string","format":"uuid"}}}`},
	{"string_len", `{"type":"object","required":["a","b"],"properties":{"a":{"type":"string","minLength":30},"b":{"type":"string","maxLength":3}}}`},
	{"numeric_bounds", `{"type":"object","required":["a","b","c","d"],"properties":{"a":{"type":"integer","minimum":100},"b":{"type":"number","exclusiveMinimum":0,"exclusiveMaximum":1},"c":{"type":"integer","maximum":-5},"d":{"type":"integer","exclusiveMinimum":10,"exclusiveMaximum":12}}}`},
	{"constraint_aware_enum", `{"type":"object","required":["a"],"properties":{"a":{"type":"integer","enum":[0,2],"minimum":1}}}`},
	{"type_alternatives_string", `{"type":"object","required":["a"],"properties":{"a":{"type":["integer","string"],"minimum":0.1,"maximum":0.9}}}`},
	{"type_alternatives_null", `{"type":"object","required":["a"],"properties":{"a":{"type":["integer","null"],"minimum":0.1,"maximum":0.9}}}`},
	{"null_only", `{"type":"object","required":["a"],"properties":{"a":{"type":"null"}}}`},
	{"optional_unsatisfiable_omitted", `{"type":"object","properties":{"x":{"type":"integer","minimum":1,"maximum":0}}}`},
	{"optional_array_unsatisfiable_items", `{"type":"object","properties":{"t":{"type":"array","items":{"type":"integer","minimum":1,"maximum":0}}}}`},
	{"nested_objects", `{"type":"object","required":["o"],"properties":{"o":{"type":"object","required":["p"],"properties":{"p":{"type":"object","required":["q"],"properties":{"q":{"type":"boolean"}}}}}}}`},
	{"ref_defs", `{"type":"object","required":["a"],"properties":{"a":{"$ref":"#/$defs/a"}},"$defs":{"a":{"type":"string","enum":["z"]}}}`},
	{"ref_definitions", `{"type":"object","required":["a"],"properties":{"a":{"$ref":"#/definitions/a"}},"definitions":{"a":{"type":"integer","minimum":7}}}`},
	{"allOf_objects", `{"type":"object","allOf":[{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}},{"type":"object","required":["b"],"properties":{"b":{"type":"integer"}}}]}`},
	{"allOf_bounds_merge", `{"type":"object","allOf":[{"type":"object","required":["x"],"properties":{"x":{"type":"integer","minimum":100}}},{"type":"object","required":["x"],"properties":{"x":{"type":"integer","maximum":200}}}]}`},
	{"allOf_length_enum_merge", `{"type":"object","allOf":[{"type":"object","required":["s","e"],"properties":{"s":{"type":"string","minLength":5},"e":{"type":"string","enum":["a","b","c"]}}},{"type":"object","required":["s","e"],"properties":{"s":{"type":"string","maxLength":6},"e":{"type":"string","enum":["c","d"]}}}]}`},
	{"no_required_all_props", `{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"integer"}}}`},
	{"empty_object", `{"type":"object"}`},
}

func TestSynthArgs_SupportedSubsetValidates(t *testing.T) {
	for _, tc := range supportedCases {
		t.Run(tc.name, func(t *testing.T) {
			out, st := synthesize(uniqueTool(), json.RawMessage(tc.schema))
			if err := oracle(t, []byte(tc.schema), out); err != nil {
				t.Fatalf("output %s does not validate: %v", out, err)
			}
			if !st.Valid {
				t.Errorf("synthesizer reported invalid for %s", out)
			}
			if st.WholeValidations > maxWholeChecks {
				t.Errorf("whole validations %d > %d", st.WholeValidations, maxWholeChecks)
			}
		})
	}
}

func TestSynthArgs_SpecificValues(t *testing.T) {
	get := func(schema, key string) any {
		out := SynthArgsForTool(uniqueTool(), json.RawMessage(schema))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}
		return m[key]
	}
	if v := get(`{"type":"object","required":["a"],"properties":{"a":{"type":"integer","enum":[0,2],"minimum":1}}}`, "a"); v != float64(2) {
		t.Errorf("enum+minimum: got %v, want 2", v)
	}
	if v, ok := get(`{"type":"object","required":["x"],"properties":{"x":{"oneOf":[{"type":"integer"},{"type":"number"}]}}}`, "x").(float64); !ok || v == float64(int(v)) {
		t.Errorf("oneOf int/number: want non-integer number, got %v", v)
	}
	if _, ok := get(`{"type":"object","required":["a"],"properties":{"a":{"type":["integer","string"],"minimum":0.1,"maximum":0.9}}}`, "a").(string); !ok {
		t.Error("type alternatives: want string")
	}
	if v := get(`{"type":"object","required":["a"],"properties":{"a":{"type":["integer","null"],"minimum":0.1,"maximum":0.9}}}`, "a"); v != nil {
		t.Errorf("integer|null with no integer fit: got %v, want null", v)
	}
	if out := SynthArgsForTool(uniqueTool(), json.RawMessage(`{"type":"object","properties":{"x":{"type":"integer","minimum":1,"maximum":0}}}`)); string(out) != "{}" {
		t.Errorf("unsatisfiable optional: got %s, want {}", out)
	}
	if v := get(`{"type":"object","required":["t"],"properties":{"t":{"type":"array","items":{"type":"string"},"minItems":1}}}`, "t").([]any); len(v) < 1 {
		t.Error("minItems:1 produced empty array")
	}
}

func TestSynthArgs_InvalidAndEmpty(t *testing.T) {
	for _, in := range []string{``, `   `, `not json`, `[]`, `true`, `{"type":"string"}`} {
		if out := SynthArgs(json.RawMessage(in)); string(out) != "{}" {
			t.Errorf("SynthArgs(%q) = %s, want {}", in, out)
		}
	}
}

func TestNormalizeGeminiSchema(t *testing.T) {
	in := `{"type":"OBJECT","required":["a","b","c"],"properties":{` +
		`"a":{"type":"STRING","nullable":true,"enum":["x"]},` +
		`"b":{"type":"ARRAY","minItems":"2","items":{"type":"INTEGER","format":"int32"}},` +
		`"c":{"anyOf":[{"type":"NUMBER"},{"type":"STRING","nullable":true}]}}}`
	norm := NormalizeGeminiSchema(json.RawMessage(in))
	if strings.Contains(string(norm), "OBJECT") || strings.Contains(string(norm), "nullable") {
		t.Fatalf("not normalized: %s", norm)
	}
	out, st := synthesize(uniqueTool(), norm)
	if !st.Valid {
		t.Fatalf("invalid result %s", out)
	}
	if err := oracle(t, norm, out); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if len(m["b"].([]any)) < 2 {
		t.Errorf("string minItems not honored: %s", out)
	}
	if got := string(NormalizeGeminiSchema(json.RawMessage("nope"))); got != "nope" {
		t.Errorf("invalid input should pass through, got %q", got)
	}
	if got := NormalizeGeminiSchema(nil); len(got) != 0 {
		t.Errorf("nil: got %q", got)
	}
}

func TestSynthArgs_NoExternalLoading(t *testing.T) {
	var loads []string
	schemaLoadHook = func(u string) { loads = append(loads, u) }
	t.Cleanup(func() { schemaLoadHook = nil })

	for _, schema := range []string{
		`{"type":"object","required":["a"],"properties":{"a":{"$ref":"file:///etc/hostname"}}}`,
		`{"type":"object","required":["a"],"properties":{"a":{"$ref":"http://127.0.0.1:1/x"}}}`,
		`{"$schema":"file:///tmp/x.json","type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`,
	} {
		buf := captureLog(t)
		out := SynthArgsForTool(uniqueTool(), json.RawMessage(schema))
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		if !strings.Contains(buf.String(), "do not satisfy schema for tool") {
			t.Errorf("%s: missing warning, log=%q", schema, buf.String())
		}
	}
	// The loader rejects everything, so any attempt was refused; nothing may
	// have been resolved from outside the in-memory resource.
	for _, u := range loads {
		if u == rootSchemaURL {
			t.Errorf("root schema should be added in memory, not loaded: %s", u)
		}
	}
	// In-document refs keep working.
	out, st := synthesize(uniqueTool(), json.RawMessage(`{"type":"object","required":["a"],"properties":{"a":{"$ref":"#/$defs/a"}},"$defs":{"a":{"type":"integer"}}}`))
	if !st.Valid || string(out) != `{"a":42}` {
		t.Errorf("in-document $ref: got %s valid=%v", out, st.Valid)
	}
}

func TestSynthArgs_RejectLoaderErrors(t *testing.T) {
	var seen string
	schemaLoadHook = func(u string) { seen = u }
	t.Cleanup(func() { schemaLoadHook = nil })
	if _, err := (rejectLoader{}).Load("file:///etc/hostname"); err == nil || seen != "file:///etc/hostname" {
		t.Errorf("reject loader must error and report; err=%v seen=%q", err, seen)
	}
}

func TestSynthArgs_ConstraintAwareAllOfConflictWarns(t *testing.T) {
	buf := captureLog(t)
	tool := uniqueTool()
	schema := `{"type":"object","allOf":[{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}},{"type":"object","required":["x"],"properties":{"x":{"type":"integer"}}}]}`
	out, st := synthesize(tool, json.RawMessage(schema))
	if len(out) == 0 || st.Valid {
		t.Fatalf("expected a best-effort invalid result, got %s valid=%v", out, st.Valid)
	}
	if !strings.Contains(buf.String(), fmt.Sprintf("do not satisfy schema for tool %q", tool)) {
		t.Errorf("missing warning: %q", buf.String())
	}
	// Once per tool name.
	buf.Reset()
	synthesize(tool, json.RawMessage(schema))
	if buf.Len() != 0 {
		t.Errorf("warning repeated: %q", buf.String())
	}
}

func bigSchema(n int) string {
	var b strings.Builder
	b.WriteString(`{"type":"object","properties":{`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"p%d":{"type":"string"}`, i)
	}
	b.WriteString(`}}`)
	return b.String()
}

// fanoutSchema: $defs/a is an anyOf of n object branches, each requiring a
// property y that $refs a again. Unbounded generation is exponential.
func fanoutSchema(n int) string {
	var br []string
	for i := 0; i < n; i++ {
		br = append(br, fmt.Sprintf(`{"type":"object","required":["y"],"properties":{"y":{"$ref":"#/$defs/a"},"i":{"const":%d}}}`, i))
	}
	return `{"type":"object","required":["x"],"properties":{"x":{"$ref":"#/$defs/a"}},"$defs":{"a":{"anyOf":[` + strings.Join(br, ",") + `]}}}`
}

var limitCases = []struct{ name, schema string }{
	{"pattern", `{"type":"object","required":["a"],"properties":{"a":{"type":"string","pattern":"^[0-9]{5}$"}}}`},
	{"not", `{"type":"object","required":["a"],"properties":{"a":{"not":{"type":"string"}}}}`},
	{"minItems_huge", `{"type":"object","required":["t"],"properties":{"t":{"type":"array","items":{"type":"string"},"minItems":100000}}}`},
	{"minLength_huge", `{"type":"object","required":["s"],"properties":{"s":{"type":"string","minLength":100000}}}`},
	{"wide_100KiB", bigSchema(5000)},
	{"nodes_3000", bigSchema(3000)},
	{"nested_arrays", `{"type":"object","required":["a"],"properties":{"a":{"type":"array","minItems":64,"items":{"type":"array","minItems":64,"items":{"type":"array","minItems":64,"items":{"type":"array","minItems":64,"items":{"type":"string","minLength":1000}}}}}}}`},
	{"recursive_ref", `{"type":"object","required":["n"],"properties":{"n":{"$ref":"#/$defs/n"}},"$defs":{"n":{"type":"object","required":["n"],"properties":{"n":{"$ref":"#/$defs/n"}}}}}`},
	{"fanout_ref_cycle_anyOf", fanoutSchema(16)},
	{"many_oneOf", `{"type":"object","required":["a"],"properties":{"a":{"oneOf":[{"type":"integer"},{"type":"integer"},{"type":"integer"},{"type":"integer"}]}}}`},
}

func TestSynthArgs_LimitsAndOutOfSubset(t *testing.T) {
	for _, tc := range limitCases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureLog(t)
			out, st := synthesize(uniqueTool(), json.RawMessage(tc.schema))
			if len(out) > maxOutputBytes {
				t.Errorf("output %d bytes > %d", len(out), maxOutputBytes)
			}
			var v any
			if err := json.Unmarshal(out, &v); err != nil {
				t.Fatalf("output is not JSON: %v", err)
			}
			if st.WholeValidations > maxWholeChecks {
				t.Errorf("whole validations %d > %d", st.WholeValidations, maxWholeChecks)
			}
			if !st.Valid && !strings.Contains(buf.String(), "do not satisfy schema for tool") {
				t.Errorf("invalid result without warning; log=%q", buf.String())
			}
		})
	}
}

func TestSynthArgs_OversizeFallsBackToTypeWalker(t *testing.T) {
	buf := captureLog(t)
	out := SynthArgsForTool(uniqueTool(), json.RawMessage(bigSchema(3000)))
	if !strings.Contains(buf.String(), "do not satisfy schema") {
		t.Errorf("missing warning")
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil || len(m) != 3000 || m["p0"] != "lorem ipsum" {
		t.Errorf("type-walker output unexpected: err=%v len=%d", err, len(m))
	}
}

func TestSimpleArgs_TypeWalker(t *testing.T) {
	doc, _ := jsonschema.UnmarshalJSON(strings.NewReader(`{"type":"object","required":["a","b","c","d","e","f"],"properties":{"a":{"type":"string"},"b":{"type":"integer"},"c":{"type":"boolean"},"d":{"type":"array"},"e":{"type":"object","properties":{"z":{"type":"number"}}},"g":{"type":"string"}}}`))
	got := string(simpleArgs(doc))
	want := `{"a":"lorem ipsum","b":42,"c":true,"d":[],"e":{"z":42},"f":"lorem ipsum"}`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if got := string(simpleArgs(map[string]any{"type": "array"})); got != "{}" {
		t.Errorf("non-object top: %s", got)
	}
	if got := string(simpleArgs("x")); got != "{}" {
		t.Errorf("non-map: %s", got)
	}
}

func TestSynthArgs_Deterministic(t *testing.T) {
	for _, tc := range supportedCases {
		a := SynthArgsForTool(uniqueTool(), json.RawMessage(tc.schema))
		b := SynthArgsForTool(uniqueTool(), json.RawMessage(tc.schema))
		if !bytes.Equal(a, b) {
			t.Errorf("%s: nondeterministic %s vs %s", tc.name, a, b)
		}
	}
}

func FuzzSynthArgs(f *testing.F) {
	for _, tc := range supportedCases {
		f.Add(tc.schema)
	}
	f.Add(fanoutSchema(16))
	for _, tc := range limitCases[:6] {
		if len(tc.schema) < 8192 {
			f.Add(tc.schema)
		}
	}
	prev := log.Writer()
	log.SetOutput(io.Discard)
	f.Cleanup(func() { log.SetOutput(prev) })
	f.Fuzz(func(t *testing.T, schema string) {
		start := time.Now()
		out, st := synthesize("fuzz", json.RawMessage(schema))
		if st.Work > maxWork+1 {
			t.Fatalf("work %d exceeds budget %d", st.Work, maxWork)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Fatalf("synthesis took %v", el)
		}
		if len(out) > maxOutputBytes {
			t.Fatalf("output %d bytes", len(out))
		}
		if !json.Valid(out) {
			t.Fatalf("invalid JSON output %q", out)
		}
		if st.WholeValidations > maxWholeChecks {
			t.Fatalf("whole validations %d", st.WholeValidations)
		}
	})
}

func BenchmarkSynthArgs(b *testing.B) {
	for _, tc := range append(append([]struct{ name, schema string }{}, supportedCases[:3]...), limitCases...) {
		b.Run(tc.name, func(b *testing.B) {
			schema := json.RawMessage(tc.schema)
			for i := 0; i < b.N; i++ {
				_, _ = synthesize("bench", schema)
			}
		})
	}
}

func TestSynthArgs_MoreSubsetShapes(t *testing.T) {
	cases := []struct{ name, schema string }{
		{"root_type_array", `{"type":["object","null"],"required":["a"],"properties":{"a":{"type":"string"}}}`},
		{"anyOf_inherits_type", `{"type":"object","required":["a"],"properties":{"a":{"type":"string","anyOf":[{"minLength":3},{"maxLength":1}]}}}`},
		{"anyOf_ref_branch", `{"type":"object","required":["a"],"properties":{"a":{"anyOf":[{"$ref":"#/$defs/s"},{"type":"null"}]}},"$defs":{"s":{"type":"string","enum":["q"]}}}`},
		{"escaped_property_names", `{"type":"object","required":["a/b","c~d","e f%"],"properties":{"a/b":{"type":"integer","minimum":3},"c~d":{"type":"string","enum":["x"]},"e f%":{"type":"boolean"}}}`},
		{"ref_with_escaped_pointer", `{"type":"object","required":["a"],"properties":{"a":{"$ref":"#/$defs/a~1b"}},"$defs":{"a/b":{"type":"integer","minimum":9}}}`},
		{"number_bounds", `{"type":"object","required":["a","b"],"properties":{"a":{"type":"number","minimum":100.5},"b":{"type":"number","maximum":-3}}}`},
		{"default_used", `{"type":"object","required":["a"],"properties":{"a":{"type":"string","default":"dflt","enum":["dflt","x"]}}}`},
		{"untyped_property", `{"type":"object","required":["a"],"properties":{"a":{}}}`},
		{"bool_property_schema", `{"type":"object","required":["a"],"properties":{"a":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, st := synthesize(uniqueTool(), json.RawMessage(tc.schema))
			if err := oracle(t, []byte(tc.schema), out); err != nil || !st.Valid {
				t.Fatalf("%s valid=%v err=%v", out, st.Valid, err)
			}
		})
	}
}

func TestLookupPointer(t *testing.T) {
	doc, _ := jsonschema.UnmarshalJSON(strings.NewReader(`{"$defs":{"a":{"x":1},"l":[{"y":2}]}}`))
	for _, ref := range []string{"#", "#/$defs/a", "#/$defs/l/0"} {
		if _, _, ok := lookupPointer(doc, ref); !ok {
			t.Errorf("%s should resolve", ref)
		}
	}
	for _, ref := range []string{"http://x/y", "#foo", "#/$defs/nope", "#/$defs/l/9", "#/$defs/l/x", "#/$defs/a/x/y", "#/$defs/%zz"} {
		if _, _, ok := lookupPointer(doc, ref); ok {
			t.Errorf("%s should not resolve", ref)
		}
	}
}

func TestTypeAllows(t *testing.T) {
	if !typeAllows(nil, "object") || !typeAllows("object", "object") || typeAllows("string", "object") {
		t.Error("string/nil handling")
	}
	if !typeAllows([]any{"null", "object"}, "object") || typeAllows([]any{"null"}, "object") || !typeAllows(7, "object") {
		t.Error("array/other handling")
	}
}

func TestSynthArgs_FanoutIsBounded(t *testing.T) {
	buf := captureLog(t)
	start := time.Now()
	out, st := synthesize(uniqueTool(), json.RawMessage(fanoutSchema(16)))
	if el := time.Since(start); el > time.Second {
		t.Fatalf("took %v, want well under a second", el)
	}
	if !st.Aborted || st.Work > maxWork+1 {
		t.Errorf("expected work-budget abort within %d units, got aborted=%v work=%d", maxWork, st.Aborted, st.Work)
	}
	if !json.Valid(out) || !strings.Contains(buf.String(), "do not satisfy schema for tool") {
		t.Errorf("want fallback JSON plus warning; out=%s log=%q", out, buf.String())
	}
}

func TestNormalizeGeminiSchema_DeepNesting(t *testing.T) {
	const n = 5000
	deep := strings.Repeat(`{"type":"OBJECT","properties":{"a":`, n) + `{"type":"STRING"}` + strings.Repeat(`}}`, n)
	start := time.Now()
	out := NormalizeGeminiSchema(json.RawMessage(deep))
	if len(out) == 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("deep normalize: len=%d in %v", len(out), time.Since(start))
	}
	_ = SynthArgsForTool(uniqueTool(), out)
}

func TestNormalizeGeminiSchema_NullableEnumDoesNotMutateShared(t *testing.T) {
	in := json.RawMessage(`{"type":"OBJECT","properties":{"a":{"type":"STRING","nullable":true,"enum":["x"]}}}`)
	a := NormalizeGeminiSchema(in)
	b := NormalizeGeminiSchema(in)
	if !bytes.Equal(a, b) || !strings.Contains(string(a), `["x",null]`) {
		t.Errorf("unexpected normalization: %s / %s", a, b)
	}
}

func TestSynthArgs_FalseBooleanPropertySchema(t *testing.T) {
	// Optional property whose schema is `false` can never validate: omitted.
	out, st := synthesize(uniqueTool(), json.RawMessage(`{"type":"object","properties":{"a":false,"b":{"type":"string"}}}`))
	if !st.Valid || string(out) != `{"b":"lorem ipsum"}` {
		t.Errorf("got %s valid=%v", out, st.Valid)
	}
}
