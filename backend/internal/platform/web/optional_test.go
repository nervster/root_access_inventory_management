package web

import (
	"encoding/json"
	"testing"
)

func TestOptional(t *testing.T) {
	var req struct {
		ID Optional[int64] `json:"id"`
	}
	for _, tt := range []struct {
		body      string
		wantSet   bool
		wantValue *int64
	}{
		{`{}`, false, nil},
		{`{"id": null}`, true, nil},
		{`{"id": 7}`, true, ptr(int64(7))},
	} {
		req.ID = Optional[int64]{}
		if err := json.Unmarshal([]byte(tt.body), &req); err != nil {
			t.Fatal(err)
		}
		if req.ID.Set != tt.wantSet || (req.ID.Value == nil) != (tt.wantValue == nil) || req.ID.Value != nil && *req.ID.Value != *tt.wantValue {
			t.Errorf("%s → %+v, want set %v, value %v", tt.body, req.ID, tt.wantSet, tt.wantValue)
		}
	}
	if err := json.Unmarshal([]byte(`{"id": "seven"}`), &req); err == nil {
		t.Error(`{"id": "seven"} decoded, want an error`)
	}
}

func ptr[T any](v T) *T { return &v }
