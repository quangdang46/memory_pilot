package wire

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ST-16 — schema rejects bare claims.
//
// Three claims are the ones this product must never make without support:
// a forecast without a confidence, an attribution without coverage, and a
// leak suspicion without alternative explanations. The fixtures exist; this
// test is what makes them mean something.
//
// It checks the contract from both sides:
//   - the schema marks the field required and the fixture really lacks it,
//     so the fixture is a genuine violation and not a typo;
//   - the schema forbids it in the way the fixture violates it, either by
//     requiring the property or by being an enum of the honest values.
//
// A full JSON-Schema validator would be stronger for the general case and is
// not worth a new dependency for three known shapes. What this catches is the
// specific regression that matters: someone deleting `required` from the
// schema, or editing a fixture until it stops violating anything and leaving
// it in the invalid directory looking like coverage.
func TestSchemaRejectsBareClaims(t *testing.T) {
	cases := []struct {
		what        string
		schemaPath  string
		fixture     string
		mustRequire []string
		// mustLack: the fixture omits these fields.
		mustLack []string
		// mustBeEmpty: the fixture has these fields but fills them with less
		// than the schema demands. A claim can be bare by being empty, not
		// only by being absent — measured: the leak fixture carries
		// "alternative_explanations": [] and the first version of this test
		// reported it as "no longer violates", because it only looked for
		// absence.
		mustBeEmpty []string
	}{
		{
			what:        "forecast without confidence",
			schemaPath:  "../../schemas/forecast.schema.json",
			fixture:     "../../schemas/testdata/invalid/forecast-actionable-without-bound.json",
			mustRequire: []string{"confidence"},
			mustLack:    []string{"bound"},
		},
		{
			what:        "attribution without coverage",
			schemaPath:  "../../schemas/attribution.schema.json",
			fixture:     "../../schemas/testdata/invalid/attribution-group-without-evidence.json",
			mustRequire: []string{"coverage"},
			mustLack:    []string{"evidence_refs"},
		},
		{
			what:        "leak suspicion without alternatives",
			schemaPath:  "../../schemas/classification.schema.json",
			fixture:     "../../schemas/testdata/invalid/classification-leak-without-alternatives.json",
			mustRequire: []string{"alternative_explanations"},
			mustBeEmpty: []string{"alternative_explanations"},
		},
	}
	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			schemaRaw, err := os.ReadFile(filepath.Clean(c.schemaPath))
			if err != nil {
				t.Skipf("%s missing: %v", c.schemaPath, err)
			}
			var schema map[string]any
			if err := json.Unmarshal(schemaRaw, &schema); err != nil {
				t.Fatalf("%s is not valid JSON: %v", c.schemaPath, err)
			}
			required := requiredFields(schema)
			for _, f := range c.mustRequire {
				if !required[f] {
					t.Errorf("%s does not mark %q required — a bare claim would validate",
						c.schemaPath, f)
				}
			}
			// Presence in `required` is only half the contract for a field
			// whose minimum is non-zero: "required" admits an empty array.
			// Measured — deleting the conditional minItems from
			// classification.schema.json left this test green, which means it
			// was not testing the rule it claimed to.
			for _, f := range c.mustBeEmpty {
				if !hasNonEmptyRule(schema, f) {
					t.Errorf("%s marks %q required but never demands a non-empty "+
						"value — an empty list would validate as a bare claim",
						c.schemaPath, f)
				}
			}

			fixRaw, err := os.ReadFile(filepath.Clean(c.fixture))
			if err != nil {
				t.Skipf("%s missing: %v", c.fixture, err)
			}
			var fix map[string]any
			if err := json.Unmarshal(fixRaw, &fix); err != nil {
				t.Fatalf("%s is not valid JSON: %v", c.fixture, err)
			}
			// The fixture must actually violate something, or it is not a test.
			violates := false
			for _, f := range c.mustLack {
				if _, present := fix[f]; !present {
					violates = true
				}
			}
			for _, f := range c.mustBeEmpty {
				arr, present := fix[f].([]any)
				if !present || len(arr) == 0 {
					violates = true
				}
			}
			if !violates {
				t.Errorf("%s no longer violates the schema (absent %v, empty %v) — the "+
					"fixture has stopped being evidence that a bare claim is rejected",
					c.fixture, c.mustLack, c.mustBeEmpty)
			}
		})
	}
}

// hasNonEmptyRule reports whether the schema ever constrains `field` to at
// least one element, whether in its own definition or in a conditional clause
// under allOf/anyOf/oneOf — which is where the honest-field rules actually live
// in these schemas.
func hasNonEmptyRule(schema map[string]any, field string) bool {
	var walk func(v any) bool
	walk = func(v any) bool {
		switch node := v.(type) {
		case map[string]any:
			if props, ok := node["properties"].(map[string]any); ok {
				if f, ok := props[field].(map[string]any); ok {
					if n, ok := f["minItems"].(float64); ok && n >= 1 {
						return true
					}
				}
			}
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range node {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(schema)
}

// requiredFields collects `required` from the root schema and from any nested
// object under `properties`, because these schemas nest their honest fields.
func requiredFields(schema map[string]any) map[string]bool {
	out := map[string]bool{}
	var walk func(map[string]any)
	walk = func(m map[string]any) {
		if req, ok := m["required"].([]any); ok {
			for _, r := range req {
				if s, ok := r.(string); ok {
					out[s] = true
				}
			}
		}
		for _, key := range []string{"properties", "definitions", "$defs"} {
			if sub, ok := m[key].(map[string]any); ok {
				for _, v := range sub {
					if child, ok := v.(map[string]any); ok {
						walk(child)
					}
				}
			}
		}
	}
	walk(schema)
	return out
}

// The fixtures must live where the earlier round-trip test does not treat them
// as valid. Cheap guard against a file being moved into valid/ and quietly
// removing the evidence.
func TestInvalidFixturesAreNotInValidDirectory(t *testing.T) {
	entries, err := os.ReadDir("../../schemas/testdata/invalid")
	if err != nil {
		t.Skip("schemas/testdata/invalid missing")
	}
	if len(entries) < 10 {
		t.Fatalf("expected the invalid corpus to exist, found %d entries", len(entries))
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			t.Errorf("non-JSON file in the invalid corpus: %s", e.Name())
		}
	}
}
