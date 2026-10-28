// Package wire: numbers that cross the per-task child's JSON pipe keep
// the type they had. encoding/json decodes every number in a map[string]any
// as float64, so a method's SetStat("count", 3) reached a Runner's engine
// as 3.0, and a condition that did arithmetic on it (result.r.count + 1)
// failed on the Walk tier with CEL's "no such overload" while it passed on
// the Crawl tier, where the stat never left the process. A whole number
// past 2^53 also lost digits, and printed in exponent form in a device
// command. ChildRequest.Params and ChildResponse.Facts therefore encode a
// whole float with a decimal point (3.0) and decode a number written with
// no point or exponent as an int, any other as a float64, so each side sees
// the int or float the other side held. A dispatch's extra variables
// (DispatchPayload.ExtraVars, Injected.ExtraVars) are decoded by the same
// rule where a Runner reads them, so vars.n + 1 works there too.
package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// maxPlainFloat is the magnitude from which encoding/json writes a float64
// in exponent form; a whole float below it is written here as digits and
// ".0" instead, and one at or above it already reads back as a float.
const maxPlainFloat = 1e21

// MarshalJSON writes r with every whole float in Params marked as a float
// (markFloats), so the child reads back the float the parent held.
func (r ChildRequest) MarshalJSON() ([]byte, error) {
	// plain has ChildRequest's fields without its methods, so marshaling it
	// does not call this method again.
	type plain ChildRequest
	p := plain(r)
	if r.Params != nil {
		p.Params = markFloats(r.Params).(map[string]any)
	}
	return json.Marshal(p)
}

// UnmarshalJSON reads a ChildRequest, giving each number in Params the
// type it was written with (restoreNumbers).
func (r *ChildRequest) UnmarshalJSON(data []byte) error {
	type plain ChildRequest
	// The outer Params shadows plain's, so every other field (the device's
	// properties among them) decodes exactly as encoding/json always did,
	// and Params alone is held back for decodeNumbers.
	var p struct {
		plain
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	params, err := decodeNumbers(p.Params)
	if err != nil {
		return fmt.Errorf("child request params: %w", err)
	}
	p.plain.Params = params
	*r = ChildRequest(p.plain)
	return nil
}

// MarshalJSON writes r with every whole float in Facts marked as a float
// (markFloats), so the parent reads back the float the method recorded.
func (r ChildResponse) MarshalJSON() ([]byte, error) {
	// plain has ChildResponse's fields without its methods, so marshaling
	// it does not call this method again.
	type plain ChildResponse
	p := plain(r)
	if r.Facts != nil {
		p.Facts = markFloats(r.Facts).(map[string]any)
	}
	return json.Marshal(p)
}

// UnmarshalJSON reads a ChildResponse, giving each number in Facts the
// type it was written with (restoreNumbers).
func (r *ChildResponse) UnmarshalJSON(data []byte) error {
	type plain ChildResponse
	// The outer Facts shadows plain's, as ChildRequest's Params does.
	var p struct {
		plain
		Facts json.RawMessage `json:"facts"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	facts, err := decodeNumbers(p.Facts)
	if err != nil {
		return fmt.Errorf("child response facts: %w", err)
	}
	p.plain.Facts = facts
	*r = ChildResponse(p.plain)
	return nil
}

// UnmarshalJSON reads a DispatchPayload, giving each number in ExtraVars
// the type the Crawl tier's own -e and runbook vars give it: a whole
// number is an int, any other a float64 (restoreNumbers). Every launch-time
// variable (a template's extra_vars, a saved configuration, a survey's
// answers, a schedule's extra data, the launch's own overrides) is merged
// into ExtraVars before dispatch, and each has crossed JSON at least once
// on the way (the API, the job's stored row, NATS), so this one decode,
// where a Runner reads its dispatch, is where vars.n + 1 starts to work on
// the Walk tier as on the Crawl tier. Nothing is marked on the way out: by
// the time the Controller writes a payload, a whole number it read from its
// database is a float64 whatever its author wrote, so a whole float an
// author wrote (3.0) reads back here as an int. Every other field decodes
// as encoding/json always decoded it.
func (p *DispatchPayload) UnmarshalJSON(data []byte) error {
	type plain DispatchPayload
	// The outer ExtraVars shadows plain's, as ChildRequest's Params does.
	var d struct {
		plain
		ExtraVars json.RawMessage `json:"extra_vars"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	vars, err := decodeNumbers(d.ExtraVars)
	if err != nil {
		return fmt.Errorf("dispatch extra_vars: %w", err)
	}
	d.plain.ExtraVars = vars
	*p = DispatchPayload(d.plain)
	return nil
}

// UnmarshalJSON reads an Injected, giving each number in ExtraVars the
// type DispatchPayload.UnmarshalJSON gives the launch's own, since the two
// are merged into one vars root on the Runner.
func (in *Injected) UnmarshalJSON(data []byte) error {
	type plain Injected
	var d struct {
		plain
		ExtraVars json.RawMessage `json:"extra_vars"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	vars, err := decodeNumbers(d.ExtraVars)
	if err != nil {
		return fmt.Errorf("injected extra_vars: %w", err)
	}
	d.plain.ExtraVars = vars
	*in = Injected(d.plain)
	return nil
}

// decodeNumbers decodes raw, a JSON object or null (or nothing, when the
// field was absent), into a map whose numbers have the types they were
// written with (restoreNumbers).
func decodeNumbers(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	// UseNumber leaves each number as a json.Number, its text as written.
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if _, err := restoreNumbers(m); err != nil {
		return nil, err
	}
	return m, nil
}

// markFloats returns v with each whole float64 or float32 inside a map,
// list or []float64 replaced by a json.Number holding its digits and ".0",
// which encoding/json writes as it is. Without the point a whole float is
// written exactly as an int is (3), and the reader could not tell them
// apart. Any other value, a typed container included, is returned as it
// is, so a whole float inside a struct or a map[string]float64 still reads
// back as an int.
func markFloats(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = markFloats(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = markFloats(e)
		}
		return out
	case []float64:
		out := make([]any, len(t))
		for i, f := range t {
			out[i] = markFloats(f)
		}
		return out
	case float64:
		if n, ok := wholeFloat(t); ok {
			return n
		}
	case float32:
		if n, ok := wholeFloat(float64(t)); ok {
			return n
		}
	}
	return v
}

// wholeFloat is f written as digits and ".0", when f is a finite whole
// number small enough that encoding/json would write it with neither a
// point nor an exponent.
func wholeFloat(f float64) (json.Number, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) >= maxPlainFloat {
		return "", false
	}
	return json.Number(strconv.FormatFloat(f, 'f', -1, 64) + ".0"), true
}

// restoreNumbers replaces each json.Number inside v, through maps and
// lists, with an int when it was written with no point or exponent and
// fits one, and a float64 otherwise. It changes maps and lists in place.
// A number no float64 can hold is an error, as encoding/json's own float64
// decoding makes it.
func restoreNumbers(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			n, err := restoreNumbers(e)
			if err != nil {
				return nil, err
			}
			t[k] = n
		}
		return t, nil
	case []any:
		for i, e := range t {
			n, err := restoreNumbers(e)
			if err != nil {
				return nil, err
			}
			t[i] = n
		}
		return t, nil
	case json.Number:
		return numberValue(t)
	}
	return v, nil
}

// numberValue is n as an int, when its text has no point or exponent and
// fits an int, or else as a float64.
func numberValue(n json.Number) (any, error) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		if i, err := strconv.ParseInt(s, 10, 64); err == nil && int64(int(i)) == i {
			return int(i), nil
		}
	}
	f, err := n.Float64()
	if err != nil {
		return nil, fmt.Errorf("number %s does not fit a float64", s)
	}
	return f, nil
}
