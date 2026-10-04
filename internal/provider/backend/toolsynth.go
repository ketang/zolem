package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Limits that bound synthesis work and output size. See docs/admin-mode.md,
// "Tool Calling".
const (
	maxSchemaBytes    = 64 << 10 // raw schema size above which verification is skipped
	maxDepth          = 8        // nesting / $ref depth
	maxOutputBytes    = 64 << 10 // synthesized arguments are never larger than this
	maxWholeChecks    = 16       // whole-schema validations per call
	maxLeafCandidates = 8        // candidates tried per leaf
	maxStringBytes    = 1024
	maxArrayItems     = 64
	maxWork           = 20000 // generation steps (nodes, candidates, validations) per call
	maxWorkTime       = 250 * time.Millisecond
	maxNormalizeDepth = 64
	maxRecursion      = 2     // times one $ref target may appear on a single expansion path
	maxValidationWork = 50000 // schema nodes x instance nodes per validation
	maxInstanceNodes  = 2000  // JSON values generated per attempt
	maxConcurrent     = 8     // concurrent verified syntheses
	semWait           = 100 * time.Millisecond
	hardTimeout       = maxWorkTime + 100*time.Millisecond

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

// warnOnce logs, once per tool name, why synthesized arguments are not
// verified. Tool names and causes can carry client-supplied text (property
// names, $refs), so both are truncated and quoted.
func warnOnce(tool string, cause any) {
	tool = truncateText(tool, maxWarnTool)
	warnedMu.Lock()
	defer warnedMu.Unlock()
	if warnedTools[tool] || len(warnedTools) >= 1024 {
		return
	}
	warnedTools[tool] = true
	log.Printf("warn: synthesized tool arguments do not satisfy schema for tool %q: %q", tool, truncateText(fmt.Sprint(cause), maxWarnCause))
}

const (
	maxWarnTool  = 128
	maxWarnCause = 256
)

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
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
	Work             int
	Aborted          bool
	Valid            bool
}

func synthesize(tool string, schema json.RawMessage) (out json.RawMessage, st synthStats) {
	empty := json.RawMessage("{}")
	// Synthesis runs on the request goroutine; a panic here (outside the
	// verification goroutine, which recovers on its own) must not take down
	// the server.
	defer func() {
		if r := recover(); r != nil {
			warnOnce(tool, fmt.Sprint("internal error: ", r))
			out, st = empty, synthStats{}
		}
	}()
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
	// Build the verification tree from scratch (allow-listed keywords,
	// clamped numbers, inlined refs, capped size). Generation also runs on
	// this tree, so hostile numbers never reach the generator either.
	vb := &verifyBuilder{doc: doc}
	expanded := vb.build(root)
	if expanded == nil {
		warnOnce(tool, vb.err)
		return simpleArgs(doc), st
	}

	select {
	case synthSem <- struct{}{}:
	case <-time.After(semWait):
		warnOnce(tool, "verification capacity exhausted")
		return simpleArgs(doc), st
	}
	g := &synth{root: expanded, hints: vb.hints, compiled: map[string]*jsonschema.Schema{}, deadline: time.Now().Add(maxWorkTime),
		// Bound validation work: (schema nodes) x (instance nodes) <= maxValidationWork.
		instCap: min(maxInstanceNodes, max(64, maxValidationWork/max(vb.nodes, 1)))}
	type result struct {
		out   json.RawMessage
		st    synthStats
		cause any
	}
	done := make(chan result, 1)
	go func() {
		// The slot is released only when the goroutine really finishes, so
		// abandoned (timed-out) work still counts against concurrency.
		defer func() { <-synthSem }()
		defer func() {
			if r := recover(); r != nil {
				done <- result{cause: fmt.Sprint("internal error: ", r)}
			}
		}()
		out, st, cause := g.run(expanded, doc)
		done <- result{out, st, cause}
	}()
	select {
	case r := <-done:
		if r.out == nil {
			warnOnce(tool, r.cause)
			return simpleArgs(doc), r.st
		}
		return r.out, r.st
	case <-time.After(hardTimeout):
		g.aborted.Store(true)
		warnOnce(tool, "synthesis timed out")
		return simpleArgs(doc), synthStats{Aborted: true}
	}
}

// run compiles the expanded schema and generates verified arguments. A nil
// output means the caller must use the fallback; cause says why.
func (g *synth) run(expanded map[string]any, orig any) (json.RawMessage, synthStats, any) {
	var st synthStats
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(rejectLoader{})
	if err := compiler.AddResource(rootSchemaURL, expanded); err != nil {
		return nil, st, err
	}
	if _, err := compiler.Compile(rootSchemaURL); err != nil {
		return nil, st, err
	}
	g.compiler = compiler

	var last json.RawMessage
	seen := map[string]bool{}
	for attempt := range maxWholeChecks {
		g.rot = attempt
		g.inst = 0
		v, valid := g.node(&sch{m: expanded, vptrs: []string{""}}, 0)
		st.WholeValidations = g.whole
		st.Work = g.work
		if g.aborted.Load() {
			st.Aborted = true
			return nil, st, "generation work limit exceeded"
		}
		out, err := json.Marshal(v)
		if _, isObj := v.(map[string]any); err != nil || !isObj || len(out) > maxOutputBytes {
			valid = false
		}
		if valid {
			st.Valid = true
			return out, st, nil
		}
		last = out
		if seen[string(last)] || g.exhausted {
			break
		}
		seen[string(last)] = true
	}
	// Never emit arguments known to violate the schema (or oversized ones).
	if g.lastErr == nil {
		g.lastErr = fmt.Errorf("no valid candidate")
	}
	return nil, st, g.lastErr
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
	hints     map[string]string
	compiler  *jsonschema.Compiler
	compiled  map[string]*jsonschema.Schema
	rot       int
	whole     int
	exhausted bool
	work      int
	aborted   atomic.Bool
	inst      int
	instCap   int
	deadline  time.Time
	lastErr   error
	produced  int
}

// tick charges one unit of work and reports whether generation may continue.
// Once the per-call budget or deadline is exceeded generation is aborted and
// the caller falls back to the unverified type-walker.
func (g *synth) tick() bool {
	if g.aborted.Load() {
		return false
	}
	g.work++
	if g.work > maxWork || (time.Now().After(g.deadline)) {
		g.aborted.Store(true)
		return false
	}
	return true
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
	if !g.tick() {
		return false
	}
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
	if depth > maxDepth || g.exhausted || !g.tick() {
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

// resolve is the identity: $refs were inlined by expander before generation.
func (g *synth) resolve(sc *sch, depth int) (*sch, int) { return sc, depth }

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
	if !g.tick() {
		return nil
	}
	m := sc.m
	var out []any
	if c, ok := m["const"]; ok {
		out = append(out, c)
	}
	if e, ok := m["enum"].([]any); ok {
		out = append(out, e...)
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
			out = append(out, stringValue(m, g.hintFor(sc)))
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
// non-null branches first. Branches inherit the parent's type when they have
// none. Branches are resolved up front (cheap) but their candidates are
// generated lazily, stopping once maxLeafCandidates are collected.
func (g *synth) branchCandidates(sc *sch, depth int) []any {
	var nonNull, null []*sch
	var depths = map[*sch]int{}
	for _, key := range []string{"anyOf", "oneOf"} {
		for i, b := range listOf(sc.m[key]) {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			if _, has := bm["type"]; !has && sc.m["type"] != nil {
				cp := make(map[string]any, len(bm)+1)
				maps.Copy(cp, bm)
				cp["type"] = sc.m["type"]
				bm = cp
			}
			bsc, d := g.resolve(&sch{m: bm, base: childPtr(sc.base, key, strconv.Itoa(i)), vptrs: sc.vptrs}, depth)
			if bsc == nil || d > maxDepth {
				continue
			}
			depths[bsc] = d
			if isNullType(bsc.m) {
				null = append(null, bsc)
			} else {
				nonNull = append(nonNull, bsc)
			}
		}
	}
	var out []any
	for _, bsc := range append(nonNull, null...) {
		if len(out) >= maxLeafCandidates || g.aborted.Load() {
			break
		}
		cands := g.candidates(bsc, depths[bsc])
		if len(cands) > 3 {
			cands = cands[:3]
		}
		out = append(out, cands...)
	}
	return out
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
		return strings.EqualFold(v, want)
	case []any:
		for _, e := range v {
			if s, _ := e.(string); strings.EqualFold(s, want) {
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

// hintFor returns the generation-only format hint for sc: its own position
// first, then the positions it is validated against (merged allOf
// properties, the parent of an anyOf/oneOf branch).
func (g *synth) hintFor(sc *sch) string {
	if h := g.hints[sc.base]; h != "" {
		return h
	}
	for _, p := range sc.vptrs {
		if h := g.hints[p]; h != "" {
			return h
		}
	}
	return ""
}

// stringValue generates a string for m; hint is a format the verification
// tree does not assert but the client schema asked for.
func stringValue(m map[string]any, hint string) string {
	s := loremValue
	f, _ := m["format"].(string)
	if f == "" {
		f = hint
	}
	if v, ok := formatValues[f]; ok {
		s = v
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
		n = max(maxOutputBytes/per, 1)
	}
	per := countInstance(item)
	if allowed := (g.instCap - g.inst) / per; allowed < n {
		if allowed < 1 {
			return []any{[]any{}}
		}
		n = allowed
	}
	g.inst += n * per
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
		for _, name := range slices.Sorted(maps.Keys(pm)) {
			raw := pm[name]
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
			// A required name without a property schema is governed by
			// additionalProperties (whose format hint must apply too).
			if ap := additionalSchema(contribs); ap != nil {
				result[k], _ = g.node(ap, depth+1)
			} else {
				result[k] = loremValue
			}
			continue
		}
		v, ok := g.node(mergeProp(list), depth+1)
		if ok || !optional {
			result[k] = v
		}
	}
	return result
}

// additionalSchema returns the additionalProperties schema of the first
// contributor that has one as an object (validated against every
// contributor's), or nil.
func additionalSchema(contribs []*sch) *sch {
	var out *sch
	for _, c := range contribs {
		ap, ok := c.m["additionalProperties"].(map[string]any)
		if !ok {
			continue
		}
		ptr := childPtr(c.base, "additionalProperties")
		if out == nil {
			out = &sch{m: ap, base: ptr}
		}
		out.vptrs = append(out.vptrs, ptr)
	}
	return out
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
		for _, k := range []string{"const", "format"} {
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
	// a and b come from the verification tree: at most maxEnumValues values
	// of at most maxValueBytes each.
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

// simpleArgs is the unverified type-walker used when the schema cannot be
// verified (compile failure, input limits, unsupported ref forms, timeouts):
// required properties (all properties when required is absent) with constant
// values by type. It follows in-document $refs a few hops, takes the first
// enum/const value (when it passes the verification clamp) and the first
// non-null anyOf/oneOf branch.
//
// It runs on the request goroutine with no timeout or semaphore, so its cost
// is bounded on its own: every schema visited, required entry, property key,
// anyOf/oneOf branch, type-array entry and $ref byte is charged against
// maxSimpleWork, each object emits at most maxSimpleKeys keys, and the
// output is abandoned ("{}") as soon as its estimated size exceeds
// maxOutputBytes.
func simpleArgs(schema any) json.RawMessage {
	w := &simpleWalker{root: schema, work: maxSimpleWork}
	v := w.leaf(schema, 0, 0, true)
	if w.outBytes > maxOutputBytes {
		return json.RawMessage("{}")
	}
	out, err := json.Marshal(v)
	if err != nil || len(out) > maxOutputBytes {
		return json.RawMessage("{}")
	}
	return out
}

const (
	maxSimpleWork  = 20000
	maxSimpleKeys  = 256
	maxSimpleHops  = 3
	maxRefBytes    = 1 << 10
	simpleRefShare = 64 // $ref bytes per work unit
)

type simpleWalker struct {
	root     any
	work     int
	outBytes int
}

// charge spends n work units and reports whether the walk may continue.
func (w *simpleWalker) charge(n int) bool {
	w.work -= n
	return w.work >= 0 && w.outBytes <= maxOutputBytes
}

func (w *simpleWalker) emit(n int) { w.outBytes += n }

func (w *simpleWalker) leaf(schema any, depth, hops int, top bool) any {
	m, ok := schema.(map[string]any)
	if !ok {
		if top {
			w.emit(2)
			return map[string]any{}
		}
		w.emit(len(loremValue) + 2)
		return loremValue
	}
	if !w.charge(1) {
		w.emit(len(loremValue) + 2)
		return loremValue
	}
	if ref, _ := m["$ref"].(string); ref != "" && hops < maxSimpleHops && len(ref) <= maxRefBytes && w.charge(1+len(ref)/simpleRefShare) {
		if target, _, ok := lookupPointer(w.root, ref); ok {
			return w.leaf(target, depth, hops+1, top)
		}
	}
	// enum/const values are used only when they pass the verification
	// clamp, so huge or unparseable numbers never reach the client.
	// Each attempt is charged as if the value were maxValueBytes long.
	if !top {
		if c, ok := m["const"]; ok && w.charge(maxValueBytes/simpleRefShare) {
			size := 0
			if cv, ok := cleanValue(c, 0, &size); ok {
				w.emit(size)
				return cv
			}
		}
		if e := listOf(m["enum"]); e != nil && w.charge(maxValueBytes/simpleRefShare) {
			size := 0
			if cv, ok := cleanValue(e[0], 0, &size); ok {
				w.emit(size)
				return cv
			}
		}
		for _, key := range []string{"anyOf", "oneOf"} {
			for _, b := range listOf(m[key]) {
				if !w.charge(1) {
					break
				}
				if bm, ok := b.(map[string]any); ok && bm["type"] != "null" {
					return w.leaf(b, depth+1, hops, false)
				}
			}
		}
	}
	t, _ := m["type"].(string)
	if ts, ok := m["type"].([]any); ok {
		for _, e := range ts {
			if !w.charge(1) {
				break
			}
			if s, _ := e.(string); s != "null" && s != "" {
				t = s
				break
			}
		}
	}
	t = strings.ToLower(t)
	if t == "" && (m["properties"] != nil || m["required"] != nil) {
		t = "object"
	}
	if top && t != "" && t != "object" {
		w.emit(2)
		return map[string]any{}
	}
	if top {
		t = "object"
	}
	switch t {
	case "number", "integer":
		w.emit(3)
		return 42
	case "boolean":
		w.emit(5)
		return true
	case "array":
		w.emit(3)
		return []any{}
	case "object":
		w.emit(2)
		if depth > maxDepth {
			return map[string]any{}
		}
		props, _ := m["properties"].(map[string]any)
		var keys []string
		for _, r := range listOf(m["required"]) {
			if len(keys) >= maxSimpleKeys || !w.charge(1) {
				break
			}
			if s, ok := r.(string); ok {
				keys = append(keys, s)
			}
		}
		if len(keys) == 0 && len(props) > 0 && w.charge(len(props)) {
			keys = slices.Sorted(maps.Keys(props))
			keys = keys[:min(len(keys), maxSimpleKeys)]
		}
		result := make(map[string]any, len(keys))
		for _, k := range keys {
			if !w.charge(1) {
				break
			}
			w.emit(len(k) + 4)
			p, ok := props[k]
			if !ok {
				w.emit(len(loremValue) + 2)
				result[k] = loremValue
				continue
			}
			result[k] = w.leaf(p, depth+1, hops, false)
		}
		return result
	default:
		w.emit(len(loremValue) + 2)
		return loremValue
	}
}

// NormalizeGeminiSchema converts a Gemini (OpenAPI-subset) function-declaration
// schema into JSON Schema: lower-case type names, nullable:true as a type
// array including "null", and string-encoded integer bounds (minItems etc.,
// which the Gemini API serializes as strings) as numbers. Input that does not
// parse is returned unchanged.
//
// Like synthesis, it runs on the request goroutine: schemas over
// maxSchemaBytes are returned unchanged (synthesis then uses its fallback,
// which accepts upper-case types), the walk visits each node of the decoded
// tree once (at most maxNormalizeDepth deep), and a panic returns raw.
func NormalizeGeminiSchema(raw json.RawMessage) (out json.RawMessage) {
	if len(bytes.TrimSpace(raw)) == 0 || len(raw) > maxSchemaBytes {
		return raw
	}
	defer func() {
		if r := recover(); r != nil {
			out = raw
		}
	}()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return raw
	}
	normalizeGeminiNode(m, 0)
	enc, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return enc
}

func normalizeGeminiNode(m map[string]any, depth int) {
	if depth > maxNormalizeDepth {
		return
	}
	for _, k := range []string{"minItems", "maxItems", "minLength", "maxLength", "minProperties", "maxProperties"} {
		if s, ok := m[k].(string); ok {
			// Re-encode: "+5" or "05" parse but are not JSON numbers.
			if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				m[k] = json.Number(strconv.FormatInt(n, 10))
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
			m["enum"] = append(slices.Clone(e), nil)
		}
	}
	delete(m, "nullable")
	for _, k := range []string{"properties", "$defs", "definitions"} {
		if pm, ok := m[k].(map[string]any); ok {
			for _, v := range pm {
				if c, ok := v.(map[string]any); ok {
					normalizeGeminiNode(c, depth+1)
				}
			}
		}
	}
	for _, k := range []string{"items", "additionalProperties", "not"} {
		if c, ok := m[k].(map[string]any); ok {
			normalizeGeminiNode(c, depth+1)
		}
	}
	for _, k := range []string{"anyOf", "oneOf", "allOf"} {
		for _, v := range listOf(m[k]) {
			if c, ok := v.(map[string]any); ok {
				normalizeGeminiNode(c, depth+1)
			}
		}
	}
}

var synthSem = make(chan struct{}, maxConcurrent)

// Verification tree.
//
// jsonschema/v6 cannot interrupt Compile or Validate, so the schema it sees
// must be cheap by construction. It is never the client's schema (nor a
// stripped copy of it): verifyBuilder builds a NEW tree that contains only
// the allow-listed keywords below, with every number re-encoded canonically
// and every structural dimension capped. Cost is therefore bounded by
//   - at most maxVerifyNodes schema nodes, maxVerifyDepth deep, anyOf/oneOf/
//     allOf fan-out <= maxFanOut, and maxVerifyBytes of keys and data;
//   - numbers that are finite, |x| <= 1e15 (and 0 or >= 1e-15), at most
//     maxNumberLiteral characters, and exactly representable by their
//     canonical encoding (so no huge exponents reach big.Rat);
//   - enum/const values of at most maxValueBytes each;
//   - formats whose checkers reject long input early or scan it linearly;
//   - at most maxInstanceNodes generated JSON values per validated instance.
//
// Building the tree is itself charged against maxVerifyBytes: every key of
// every visited schema object (kept or dropped), every JSON pointer
// materialized for a node, every $ref string (at most maxRefBytes), and all
// emitted keys and data. Total builder work is therefore O(maxVerifyBytes)
// regardless of request size or how often $refs revisit the same object.
//
// Checked keywords: type, properties, required, additionalProperties, items
// (single schema), min/maxItems, min/maxLength, min/maxProperties, enum,
// const, minimum, maximum, exclusiveMinimum, exclusiveMaximum, anyOf, oneOf,
// allOf, not, and format for verifiedFormats. In-document $refs are inlined.
//
// Every other keyword (pattern, multipleOf, uniqueItems, contains, dependent*,
// propertyNames, if/then/else, unevaluated*, other formats, ...) is omitted.
// An omitted keyword is simply not checked, so a VERIFIED output can still
// violate it (for example a pattern); omission never changes what a kept
// keyword means, with these exceptions:
//   - prefixItems and array-form (tuple) items change which elements a kept
//     items covers, so schemas using them skip verification instead;
//   - patternProperties narrows what a kept additionalProperties covers, so
//     omitting it makes the tree stricter (it can reject a valid output and
//     fall back, never accept an invalid one).
//
// A value failing a clamp or an exceeded cap skips verification entirely.
const (
	maxVerifyDepth   = 32
	maxVerifyNodes   = 500
	maxVerifyBytes   = 64 << 10
	maxFanOut        = 16
	maxRequired      = 256
	maxEnumValues    = 256
	maxValueBytes    = 4 << 10
	maxNumberLiteral = 40
	maxAbsNumber     = 1e15
	minAbsNumber     = 1e-15
)

// verifiedFormats are the formats asserted during verification: each is
// produced by the generator, and its jsonschema/v6 checker rejects long
// input in constant time (email: > 254 bytes) or parses a fixed-size prefix
// (date, date-time). Other formats in formatValues (uri, uuid) guide
// generation only; regex, idn-email, uri-reference, iri*, uri-template and
// json-pointer variants are never asserted.
var verifiedFormats = map[string]bool{"email": true, "date": true, "date-time": true}

var primitiveTypes = map[string]bool{"null": true, "boolean": true, "object": true, "array": true, "number": true, "integer": true, "string": true}

var countKeys = map[string]bool{"minItems": true, "maxItems": true, "minLength": true, "maxLength": true, "minProperties": true, "maxProperties": true}

var boundKeys = map[string]bool{"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true}

// unsupportedRefKeys cannot be inlined soundly; schemas using them (in a
// reachable schema position) skip verification.
var unsupportedRefKeys = []string{"$dynamicRef", "$recursiveRef", "$anchor", "$dynamicAnchor", "$recursiveAnchor"}

// verifyBuilder builds the verification tree described above from a decoded
// client schema, walking schema positions only (property names such as
// "enum" or "$ref" are just names) and inlining in-document $refs. hints
// records, by JSON pointer in the new tree, formats the generator should
// produce but verification does not assert.
type verifyBuilder struct {
	doc   any
	nodes int
	bytes int
	path  map[string]int
	hints map[string]string
	err   error
	// negated is true under an odd number of enclosing "not"s.
	negated bool
}

func (b *verifyBuilder) fail(format string, args ...any) any {
	if b.err == nil {
		b.err = fmt.Errorf(format, args...)
	}
	return nil
}

func (b *verifyBuilder) charge(n int) bool {
	b.bytes += n
	if b.bytes > maxVerifyBytes {
		b.fail("schema exceeds the %d KiB verification limit", maxVerifyBytes>>10)
		return false
	}
	return true
}

// build returns the verification tree for root, or nil with b.err set.
func (b *verifyBuilder) build(root map[string]any) map[string]any {
	m, _ := b.schema(root, "", 0, 0).(map[string]any)
	if b.err != nil || m == nil {
		return nil
	}
	return m
}

func (b *verifyBuilder) schema(v any, ptr string, depth, hops int) any {
	if b.err != nil {
		return nil
	}
	if depth > maxVerifyDepth {
		return b.fail("schema nests deeper than %d levels", maxVerifyDepth)
	}
	switch t := v.(type) {
	case bool:
		b.charge(6)
		return t
	case map[string]any:
		b.nodes++
		if b.nodes > maxVerifyNodes {
			return b.fail("schema exceeds the %d-node verification limit", maxVerifyNodes)
		}
		// Every node materializes its JSON pointer (built from the whole
		// ancestor key path), so pointer bytes count against the cap too.
		if !b.charge(len(ptr) + 1) {
			return nil
		}
		for _, k := range unsupportedRefKeys {
			if _, has := t[k]; has {
				return b.fail("schema uses %s, which is not verified", k)
			}
		}
		if _, has := t["$id"]; has && (ptr != "" || hops > 0) {
			return b.fail("schema uses a nested $id, which is not verified")
		}
		if _, has := t["$ref"]; has {
			return b.ref(t, ptr, depth, hops)
		}
		return b.object(t, ptr, depth, hops)
	}
	return b.fail("schema position holds a non-schema value")
}

// ref inlines an in-document $ref. Siblings are merged when no emitted
// keyword clashes; a clash skips verification. Recursion through the same
// ref beyond maxRecursion (or maxDepth hops) is cut (see below).
func (b *verifyBuilder) ref(m map[string]any, ptr string, depth, hops int) any {
	rs, _ := m["$ref"].(string)
	if !strings.HasPrefix(rs, "#") {
		return b.fail("schema has a $ref that is not an in-document pointer")
	}
	if len(rs) > maxRefBytes {
		return b.fail("schema has a $ref longer than %d bytes", maxRefBytes)
	}
	// lookupPointer unescapes and splits the ref; the sibling copy below
	// touches every key of m.
	if !b.charge(len(rs) + 2*len(m)) {
		return nil
	}
	target, _, ok := lookupPointer(b.doc, rs)
	if !ok {
		return b.fail("schema has an unresolvable $ref")
	}
	var exp map[string]any
	if hops+1 > maxDepth || b.path[rs] >= maxRecursion {
		// Cut recursion with the schema that makes verification STRICTER
		// here: false (no value) normally, true under an odd number of nots.
		// Recursive structures then verify through their null/empty
		// alternatives, and the output validates against the uncut schema.
		exp = map[string]any{}
		if !b.negated {
			exp["not"] = map[string]any{}
		}
	} else {
		if b.path == nil {
			b.path = map[string]int{}
		}
		b.path[rs]++
		e := b.schema(target, ptr, depth, hops+1)
		b.path[rs]--
		if bv, isBool := e.(bool); isBool {
			exp = map[string]any{}
			if !bv {
				exp["not"] = map[string]any{}
			}
		} else {
			exp, _ = e.(map[string]any)
		}
	}
	rest := make(map[string]any, len(m))
	for k, v := range m {
		if k != "$ref" {
			rest[k] = v
		}
	}
	sib := b.object(rest, ptr, depth, hops)
	if b.err != nil {
		return nil
	}
	for k, v := range sib {
		if _, clash := exp[k]; clash {
			return b.fail("schema has a $ref whose sibling %q clashes with its target", k)
		}
		exp[k] = v
	}
	return exp
}

// object emits the allow-listed keywords of schema object m.
func (b *verifyBuilder) object(m map[string]any, ptr string, depth, hops int) map[string]any {
	// Every key is iterated (and sorted) even when dropped: charge them all
	// before touching them, so repeated visits through $refs stay bounded.
	if !b.charge(2 * len(m)) {
		return nil
	}
	// prefixItems and array-form items change which elements a kept "items"
	// covers; dropping them would make the tree check a different schema.
	if _, has := m["prefixItems"]; has {
		b.fail("schema uses prefixItems, which is not verified")
		return nil
	}
	if _, tuple := m["items"].([]any); tuple {
		b.fail("schema uses array-form items, which is not verified")
		return nil
	}
	out := map[string]any{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if b.err != nil {
			return nil
		}
		val := m[k]
		switch {
		case k == "type":
			t, ok := cleanType(val)
			if !ok {
				b.fail("schema has an unsupported type %v", val)
				return nil
			}
			out[k] = t
		case k == "properties":
			pm, ok := val.(map[string]any)
			if !ok {
				b.fail("schema has non-object properties")
				return nil
			}
			if !b.charge(4 * len(pm)) {
				return nil
			}
			cp := make(map[string]any, len(pm))
			for _, name := range slices.Sorted(maps.Keys(pm)) {
				if !b.charge(len(name)) {
					return nil
				}
				cp[name] = b.schema(pm[name], childPtr(ptr, "properties", name), depth+1, hops)
			}
			out[k] = cp
		case k == "required":
			l, ok := val.([]any)
			if !ok || len(l) > maxRequired {
				b.fail("schema has an unsupported required list")
				return nil
			}
			var req []any
			seen := map[string]bool{}
			for _, r := range l {
				s, ok := r.(string)
				if !ok {
					b.fail("schema has a non-string required entry")
					return nil
				}
				if !seen[s] {
					seen[s] = true
					req = append(req, s)
					b.charge(len(s) + 3)
				}
			}
			out[k] = req
		case k == "additionalProperties":
			out[k] = b.schema(val, childPtr(ptr, k), depth+1, hops)
		case k == "not":
			b.negated = !b.negated
			out[k] = b.schema(val, childPtr(ptr, k), depth+1, hops)
			b.negated = !b.negated
		case k == "items":
			out[k] = b.schema(val, childPtr(ptr, k), depth+1, hops)
		case k == "anyOf" || k == "oneOf" || k == "allOf":
			l, ok := val.([]any)
			if !ok || len(l) == 0 || len(l) > maxFanOut {
				b.fail("schema has %s with more than %d entries", k, maxFanOut)
				return nil
			}
			cp := make([]any, len(l))
			for i, sub := range l {
				cp[i] = b.schema(sub, childPtr(ptr, k, strconv.Itoa(i)), depth+1, hops)
			}
			out[k] = cp
		case countKeys[k]:
			n, ok := cleanCount(val)
			if !ok {
				b.fail("schema has an out-of-range %s", k)
				return nil
			}
			b.charge(len(k) + len(n) + 4)
			out[k] = n
		case boundKeys[k]:
			n, ok := cleanNumber(val)
			if !ok {
				b.fail("schema has an out-of-range %s", k)
				return nil
			}
			b.charge(len(k) + len(n) + 4)
			out[k] = n
		case k == "enum":
			l, ok := val.([]any)
			if !ok || len(l) > maxEnumValues {
				b.fail("schema has an unsupported enum")
				return nil
			}
			cp := make([]any, len(l))
			for i, e := range l {
				if cp[i], ok = b.value(e); !ok {
					return nil
				}
			}
			out[k] = cp
		case k == "const":
			c, ok := b.value(val)
			if !ok {
				return nil
			}
			out[k] = c
		case k == "format":
			f, _ := val.(string)
			if verifiedFormats[f] {
				out[k] = f
				b.charge(len(f) + 12)
			} else if _, gen := formatValues[f]; gen {
				if b.hints == nil {
					b.hints = map[string]string{}
				}
				b.hints[ptr] = f
			}
		}
		// Everything else is dropped.
	}
	if b.err != nil {
		return nil
	}
	return out
}

// value returns a canonical copy of an enum/const value: at most
// maxValueBytes, nested at most maxVerifyDepth, every number cleaned.
func (b *verifyBuilder) value(v any) (any, bool) {
	size := 0
	out, ok := cleanValue(v, 0, &size)
	if !ok {
		b.fail("schema has an enum/const value that is too large or out of range")
		return nil, false
	}
	return out, b.charge(size)
}

func cleanValue(v any, depth int, size *int) (any, bool) {
	if depth > maxVerifyDepth || *size > maxValueBytes {
		return nil, false
	}
	switch t := v.(type) {
	case nil, bool:
		*size += 5
		return t, true
	case string:
		*size += len(t) + 2
		return t, *size <= maxValueBytes
	case json.Number, float64:
		n, ok := cleanNumber(t)
		*size += len(n)
		return n, ok
	case []any:
		if len(t) > maxValueBytes-*size {
			return nil, false // every element costs at least one byte
		}
		out := make([]any, len(t))
		for i, e := range t {
			var ok bool
			if out[i], ok = cleanValue(e, depth+1, size); !ok {
				return nil, false
			}
			*size++
		}
		return out, true
	case map[string]any:
		if 3*len(t) > maxValueBytes-*size {
			return nil, false // every member costs at least three bytes
		}
		out := make(map[string]any, len(t))
		for k, e := range t {
			*size += len(k) + 3
			c, ok := cleanValue(e, depth+1, size)
			if !ok {
				return nil, false
			}
			out[k] = c
		}
		return out, true
	}
	return nil, false
}

// cleanNumber re-encodes a JSON number canonically, refusing anything that is
// not finite, has |x| > 1e15, is nonzero with |x| < 1e-15, has a literal
// longer than maxNumberLiteral, or whose canonical encoding is not exactly
// equal to the literal. The rational comparison is cheap because the literal
// is short and its magnitude bounded, so its exponent is small.
func cleanNumber(v any) (json.Number, bool) {
	var lit string
	switch n := v.(type) {
	case json.Number:
		lit = string(n)
	case float64:
		lit = strconv.FormatFloat(n, 'g', -1, 64)
	default:
		return "", false
	}
	if len(lit) > maxNumberLiteral {
		return "", false
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > maxAbsNumber {
		return "", false
	}
	if f == 0 {
		// ParseFloat underflows 1e-999999 to 0 without error: accept only
		// literals whose mantissa is all zeros.
		mant, _, _ := strings.Cut(strings.ToLower(lit), "e")
		if strings.Trim(mant, "+-0.") != "" {
			return "", false
		}
		return "0", true
	}
	if math.Abs(f) < minAbsNumber {
		return "", false
	}
	canon := strconv.FormatFloat(f, 'g', -1, 64)
	if canon != lit {
		a, okA := new(big.Rat).SetString(lit)
		c, okC := new(big.Rat).SetString(canon)
		if !okA || !okC || a.Cmp(c) != 0 {
			return "", false
		}
	}
	return json.Number(canon), true
}

// cleanCount accepts a non-negative integer <= 1e15 and re-encodes it in
// plain decimal.
func cleanCount(v any) (json.Number, bool) {
	n, ok := cleanNumber(v)
	if !ok {
		return "", false
	}
	f, _ := strconv.ParseFloat(string(n), 64)
	if f < 0 || f != math.Trunc(f) {
		return "", false
	}
	return json.Number(strconv.FormatInt(int64(f), 10)), true
}

// cleanType accepts a primitive type name or a non-empty array of distinct
// primitive type names.
func cleanType(v any) (any, bool) {
	switch t := v.(type) {
	case string:
		return t, primitiveTypes[t]
	case []any:
		if len(t) == 0 || len(t) > len(primitiveTypes) {
			return nil, false
		}
		seen := map[string]bool{}
		out := make([]any, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok || !primitiveTypes[s] {
				return nil, false
			}
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
		return out, true
	}
	return nil, false
}

func countInstance(v any) int {
	n := 1
	switch t := v.(type) {
	case map[string]any:
		for _, c := range t {
			n += countInstance(c)
		}
	case []any:
		for _, c := range t {
			n += countInstance(c)
		}
	}
	return n
}
