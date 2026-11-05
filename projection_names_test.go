package fhirpkg

import (
	"encoding/json"
	"strings"
	"testing"
)

// Los nombres de FHIR de la proyección (fhirnames.go, ADR-005) comprobados contra
// los núcleos oficiales en R4, R4B y R5 (make check-packages): cada literal que
// ningún dato marca tiene aquí el test que dice por qué es correcto.

type rawSnapshot struct {
	Snapshot struct {
		Element []struct {
			ID         string `json:"id"`
			Path       string `json:"path"`
			Min        int    `json:"min"`
			IsSummary  bool   `json:"isSummary"`
			IsModifier bool   `json:"isModifier"`
			Base       struct {
				Path string `json:"path"`
			} `json:"base"`
			Type []struct {
				Code string `json:"code"`
			} `json:"type"`
		} `json:"element"`
		// Los constraint de la raíz viven en cada elemento; Reference los trae en la raíz.
	} `json:"snapshot"`
}

func rawSD(t *testing.T, c *Context, typeName string) rawSnapshot {
	t.Helper()
	r, err := c.Resolve("StructureDefinition", "http://hl7.org/fhir/StructureDefinition/"+typeName)
	if err != nil {
		t.Fatal(err)
	}
	var sd rawSnapshot
	if err := json.Unmarshal(r.Resource.Raw, &sd); err != nil {
		t.Fatal(err)
	}
	return sd
}

// search.html (R4 §3.1.1.5.8, R5 §3.2.1.7.5): «the text element», «the 'id'
// element, the 'meta' element». En las tres versiones `text` es de tipo Narrative
// con base DomainResource.text; id y meta vienen de Resource. Ningún recurso
// define otro elemento de la raíz con esos nombres.
func TestRealProjectionNamesTextIdMeta(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			m := c.Model()
			withText := 0
			for _, rt := range c.ResourceTypes() {
				for _, e := range rawSD(t, c, rt).Snapshot.Element {
					root, name, ok := strings.Cut(e.Path, ".")
					if !ok || strings.Contains(name, ".") || root != rt {
						continue
					}
					switch name {
					case elemText:
						withText++
						if len(e.Type) != 1 || e.Type[0].Code != "Narrative" || e.Base.Path != "DomainResource.text" {
							t.Errorf("%s: type %v and base %q, want Narrative from DomainResource.text", e.Path, e.Type, e.Base.Path)
						}
					case elemID:
						if e.Base.Path != "Resource.id" {
							t.Errorf("%s: base %q, want Resource.id", e.Path, e.Base.Path)
						}
					case elemMeta:
						if e.Base.Path != "Resource.meta" || len(e.Type) != 1 || e.Type[0].Code != "Meta" {
							t.Errorf("%s: base %q, want Resource.meta of type Meta", e.Path, e.Base.Path)
						}
					case elemContained:
						if e.Base.Path != "DomainResource.contained" {
							t.Errorf("%s: base %q, want DomainResource.contained", e.Path, e.Base.Path)
						}
					}
				}
			}
			if withText < 100 {
				t.Errorf("only %d resources have a text element", withText)
			}
			if !m.HasType("Narrative") || m.TypeOf("DomainResource.text") != "Narrative" {
				t.Error("DomainResource.text is not a Narrative")
			}
		})
	}
}

// search.html: «Servers SHOULD mark the resources with the tag SUBSETTED». El
// concepto está en el CodeSystem de la terminología de HL7 que cada lock trae.
func TestRealSubsettedConceptExists(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			r, err := c.Resolve(typeCodeSystem, systemSubsetted)
			if err != nil {
				t.Fatalf("the lock has no %s: %v", systemSubsetted, err)
			}
			var doc struct {
				Concept []conceptDoc `json:"concept"`
			}
			if err := json.Unmarshal(r.Resource.Raw, &doc); err != nil {
				t.Fatal(err)
			}
			cd := findConcept(doc.Concept, codeSubsetted)
			if cd == nil || cd.Display == "" {
				t.Fatalf("%s has no concept %s with a display: %+v", systemSubsetted, codeSubsetted, cd)
			}
			tag, err := c.subsettedTag()
			if err != nil || !strings.Contains(string(tag), `"display":`+strconvQuote(cd.Display)) {
				t.Errorf("the tag %s (%v) does not carry the display %q of the code system", tag, err, cd.Display)
			}
			// Meta.tag es un arreglo de Coding con system, code y display (resource.html, datatypes.html).
			m := c.Model()
			if m.TypeOf("Meta.tag") != "Coding" || m.TypeOf("Coding.system") != "uri" || m.TypeOf("Coding.code") != "code" || m.TypeOf("Coding.display") != "string" {
				t.Error("Meta.tag or Coding.system, code, display have another type")
			}
			if _, hi, _ := m.Cardinality("Meta.tag"); hi != Unbounded {
				t.Error("Meta.tag does not repeat")
			}
		})
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// D1: Extension es el tipo de Element.extension y de modifierExtension en las
// tres versiones, y Extension.url es obligatorio y no está en el resumen (la
// razón de copiar entera una Extension conservada).
func TestRealExtensionTypeNames(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			m := c.Model()
			for _, p := range []string{"Element.extension", "DomainResource.modifierExtension", "BackboneElement.modifierExtension", "Patient.extension"} {
				if m.TypeOf(p) != typeExtension {
					t.Errorf("TypeOf(%s) = %q, want %s", p, m.TypeOf(p), typeExtension)
				}
			}
			if min, _, _ := m.Cardinality("Extension.url"); min != 1 || m.IsSummary("Extension.url") {
				t.Error("Extension.url must be mandatory and outside the summary")
			}
		})
	}
}

// D9: la referencia local es «#id» (references.html, «contained resources»); la
// invariante ref-1 de Reference lo dice con '#' y %rootResource.contained.id.
func TestRealLocalReferenceNames(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			r, err := c.Resolve("StructureDefinition", "http://hl7.org/fhir/StructureDefinition/Reference")
			if err != nil {
				t.Fatal(err)
			}
			var sd struct {
				Snapshot struct {
					Element []struct {
						Path       string `json:"path"`
						Constraint []struct {
							Key        string `json:"key"`
							Expression string `json:"expression"`
						} `json:"constraint"`
					} `json:"element"`
				} `json:"snapshot"`
			}
			if err := json.Unmarshal(r.Resource.Raw, &sd); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range sd.Snapshot.Element {
				for _, k := range e.Constraint {
					if k.Key == "ref-1" {
						found = true
						if !strings.Contains(k.Expression, "'"+localRefPrefix+"'") || !strings.Contains(k.Expression, "%rootResource.contained.id") {
							t.Errorf("ref-1 = %q", k.Expression)
						}
					}
				}
			}
			if !found {
				t.Error("Reference has no ref-1")
			}
			m := c.Model()
			if m.TypeOf("Reference."+elemReference) != "string" || m.TypeOf("DomainResource.contained") != "Resource" {
				t.Error("Reference.reference or DomainResource.contained have another type")
			}
		})
	}
}

// D9: `canonical` es un tipo primitivo y los elementos que nombran un recurso por
// su URL lo usan (datatypes.html, references.html «fragmentos»): Questionnaire.item.answerValueSet
// es un canonical(ValueSet) en las tres versiones.
func TestRealCanonicalTypeName(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			m := c.Model()
			if !m.IsPrimitive(typeCanonical) || m.TypeOf("Questionnaire.item.answerValueSet") != typeCanonical {
				t.Errorf("%s: canonical primitive %v, answerValueSet type %q", rc.name, m.IsPrimitive(typeCanonical), m.TypeOf("Questionnaire.item.answerValueSet"))
			}
		})
	}
}
