package web

import (
	"bytes"
	"encoding/json"
)

// Optional is a JSON field that can be left out (keep the current value), null (clear it), or a
// value (set it). Plain Go pointers can't tell "left out" from null; this can, because Go only calls
// UnmarshalJSON for fields that are present.
//
//	var req struct{ VariegationID web.Optional[int64] `json:"variegationId"` }
//	// {}                     → Set false
//	// {"variegationId": null} → Set true, Value nil
//	// {"variegationId": 7}    → Set true, Value 7
type Optional[T any] struct {
	Set   bool
	Value *T
}

func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(data, []byte("null")) {
		o.Value = nil
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}
