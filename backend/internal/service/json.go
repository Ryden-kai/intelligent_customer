package service

import "encoding/json"

// jsonUnmarshal is a tiny indirection so tests can substitute it. It exists
// because encoding/json lives in stdlib and that's perfectly fine — but
// keeping the call site minimal makes it trivial to swap to a more lenient
// parser later if we ever need to.
func jsonUnmarshal(data string, v any) error {
	return json.Unmarshal([]byte(data), v)
}