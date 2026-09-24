package middleware

import (
	"encoding/json"
	"io"
)

// jsonEncode is split out so tests can stub it later if needed.
func jsonEncode(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}