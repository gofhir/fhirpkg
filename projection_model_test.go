package fhirpkg

import "testing"

// Model.IsSummary e IsModifier sobre la fixture (plan 005, paso 1). Los valores
// esperados salen de los flags que projFlags pone, que imitan los de los núcleos
// (elementdefinition.html: isSummary, isModifier).
func TestModelIsSummaryAndModifier(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			m := projContext(t, v, true).Model()
			for _, tc := range []struct {
				path         string
				summary, mod bool
			}{
				// Resource.id y meta son del resumen; language y text, no.
				{"Patient.id", true, false}, {"Patient.meta", true, false}, {"Patient.language", false, false}, {"Patient.text", false, false},
				// elementdefinition.html: implicitRules es modificador.
				{"Patient.implicitRules", true, true},
				// Un [x] responde por el camino sin sufijo y por cada variante.
				{"Patient.deceased", true, true}, {"Patient.deceasedBoolean", true, true}, {"Patient.deceasedDateTime", true, true},
				{"Observation.value", true, false}, {"Observation.valueQuantity", true, false}, {"Observation.valueString", true, false},
				// El isSummary de un hijo de un tipo de datos está en el tipo.
				{"HumanName.family", true, false}, {"Narrative.div", false, false}, {"Extension.url", false, false},
				{"Element.id", false, false}, {"date.extension", false, false},
				// Un hijo del resumen bajo un padre que no lo es.
				{"Patient.contact", false, false}, {"Patient.contact.name", true, false}, {"Patient.contact.flag", false, true},
				// Un obligatorio de la raíz sin isSummary, y un modificador que no es del resumen.
				{"Observation.status", false, true},
				// Un camino que no existe.
				{"Patient.nope", false, false}, {"Nope.x", false, false}, {"", false, false},
			} {
				if got := m.IsSummary(tc.path); got != tc.summary {
					t.Errorf("IsSummary(%q) = %v, want %v", tc.path, got, tc.summary)
				}
				if got := m.IsModifier(tc.path); got != tc.mod {
					t.Errorf("IsModifier(%q) = %v, want %v", tc.path, got, tc.mod)
				}
			}
			// DomainResource.modifierExtension: del resumen solo en R5 (revisión previa §2).
			if got := m.IsSummary("Patient.modifierExtension"); got != (v.name == "R5") {
				t.Errorf("IsSummary(Patient.modifierExtension) = %v in %s", got, v.name)
			}
			if !m.IsSummary("BackboneElement.modifierExtension") || !m.IsModifier("Patient.modifierExtension") {
				t.Error("BackboneElement.modifierExtension must be summary and every modifierExtension a modifier")
			}
		})
	}
}

// Sin los flags (un snapshot de otro origen que no los trae), todo es false: la
// ausencia de isSummary es false (elementdefinition.html: «isSummary» por defecto).
func TestModelFlagsDefaultToFalse(t *testing.T) {
	for _, v := range modelVersions {
		m := normContext(t, v).Model()
		if m.IsSummary("Patient.name") || m.IsModifier("Patient.implicitRules") {
			t.Errorf("%s: a snapshot without flags gave true", v.name)
		}
	}
}
