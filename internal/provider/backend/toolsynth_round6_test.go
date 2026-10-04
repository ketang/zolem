package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// fallbackRequiredShape: a $defs target with an n-entry required list
// (over the verification cap, so the fallback runs) referenced from 499
// properties. The fallback used to emit 499 x n keys.
func fallbackRequiredShape(n int) string {
	var req, ps, top []string
	for i := range n {
		req = append(req, fmt.Sprintf(`"r%d"`, i))
	}
	for i := range 499 {
		ps = append(ps, fmt.Sprintf(`"p%d":{"$ref":"#/$defs/B"}`, i))
		top = append(top, fmt.Sprintf(`"p%d"`, i))
	}
	return `{"type":"object","properties":{` + strings.Join(ps, ",") + `},"required":[` + strings.Join(top, ",") +
		`],"$defs":{"B":{"type":"object","required":[` + strings.Join(req, ",") + `]}}}`
}

// fallbackAnyOfShape: 499 properties referencing an n-branch anyOf of nulls,
// which the fallback scans for a non-null branch.
func fallbackAnyOfShape(n int) string {
	var ps, top []string
	for i := range 499 {
		ps = append(ps, fmt.Sprintf(`"p%d":{"$ref":"#/$defs/B"}`, i))
		top = append(top, fmt.Sprintf(`"p%d"`, i))
	}
	return `{"type":"object","properties":{` + strings.Join(ps, ",") + `},"required":[` + strings.Join(top, ",") +
		`],"$defs":{"B":{"anyOf":[` + repeatJoin(`{"type":"null"}`, n) + `]}}}`
}

// longKeyShape: one property with a long (multi-byte) name whose object has
// kids children, so every child's JSON pointer repeats the escaped name.
func longKeyShape(ch string, klen, kids int) string {
	var ks []string
	for i := range kids {
		ks = append(ks, fmt.Sprintf(`"c%d":{"type":"string"}`, i))
	}
	return `{"type":"object","properties":{"` + strings.Repeat(ch, klen) + `":{"type":"object","properties":{` + strings.Join(ks, ",") + `}}}}`
}

// longRefShape: 160 properties referencing a def that $refs a 20000-char
// name.
func longRefShape(ch string) string {
	long := strings.Repeat(ch, 20000)
	var ps []string
	for i := range 160 {
		ps = append(ps, fmt.Sprintf(`"p%d":{"$ref":"#/$defs/T"}`, i))
	}
	name := strings.ReplaceAll(strings.ReplaceAll(long, "~0", "~"), "%41", "A")
	return `{"type":"object","properties":{` + strings.Join(ps, ",") + `},"$defs":{"T":{"$ref":"#/$defs/` + long + `"},"` + name + `":{"type":"string"}}}`
}

// round6Shapes are the sixth review's shapes (all within the 64 KiB
// verification input limit, so the verified path runs).
func round6Shapes() map[string]string {
	m := map[string]string{}
	var names, props []string
	for i := range 256 {
		names = append(names, fmt.Sprintf(`"r%03d"`, i))
		props = append(props, fmt.Sprintf(`"r%03d":{"type":"string"}`, i))
	}
	req := strings.Join(names, ",")
	m["req256_allOf16"] = `{"type":"object","properties":{` + strings.Join(props, ",") + `},"required":[` + req + `],"allOf":[` + repeatJoin(`{"required":[`+req+`]}`, 16) + `]}`
	m["allOf16x16"] = `{"type":"object","allOf":[` + repeatJoin(`{"type":"object","properties":{"a":{"type":"string"}},"allOf":[`+repeatJoin(`{"properties":{"b":{"type":"integer","minimum":1}}}`, 16)+`]}`, 16) + `]}`
	m["anyOf16x16x2"] = `{"type":"object","properties":{"x":{"anyOf":[` + repeatJoin(`{"anyOf":[`+repeatJoin(`{"anyOf":[{"type":"string"},{"type":"integer"}]}`, 15)+`]}`, 16) + `]}}}`
	var ev []string
	for i := range 256 {
		ev = append(ev, fmt.Sprintf(`"%s%03d"`, strings.Repeat("e", 240), i))
	}
	m["enum256"] = `{"type":"object","properties":{"x":{"enum":[` + strings.Join(ev, ",") + `]}}}`
	m["anyOf16_enum14"] = `{"type":"object","properties":{"x":{"anyOf":[` + repeatJoin(`{"enum":[`+strings.Join(ev[:14], ",")+`]}`, 16) + `]}}}`
	m["items_enum200_minItems1e15"] = `{"type":"object","properties":{"x":{"type":"array","minItems":1000000000000000,"items":{"enum":[` + strings.Join(ev[:200], ",") + `]}}},"required":["x"]}`
	m["huge_key_60k"] = `{"type":"object","properties":{"` + strings.Repeat("k", 60000) + `":{"type":"string"}},"required":["` + strings.Repeat("k", 60000) + `"]}`
	m["key_euro19000_kids200"] = longKeyShape("€", 19000, 200)
	m["key_emoji14000_kids200"] = longKeyShape("\U0001F600", 14000, 200)
	m["key_k60000_kids200"] = strings.Replace(longKeyShape("k", 1, 200), `"k":`, `"`+strings.Repeat("k", 60000)+`":`, 1)
	for _, ch := range []string{"k", "%41", "~0", "é"} {
		m["long_ref_"+ch] = longRefShape(ch)
	}
	var defs []string
	for i := range 30 {
		defs = append(defs, fmt.Sprintf(`"d%d":{"type":"object","properties":{"a":{"$ref":"#/$defs/d%d"},"b":{"$ref":"#/$defs/d%d"}}}`, i, i+1, i+1))
	}
	defs = append(defs, `"d30":{"type":"string"}`)
	m["ref_dag_binary30"] = `{"type":"object","properties":{"a":{"$ref":"#/$defs/d0"}},"$defs":{` + strings.Join(defs, ",") + `}}`
	m["ref_allOf16_cubed"] = `{"type":"object","allOf":[` + repeatJoin(`{"$ref":"#/$defs/a"}`, 16) + `],"$defs":{"a":{"allOf":[` + repeatJoin(`{"$ref":"#/$defs/b"}`, 16) + `]},"b":{"allOf":[` + repeatJoin(`{"$ref":"#/$defs/c"}`, 16) + `]},"c":{"type":"object"}}}`
	m["oneOf16_nodes480"] = `{"type":"object","properties":{"x":{"oneOf":[` + repeatJoin(`{"type":"object","properties":{"a":{"type":"array","items":{"type":"string","minLength":5},"minItems":50}}}`, 16) + `]}}}`
	obj20 := func(leaf string) string {
		var p []string
		for i := range 20 {
			p = append(p, fmt.Sprintf(`"p%d":%s`, i, leaf))
		}
		return `{"type":"object","properties":{` + strings.Join(p, ",") + `},"required":["p0","p1","p2","p3"]}`
	}
	m["unsat_big_instance"] = `{"type":"object","properties":{"x":{"type":"array","minItems":64,"items":` + obj20(`{"type":"string","not":{},"minLength":1000}`) + `}},"required":["x"]}`
	m["big_instance_ok"] = `{"type":"object","properties":{"x":{"type":"array","minItems":64,"items":` + obj20(`{"type":"string","minLength":1000}`) + `}},"required":["x"]}`
	var oo []string
	for range 16 {
		oo = append(oo, `{"type":"array","minItems":64,"items":`+obj20(`{"type":"integer","not":{}}`)+`}`)
	}
	m["oneOf16_unsat"] = `{"type":"object","properties":{"x":{"oneOf":[` + strings.Join(oo, ",") + `]}},"required":["x"]}`
	// Many unknown keys on an object reached through many $refs.
	var junk []string
	for i := range 2000 {
		junk = append(junk, fmt.Sprintf(`"x-%d":0`, i))
	}
	var refs []string
	for i := range 300 {
		refs = append(refs, fmt.Sprintf(`"p%d":{"$ref":"#/$defs/J"}`, i))
	}
	m["unknown_keys_x300_refs"] = `{"type":"object","properties":{` + strings.Join(refs, ",") + `},"$defs":{"J":{"type":"string",` + strings.Join(junk, ",") + `}}}`
	return m
}

// largeFallbackShapes exceed the 64 KiB verification limit and exercise the
// unverified fallback alone (no semaphore, no timeout).
func largeFallbackShapes() map[string]string {
	return map[string]string{
		"fallback_required_5000":   fallbackRequiredShape(5000),
		"fallback_required_20000":  fallbackRequiredShape(20000),
		"fallback_required_100000": fallbackRequiredShape(100000),
		"fallback_anyOf_20000":     fallbackAnyOfShape(20000),
		"fallback_anyOf_50000":     fallbackAnyOfShape(50000),
		"fallback_props_wide":      bigSchema(60000),
	}
}

func measure(f func()) (time.Duration, uint64) {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	start := time.Now()
	f()
	el := time.Since(start)
	runtime.ReadMemStats(&b)
	return el, b.TotalAlloc - a.TotalAlloc
}

func TestSimpleArgs_BoundedWithoutSemaphore(t *testing.T) {
	captureLog(t)
	for name, schema := range largeFallbackShapes() {
		t.Run(name, func(t *testing.T) {
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
			if err != nil {
				t.Fatal(err)
			}
			var out json.RawMessage
			el, alloc := measure(func() { out = simpleArgs(doc) })
			t.Logf("%-26s %5d KiB: fallback alone %v, %d MB", name, len(schema)>>10, el.Round(time.Microsecond), alloc>>20)
			if el > 100*time.Millisecond || alloc > 50<<20 {
				t.Errorf("fallback took %v, %d MB", el, alloc>>20)
			}
			if !json.Valid(out) || len(out) > maxOutputBytes {
				t.Errorf("bad output (%d bytes)", len(out))
			}
			// End to end, including decoding the request-sized schema.
			el, alloc = measure(func() { out, _ = synthesize(uniqueTool(), json.RawMessage(schema)) })
			t.Logf("%-26s synthesize %v, %d MB", name, el.Round(time.Microsecond), alloc>>20)
			if el > time.Second || alloc > 100<<20 {
				t.Errorf("synthesize took %v, %d MB", el, alloc>>20)
			}
		})
	}
}

func TestVerifyBuilder_ChargesPointersRefsAndKeys(t *testing.T) {
	for _, name := range []string{"huge_key_60k", "key_euro19000_kids200", "key_emoji14000_kids200", "key_k60000_kids200", "long_ref_k", "long_ref_%41", "long_ref_~0", "long_ref_é", "unknown_keys_x300_refs"} {
		schema := round6Shapes()[name]
		doc := parseSchema(t, schema)
		b := &verifyBuilder{doc: doc}
		el, alloc := measure(func() { b.build(doc) })
		t.Logf("%-24s build %v, %d KB, err=%v", name, el.Round(time.Microsecond), alloc>>10, b.err)
		if el > 50*time.Millisecond || alloc > 8<<20 {
			t.Errorf("%s: build took %v, %d KB", name, el, alloc>>10)
		}
		if b.err == nil {
			t.Errorf("%s: should exceed the verification limits", name)
		}
	}
}

// origVerdict validates out against the ORIGINAL schema with an
// independent full validator. "skip" means the original does not compile.
func origVerdict(schema string, out []byte) string {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schema))
	if err != nil {
		return "skip"
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	c.UseLoader(rejectLoader{})
	if c.AddResource("mem://o.json", doc) != nil {
		return "skip"
	}
	s, err := c.Compile("mem://o.json")
	if err != nil {
		return "skip"
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(out))
	if err != nil {
		return "badjson"
	}
	if err := s.Validate(inst); err != nil {
		return "INVALID: " + truncateText(err.Error(), 300)
	}
	return "ok"
}

// Whatever synthesis marks VERIFIED must validate against the original
// schema whenever the original uses only checked keywords, tuple forms or
// format hints.
func TestSynthArgs_VerifiedMeansValidAgainstOriginal(t *testing.T) {
	cases := map[string]string{
		"prefixItems_items":    `{"type":"object","properties":{"p":{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":{"type":"boolean"},"minItems":2}},"required":["p"]}`,
		"d07_tuple_items":      `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"p":{"type":"array","items":[{"type":"string"},{"type":"integer"}],"minItems":2,"additionalItems":false}},"required":["p"]}`,
		"additional_uuid":      `{"type":"object","additionalProperties":{"type":"string","format":"uuid"},"required":["id"]}`,
		"additional_uri_allOf": `{"type":"object","allOf":[{"type":"object","additionalProperties":{"type":"string","format":"uri"}}],"required":["u"]}`,
		"allOf_merge_uuid":     `{"type":"object","allOf":[{"type":"object","required":["x"],"properties":{"x":{"type":"string"}}},{"type":"object","required":["x"],"properties":{"x":{"type":"string","format":"uuid"}}}]}`,
		"anyOf_parent_uuid":    `{"type":"object","required":["x"],"properties":{"x":{"format":"uuid","anyOf":[{"type":"string"},{"type":"null"}]}}}`,
		"bintree":              `{"type":"object","properties":{"t":{"$ref":"#/$defs/N"}},"required":["t"],"$defs":{"N":{"type":"object","properties":{"v":{"type":"integer"},"l":{"$ref":"#/$defs/N"},"r":{"$ref":"#/$defs/N"}},"required":["v"]}}}`,
		"kids_min1":            `{"type":"object","properties":{"name":{"type":"string"},"kids":{"type":"array","minItems":1,"items":{"$ref":"#"}}},"required":["name","kids"]}`,
		"req_self":             `{"type":"object","properties":{"a":{"$ref":"#/$defs/n"}},"required":["a"],"$defs":{"n":{"type":"object","properties":{"l":{"$ref":"#/$defs/n"}},"required":["l"]}}}`,
		"d07_ref_sibling":      `{"$schema":"http://json-schema.org/draft-07/schema#","definitions":{"A":{"type":"string"}},"type":"object","properties":{"a":{"$ref":"#/definitions/A","type":"integer"}},"required":["a"]}`,
		"not_enum":             `{"type":"object","properties":{"a":{"type":"string","not":{"enum":["lorem ipsum"]}}},"required":["a"]}`,
	}
	for name, schema := range cases {
		out, st := synthesize(uniqueTool(), json.RawMessage(schema))
		if v := origVerdict(schema, out); st.Valid && v != "ok" && v != "skip" {
			t.Errorf("%s: VERIFIED but %s\nout=%s", name, v, out)
		}
	}
	for _, name := range []string{"prefixItems_items", "d07_tuple_items"} {
		if _, st := synthesize(uniqueTool(), json.RawMessage(cases[name])); st.Valid {
			t.Errorf("%s: tuple schemas must skip verification", name)
		}
	}
	for _, name := range []string{"additional_uuid", "allOf_merge_uuid", "anyOf_parent_uuid"} {
		if _, st := synthesize(uniqueTool(), json.RawMessage(cases[name])); !st.Valid {
			t.Errorf("%s: should verify via the format hint", name)
		}
	}
}

// randSchema generates schemas from the checked keywords plus tuple forms and
// generation-only formats (uuid, uri) anywhere, including additionalProperties.
// Every VERIFIED output must validate against the original.
type randSchema struct{ r *rand.Rand }

var (
	rsNums  = []string{"0", "1", "2", "3", "5", "10", "-1", "-0", "0.5", "1e15", "1e-15", "1000000000000000", "1001", "0.1", "1E5", "42.5"}
	rsNames = []string{"a", "b", "c", "enum", "$ref", "type", "required", "properties", "items", "x/y", "~", "%41", "a b", "é", "", "#"}
	rsTypes = []string{"object", "string", "integer", "number", "boolean", "array", "null"}
)

func (g randSchema) num() json.Number { return json.Number(rsNums[g.r.Intn(len(rsNums))]) }

func (g randSchema) val(d int) any {
	switch k := g.r.Intn(8); {
	case k == 0:
		return nil
	case k == 1:
		return g.r.Intn(2) == 0
	case k == 2:
		return g.num()
	case k == 3 || d > 3:
		return strings.Repeat("s", g.r.Intn(6))
	case k <= 5:
		a := make([]any, g.r.Intn(3))
		for i := range a {
			a[i] = g.val(d + 1)
		}
		return a
	default:
		m := map[string]any{}
		for i := g.r.Intn(3); i > 0; i-- {
			m[rsNames[g.r.Intn(len(rsNames))]] = g.val(d + 1)
		}
		return m
	}
}

func (g randSchema) sc(d int, top bool) any {
	r := g.r
	if !top && r.Intn(12) == 0 {
		return r.Intn(2) == 0
	}
	m := map[string]any{}
	switch {
	case top:
		m["type"] = "object"
	case r.Intn(6) == 0:
		m["type"] = []any{rsTypes[r.Intn(7)], rsTypes[r.Intn(7)]}
	case r.Intn(3) != 0:
		m["type"] = rsTypes[r.Intn(7)]
	}
	if d < 5 {
		if top || r.Intn(2) == 0 {
			p := map[string]any{}
			for i := r.Intn(4); i > 0; i-- {
				p[rsNames[r.Intn(len(rsNames))]] = g.sc(d+1, false)
			}
			m["properties"] = p
			if r.Intn(2) == 0 {
				var req []any
				for i := r.Intn(3); i > 0; i-- {
					req = append(req, rsNames[r.Intn(len(rsNames))])
				}
				m["required"] = req
			}
		}
		if r.Intn(4) == 0 {
			m["additionalProperties"] = g.sc(d+1, false)
		}
		switch r.Intn(8) {
		case 0, 1:
			m["items"] = g.sc(d+1, false)
		case 2:
			m["items"] = []any{g.sc(d+1, false), g.sc(d+1, false)}
		case 3:
			m["prefixItems"] = []any{g.sc(d+1, false)}
			if r.Intn(2) == 0 {
				m["items"] = g.sc(d+1, false)
			}
		}
		for _, k := range []string{"anyOf", "oneOf", "allOf"} {
			if r.Intn(8) == 0 {
				l := make([]any, 1+r.Intn(4))
				for i := range l {
					l[i] = g.sc(d+1, false)
				}
				m[k] = l
			}
		}
		if r.Intn(10) == 0 {
			m["not"] = g.sc(d+1, false)
		}
		if r.Intn(8) == 0 {
			refs := []string{"#", "#/$defs/a", "#/$defs/b", "#/properties/a", "#/$defs/zz"}
			m["$ref"] = refs[r.Intn(len(refs))]
		}
	}
	for _, k := range []string{"minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties"} {
		if r.Intn(10) == 0 {
			m[k] = json.Number(fmt.Sprint(r.Intn(5)))
		}
	}
	for _, k := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum"} {
		if r.Intn(12) == 0 {
			m[k] = g.num()
		}
	}
	if r.Intn(8) == 0 {
		l := make([]any, r.Intn(4))
		for i := range l {
			l[i] = g.val(0)
		}
		m["enum"] = l
	}
	if r.Intn(10) == 0 {
		m["const"] = g.val(0)
	}
	if r.Intn(5) == 0 {
		f := []string{"email", "date", "date-time", "uuid", "uri"}
		m["format"] = f[r.Intn(len(f))]
	}
	if top {
		m["$defs"] = map[string]any{"a": g.sc(d+1, false), "b": g.sc(d+1, false)}
	}
	return m
}

func TestSynthArgs_RandomVerifiedOutputsValidateAgainstOriginal(t *testing.T) {
	captureLog(t)
	n, verified, bad := 0, 0, 0
	var worst time.Duration
	for seed := range int64(1000) {
		raw, err := json.Marshal(randSchema{rand.New(rand.NewSource(seed))}.sc(0, true))
		if err != nil {
			continue
		}
		start := time.Now()
		// One tool name: warnOnce remembers at most 1024 names per process.
		out, st := synthesize("random-schema", raw)
		worst = max(worst, time.Since(start))
		n++
		if !json.Valid(out) || !bytes.HasPrefix(out, []byte("{")) {
			t.Fatalf("seed %d: bad output %q", seed, out)
		}
		if !st.Valid {
			continue
		}
		verified++
		if v := origVerdict(string(raw), out); v != "ok" && v != "skip" {
			if bad++; bad <= 5 {
				t.Errorf("seed %d: VERIFIED but %s\nschema=%s\nout=%s", seed, v, raw, out)
			}
		}
	}
	t.Logf("%d random schemas, %d verified, %d verified-but-invalid, slowest %v", n, verified, bad, worst)
	if verified < 30 {
		t.Errorf("only %d of %d verified; generator not exercising the verified path", verified, n)
	}
}

func TestNormalizeGeminiSchema_StringIntegerForms(t *testing.T) {
	norm := NormalizeGeminiSchema(json.RawMessage(`{"type":"OBJECT","required":["a"],"properties":{"a":{"type":"ARRAY","minItems":"+5","maxItems":" 07 ","items":{"type":"STRING"}}}}`))
	if !json.Valid(norm) || strings.Contains(string(norm), "OBJECT") {
		t.Fatalf("not normalized: %s", norm)
	}
	out, st := synthesize(uniqueTool(), norm)
	var m map[string][]any
	if err := json.Unmarshal(out, &m); err != nil || !st.Valid || len(m["a"]) < 5 || len(m["a"]) > 7 {
		t.Errorf("got %s valid=%v", out, st.Valid)
	}
	// Oversized Gemini schemas are left alone, and the fallback still reads
	// their upper-case types.
	big := `{"type":"OBJECT","required":["a"],"properties":{"a":{"type":"INTEGER"},` + strings.TrimPrefix(bigSchema(4000), `{"type":"object","properties":{`)
	if got := NormalizeGeminiSchema(json.RawMessage(big)); string(got) != big {
		t.Error("oversized schema should pass through unchanged")
	}
	captureLog(t)
	if got := string(SynthArgsForTool(uniqueTool(), json.RawMessage(big))); got != `{"a":42}` {
		t.Errorf("fallback over upper-case types: %s", got)
	}
}

func TestWarnOnce_TruncatesClientText(t *testing.T) {
	buf := captureLog(t)
	tool := strings.Repeat("t", 10000) + "\n"
	warnOnce(tool, strings.Repeat("c", 10000)+"\ninjected line")
	line := buf.String()
	if len(line) > 600 || strings.Count(line, "\n") != 1 {
		t.Errorf("warning not truncated/sanitized: %d bytes, %d newlines", len(line), strings.Count(line, "\n"))
	}
}
