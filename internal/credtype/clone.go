// This file holds deep copies of a credential type's documents, for a
// caller that hands a type to code it does not control and then compares
// what comes back with what it handed over: a copy that shared a slice or a
// map with the original would compare an edit with itself.
package credtype

// Clone returns a copy of ct that shares no slice or map with it.
func (ct CredentialType) Clone() CredentialType {
	ct.Inputs = ct.Inputs.Clone()
	ct.Injectors = ct.Injectors.Clone()
	return ct
}

// Clone returns a copy of the schema that shares no slice with it.
func (s InputSchema) Clone() InputSchema {
	out := InputSchema{Required: cloneStrings(s.Required)}
	if s.Fields != nil {
		out.Fields = make([]InputField, len(s.Fields))
		for i, f := range s.Fields {
			f.Choices = cloneStrings(f.Choices)
			out.Fields[i] = f
		}
	}
	return out
}

// Clone returns a copy of the document that shares no map or slice with it,
// extra variables copied to every leaf.
func (inj Injectors) Clone() Injectors {
	return Injectors{
		Env:       cloneStringMap(inj.Env),
		ExtraVars: cloneAnyMap(inj.ExtraVars),
		File:      cloneStringMap(inj.File),
		OmitEmpty: cloneStrings(inj.OmitEmpty),
	}
}

// cloneStrings copies a slice, keeping nil as nil.
func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

// cloneStringMap copies a map, keeping nil as nil.
func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// cloneAnyMap copies a decoded JSON object to every leaf, keeping nil as nil.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = cloneAny(v)
	}
	return out
}

// cloneAny copies one decoded JSON value: an object or an array is copied,
// and a scalar is a value already.
func cloneAny(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return cloneAnyMap(value)
	case []any:
		out := make([]any, len(value))
		for i, nested := range value {
			out[i] = cloneAny(nested)
		}
		return out
	default:
		return v
	}
}
