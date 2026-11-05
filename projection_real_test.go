package fhirpkg

import (
	"strings"
	"testing"
)

// Casos que dependen de los núcleos oficiales (make check-packages).

func realTargets(t testing.TB) []projTarget {
	t.Helper()
	// Sin los packages un caso solo-real pasaría sin comprobar nada: se omite a la
	// vista, y con ellos (make check-packages) que no haya targets es un fallo.
	if !realPackages() {
		t.Skip("FHIRPKG_PACKAGES is not set (make check-packages)")
	}
	var out []projTarget
	for _, pt := range projTargets(t) {
		if pt.real {
			out = append(out, pt)
		}
	}
	if len(out) == 0 {
		t.Fatal("FHIRPKG_PACKAGES is set but there are no real targets")
	}
	return out
}

// Un obligatorio de la raíz con [x] se conserva por la variante presente
// (plan 005 regla 4), y la referencia local que contiene conserva su contenido
// (D9): en los ejemplos oficiales, `medication[x]` → `#med` dejaba la referencia
// colgando con text. En R5 medication es un CodeableReference.
func TestRealMandatoryChoiceAndContained(t *testing.T) {
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			med := `"medicationReference":{"reference":"#med"}`
			if pt.ver == "R5" {
				med = `"medication":{"reference":{"reference":"#med"}}`
			}
			in := `{"resourceType":"MedicationRequest","status":"active","intent":"order",` + med +
				`,"subject":{"reference":"Patient/1"},"contained":[{"resourceType":"Medication","id":"med"},{"resourceType":"Medication","id":"other"}],"note":[{"text":"n"}]}`
			want := `{"resourceType":"MedicationRequest","meta":{"tag":[` + expectedTag(t, pt.ctx) + `]},"contained":[{"resourceType":"Medication","id":"med"}],` +
				`"status":"active","intent":"order",` + med + `,"subject":{"reference":"Patient/1"}}`
			got, err := pt.project(t, "MedicationRequest", SummaryText, nil, in)
			if err != nil || got != want {
				t.Errorf("\n got %s (%v)\nwant %s", got, err, want)
			}
		})
	}
}

// D8 (revisión previa §2): los obligatorios de la raíz sin isSummary. Con true se
// suman, así que el recurso conserva su cardinalidad; la lista sale del
// oráculo del plan (4, 5 y 4 elementos).
func TestRealMandatoryWithoutSummary(t *testing.T) {
	cases := map[string][][2]string{ // versión -> recurso y elemento
		"R4":  {{"Appointment", "participant"}, {"Provenance", "agent"}, {"AuditEvent", "agent"}, {"AuditEvent", "source"}},
		"R4B": {{"Appointment", "participant"}, {"Provenance", "agent"}, {"AuditEvent", "agent"}, {"AuditEvent", "source"}, {"Evidence", "variableDefinition"}},
		"R5":  {{"Appointment", "participant"}, {"Evidence", "variableDefinition"}, {"NutritionIntake", "consumedItem"}, {"BodyStructure", "includedStructure"}},
	}
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			m := pt.ctx.Model()
			for _, c := range cases[pt.ver] {
				path := c[0] + "." + c[1]
				if min, _, _ := m.Cardinality(path); min == 0 || m.IsSummary(path) {
					t.Errorf("%s: min %d, summary %v; the plan lists it as mandatory without isSummary", path, min, m.IsSummary(path))
				}
			}
			// Appointment.participant.status es obligatorio; el hijo con isSummary falso
			// que no es obligatorio (actor) cae.
			in := `{"resourceType":"Appointment","status":"booked","participant":[{"status":"accepted","actor":{"reference":"Patient/1"}}]}`
			got, err := pt.project(t, "Appointment", SummaryTrue, nil, in)
			if err != nil || !strings.Contains(got, `"participant":[{`) || !strings.Contains(got, `"status":"accepted"`) {
				t.Errorf("Appointment with true: %s (%v)", got, err)
			}
		})
	}
}

// Patient.contact no es isSummary y ninguno de sus hijos obligatorios lo
// devuelve: con true, el elemento entero queda fuera (D8 solo agrega hijos de lo
// que se conserva).
func TestRealPatientContactStaysOut(t *testing.T) {
	for _, pt := range realTargets(t) {
		in := `{"resourceType":"Patient","contact":[{"name":{"family":"F"},"gender":"male"}],"gender":"male"}`
		got, err := pt.project(t, "Patient", SummaryTrue, nil, in)
		if err != nil || strings.Contains(got, "contact") {
			t.Errorf("%s: Patient.contact is not in the summary: %s (%v)", pt.name, got, err)
		}
	}
}

// D8 ampliada (04-11-2026, aprobada por el usuario; json.html): en los núcleos
// oficiales. R5 QuestionnaireResponse.questionnaire es 1..1 y el ejemplo oficial f201
// lo cumple solo con `_questionnaire`; en R4 y R4B es 0..1 y el `_x` cae como
// antes. SearchParameter.base (1..*, code) cubre el primitivo repetido y
// CapabilityStatement.rest.mode (1..1) cubre un nivel anidado.
func TestRealMandatoryPrimitiveKeepsUnderscore(t *testing.T) {
	ext := `{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/display","valueString":"L"}]}`
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			tag := expectedTag(t, pt.ctx)
			check := func(rt string, mode SummaryMode, in, want string) {
				t.Helper()
				got, err := pt.project(t, rt, mode, nil, in)
				if err != nil || got != strings.ReplaceAll(want, "TAG", tag) {
					t.Errorf("\n got %s (%v)\nwant %s", got, err, strings.ReplaceAll(want, "TAG", tag))
				}
			}
			qr := `{"resourceType":"QuestionnaireResponse","_questionnaire":` + ext + `,"status":"completed","item":[{"linkId":"1"}]}`
			if pt.ver == "R5" {
				check("QuestionnaireResponse", SummaryTrue, qr, `{"resourceType":"QuestionnaireResponse","meta":{"tag":[TAG]},"_questionnaire":`+ext+`,"status":"completed"}`)
			} else {
				check("QuestionnaireResponse", SummaryTrue, qr, `{"resourceType":"QuestionnaireResponse","meta":{"tag":[TAG]},"status":"completed"}`)
			}
			sp := `{"resourceType":"SearchParameter","base":[null,"Person"],"_base":[` + ext + `,{"id":"i"}]}`
			check("SearchParameter", SummaryTrue, sp, sp)
			cs := `{"resourceType":"CapabilityStatement","rest":[{"mode":"server","_mode":{"id":"i"},"documentation":"d"}]}`
			check("CapabilityStatement", SummaryTrue, cs, `{"resourceType":"CapabilityStatement","meta":{"tag":[TAG]},"rest":[{"mode":"server","_mode":{"id":"i"}}]}`)
		})
	}
}

// references.html (canonical y fragmentos): un canonical local `#id` es una
// referencia al recurso contenido, igual que Reference.reference (D9 y ref-1);
// Questionnaire.item.answerValueSet es un canonical(ValueSet) que se conserva con
// _elements=item, así que su contenido también.
func TestRealCanonicalLocalKeepsContained(t *testing.T) {
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			typ := "choice"
			if pt.ver == "R5" {
				typ = "coding"
			}
			in := `{"resourceType":"Questionnaire","status":"draft","contained":[{"resourceType":"ValueSet","id":"vs","status":"draft"},{"resourceType":"ValueSet","id":"other","status":"draft"}],` +
				`"item":[{"linkId":"q","type":"` + typ + `","answerValueSet":"#vs"}]}`
			out, err := pt.project(t, "Questionnaire", SummaryFalse, []string{"item"}, in)
			if err != nil || !strings.Contains(out, `"id":"vs"`) || strings.Contains(out, `"id":"other"`) {
				t.Errorf("answerValueSet #vs must keep its target and only it: %s (%v)", out, err)
			}
		})
	}
}

// ElementDefinition.min y D8 («to ensure that the payload is valid»): un
// elemento obligatorio puede existir solo por una extensión (data-absent-reason);
// si el filtro lo deja vacío se conserva entero en vez de quitarlo hacia arriba.
func TestRealMandatoryComplexEmptiedByFilterStaysWhole(t *testing.T) {
	ext := `{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]}`
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			in := `{"resourceType":"Provenance","target":[{"reference":"Patient/p"}],"recorded":"2020-01-01T00:00:00Z","agent":[{"who":` + ext + `}]}`
			out, err := pt.project(t, "Provenance", SummaryTrue, nil, in)
			if err != nil || !strings.Contains(out, `"agent":[{"who":`+ext+`}]`) {
				t.Errorf("the mandatory agent.who must stay whole: %s (%v)", out, err)
			}
		})
	}
}

// D8: conservar un elemento obligatorio no cambia el valor de sus hijos: el
// status de Appointment.participant llega igual que en la entrada.
func TestRealMandatoryChildValueIsPreserved(t *testing.T) {
	for _, pt := range realTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			in := `{"resourceType":"Appointment","status":"proposed","description":"Visit","participant":[{"status":"accepted"}]}`
			out, err := pt.project(t, "Appointment", SummaryTrue, nil, in)
			if err != nil || !strings.Contains(out, `"participant":[{"status":"accepted"}]`) {
				t.Errorf("participant.status changed: %s (%v)", out, err)
			}
		})
	}
}
