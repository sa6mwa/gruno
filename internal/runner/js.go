package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"pkt.systems/pslog"
)

func executeTests(ctx context.Context, p parsedFile, resp *http.Response, duration time.Duration, exp *expander, logger pslog.Base, prelude string, iter iterationInfo) (CaseResult, error) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return CaseResult{}, err
	}

	vm := goja.New()
	var consoleLogs []string
	registerConsole(vm, &consoleLogs, logger)

	registerEnv(vm, exp)
	registerProcessEnv(vm, exp)
	registerBru(vm, exp, iter)
	resObj := newResponseObject(vm, resp, duration, bodyBytes)
	vm.Set("res", resObj)
	vm.Set("expect", expectFactory(vm))
	runPrelude(vm, prelude)
	// Normalize common fields so JS string helpers (match, etc.) are present.
	_, _ = vm.RunString(`
		if (typeof Object.prototype.match !== 'function') {
			Object.defineProperty(Object.prototype, 'match', {
				value: function(re) { return String(this).match(re); },
				enumerable: false
			});
		}
		if (res && res.body && res.body.message && typeof res.body.message.match !== 'function') {
			const str = String(res.body.message);
			res.body.message = str;
			res.body.message.match = function(re) { return String(str).match(re); };
		}
	`)
	if p.Scripts.PostResponse != "" {
		runPostScript(vm, p.Scripts.PostResponse, resObj)
	}
	runScript(vm, p.Scripts.PreRequest)

	tests := make([]jsTest, 0)
	vm.Set("test", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.NewGoError(fmt.Errorf("test(name, fn) requires 2 args")))
		}
		name := call.Arguments[0].String()
		fn, ok := goja.AssertFunction(call.Arguments[1])
		if !ok {
			panic(vm.NewGoError(fmt.Errorf("second arg must be function")))
		}
		tests = append(tests, jsTest{name: name, fn: fn})
		return goja.Undefined()
	})

	if _, err := vm.RunString(p.TestsRaw); err != nil {
		return CaseResult{}, err
	}

	// run assert block first
	result := CaseResult{Passed: true, Console: consoleLogs}
	for _, ar := range p.Assert {
		if err := evalAssert(vm, resObj, ar); err != nil {
			result.Passed = false
			result.Failures = append(result.Failures, AssertionFailure{
				Name:    ar.Left,
				Message: withHTTPContext(err.Error(), resp.StatusCode, bodyBytes),
			})
		}
	}
	for _, t := range tests {
		_, err := t.fn(goja.Undefined())
		if err != nil {
			result.Passed = false
			result.Failures = append(result.Failures, AssertionFailure{
				Name:    t.name,
				Message: withHTTPContext(err.Error(), resp.StatusCode, bodyBytes),
			})
		}
	}
	return result, nil
}

type jsTest struct {
	name string
	fn   goja.Callable
}

type iterationInfo struct {
	index int
	total int
	data  map[string]any
	exp   *expander
}

func registerConsole(vm *goja.Runtime, logs *[]string, logger pslog.Base) {
	console := vm.NewObject()
	logFn := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for i, arg := range call.Arguments {
			parts[i] = arg.String()
		}
		line := strings.Join(parts, " ")
		*logs = append(*logs, line)
		if logger != nil {
			logger.Debug("js", "msg", line)
		}
		return goja.Undefined()
	}
	console.Set("log", logFn)
	vm.Set("console", console)
}

func registerEnv(vm *goja.Runtime, exp *expander) {
	vm.Set("env", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		if v, ok := exp.get(key); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})
}

func registerProcessEnv(vm *goja.Runtime, exp *expander) {
	envObj := vm.NewObject()
	setKeys := map[string]struct{}{}
	if exp != nil {
		for k, v := range exp.vars {
			envObj.Set(k, v)
			setKeys[k] = struct{}{}
		}
	}
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			if _, exists := setKeys[parts[0]]; !exists {
				envObj.Set(parts[0], parts[1])
			}
		}
	}
	proc := vm.NewObject()
	proc.Set("env", envObj)
	vm.Set("process", proc)
}

func registerBru(vm *goja.Runtime, exp *expander, iter iterationInfo) {
	bru := vm.NewObject()
	bru.Set("setVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		val := call.Arguments[1].String()
		if exp != nil {
			exp.set(key, val)
		}
		if proc := vm.Get("process"); proc != nil {
			if procObj := proc.ToObject(vm); procObj != nil {
				if envVal := procObj.Get("env"); envVal != nil {
					if envObj := envVal.ToObject(vm); envObj != nil {
						envObj.Set(key, val)
					}
				}
			}
		}
		return goja.Undefined()
	})
	bru.Set("setEnvVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		val := call.Arguments[1].String()
		if exp != nil {
			exp.set(key, val)
		}
		_ = os.Setenv(key, val)
		if proc := vm.Get("process"); proc != nil {
			if procObj := proc.ToObject(vm); procObj != nil {
				if envVal := procObj.Get("env"); envVal != nil {
					if envObj := envVal.ToObject(vm); envObj != nil {
						envObj.Set(key, val)
					}
				}
			}
		}
		return goja.Undefined()
	})
	bru.Set("getVar", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		if iter.data != nil {
			if v, ok := iter.data[key]; ok {
				if jsVal, err := toJSValue(vm, v); err == nil {
					return jsVal
				}
				return vm.ToValue(v)
			}
		}
		if v, ok := exp.get(key); ok {
			return vm.ToValue(v)
		}
		return goja.Undefined()
	})

	// runner metadata (iteration info)
	total := iter.total
	if total == 0 {
		total = 1
	}
	runnerObj := vm.NewObject()
	runnerObj.Set("iterationIndex", iter.index)
	runnerObj.Set("totalIterations", total)
	runnerObj.Set("iterationData", newIterationDataObject(vm, exp, iter))
	bru.Set("runner", runnerObj)
	vm.Set("bru", bru)
}

func newIterationDataObject(vm *goja.Runtime, exp *expander, iter iterationInfo) *goja.Object {
	data := iter.data
	if data == nil {
		data = map[string]any{}
	}
	obj := vm.NewObject()
	obj.Set("has", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		_, ok := data[key]
		return vm.ToValue(ok)
	})
	obj.Set("get", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		val, ok := data[key]
		if !ok {
			return goja.Undefined()
		}
		if jsVal, err := toJSValue(vm, val); err == nil {
			return jsVal
		}
		return vm.ToValue(val)
	})
	obj.Set("getAll", func(goja.FunctionCall) goja.Value {
		if jsVal, err := toJSValue(vm, data); err == nil {
			return jsVal
		}
		return vm.ToValue(data)
	})
	obj.Set("stringify", func(goja.FunctionCall) goja.Value {
		if b, err := json.Marshal(data); err == nil {
			return vm.ToValue(string(b))
		}
		return vm.ToValue("{}")
	})
	obj.Set("unset", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		delete(data, key)
		if exp != nil && exp.vars != nil {
			delete(exp.vars, key)
		}
		return goja.Undefined()
	})
	obj.Set("set", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			return goja.Undefined()
		}
		key := call.Arguments[0].String()
		val := call.Arguments[1].Export()
		data[key] = val
		if exp != nil {
			exp.set(key, fmt.Sprint(val))
		}
		return goja.Undefined()
	})
	return obj
}

func newResponseObject(vm *goja.Runtime, resp *http.Response, duration time.Duration, body []byte) *goja.Object {
	obj := vm.NewObject()
	obj.Set("status", resp.StatusCode)
	obj.Set("durationMs", duration.Milliseconds())
	obj.Set("getResponseTime", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(duration.Milliseconds())
	})

	headers := vm.NewObject()
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			lower := strings.ToLower(k)
			val := vals[0]
			headers.Set(lower, val)
			headers.Set(k, val) // keep original casing too
		}
	}
	obj.Set("headers", headers)

	textVal := string(body)
	obj.Set("text", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(textVal)
	})
	obj.Set("json", func(call goja.FunctionCall) goja.Value {
		var target any
		if err := json.Unmarshal(body, &target); err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(target)
	})

	// Provide res.body convenience like Bruno does. Prefer a real JS object
	// (JSON.parse) so string methods like .match work as expected.
	if len(body) > 0 {
		var target any
		if err := json.Unmarshal(body, &target); err == nil {
			if jsVal, err := toJSValue(vm, target); err == nil {
				obj.Set("body", jsVal)
			} else {
				obj.Set("body", vm.ToValue(target))
			}
		} else {
			obj.Set("body", textVal)
		}
	}
	return obj
}

// toJSValue marshals a Go value to JSON and re-parses it inside goja, ensuring
// native JS strings/arrays/objects (so methods like .match exist).
func toJSValue(vm *goja.Runtime, v any) (goja.Value, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	jsonObj := vm.Get("JSON").ToObject(vm)
	parseFn, ok := goja.AssertFunction(jsonObj.Get("parse"))
	if !ok {
		return nil, fmt.Errorf("JSON.parse missing")
	}
	return parseFn(jsonObj, vm.ToValue(string(b)))
}

func runPrelude(vm *goja.Runtime, code string) {
	if code == "" {
		return
	}
	_, _ = vm.RunString(code)
}

func runScript(vm *goja.Runtime, code string) {
	if strings.TrimSpace(code) == "" {
		return
	}
	_, _ = vm.RunString(code)
}

func runPostScript(vm *goja.Runtime, code string, resObj *goja.Object) {
	if strings.TrimSpace(code) == "" {
		return
	}
	vm.Set("res", resObj)
	_, _ = vm.RunString(code)
}

// withHTTPContext appends status/body snippets to aid debugging when tests fail.
func withHTTPContext(msg string, status int, body []byte) string {
	const maxBody = 256
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > maxBody {
		snippet = snippet[:maxBody] + "…"
	}
	return fmt.Sprintf("%s (status=%d, body=%q)", msg, status, snippet)
}

func evalAssert(vm *goja.Runtime, res *goja.Object, ar assertRule) error {
	if ar.Op != "eq" {
		return fmt.Errorf("unsupported op %s", ar.Op)
	}
	val := getPathValue(vm, res, ar.Left)
	want := literalToValue(vm, ar.Right)
	if !val.StrictEquals(want) {
		return fmt.Errorf("expected %v to equal %v", val, want)
	}
	return nil
}

func getPathValue(vm *goja.Runtime, obj *goja.Object, path string) goja.Value {
	parts := strings.Split(path, ".")
	if len(parts) > 0 && parts[0] == "res" {
		parts = parts[1:]
	}
	var current goja.Value = obj
	for _, p := range parts {
		// Support bracket notation: headers['x-trace-id'] or body["trace_id"]
		if strings.Contains(p, "['") || strings.Contains(p, "[\"") {
			before, _, ok := strings.Cut(p, "[")
			prop := p
			if ok {
				// property before bracket (e.g., headers['x'])
				prop = before
			}
			if prop != "" {
				if o, ok := current.(*goja.Object); ok {
					current = o.Get(prop)
				} else {
					return goja.Undefined()
				}
			}
			key := p
			if strings.Contains(p, "['") {
				key = p[strings.Index(p, "['")+2:]
				key = key[:strings.Index(key, "']")]
			} else if strings.Contains(p, "[\"") {
				key = p[strings.Index(p, "[\"")+2:]
				key = key[:strings.Index(key, "\"]")]
			}
			if o, ok := current.(*goja.Object); ok {
				current = o.Get(key)
				continue
			}
			return goja.Undefined()
		}
		if current == nil {
			return goja.Undefined()
		}
		if o, ok := current.(*goja.Object); ok {
			current = o.Get(p)
		} else {
			return goja.Undefined()
		}
	}
	if current == nil {
		return goja.Undefined()
	}
	return current
}

func literalToValue(vm *goja.Runtime, lit string) goja.Value {
	if lit == "true" || lit == "false" {
		return vm.ToValue(lit == "true")
	}
	if i, err := strconv.ParseInt(lit, 10, 64); err == nil {
		return vm.ToValue(i)
	}
	if f, err := strconv.ParseFloat(lit, 64); err == nil {
		return vm.ToValue(f)
	}
	return vm.ToValue(lit)
}

func expectFactory(vm *goja.Runtime) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		val := call.Arguments[0]
		exp := newExpectation(vm, val)
		return exp.base
	}
}

type expectation struct {
	vm   *goja.Runtime
	val  goja.Value
	base *goja.Object
}

func newExpectation(vm *goja.Runtime, v goja.Value) *expectation {
	e := &expectation{vm: vm, val: v}

	// helper to create chain objects for a given negation flag
	makeChain := func(neg bool) *goja.Object {
		base := vm.NewObject()
		be := vm.NewObject()
		to := vm.NewObject()
		deep := vm.NewObject()
		have := vm.NewObject()
		at := vm.NewObject()

		// wiring
		base.Set("to", to)
		base.Set("be", be)
		base.Set("deep", deep)
		base.Set("have", have)
		base.Set("at", at)
		to.Set("be", be)
		to.Set("deep", deep)
		to.Set("have", have)
		to.Set("at", at)
		be.Set("at", at)

		equal := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewGoError(fmt.Errorf("equal expects arg")))
			}
			other := call.Arguments[0]
			matches := v.StrictEquals(other)
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected %v to equal %v", v, other)))
			}
			return goja.Undefined()
		}
		below := func(call goja.FunctionCall) goja.Value {
			want := floatFromArg(vm, call)
			got := floatValue(vm, v)
			matches := got < want
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected %v to be below %v", got, want)))
			}
			return goja.Undefined()
		}
		greaterThan := func(call goja.FunctionCall) goja.Value {
			want := floatFromArg(vm, call)
			got := floatValue(vm, v)
			matches := got > want
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected %v to be greater than %v", got, want)))
			}
			return goja.Undefined()
		}
		exist := func(goja.FunctionCall) goja.Value {
			matches := !(v == nil || goja.IsUndefined(v) || goja.IsNull(v))
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected value to exist")))
			}
			return goja.Undefined()
		}
		include := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewGoError(fmt.Errorf("include expects arg")))
			}
			target := call.Arguments[0]
			if s, ok := v.(goja.String); ok {
				found := strings.Contains(s.String(), target.String())
				if neg {
					found = !found
				}
				if !found {
					panic(vm.NewGoError(fmt.Errorf("expected %v to include %v", v, target)))
				}
				return goja.Undefined()
			}
			if obj, ok := v.(*goja.Object); ok && obj.ClassName() == "Array" {
				length := obj.Get("length").ToInteger()
				found := false
				for i := range length {
					item := obj.Get(fmt.Sprintf("%d", i))
					if item.StrictEquals(target) {
						found = true
						break
					}
				}
				if neg {
					found = !found
				}
				if !found {
					panic(vm.NewGoError(fmt.Errorf("expected array to include %v", target)))
				}
				return goja.Undefined()
			}
			panic(vm.NewGoError(fmt.Errorf("include not supported for value %v", v)))
		}
		within := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				panic(vm.NewGoError(fmt.Errorf("within expects min,max")))
			}
			min := floatFromArg(vm, call)
			max := floatFromArg(vm, goja.FunctionCall{Arguments: call.Arguments[1:]})
			val := floatValue(vm, v)
			match := val >= min && val <= max
			if neg {
				match = !match
			}
			if !match {
				panic(vm.NewGoError(fmt.Errorf("expected %v to be within %v..%v", val, min, max)))
			}
			return goja.Undefined()
		}
		undefined := func(goja.FunctionCall) goja.Value {
			matches := goja.IsUndefined(v) || goja.IsNull(v)
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected value to be undefined")))
			}
			return goja.Undefined()
		}
		least := func(call goja.FunctionCall) goja.Value {
			want := floatFromArg(vm, call)
			got := floatValue(vm, v)
			match := got >= want
			if neg {
				match = !match
			}
			if !match {
				panic(vm.NewGoError(fmt.Errorf("expected %v to be at least %v", got, want)))
			}
			return goja.Undefined()
		}
		most := func(call goja.FunctionCall) goja.Value {
			want := floatFromArg(vm, call)
			got := floatValue(vm, v)
			match := got <= want
			if neg {
				match = !match
			}
			if !match {
				panic(vm.NewGoError(fmt.Errorf("expected %v to be at most %v", got, want)))
			}
			return goja.Undefined()
		}
		an := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewGoError(fmt.Errorf("an expects type")))
			}
			want := strings.ToLower(call.Arguments[0].String())
			ok := false
			switch want {
			case "string":
				_, ok = v.(goja.String)
			case "number":
				if val := v.ToFloat(); !isNaN(val) {
					ok = true
				}
			case "object":
				_, ok = v.(*goja.Object)
			case "array":
				if obj, okObj := v.(*goja.Object); okObj {
					ok = obj.ClassName() == "Array"
				}
			}
			matches := ok
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected %v to be %s", v, want)))
			}
			return goja.Undefined()
		}
		deepEqual := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewGoError(fmt.Errorf("deep.equal expects arg")))
			}
			a := v.Export()
			b := call.Arguments[0].Export()
			matches := reflect.DeepEqual(a, b)
			if neg {
				matches = !matches
			}
			if !matches {
				panic(vm.NewGoError(fmt.Errorf("expected %v to deep equal %v", v, call.Arguments[0])))
			}
			return goja.Undefined()
		}
		property := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				panic(vm.NewGoError(fmt.Errorf("property expects name")))
			}
			name := call.Arguments[0].String()
			prop := goja.Undefined()
			if obj, ok := v.(*goja.Object); ok {
				prop = obj.Get(name)
			}
			exists := prop != nil && !goja.IsUndefined(prop) && !goja.IsNull(prop)
			if neg {
				exists = !exists
			}
			if !exists {
				panic(vm.NewGoError(fmt.Errorf("expected property %s on %v", name, v)))
			}
			return prop
		}

		base.Set("equal", equal)
		base.Set("eql", equal)
		base.Set("exist", exist)
		base.Set("include", include)
		base.Set("contain", include)
		to.Set("equal", equal)
		to.Set("eql", equal)
		to.Set("exist", exist)
		to.Set("include", include)
		to.Set("contain", include)
		be.Set("below", below)
		be.Set("greaterThan", greaterThan)
		be.Set("an", an)
		be.Set("a", an)
		be.Set("undefined", undefined)
		be.Set("within", within)
		deep.Set("equal", deepEqual)
		have.Set("property", property)
		// have.length chain (minimal assertion helpers)
		have.Set("length", lengthExpectation(vm, lengthOfValue(v), neg))
		at.Set("least", least)
		at.Set("most", most)

		return base
	}

	pos := makeChain(false)
	neg := makeChain(true)
	// link not
	pos.Set("not", neg)
	if to := pos.Get("to"); to != nil {
		if obj, ok := to.(*goja.Object); ok {
			obj.Set("not", neg)
		}
	}
	if be := pos.Get("be"); be != nil {
		if obj, ok := be.(*goja.Object); ok {
			obj.Set("not", neg)
		}
	}
	if deep := pos.Get("deep"); deep != nil {
		if obj, ok := deep.(*goja.Object); ok {
			obj.Set("not", neg)
		}
	}
	if have := pos.Get("have"); have != nil {
		if obj, ok := have.(*goja.Object); ok {
			obj.Set("not", neg)
		}
	}
	if at := pos.Get("at"); at != nil {
		if obj, ok := at.(*goja.Object); ok {
			obj.Set("not", neg)
		}
	}

	e.base = pos
	return e
}

// lengthOfValue returns len for strings/arrays/objects with length property.
func lengthOfValue(v goja.Value) int64 {
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return 0
	}
	switch val := v.Export().(type) {
	case string:
		return int64(len(val))
	case []any:
		return int64(len(val))
	}
	if obj, ok := v.(*goja.Object); ok {
		if l := obj.Get("length"); l != nil && !goja.IsUndefined(l) && !goja.IsNull(l) {
			return l.ToInteger()
		}
	}
	return 0
}

// lengthExpectation returns a small object with greaterThan/equal/below for length assertions.
func lengthExpectation(vm *goja.Runtime, val int64, neg bool) *goja.Object {
	obj := vm.NewObject()
	gt := func(call goja.FunctionCall) goja.Value {
		want := call.Argument(0).ToInteger()
		match := val > want
		if neg {
			match = !match
		}
		if !match {
			panic(vm.NewGoError(fmt.Errorf("expected length %d to be greater than %d", val, want)))
		}
		return goja.Undefined()
	}
	eq := func(call goja.FunctionCall) goja.Value {
		want := call.Argument(0).ToInteger()
		match := val == want
		if neg {
			match = !match
		}
		if !match {
			panic(vm.NewGoError(fmt.Errorf("expected length %d to equal %d", val, want)))
		}
		return goja.Undefined()
	}
	bl := func(call goja.FunctionCall) goja.Value {
		want := call.Argument(0).ToInteger()
		match := val < want
		if neg {
			match = !match
		}
		if !match {
			panic(vm.NewGoError(fmt.Errorf("expected length %d to be below %d", val, want)))
		}
		return goja.Undefined()
	}
	obj.Set("greaterThan", gt)
	obj.Set("above", gt)
	obj.Set("equal", eq)
	obj.Set("below", bl)
	return obj
}

func floatFromArg(vm *goja.Runtime, call goja.FunctionCall) float64 {
	if len(call.Arguments) == 0 {
		panic(vm.NewGoError(fmt.Errorf("missing numeric arg")))
	}
	return floatValue(vm, call.Arguments[0])
}

func floatValue(vm *goja.Runtime, v goja.Value) float64 {
	return v.ToFloat()
}

func isNaN(f float64) bool {
	return f != f
}
