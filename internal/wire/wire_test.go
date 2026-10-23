package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// stripNulls removes null-valued keys recursively: the schemas treat absent
// and explicit-null as equivalent for non-required fields, so canonical form
// drops them on both sides.
func stripNulls(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			if val == nil {
				continue
			}
			out[k] = stripNulls(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = stripNulls(val)
		}
		return out
	}
	return v
}

// zh5: every valid fixture round-trips through the typed Go struct to
// byte-identical canonical JSON.
func TestValidFixturesRoundTrip(t *testing.T) {
	entries, err := os.ReadDir("../../schemas/testdata/valid")
	if err != nil {
		t.Skip("schemas/testdata missing")
	}
	if len(entries) < 16 {
		t.Fatalf("expected >=16 valid fixtures, got %d", len(entries))
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join("../../schemas/testdata/valid", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var wantAny any
		json.Unmarshal(b, &wantAny)
		wantB, _ := json.Marshal(stripNulls(wantAny))
		want := wantB
		if err != nil {
			t.Fatalf("%s: fixture is not valid JSON: %v", e.Name(), err)
		}
		obj := typedObject(e.Name())
		if d, ok := obj.(*Decision); ok {
			defer d.Normalize()
		}
		if obj == nil {
			t.Fatalf("%s: no typed struct mapped", e.Name())
		}
		if err := json.Unmarshal(b, obj); err != nil {
			t.Errorf("%s: typed unmarshal failed: %v", e.Name(), err)
			continue
		}
		got, err := json.Marshal(obj)
		if err != nil {
			t.Errorf("%s: marshal failed: %v", e.Name(), err)
			continue
		}
		var gotAny any
		json.Unmarshal(got, &gotAny)
		gotB, _ := json.Marshal(stripNulls(gotAny))
		gotC := gotB
		if string(gotC) != string(want) {
			t.Errorf("%s: round-trip mismatch\n want %s\n got  %s", e.Name(), want, gotC)
		}
	}
}

func typedObject(name string) any {
	switch {
	case has(name, "pressure-state"):
		return &PressureState{}
	case has(name, "forecast"):
		return &Forecast{}
	case has(name, "attribution"):
		return &Attribution{}
	case has(name, "classification"):
		return &Classification{}
	case has(name, "decision"):
		return &Decision{}
	case has(name, "action-result"):
		return &ActionResult{}
	case has(name, "state-file"):
		return &StateFile{}
	}
	return nil
}

func has(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
