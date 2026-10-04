package backend

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// regexBomb is a valid but expensive-to-compile regular expression of about
// n bytes.
func regexBomb(n int) string {
	unit := `(?:(?:a{10}){10}){10}|`
	return strings.Repeat(unit, n/len(unit)) + "b"
}

func repeatJoin(s string, n int) string {
	return strings.TrimSuffix(strings.Repeat(s+",", n), ",")
}

// round5Shapes are the schemas that broke the previous (deny-list) verifier
// in the fifth review round, plus cheaper amplifiers found alongside them.
func round5Shapes() map[string]string {
	m := map[string]string{}
	big := regexBomb(33 << 10)
	// (1) format:"regex" with the pattern stored in enum/const/default, plus
	// 100 allOf {"format":"regex"} branches.
	m["regex_in_enum"] = `{"type":"object","required":["x"],"properties":{"x":{"type":"string","format":"regex","enum":["` + big +
		`"],"allOf":[` + repeatJoin(`{"format":"regex"}`, 100) + `]}}}`
	m["regex_in_const"] = `{"type":"object","required":["x"],"properties":{"x":{"format":"regex","const":"` + big + `"}}}`
	m["regex_in_default"] = `{"type":"object","required":["x"],"properties":{"x":{"type":"string","format":"regex","default":"` + big + `"}}}`
	// The same within every cap: a 4000-byte regex in enum, 16 branches.
	m["regex_in_enum_within_caps"] = `{"type":"object","required":["x"],"properties":{"x":{"type":"string","format":"regex","enum":["` + regexBomb(4000) +
		`"],"allOf":[` + repeatJoin(`{"format":"regex"}`, 16) + `]}}}`
	// (2) huge / tiny numbers that jsonschema/v6 turns into enormous big.Rats.
	var props []string
	for i := 0; i < 400; i++ {
		props = append(props, fmt.Sprintf(`"p%d":{"type":"number","multipleOf":1e999999,"minimum":1e-999999}`, i))
	}
	m["multipleOf_huge_400"] = `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`
	props = props[:0]
	for i := 0; i < 400; i++ {
		props = append(props, fmt.Sprintf(`"p%d":{"type":"number","multipleOf":1e-999999}`, i))
	}
	m["multipleOf_tiny_400"] = `{"type":"object","properties":{` + strings.Join(props, ",") + `}}`
	m["minimum_huge"] = `{"type":"object","required":["n"],"properties":{"n":{"type":"number","minimum":1e999999}}}`
	m["maximum_tiny"] = `{"type":"object","required":["n"],"properties":{"n":{"type":"number","maximum":-1e-999999}}}`
	m["enum_huge_numbers"] = `{"type":"object","required":["n"],"properties":{"n":{"enum":[1e999999,-1e999999,1e-999999]}}}`
	m["minItems_huge_exponent"] = `{"type":"object","required":["a"],"properties":{"a":{"type":"array","minItems":1e999999,"maxLength":1e-999999}}}`
	// (3) deep nesting.
	m["deep_properties_480"] = strings.Repeat(`{"type":"object","required":["a"],"properties":{"a":`, 480) + `{"type":"string"}` + strings.Repeat(`}}`, 480)
	m["deep_not_480"] = `{"type":"object","required":["a"],"properties":{"a":` + strings.Repeat(`{"not":`, 480) + `{}` + strings.Repeat(`}`, 480) + `}}`
	// (4) cheaper amplifiers: expensive formats over long strings.
	long := strings.Repeat("a", 4000)
	for _, f := range []string{"idn-email", "uri-reference", "iri-reference", "uri-template", "iri", "json-pointer", "relative-json-pointer", "hostname", "uri", "uuid"} {
		m["format_"+f] = `{"type":"object","required":["s"],"properties":{"s":{"type":"string","format":"` + f + `","minLength":1000,"enum":["` + long + `"],"allOf":[` +
			repeatJoin(`{"format":"`+f+`"}`, 16) + `]}}}`
	}
	return m
}

// worstRound5 are the three shapes the concurrency test runs together.
var worstRound5 = []string{"regex_in_enum", "multipleOf_huge_400", "deep_properties_480"}

func allBoundedShapes() map[string]string {
	m := round5Shapes()
	for k, v := range adversarialShapes() {
		m[k] = v
	}
	for _, refs := range []int{0, 2, 8} {
		m[fmt.Sprintf("pattern_bomb_refs%d", refs)] = patternBomb(refs)
	}
	return m
}

func goroutinesBackTo(n int, d time.Duration) bool {
	end := time.Now().Add(d)
	for {
		if runtime.NumGoroutine() <= n {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestSynthArgs_Round5ShapesAreBounded(t *testing.T) {
	captureLog(t)
	shapes := allBoundedShapes()
	names := make([]string, 0, len(shapes))
	for k := range shapes {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		schema := shapes[name]
		t.Run(name, func(t *testing.T) {
			if !slotsFreeWithin(time.Second) {
				t.Fatal("slots busy before start")
			}
			runtime.GC()
			base := runtime.NumGoroutine()
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			before := ms.TotalAlloc
			start := time.Now()
			out, st := synthesize(uniqueTool(), json.RawMessage(schema))
			el := time.Since(start)
			runtime.ReadMemStats(&ms)
			alloc := ms.TotalAlloc - before
			freed := slotsFreeWithin(100 * time.Millisecond)
			t.Logf("%-28s %4d KiB schema: %8v, %4d MB allocated, heap %4d MB, valid=%v", name, len(schema)>>10, el.Round(time.Microsecond), alloc>>20, ms.HeapAlloc>>20, st.Valid)
			if el > time.Second {
				t.Errorf("took %v, want <1s", el)
			}
			if alloc > 200<<20 {
				t.Errorf("allocated %d MB, want <200 MB", alloc>>20)
			}
			if !freed {
				t.Error("verification slot still held 100ms after return")
			}
			if !goroutinesBackTo(base, 200*time.Millisecond) {
				t.Errorf("goroutines %d, baseline %d", runtime.NumGoroutine(), base)
			}
			if !json.Valid(out) || len(out) > maxOutputBytes {
				t.Errorf("bad output (%d bytes)", len(out))
			}
		})
	}
}

// Shapes whose only hostile part is a dropped keyword are still verified.
func TestSynthArgs_DroppedKeywordsStillVerify(t *testing.T) {
	shapes := round5Shapes()
	for _, name := range []string{"multipleOf_tiny_400", "regex_in_enum_within_caps", "format_uri-reference"} {
		out, st := synthesize(uniqueTool(), json.RawMessage(shapes[name]))
		if !st.Valid {
			t.Errorf("%s: not verified: %.200s", name, out)
		}
	}
}

func TestSynthArgs_ConcurrentWorstShapesThenNormalCallVerifies(t *testing.T) {
	captureLog(t)
	shapes := round5Shapes()
	runtime.GC()
	base := runtime.NumGoroutine()
	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < 8; i++ {
		for _, name := range worstRound5 {
			wg.Add(1)
			go func(schema string) {
				defer wg.Done()
				synthesize(uniqueTool(), json.RawMessage(schema))
			}(shapes[name])
		}
	}
	wg.Wait()
	t.Logf("24 concurrent worst-shape calls took %v", time.Since(start))
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("concurrent adversarial calls took %v", el)
	}
	start = time.Now()
	out, st := synthesize(uniqueTool(), json.RawMessage(`{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`))
	if el := time.Since(start); !st.Valid || el > 500*time.Millisecond {
		t.Errorf("normal call after adversarial load: valid=%v in %v (%s)", st.Valid, el, out)
	}
	if !goroutinesBackTo(base, 200*time.Millisecond) {
		t.Errorf("goroutines %d, baseline %d", runtime.NumGoroutine(), base)
	}
}

func TestSynthArgs_HostileNumbersInGeneration(t *testing.T) {
	captureLog(t)
	nums := []string{"1e999999", "-1e999999", "1e-999999", "-1e-999999", "1e308", "123456789012345678901234567890", "0.0000000000000000000000001"}
	for _, n := range nums {
		for _, kw := range []string{"minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"} {
			schema := fmt.Sprintf(`{"type":"object","required":["a","s","n","o"],"properties":{"a":{"type":"array","items":{"type":"integer"},"%[1]s":%[2]s},"s":{"type":"string","%[1]s":%[2]s},"n":{"type":"number","%[1]s":%[2]s,"enum":[%[2]s]},"o":{"type":"object","%[1]s":%[2]s,"const":{"k":%[2]s}}}}`, kw, n)
			start := time.Now()
			out, _ := synthesize(uniqueTool(), json.RawMessage(schema))
			if el := time.Since(start); el > 500*time.Millisecond || !json.Valid(out) || len(out) > 4096 {
				t.Errorf("%s=%s: %v, %d bytes", kw, n, el, len(out))
			}
			// The Gemini normalizer re-encodes the same numbers.
			if norm := NormalizeGeminiSchema(json.RawMessage(strings.ToUpper(schema[:9]) + schema[9:])); !json.Valid(norm) {
				t.Errorf("normalize %s=%s produced invalid JSON", kw, n)
			}
		}
	}
	if f, ok := toFloat(json.Number("1e999999")); ok {
		t.Errorf("toFloat(1e999999) = %v, true", f)
	}
	if _, ok := toInt(json.Number("1e-999999")); !ok {
		t.Error("toInt(1e-999999) should be 0")
	}
}

// realisticSchemas are tool schemas as emitted by common SDKs. They must be
// verified, and the output must validate against the ORIGINAL schema.
var realisticSchemas = []struct{ name, schema string }{
	{"pydantic_v2", `{"$defs":{"Address":{"properties":{"street":{"title":"Street","type":"string"},"zip":{"anyOf":[{"type":"string","pattern":"^[0-9]{5}$"},{"type":"null"}],"default":null,"title":"Zip"}},"required":["street"],"title":"Address","type":"object"},"Color":{"enum":["red","green"],"title":"Color","type":"string"}},` +
		`"properties":{"name":{"maxLength":50,"minLength":1,"title":"Name","type":"string"},"age":{"exclusiveMinimum":0,"title":"Age","type":"integer"},"email":{"format":"email","title":"Email","type":"string"},"id":{"format":"uuid","title":"Id","type":"string"},` +
		`"site":{"format":"uri","maxLength":2083,"minLength":1,"type":"string"},"tags":{"items":{"type":"string"},"maxItems":10,"title":"Tags","type":"array"},"color":{"$ref":"#/$defs/Color"},"addr":{"$ref":"#/$defs/Address"},"when":{"format":"date-time","title":"When","type":"string"},` +
		`"score":{"anyOf":[{"maximum":1.0,"minimum":0.0,"type":"number"},{"type":"null"}],"default":null,"title":"Score"}},"required":["name","age","email","id","site","tags","color","addr","when","score"],"title":"User","type":"object"}`},
	{"zod", `{"type":"object","properties":{"q":{"type":"string","minLength":1},"limit":{"type":"integer","minimum":1,"maximum":100,"default":10},"mode":{"type":"string","enum":["fast","slow"]},` +
		`"filters":{"type":"array","items":{"type":"object","properties":{"k":{"type":"string"},"v":{"type":["string","number","boolean"]}},"required":["k","v"],"additionalProperties":false},"minItems":1}},` +
		`"required":["q","limit","mode","filters"],"additionalProperties":false,"$schema":"http://json-schema.org/draft-07/schema#"}`},
	{"openai_strict", `{"type":"object","properties":{"location":{"type":"string","description":"City, e.g. Paris"},"unit":{"type":["string","null"],"enum":["c","f",null]},"days":{"type":"integer"},` +
		`"opts":{"type":"object","properties":{"hourly":{"type":"boolean"}},"required":["hourly"],"additionalProperties":false}},"required":["location","unit","days","opts"],"additionalProperties":false}`},
	{"anthropic", `{"type":"object","properties":{"ticker":{"type":"string","description":"Ticker symbol"},"range":{"type":"string","enum":["1d","5d","1mo"],"description":"Range"},"limit":{"type":"integer","minimum":1,"maximum":500}},"required":["ticker","range"]}`},
	{"binary_tree", `{"type":"object","required":["t"],"properties":{"t":{"$ref":"#/$defs/n"}},"$defs":{"n":{"type":"object","required":["v","l","r"],"properties":{"v":{"type":"integer"},"l":{"anyOf":[{"$ref":"#/$defs/n"},{"type":"null"}]},"r":{"anyOf":[{"$ref":"#/$defs/n"},{"type":"null"}]}}}}}`},
	{"kids_next", `{"type":"object","required":["t"],"properties":{"t":{"$ref":"#/$defs/n"}},"$defs":{"n":{"type":"object","required":["v","kids","next"],"properties":{"v":{"type":"string"},"kids":{"type":"array","items":{"$ref":"#/$defs/n"}},"next":{"anyOf":[{"$ref":"#/$defs/n"},{"type":"null"}]}}}}}`},
}

func TestSynthArgs_RealisticSchemasVerified(t *testing.T) {
	for _, tc := range realisticSchemas {
		t.Run(tc.name, func(t *testing.T) {
			out, st := synthesize(uniqueTool(), json.RawMessage(tc.schema))
			if !st.Valid {
				t.Fatalf("not verified: %s", out)
			}
			if err := oracle(t, []byte(tc.schema), out); err != nil {
				t.Fatalf("%s violates original schema: %v", out, err)
			}
		})
	}
	gem := NormalizeGeminiSchema(json.RawMessage(`{"type":"OBJECT","properties":{"city":{"type":"STRING"},"days":{"type":"INTEGER","nullable":true},"tags":{"type":"ARRAY","items":{"type":"STRING","enum":["a","b"]},"minItems":"1"}},"required":["city","days","tags"]}`))
	out, st := synthesize(uniqueTool(), gem)
	if !st.Valid {
		t.Fatalf("gemini not verified: %s", out)
	}
	if err := oracle(t, gem, out); err != nil {
		t.Fatalf("gemini %s: %v", out, err)
	}
}

// allowedVerifyKeys is the allow-list, restated independently of the
// implementation.
var allowedVerifyKeys = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"minItems": true, "maxItems": true, "minLength": true, "maxLength": true, "minProperties": true, "maxProperties": true,
	"enum": true, "const": true, "minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true,
	"anyOf": true, "oneOf": true, "allOf": true, "not": true, "format": true,
}

// checkVerifyTree walks a verification tree in schema positions and reports
// the first key outside the allow-list or number outside the clamp.
func checkVerifyTree(v any, depth int, nodes *int) error {
	if depth > maxVerifyDepth+1 {
		return fmt.Errorf("depth %d", depth)
	}
	if _, ok := v.(bool); ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("schema position holds %T", v)
	}
	if *nodes++; *nodes > maxVerifyNodes {
		return fmt.Errorf("more than %d nodes", maxVerifyNodes)
	}
	for k, val := range m {
		if !allowedVerifyKeys[k] {
			return fmt.Errorf("keyword %q emitted", k)
		}
		var err error
		switch k {
		case "properties":
			for _, sub := range val.(map[string]any) {
				if err = checkVerifyTree(sub, depth+1, nodes); err != nil {
					break
				}
			}
		case "items", "additionalProperties", "not":
			err = checkVerifyTree(val, depth+1, nodes)
		case "anyOf", "oneOf", "allOf":
			l := val.([]any)
			if len(l) > maxFanOut {
				return fmt.Errorf("%s fan-out %d", k, len(l))
			}
			for _, sub := range l {
				if err = checkVerifyTree(sub, depth+1, nodes); err != nil {
					break
				}
			}
		case "format":
			if !verifiedFormats[val.(string)] {
				err = fmt.Errorf("format %q emitted", val)
			}
		case "enum", "const":
			err = checkData(val)
		case "type", "required":
		default:
			err = checkNumber(val)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

func checkNumber(v any) error {
	n, ok := v.(json.Number)
	if !ok {
		return fmt.Errorf("number is %T", v)
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsInf(f, 0) || math.Abs(f) > 1e15 || (f != 0 && math.Abs(f) < 1e-15) || len(n) > 25 {
		return fmt.Errorf("number %s not clamped", n)
	}
	if strconv.FormatFloat(f, 'g', -1, 64) != string(n) && strconv.FormatInt(int64(f), 10) != string(n) {
		return fmt.Errorf("number %s not canonical", n)
	}
	return nil
}

func checkData(v any) error {
	switch t := v.(type) {
	case json.Number, float64:
		return checkNumber(t)
	case string:
		if len(t) > maxValueBytes {
			return fmt.Errorf("value of %d bytes", len(t))
		}
	case []any:
		for _, e := range t {
			if err := checkData(e); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, e := range t {
			if err := checkData(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// vocabulary is (most of) the JSON Schema 2020-12 + draft-07 vocabulary.
var vocabulary = []string{
	"type", "properties", "required", "additionalProperties", "items", "minItems", "maxItems", "minLength", "maxLength",
	"minProperties", "maxProperties", "enum", "const", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
	"anyOf", "oneOf", "allOf", "not", "format", "pattern", "patternProperties", "multipleOf", "uniqueItems", "contains",
	"minContains", "maxContains", "dependentRequired", "dependentSchemas", "dependencies", "propertyNames", "if", "then", "else",
	"prefixItems", "additionalItems", "unevaluatedItems", "unevaluatedProperties", "$ref", "$dynamicRef", "$anchor",
	"$dynamicAnchor", "$id", "$schema", "$defs", "definitions", "$comment", "title", "description", "default", "examples",
	"deprecated", "readOnly", "writeOnly", "contentEncoding", "contentMediaType", "contentSchema", "nullable", "$vocabulary",
}

var hostileNumbers = []string{"1e999999", "-1e999999", "1e-999999", "1e308", "5e-324", "0.1", "3", "-7", "1e15", "1e16", "1.5", "0", "123456789012345678901234567890", "0.10000000000000001"}

var formatNames = []string{"regex", "email", "idn-email", "date", "date-time", "time", "uri", "uri-reference", "iri", "iri-reference", "uri-template", "uuid", "json-pointer", "relative-json-pointer", "hostname", "ipv4", "duration", "bogus"}

func randomValue(r *rand.Rand, depth int) string {
	switch r.Intn(7) {
	case 0:
		return hostileNumbers[r.Intn(len(hostileNumbers))]
	case 1:
		return strconv.Quote(strings.Repeat("x", r.Intn(3)*3000))
	case 2:
		return `null`
	case 3:
		return `true`
	case 4:
		if depth < 3 {
			return `[` + randomValue(r, depth+1) + `,` + randomValue(r, depth+1) + `]`
		}
	case 5:
		if depth < 3 {
			return `{"$ref":"#","k":` + randomValue(r, depth+1) + `}`
		}
	}
	return `"v"`
}

func randomSchema(r *rand.Rand, depth int) string {
	if depth > 5 || r.Intn(5) == 0 {
		return []string{`{}`, `true`, `false`, `{"type":"string"}`}[r.Intn(4)]
	}
	n := 1 + r.Intn(5)
	parts := []string{}
	for i := 0; i < n; i++ {
		k := vocabulary[r.Intn(len(vocabulary))]
		var v string
		switch k {
		case "type":
			v = []string{`"object"`, `"string"`, `["integer","null"]`, `"OBJECT"`, `"file"`, `[]`}[r.Intn(6)]
		case "properties", "patternProperties", "dependentSchemas", "$defs", "definitions":
			v = `{"a":` + randomSchema(r, depth+1) + `,"enum":` + randomSchema(r, depth+1) + `}`
		case "required":
			v = `["a","enum"]`
		case "items", "additionalProperties", "not", "if", "then", "else", "contains", "propertyNames", "unevaluatedItems", "unevaluatedProperties", "additionalItems", "contentSchema":
			if r.Intn(4) == 0 {
				v = `[` + randomSchema(r, depth+1) + `]`
			} else {
				v = randomSchema(r, depth+1)
			}
		case "anyOf", "oneOf", "allOf", "prefixItems":
			var l []string
			width := 1 + r.Intn(3)
			if depth == 0 && r.Intn(4) == 0 {
				width = 15 + r.Intn(4) // straddle the fan-out cap
			}
			for j := 0; j < width; j++ {
				l = append(l, randomSchema(r, depth+1))
			}
			v = `[` + strings.Join(l, ",") + `]`
		case "enum", "examples":
			v = `[` + randomValue(r, 0) + `,` + randomValue(r, 0) + `]`
		case "const", "default":
			v = randomValue(r, 0)
		case "format":
			v = strconv.Quote(formatNames[r.Intn(len(formatNames))])
		case "pattern":
			v = strconv.Quote(regexBomb(r.Intn(3) * 1000))
		case "$ref":
			v = []string{`"#"`, `"#/properties/a"`, `"#/$defs/a"`, `"http://x/y"`, `"#anchor"`}[r.Intn(5)]
		default:
			v = randomValue(r, 0)
		}
		parts = append(parts, strconv.Quote(k)+":"+v)
	}
	return `{` + strings.Join(parts, ",") + `}`
}

func TestVerifyBuilder_RandomVocabularyEmitsOnlyAllowList(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	built := 0
	for i := 0; i < 3000; i++ {
		schema := `{"type":"object","properties":{"a":` + randomSchema(r, 0) + `,"b":` + randomSchema(r, 0) + `}}`
		if err := checkBuiltSchema(schema); err != nil {
			t.Fatalf("%v\nschema: %.2000s", err, schema)
		}
		d := parseSchema(t, schema)
		if (&verifyBuilder{doc: d}).build(d) != nil {
			built++
		}
	}
	t.Logf("%d of 3000 random schemas produced a verification tree", built)
	if built < 100 {
		t.Errorf("only %d random schemas were verifiable; generator too hostile to be a useful check", built)
	}
}

// checkBuiltSchema builds the verification tree for schema (if any) and
// checks the allow-list invariant on it.
func checkBuiltSchema(schema string) error {
	var doc any
	dec := json.NewDecoder(strings.NewReader(schema))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return nil
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	b := &verifyBuilder{doc: doc}
	tree := b.build(root)
	if tree == nil {
		return nil
	}
	nodes := 0
	if err := checkVerifyTree(tree, 0, &nodes); err != nil {
		return err
	}
	if enc, _ := json.Marshal(tree); len(enc) > maxVerifyBytes+maxVerifyBytes/4 {
		return fmt.Errorf("tree encodes to %d bytes", len(enc))
	}
	return nil
}
