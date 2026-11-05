package fhirpkg

import (
	"encoding/json"
	"strings"
	"testing"
)

// ADR-005 D8 and ElementDefinition.min: each mandatory occurrence emptied by
// projection must survive, including members of a repeated mandatory field.
func TestRealRepeatedMandatoryOccurrenceKeptWhole(t *testing.T) {
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			in := `{"resourceType":"Provenance","target":[{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]},{"reference":"Patient/p"}],"recorded":"2020-01-01T00:00:00Z","agent":[{"who":{"reference":"Practitioner/p"}}]}`
			out, err := pt.project(t, "Provenance", SummaryTrue, nil, in)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(out), &m); err != nil {
				t.Fatal(err)
			}
			if len(m["target"].([]any)) != 2 {
				t.Errorf("mandatory target occurrence lost: %s", out)
			}
		})
	}
}

// references.html: canonical in a retained extension uses the same local id
// resolution as an ordinary canonical, including transitive contained refs.
func TestRealCanonicalInRetainedExtension(t *testing.T) {
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			for _, mode := range []SummaryMode{SummaryTrue, SummaryText, SummaryFalse} {
				var els []string
				if mode == SummaryFalse {
					els = []string{"modifierExtension"}
				}
				in := `{"resourceType":"Patient","modifierExtension":[{"url":"https://example.org/modifier","valueCanonical":"#vs"}],"contained":[{"resourceType":"ValueSet","id":"vs","status":"draft","modifierExtension":[{"url":"https://example.org/modifier","valueCanonical":"#vs2"}]},{"resourceType":"ValueSet","id":"vs2","status":"draft"}]}`
				out, err := pt.project(t, "Patient", mode, els, in)
				if err != nil || !strings.Contains(out, `"id":"vs"`) || !strings.Contains(out, `"id":"vs2"`) {
					t.Errorf("mode %v: %s (%v)", mode, out, err)
				}
			}
		})
	}
}
