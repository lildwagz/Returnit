package purchases

import (
	"bytes"
	"encoding/json"
	"slices"
)

// Categories is the fixed list of purchase categories. The database CHECK (migration 12) repeats it:
// change both together.
var Categories = []string{"phone", "computer", "tv_audio", "camera", "appliance",
	"kitchen", "vehicle", "tools", "furniture", "wearable", "other"}

// IsCategory reports whether key is one of Categories.
func IsCategory(key string) bool { return slices.Contains(Categories, key) }

// OptString tells an absent JSON field from an explicit null. An update that does not mention the field
// must keep the stored value: old app versions send the whole record without it.
type OptString struct {
	Set   bool    // the field was present in the request (a value or null)
	Value *string // nil when the field was null or absent
}

func (o *OptString) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.Value = &s
	return nil
}
