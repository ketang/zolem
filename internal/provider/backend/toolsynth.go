package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Limits that bound synthesis work and output size. See docs/admin-mode.md,
// "Tool Calling".
const (
	maxSchemaBytes    = 64 << 10 // raw schema size above which verification is skipped
	maxSchemaNodes    = 2000     // JSON objects in the schema above which verification is skipped
	maxDepth          = 8        // nesting / $ref depth
	maxOutputBytes    = 64 << 10 // synthesized arguments are never larger than this
	maxWholeChecks    = 16       // whole-schema validations per call
	maxLeafCandidates = 8        // candidates tried per leaf
	maxStringBytes    = 1024
	maxArrayItems     = 64

	rootSchemaURL = "mem://zolem/tool-schema.json"
	loremValue    = "lorem ipsum"
)

var formatValues = map[string]string{
	"email":     "user@example.com",
	"date-time": "2024-01-01T00:00:00Z",
	"date":      "2024-01-01",
	"uri":       "https://example.com/",
	"uuid":      "123e4567-e89b-12d3-a456-426614174000",
}

// schemaLoadHook, when non-nil, observes every attempt by the schema compiler
// to load an external resource. Tests use it to prove nothing is loaded.
var schemaLoadHook func(url string)

// rejectLoader refuses every external load so request-supplied schemas can
// never make zolem read files or reach the network.
type rejectLoader struct{}

func (rejectLoader) Load(u string) (any, error) {
	if schemaLoadHook != nil {
		schemaLoadHook(u)
	}
	return nil, fmt.Errorf("external schema loading is disabled: %s", u)
}

var (
	warnedMu    sync.Mutex
	warnedTools = map[string]bool{}
)

func warnOnce(tool string, cause any) {
	warnedMu.Lock()
	defer warnedMu.Unlock()
	if warnedTools[tool] || len(warnedTools) >= 1024 {
		return
	}
	warnedTools[tool] = true
	log.Printf("warn: synthesized tool arguments do not satisfy schema for tool %q: %v", tool, cause)
}

// SynthArgs generates deterministic JSON arguments for a tool's JSON Schema.
// See SynthArgsForTool.
func SynthArgs(schema json.RawMessage) json.RawMessage {
	return SynthArgsForTool("", schema)
}

// SynthArgsForTool generates fake JSON arguments that validate against schema
// (for the supported subset documented in docs/admin-mode.md). tool names the
// tool for the one-time warning logged when the result cannot be made to
// validate. Empty or invalid schemas yield "{}".
func SynthArgsForTool(tool string, schema json.RawMessage) json.RawMessage {
	out, _ := synthesize(tool, schema)
	return out
}

type synthStats struct {
	WholeValidations int
	Valid            bool
}

func synthesize(tool string, schema json.RawMessage) (json.RawMessage, synthStats) {
	empty := json.RawMessage("{}")
	var st synthStats
	if len(bytes.TrimSpace(schema)) == 0 {
		return empty, st
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return empty, st
	}
	root, ok := doc.(map[string]any)
	if !ok {
		return empty, st
	}
	if !typeAllows(root["type"], "object") {
		return empty, st
	}

	if len(schema) > maxSchemaBytes {
		warnOnce(tool, "schema exceeds the 64 KiB verification limit")
		return simpleArgs(doc), st
	}
	if countObjects(doc) > maxSchemaNodes {
		warnOnce(tool, "schema exceeds the 2000-node verification limit")
		return simpleArgs(doc), st
	}

	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(rejectLoader{})
	if err := compiler.AddResource(rootSchemaURL, doc); err != nil {
		warnOnce(tool, err)
		return simpleArgs(doc), st
	}
	if _, err := compiler.Compile(rootSchemaURL); err != nil {
		warnOnce(tool, err)
		return simpleArgs(doc), st
	}

	g := &synth{root: doc, compiler: compiler, compiled: map[string]*jsonschema.Schema{}}
	var last json.RawMessage
	seen := map[string]bool{}
	for attempt := range maxWholeChecks {
		g.rot = attempt
		v, valid := g.node(&sch{m: root, vptrs: []string{""}}, 0)
		out, err := json.Marshal(v)
		if _, isObj := v.(map[string]any); err != nil || !isObj || len(out) > maxOutputBytes {
			out = simpleArgs(doc)
			valid = false
		}
		last = out
		if valid {
			st.Valid = true
			break
		}
		if seen[string(out)] || g.exhausted {
			break
		}
		seen[string(out)] = true
	}
	st.WholeValidations = g.whole
	if !st.Valid {
		warnOnce(tool, g.lastErr)
	}
	return last, st
}

// sch is a schema node being generated: its decoded form, the JSON pointer of
// its position in the document (used to build child pointers), and the
// pointers its value must validate against (more than one for merged allOf
// properties).
type sch struct {
	m     map[string]any
	base  string
	vptrs []string
}

type synth struct {
	root      any
	compiler  *jsonschema.Compiler
	compiled  map[string]*jsonschema.Schema
	rot       int
	whole     int
	exhausted bool
	lastErr   error
	produced  int
}

func (g *synth) compile(ptr string) *jsonschema.Schema {
	if s, ok := g.compiled[ptr]; ok {
		return s
	}
	s, err := g.compiler.Compile(rootSchemaURL + "#" + ptr)
	if err != nil {
		s = nil
	}
	g.compiled[ptr] = s
	return s
}

func (g *synth) valid(ptrs []string, v any) bool {
	for _, p := range ptrs {
		if p == "" {
			if g.whole >= maxWholeChecks {
				g.exhausted = true
				return false
			}
			g.whole++
		}
		s := g.compile(p)
		if s == nil {
			continue
		}
		if err := s.Validate(v); err != nil {
			g.lastErr = err
			return false
		}
	}
	return true
}

func isCombined(m map[string]any) bool {
	return listOf(m["anyOf"]) != nil || listOf(m["oneOf"]) != nil
}

func listOf(v any) []any {
	l, _ := v.([]any)
	if len(l) == 0 {
		return nil
	}
	return l
}

// node generates the value for sc: the first candidate that validates (or the
// rot-th valid one for anyOf/oneOf nodes). The bool reports whether a
// candidate validated.
func (g *synth) node(sc *sch, depth int) (any, bool) {
	if depth > maxDepth {
		return nil, false
	}
	sc, depth = g.resolve(sc, depth)
	if sc == nil || depth > maxDepth {
		return nil, false
	}
	cands := g.candidates(sc, depth)
	if len(cands) > maxLeafCandidates {
		cands = cands[:maxLeafCandidates]
	}
	want := 1
	if isCombined(sc.m) {
		want = g.rot + 1
	}
	var valid []any
	for _, c := range cands {
		if g.valid(sc.vptrs, c) {
			valid = append(valid, c)
			if len(valid) >= want {
				break
			}
		}
	}
	if len(valid) > 0 {
		return valid[len(valid)-1], true
	}
	if len(cands) == 0 {
		return nil, false
	}
	return cands[0], false
}

// resolve follows in-document $ref chains, consuming depth per hop.
func (g *synth) resolve(sc *sch, depth int) (*sch, int) {
	for {
		ref, _ := sc.m["$ref"].(string)
		if ref == "" {
			return sc, depth
		}
		depth++
		if depth > maxDepth {
			return nil, depth
		}
		target, ptr, ok := lookupPointer(g.root, ref)
		if !ok {
			return &sch{m: map[string]any{}, base: sc.base, vptrs: sc.vptrs}, depth
		}
		sc = &sch{m: target, base: ptr, vptrs: sc.vptrs}
	}
}

func lookupPointer(root any, ref string) (map[string]any, string, bool) {
	if !strings.HasPrefix(ref, "#") {
		return nil, "", false
	}
	frag, err := url.PathUnescape(ref[1:])
	if err != nil {
		return nil, "", false
	}
	cur := root
	if frag != "" {
		if !strings.HasPrefix(frag, "/") {
			return nil, "", false
		}
		for _, tok := range strings.Split(frag[1:], "/") {
			tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
			switch c := cur.(type) {
			case map[string]any:
				cur = c[tok]
			case []any:
				i, err := strconv.Atoi(tok)
				if err != nil || i < 0 || i >= len(c) {
					return nil, "", false
				}
				cur = c[i]
			default:
				return nil, "", false
			}
		}
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, "", false
	}
	return m, escapePointer(frag), true
}

// escapePointer percent-encodes a decoded JSON pointer for use in a URL fragment.
func escapePointer(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func childPtr(base string, toks ...string) string {
	for _, t := range toks {
		t = strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1")
		base += "/" + url.PathEscape(t)
	}
	return base
}

func (g *synth) candidates(sc *sch, depth int) []any {
	m := sc.m
	var out []any
	if c, ok := m["const"]; ok {
		out = append(out, c)
	}
	if e, ok := m["enum"].([]any); ok {
		out = append(out, e...)
	}
	if d, ok := m["default"]; ok {
		out = append(out, d)
	}

	if isCombined(m) {
		return append(out, g.branchCandidates(sc, depth)...)
	}

	if contribs, ok := g.objectContribs(sc, depth); ok {
		return append(out, g.objectValue(contribs, depth))
	}

	types := schemaTypes(m)
	if len(types) == 0 {
		return append(out, loremValue)
	}
	for _, t := range types {
		switch t {
		case "string":
			out = append(out, stringValue(m))
		case "integer":
			out = append(out, numberCandidates(m, true)...)
		case "number":
			out = append(out, numberCandidates(m, false)...)
		case "boolean":
			out = append(out, true, false)
		case "null":
			out = append(out, nil)
		case "array":
			out = append(out, g.arrayCandidates(sc, depth)...)
		case "object":
			out = append(out, g.objectValue([]*sch{sc}, depth))
		}
	}
	return out
}

// branchCandidates collects candidates from anyOf/oneOf branches in order,
// non-null branches first. Branches inherit the parent's type when they have none.
func (g *synth) branchCandidates(sc *sch, depth int) []any {
	var nonNull, null []any
	for _, key := range []string{"anyOf", "oneOf"} {
		for i, b := range listOf(sc.m[key]) {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if _, has := bm["type"]; !has && sc.m["type"] != nil {
				cp := make(map[string]any, len(bm)+1)
				for k, v := range bm {
					cp[k] = v
				}
				cp["type"] = sc.m["type"]
				bm = cp
			}
			bsc, d := g.resolve(&sch{m: bm, base: childPtr(sc.base, key, strconv.Itoa(i)), vptrs: sc.vptrs}, depth)
			if bsc == nil || d > maxDepth {
				continue
			}
			cands := g.candidates(bsc, d)
			if len(cands) > 3 {
				cands = cands[:3]
			}
			if isNullType(bsc.m) {
				null = append(null, cands...)
			} else {
				nonNull = append(nonNull, cands...)
			}
		}
	}
	return append(nonNull, null...)
}

func isNullType(m map[string]any) bool {
	t := schemaTypes(m)
	return len(t) == 1 && t[0] == "null"
}

// schemaTypes returns the declared types in order with "null" moved last,
// inferring object/array from properties/items when type is absent.
func schemaTypes(m map[string]any) []string {
	var types []string
	switch t := m["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				types = append(types, s)
			}
		}
	}
	if len(types) == 0 {
		if m["properties"] != nil || m["required"] != nil {
			return []string{"object"}
		}
		if m["items"] != nil {
			return []string{"array"}
		}
		return nil
	}
	var nulls []string
	var rest []string
	for _, s := range types {
		if s == "null" {
			nulls = append(nulls, s)
		} else {
			rest = append(rest, s)
		}
	}
	return append(rest, nulls...)
}

func typeAllows(t any, want string) bool {
	switch v := t.(type) {
	case nil:
		return true
	case string:
		return v == want
	case []any:
		for _, e := range v {
			if e == want {
				return true
			}
		}
		return false
	}
	return true
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	f, ok := toFloat(v)
	if !ok || f < 0 || f > 1e9 {
		return 0, false
	}
	return int(f), true
}

func stringValue(m map[string]any) string {
	s := loremValue
	if f, ok := m["format"].(string); ok {
		if v, ok := formatValues[f]; ok {
			s = v
		}
	}
	if n, ok := toInt(m["minLength"]); ok && len(s) < n {
		pad := n
		if pad > maxStringBytes {
			pad = maxStringBytes
		}
		s += strings.Repeat("a", pad-len(s))
	}
	if n, ok := toInt(m["maxLength"]); ok && len(s) > n {
		s = s[:n]
	}
	if len(s) > maxStringBytes {
		s = s[:maxStringBytes]
	}
	return s
}

func numberCandidates(m map[string]any, integer bool) []any {
	var lo, hi float64
	var hasLo, hasHi, loEx, hiEx bool
	if v, ok := toFloat(m["minimum"]); ok {
		lo, hasLo = v, true
	}
	if v, ok := toFloat(m["exclusiveMinimum"]); ok && (!hasLo || v >= lo) {
		lo, hasLo, loEx = v, true, true
	}
	if v, ok := toFloat(m["maximum"]); ok {
		hi, hasHi = v, true
	}
	if v, ok := toFloat(m["exclusiveMaximum"]); ok && (!hasHi || v <= hi) {
		hi, hasHi, hiEx = v, true, true
	}
	var c []float64
	if integer {
		c = append(c, 42)
		if hasLo {
			v := math.Ceil(lo)
			if loEx && v == lo {
				v++
			}
			c = append(c, v)
		}
		if hasHi {
			v := math.Floor(hi)
			if hiEx && v == hi {
				v--
			}
			c = append(c, v)
		}
	} else {
		c = append(c, 42, 42.5)
		if hasLo && hasHi {
			c = append(c, (lo+hi)/2)
		}
		if hasLo {
			c = append(c, lo+1, lo+0.5)
			if !loEx {
				c = append(c, lo)
			}
		}
		if hasHi {
			c = append(c, hi-1, hi-0.5)
			if !hiEx {
				c = append(c, hi)
			}
		}
	}
	out := make([]any, 0, len(c))
	seen := map[float64]bool{}
	for _, f := range c {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

func (g *synth) arrayCandidates(sc *sch, depth int) []any {
	m := sc.m
	minItems, _ := toInt(m["minItems"])
	n := minItems
	if n < 1 {
		n = 1
	}
	if n > maxArrayItems {
		n = maxArrayItems
	}
	if mx, ok := toInt(m["maxItems"]); ok && n > mx {
		n = mx
	}
	if n == 0 || g.produced > 4*maxOutputBytes {
		return []any{[]any{}}
	}
	items, _ := m["items"].(map[string]any)
	if items == nil {
		items = map[string]any{}
	}
	itemPtr := childPtr(sc.base, "items")
	item, _ := g.node(&sch{m: items, base: itemPtr, vptrs: []string{itemPtr}}, depth+1)
	enc, _ := json.Marshal(item)
	if per := len(enc) + 1; per*n > maxOutputBytes {
		n = maxOutputBytes / per
		if n < 1 {
			n = 1
		}
	}
	g.produced += len(enc) * n
	arr := make([]any, n)
	for i := range arr {
		arr[i] = item
	}
	if minItems == 0 {
		return []any{arr, []any{}}
	}
	return []any{arr}
}

// objectContribs reports whether sc generates as an object, returning the
// schemas whose properties/required are combined: sc itself plus its allOf
// branches when every allOf branch is an object schema.
func (g *synth) objectContribs(sc *sch, depth int) ([]*sch, bool) {
	all := listOf(sc.m["allOf"])
	types := schemaTypes(sc.m)
	if all == nil {
		if len(types) > 0 && types[0] == "object" {
			return []*sch{sc}, true
		}
		return nil, false
	}
	contribs := []*sch{sc}
	for i, b := range all {
		bm, ok := b.(map[string]any)
		if !ok {
			return nil, false
		}
		bsc, d := g.resolve(&sch{m: bm, base: childPtr(sc.base, "allOf", strconv.Itoa(i)), vptrs: sc.vptrs}, depth)
		if bsc == nil || d > maxDepth {
			return nil, false
		}
		bt := schemaTypes(bsc.m)
		if len(bt) == 0 || bt[0] != "object" {
			return nil, false
		}
		contribs = append(contribs, bsc)
	}
	if len(types) > 0 && types[0] != "object" {
		return nil, false
	}
	return contribs, true
}

func (g *synth) objectValue(contribs []*sch, depth int) any {
	props := map[string][]*sch{}
	var required []string
	haveReq := map[string]bool{}
	var order []string
	for _, c := range contribs {
		pm, _ := c.m["properties"].(map[string]any)
		for name, raw := range pm {
			pmap, ok := raw.(map[string]any)
			if !ok {
				pmap = map[string]any{}
				if b, isBool := raw.(bool); isBool && !b {
					pmap = map[string]any{"not": map[string]any{}}
				}
			}
			ptr := childPtr(c.base, "properties", name)
			if _, seen := props[name]; !seen {
				order = append(order, name)
			}
			props[name] = append(props[name], &sch{m: pmap, base: ptr, vptrs: []string{ptr}})
		}
		for _, r := range listOf(c.m["required"]) {
			if s, ok := r.(string); ok && !haveReq[s] {
				haveReq[s] = true
				required = append(required, s)
			}
		}
	}
	keys := required
	optional := false
	if len(keys) == 0 {
		keys = order
		optional = true
	}
	result := make(map[string]any, len(keys))
	for _, k := range keys {
		list := props[k]
		if len(list) == 0 {
			result[k] = loremValue
			continue
		}
		v, ok := g.node(mergeProp(list), depth+1)
		if ok || !optional {
			result[k] = v
		}
	}
	return result
}

// mergeProp combines same-named property schemas from allOf branches. Only
// same-type primitive overlaps are merged; anything else uses the first schema
// and relies on validation against every branch (so it fails and falls back).
func mergeProp(list []*sch) *sch {
	if len(list) == 1 {
		return list[0]
	}
	out := &sch{m: list[0].m, base: list[0].base}
	for _, s := range list {
		out.vptrs = append(out.vptrs, s.vptrs...)
	}
	t0, _ := list[0].m["type"].(string)
	if t0 == "" || t0 == "object" || t0 == "array" {
		return out
	}
	for _, s := range list[1:] {
		if t, _ := s.m["type"].(string); t != t0 {
			return out
		}
	}
	merged := make(map[string]any, len(list[0].m))
	for k, v := range list[0].m {
		merged[k] = v
	}
	for _, s := range list[1:] {
		for _, k := range []string{"minimum", "exclusiveMinimum", "minLength"} {
			mergeBound(merged, s.m, k, true)
		}
		for _, k := range []string{"maximum", "exclusiveMaximum", "maxLength"} {
			mergeBound(merged, s.m, k, false)
		}
		if e, ok := s.m["enum"].([]any); ok {
			if cur, ok := merged["enum"].([]any); ok {
				merged["enum"] = intersectEnum(cur, e)
			} else {
				merged["enum"] = e
			}
		}
		for _, k := range []string{"const", "default", "format"} {
			if _, ok := merged[k]; !ok {
				if v, ok := s.m[k]; ok {
					merged[k] = v
				}
			}
		}
	}
	out.m = merged
	return out
}

func mergeBound(dst, src map[string]any, key string, wantMax bool) {
	sv, ok := toFloat(src[key])
	if !ok {
		return
	}
	dv, ok := toFloat(dst[key])
	if !ok || (wantMax && sv > dv) || (!wantMax && sv < dv) {
		dst[key] = src[key]
	}
}

func intersectEnum(a, b []any) []any {
	keep := map[string]bool{}
	for _, v := range b {
		j, _ := json.Marshal(v)
		keep[string(j)] = true
	}
	out := []any{}
	for _, v := range a {
		j, _ := json.Marshal(v)
		if keep[string(j)] {
			out = append(out, v)
		}
	}
	return out
}

func countObjects(v any) int {
	n := 0
	switch t := v.(type) {
	case map[string]any:
		n = 1
		for _, c := range t {
			n += countObjects(c)
		}
	case []any:
		for _, c := range t {
			n += countObjects(c)
		}
	}
	return n
}

// simpleArgs is the unverified type-walker used when the schema cannot be
// compiled or exceeds the input limits: required properties (all properties
// when required is absent) with constant values by type.
func simpleArgs(schema any) json.RawMessage {
	out, _ := json.Marshal(simpleLeaf(schema, 0, true))
	if len(out) > maxOutputBytes {
		return json.RawMessage("{}")
	}
	return out
}

func simpleLeaf(schema any, depth int, top bool) any {
	m, ok := schema.(map[string]any)
	if !ok {
		if top {
			return map[string]any{}
		}
		return loremValue
	}
	t, _ := m["type"].(string)
	if top && t != "" && t != "object" {
		return map[string]any{}
	}
	if top {
		t = "object"
	}
	switch t {
	case "number", "integer":
		return 42
	case "boolean":
		return true
	case "array":
		return []any{}
	case "object":
		if depth > maxDepth {
			return map[string]any{}
		}
		props, _ := m["properties"].(map[string]any)
		var keys []string
		for _, r := range listOf(m["required"]) {
			if s, ok := r.(string); ok {
				keys = append(keys, s)
			}
		}
		if len(keys) == 0 {
			for k := range props {
				keys = append(keys, k)
			}
		}
		result := make(map[string]any, len(keys))
		for _, k := range keys {
			p, ok := props[k]
			if !ok {
				result[k] = loremValue
				continue
			}
			result[k] = simpleLeaf(p, depth+1, false)
		}
		return result
	default:
		return loremValue
	}
}

// NormalizeGeminiSchema converts a Gemini (OpenAPI-subset) function-declaration
// schema into JSON Schema: lower-case type names, nullable:true as a type
// array including "null", and string-encoded integer bounds (minItems etc.,
// which the Gemini API serializes as strings) as numbers. Input that does not
// parse is returned unchanged.
func NormalizeGeminiSchema(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return raw
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return raw
	}
	normalizeGeminiNode(m)
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

func normalizeGeminiNode(m map[string]any) {
	for _, k := range []string{"minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties"} {
		if s, ok := m[k].(string); ok {
			if _, err := strconv.ParseInt(s, 10, 64); err == nil {
				m[k] = json.Number(s)
			}
		}
	}
	if t, ok := m["type"].(string); ok {
		m["type"] = strings.ToLower(t)
	}
	if nullable, _ := m["nullable"].(bool); nullable {
		switch t := m["type"].(type) {
		case string:
			m["type"] = []any{t, "null"}
		case nil:
		}
		if e, ok := m["enum"].([]any); ok {
			m["enum"] = append(e, nil)
		}
	}
	delete(m, "nullable")
	for _, k := range []string{"properties", "$defs", "definitions"} {
		if pm, ok := m[k].(map[string]any); ok {
			for _, v := range pm {
				if c, ok := v.(map[string]any); ok {
					normalizeGeminiNode(c)
				}
			}
		}
	}
	for _, k := range []string{"items", "additionalProperties", "not"} {
		if c, ok := m[k].(map[string]any); ok {
			normalizeGeminiNode(c)
		}
	}
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		for _, v := range listOf(m[k]) {
			if c, ok := v.(map[string]any); ok {
				normalizeGeminiNode(c)
			}
		}
	}
}
