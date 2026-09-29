// Collecting a credential's values for masking.
package redact

// MapValues returns every value in secrets, the shape Text and Value take:
// masking needs to know which strings must never appear, not which
// credential field each one was.
//
// Every value is returned, the username included, so every tier masks a
// credential the same way whatever fields it happens to carry. A value
// shorter than MinLiteralLength is still returned: Text scrubs whatever it
// is given, and only the process-wide Literals set refuses short values.
func MapValues(secrets map[string]string) []string {
	values := make([]string, 0, len(secrets))
	for _, v := range secrets {
		values = append(values, v)
	}
	return values
}
