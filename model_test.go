package fhirpkg

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Los escenarios son sintéticos (H7) y sus valores esperados están escritos a
// mano desde la especificación: structuredefinition.html (kind, abstract,
// derivation, baseDefinition, type), elementdefinition.html (path, type.code,
// type.targetProfile, contentReference, min, max, «Choice types») y
// extension-structuredefinition-fhir-type.html. Cada caso corre en R4, R4B y R5.

type modelVersion struct{ name, fhir string }

var modelVersions = []modelVersion{{"R4", "4.0.1"}, {"R4B", "4.3.0"}, {"R5", "5.0.0"}}

// mcore arma el núcleo sintético de la versión: R4 declara su versión en
// fhir-version-list y sus tipos primitivos del id son string (datatypes.html);
// desde R4B el id es id.
func mcore(v modelVersion, rs ...string) pkg {
	p := pkg{name: "core", version: v.fhir, canonical: hl7, files: fileSet(rs...)}
	if v.name == "R4" {
		p.typ, p.legacyList = "fhir.core", []string{v.fhir}
	} else {
		p.typ, p.fhirVersions = "Core", []string{v.fhir}
	}
	return p
}

// idType es el tipo FHIR de Resource.id según la versión (plan 003 §4).
func idType(v modelVersion) string {
	if v.name == "R4" {
		return "string"
	}
	return "id"
}

// ty arma un type de ElementDefinition con sus targetProfile.
func ty(code string, targets ...string) map[string]any {
	m := map[string]any{"code": code}
	if len(targets) > 0 {
		var ps []string
		for _, t := range targets {
			ps = append(ps, sdBase+t)
		}
		m["targetProfile"] = ps
	}
	return m
}

// sysTy es un type cuyo código es un tipo del sistema de FHIRPath, con su tipo
// FHIR en la extensión fhir-type.
func sysTy(system, fhirType string) map[string]any {
	return map[string]any{
		"code": "http://hl7.org/fhirpath/System." + system,
		"extension": []any{map[string]any{
			"url": "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type", "valueUrl": fhirType}},
	}
}

// el arma un ElementDefinition; el id es el path salvo que se pase otro.
func el(path string, min int, max string, types ...map[string]any) map[string]any {
	m := map[string]any{"id": path, "path": path, "min": min, "max": max}
	if len(types) > 0 {
		m["type"] = types
	}
	return m
}

func without(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		delete(m, k)
	}
	return m
}

func with(m map[string]any, kv ...any) map[string]any {
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// tsd arma un StructureDefinition de tipo con snapshot. parent vacío es la
// raíz (en R4 y R4B sin derivation).
func tsd(v modelVersion, name, kind, parent string, abstract bool, elems ...map[string]any) string {
	kv := []any{"kind", kind, "type", name, "abstract", abstract, "status", "active", "version", v.fhir,
		"snapshot", map[string]any{"element": elems}}
	if parent != "" {
		kv = append(kv, "derivation", "specialization", "baseDefinition", sdBase+parent)
	}
	return j("StructureDefinition", sdBase+name, kv...)
}

// fixtureCodes son los tipos que la fixture usa como type.code y no necesita
// detallar: un snapshot cuyo código no es un tipo es error (M1).
var (
	fixturePrimitives = []string{"decimal", "string", "integer", "boolean", "dateTime", "code", "canonical"}
	fixtureComplex    = []string{"Extension", "Meta", "Narrative", "HumanName", "CodeableConcept", "BackboneElement", "Reference"}
)

// fixture es un núcleo con lo que el plan 003 §4 pide ejercer.
func fixture(v modelVersion) []string {
	out := fixtureTypes(v)
	for _, n := range fixturePrimitives {
		elems := []map[string]any{el(n, 0, "*")}
		if n == "string" {
			// El conjunto de tipos del sistema sale de los primitivos del núcleo
			// (plan 003, ronda 2): string declara System.String.
			elems = append(elems, with(el("string.value", 0, "1", sysTy("String", "string")), "base", map[string]any{"path": "string.value"}))
		}
		out = append(out, tsd(v, n, "primitive-type", "Element", false, elems...))
	}
	for _, n := range fixtureComplex {
		out = append(out, tsd(v, n, "complex-type", "Element", false, el(n, 0, "*")))
	}
	return out
}

func fixtureTypes(v modelVersion) []string {
	return []string{
		tsd(v, "Element", "complex-type", "", true,
			el("Element", 0, "*"),
			el("Element.id", 0, "1", sysTy("String", idType(v))),
			el("Element.extension", 0, "*", ty("Extension"))),
		tsd(v, "Quantity", "complex-type", "Element", false,
			el("Quantity", 0, "*"),
			el("Quantity.value", 0, "1", ty("decimal")),
			// Una extensión que no es fhir-type no da tipo FHIR aunque traiga valueUrl.
			el("Quantity.unit", 0, "1", with(ty("string"), "extension", []any{map[string]any{"url": "http://example.org/other", "valueUrl": "bogus"}})),
			// Varios tipos sin [x]: no hay un tipo único (elementdefinition.html: type.code
			// es el tipo del elemento solo si es uno) y tampoco un tipo FHIR.
			el("Quantity.alt", 0, "1", with(ty("string"), "extension", []any{map[string]any{"url": "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type", "valueUrl": "string"}}), ty("integer"))),
		tsd(v, "Age", "complex-type", "Quantity", false, el("Age", 0, "*"), el("Age.value", 0, "1", ty("decimal"))),
		tsd(v, "date", "primitive-type", "Element", false,
			el("date", 0, "*"),
			with(el("date.id", 0, "1", sysTy("String", idType(v))), "base", map[string]any{"path": "Element.id"}),
			with(el("date.extension", 0, "*", ty("Extension")), "base", map[string]any{"path": "Element.extension"}),
			with(el("date.value", 0, "1", sysTy("Date", "date")), "base", map[string]any{"path": "date.value"})),
		tsd(v, "Resource", "resource", "", true,
			el("Resource", 0, "*"),
			el("Resource.id", 0, "1", sysTy("String", idType(v))),
			el("Resource.meta", 0, "1", ty("Meta"))),
		tsd(v, "DomainResource", "resource", "Resource", true,
			el("DomainResource", 0, "*"),
			el("DomainResource.contained", 0, "*", ty("Resource")),
			el("DomainResource.text", 0, "1", ty("Narrative"))),
		tsd(v, "Patient", "resource", "DomainResource", false,
			el("Patient", 0, "*"),
			el("Patient.id", 0, "1", sysTy("String", idType(v))),
			el("Patient.name", 0, "*", ty("HumanName")),
			with(el("Patient.name", 0, "*", ty("HumanName")), "id", "Patient.name:official"),
			el("Patient.gender", 0, "1", ty("code")),
			el("Patient.deceased[x]", 0, "1", ty("boolean"), ty("dateTime")),
			// Un [x] con un tipo repetido (Reference con dos perfiles en entradas
			// distintas) y destinos por variante.
			el("Patient.partner[x]", 0, "1", ty("Reference", "RelatedPerson"), ty("canonical", "Observation"), ty("Reference", "Organization")),
			el("Patient.managingOrganization", 0, "1", ty("Reference", "Organization")),
			el("Patient.link", 0, "*", ty("BackboneElement")),
			el("Patient.link.other", 1, "1", ty("Reference", "Patient", "RelatedPerson"))),
		tsd(v, "Observation", "resource", "DomainResource", false,
			el("Observation", 0, "*"),
			el("Observation.status", 1, "1", ty("code")),
			el("Observation.value[x]", 0, "1", ty("Quantity"), ty("CodeableConcept"), ty("string"), ty("dateTime")),
			// Reference(Any) es Resource (R4, referencelist); un perfil de Patient
			// y dos targetProfile que resuelven al mismo tipo se cuentan una vez.
			el("Observation.focus", 0, "*", ty("Reference", "Resource")),
			el("Observation.subject", 0, "1", ty("Reference", "Patient", "vitalsigns-patient", "Patient"))),
		tsd(v, "Organization", "resource", "DomainResource", false, el("Organization", 0, "*")),
		tsd(v, "RelatedPerson", "resource", "DomainResource", false, el("RelatedPerson", 0, "*")),
		// Un perfil: su type es Patient, pero no define un tipo.
		j("StructureDefinition", sdBase+"vitalsigns-patient", "kind", "resource", "derivation", "constraint", "type", "Patient",
			"baseDefinition", sdBase+"Patient", "status", "active", "version", v.fhir,
			"snapshot", map[string]any{"element": []any{el("Patient", 0, "*"), el("Patient.name", 1, "*", ty("HumanName"))}}),
		// Un modelo lógico: no es un tipo (structuredefinition.html, kind).
		tsd(v, "Foo", "logical", "Element", false, el("Foo", 0, "*"), el("Foo.bar", 0, "1", ty("string"))),
		// Sin snapshot no hay elementos que leer: no define un tipo.
		j("StructureDefinition", sdBase+"NoSnapshot", "kind", "complex-type", "type", "NoSnapshot", "derivation", "specialization",
			"baseDefinition", sdBase+"Element", "status", "active", "version", v.fhir),
		// Casos de la ronda 2: un targetProfile y un contentReference con
		// url|versión (canonical.html: «url|version»; el contentReference es uri y no
		// define la versión, pero la tolera como un canónico) y un [x] de un solo
		// tipo con la extensión fhir-type.
		tsd(v, "Linked", "complex-type", "Element", false,
			el("Linked", 0, "*"),
			el("Linked.org", 0, "1", ty("Reference", "Organization|"+v.fhir)),
			el("Linked.item", 0, "*", ty("BackboneElement")),
			with(el("Linked.item.again", 0, "*"), "contentReference", sdBase+"Linked|"+v.fhir+"#Linked.item"),
			el("Linked.single[x]", 0, "1", with(ty("string"), "extension", []any{map[string]any{
				"url": "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type", "valueUrl": "string"}}))),
		tsd(v, "Questionnaire", "resource", "DomainResource", false,
			el("Questionnaire", 0, "*"),
			el("Questionnaire.item", 0, "*", ty("BackboneElement")),
			el("Questionnaire.item.text", 0, "1", ty("string")),
			with(el("Questionnaire.item.item", 0, "*"), "contentReference", "#Questionnaire.item"),
			// Sintético (H7): la forma url#ruta de R5 no aparece en ningún núcleo.
			with(el("Questionnaire.item.sub", 0, "*"), "contentReference", sdBase+"Questionnaire#Questionnaire.item")),
	}
}

// buildModelFixture arma el Context de la fixture, con extras opcionales.
func buildModelFixture(t testing.TB, v modelVersion, extra ...string) *Context {
	t.Helper()
	c, err := tryCtx(t, mcore(v, append(fixture(v), extra...)...))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func forVersions(t *testing.T, f func(t *testing.T, v modelVersion, m *Model)) {
	t.Helper()
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) { f(t, v, buildModelFixture(t, v).Model()) })
	}
}

func wantStrings(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func wantString(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// §3.2 reglas 1 y 2: los tipos son los SD que definen uno; el padre sale de
// baseDefinition.
func TestModelTypesAndHierarchy(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		for _, name := range []string{"Element", "Quantity", "Age", "date", "Resource", "DomainResource", "Patient", "Observation", "Questionnaire"} {
			if !m.HasType(name) {
				t.Errorf("HasType(%s) = false", name)
			}
		}
		// Un perfil, un modelo lógico y un SD sin snapshot no son tipos;
		// los nombres distinguen mayúsculas (datatypes.html).
		for _, name := range []string{"vitalsigns-patient", "Foo", "NoSnapshot", "patient", "", "System.String"} {
			if m.HasType(name) {
				t.Errorf("HasType(%q) = true", name)
			}
		}
		wantString(t, "ParentType(Age)", m.ParentType("Age"), "Quantity")
		wantString(t, "ParentType(Patient)", m.ParentType("Patient"), "DomainResource")
		wantString(t, "ParentType(DomainResource)", m.ParentType("DomainResource"), "Resource")
		wantString(t, "ParentType(Resource)", m.ParentType("Resource"), "")
		wantString(t, "ParentType(unknown)", m.ParentType("Nope"), "")
		for _, c := range []struct {
			child, parent string
			want          bool
		}{
			{"Age", "Quantity", true}, {"Age", "Element", true}, {"Age", "Age", true}, {"Quantity", "Age", false},
			{"Patient", "Resource", true}, {"Patient", "DomainResource", true}, {"Resource", "Patient", false},
			{"Patient", "Observation", false}, {"Patient", "", false}, {"", "Resource", false},
		} {
			if got := m.IsSubtype(c.child, c.parent); got != c.want {
				t.Errorf("IsSubtype(%q, %q) = %v, want %v", c.child, c.parent, got, c.want)
			}
		}
		wantString(t, "FHIRVersion", m.FHIRVersion(), v.fhir)
	})
}

// §3.2 regla 3: IsResource es kind resource y no abstracto; un perfil no.
func TestModelIsResource(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		for name, want := range map[string]bool{
			"Patient": true, "Observation": true, "Questionnaire": true,
			"Resource": false, "DomainResource": false, // abstractos
			"Quantity": false, "date": false, "Foo": false, "vitalsigns-patient": false, "Nope": false,
		} {
			if got := m.IsResource(name); got != want {
				t.Errorf("IsResource(%s) = %v, want %v", name, got, want)
			}
		}
	})
}

// §3.2 regla 4: [x] se pide por su base (los tipos en el orden del SD) y por
// cada variante; el elemento con [x] no tiene TypeOf propio.
func TestModelChoiceElements(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantStrings(t, "ChoiceTypes(Observation.value)", m.ChoiceTypes("Observation.value"), "Quantity", "CodeableConcept", "string", "dateTime")
		wantStrings(t, "ChoiceTypes(Patient.deceased)", m.ChoiceTypes("Patient.deceased"), "boolean", "dateTime")
		for path, want := range map[string]string{
			"Observation.valueQuantity": "Quantity", "Observation.valueCodeableConcept": "CodeableConcept",
			"Observation.valueString": "string", "Observation.valueDateTime": "dateTime",
			"Patient.deceasedBoolean": "boolean", "Patient.deceasedDateTime": "dateTime",
			"Observation.value[x]": "", "Observation.value": "", "Observation.valueInteger": "",
		} {
			wantString(t, "TypeOf("+path+")", m.TypeOf(path), want)
		}
		if got := m.ChoiceTypes("Patient.gender"); got != nil {
			t.Errorf("ChoiceTypes(Patient.gender) = %q, want nil", got)
		}
		wantStrings(t, "ChildElements(Observation)", m.ChildElements("Observation"), "status", "value[x]", "focus", "subject")
	})
}

// §3.2 regla 4: un slice no es un hijo ni un elemento.
func TestModelSlicesAreNotChildren(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantStrings(t, "ChildElements(Patient)", m.ChildElements("Patient"), "id", "name", "gender", "deceased[x]", "partner[x]", "managingOrganization", "link")
		for _, p := range m.Paths() {
			if strings.Contains(p, ":") {
				t.Errorf("the path %q has a slice mark", p)
			}
		}
		if min, max, ok := m.Cardinality("Patient.name"); !ok || min != 0 || max != Unbounded {
			t.Errorf("Patient.name cardinality = %d..%d %v: the slice (1..*) must not replace the element", min, max, ok)
		}
	})
}

// §3.2 regla 4: ChildElements de un tipo y de un elemento, en orden.
func TestModelChildElements(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantStrings(t, "Quantity", m.ChildElements("Quantity"), "value", "unit", "alt")
		wantStrings(t, "Patient.link", m.ChildElements("Patient.link"), "other")
		wantStrings(t, "Questionnaire.item", m.ChildElements("Questionnaire.item"), "text", "item", "sub")
		if got := m.ChildElements("Patient.gender"); got != nil {
			t.Errorf("ChildElements(Patient.gender) = %q, want nil", got)
		}
		if got := m.ChildElements("Nope"); got != nil {
			t.Errorf("ChildElements(Nope) = %q, want nil", got)
		}
		wantString(t, "TypeOf(Patient.link)", m.TypeOf("Patient.link"), "BackboneElement")
	})
}

// §3.2 regla 4: contentReference en las dos formas.
func TestModelContentReference(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantString(t, "ResolvePath(#ruta)", m.ResolvePath("Questionnaire.item.item"), "Questionnaire.item")
		// Sintético (H7): url#ruta.
		wantString(t, "ResolvePath(url#ruta)", m.ResolvePath("Questionnaire.item.sub"), "Questionnaire.item")
		wantString(t, "ResolvePath(self-defined)", m.ResolvePath("Questionnaire.item.text"), "Questionnaire.item.text")
		wantString(t, "ResolvePath(unknown)", m.ResolvePath("Nope.x"), "Nope.x")
		wantString(t, "TypeOf(contentReference)", m.TypeOf("Questionnaire.item.item"), "")
	})
}

// Ronda 2 (A): url|versión en un targetProfile (canonical) y en un
// contentReference; (I): el tipo FHIR de un [x] de un solo tipo es el de su
// variante, no el de la base.
func TestModelVersionedReferencesAndSingleTypeChoice(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantStrings(t, "ReferenceTargets(Linked.org)", m.ReferenceTargets("Linked.org"), "Organization")
		wantString(t, "ResolvePath(url|version#ruta)", m.ResolvePath("Linked.item.again"), "Linked.item")
		wantString(t, "FHIRTypeOf(Linked.singleString)", m.FHIRTypeOf("Linked.singleString"), "string")
		wantString(t, "FHIRTypeOf(Linked.single)", m.FHIRTypeOf("Linked.single"), "")
	})
}

// §3.2 regla 5: los elementos propios de un primitivo; el value no tiene tipo FHIR.
func TestModelPrimitiveElements(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantStrings(t, "ChildElements(date)", m.ChildElements("date"), "id", "extension", "value")
		wantString(t, "TypeOf(date.id)", m.TypeOf("date.id"), "http://hl7.org/fhirpath/System.String")
		wantString(t, "TypeOf(date.extension)", m.TypeOf("date.extension"), "Extension")
		wantString(t, "TypeOf(date.value)", m.TypeOf("date.value"), "http://hl7.org/fhirpath/System.Date")
		// H3: date.value tiene la extensión fhir-type (el propio primitivo) y no
		// figura; date.id y date.extension sí cuando la tienen.
		wantString(t, "FHIRTypeOf(date.value)", m.FHIRTypeOf("date.value"), "")
		wantString(t, "FHIRTypeOf(date.id)", m.FHIRTypeOf("date.id"), idType(v))
	})
}

// §3.2 regla 4: el tipo FHIR de un elemento de tipo del sistema (extension fhir-type).
func TestModelFHIRTypes(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantString(t, "FHIRTypeOf(Patient.id)", m.FHIRTypeOf("Patient.id"), idType(v))
		wantString(t, "FHIRTypeOf(Resource.id)", m.FHIRTypeOf("Resource.id"), idType(v))
		wantString(t, "FHIRTypeOf(Patient.name)", m.FHIRTypeOf("Patient.name"), "")
		wantString(t, "FHIRTypeOf(Quantity.unit): another extension", m.FHIRTypeOf("Quantity.unit"), "")
		wantString(t, "FHIRTypeOf(Quantity.alt): several types", m.FHIRTypeOf("Quantity.alt"), "")
		wantString(t, "TypeOf(Quantity.alt): several types without [x]", m.TypeOf("Quantity.alt"), "")
		wantString(t, "TypeOf(Patient.id)", m.TypeOf("Patient.id"), "http://hl7.org/fhirpath/System.String")
	})
}

// §3.2 regla 4: destinos de referencia por el type de cada targetProfile.
func TestModelReferenceTargets(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		wantStrings(t, "Observation.focus (Reference(Any))", m.ReferenceTargets("Observation.focus"), "Resource")
		wantStrings(t, "Patient.link.other", m.ReferenceTargets("Patient.link.other"), "Patient", "RelatedPerson")
		// Un perfil de Patient resuelve a Patient, y no se repite.
		wantStrings(t, "Observation.subject", m.ReferenceTargets("Observation.subject"), "Patient")
		wantStrings(t, "Patient.managingOrganization", m.ReferenceTargets("Patient.managingOrganization"), "Organization")
		// Cada variante de un [x] trae los destinos de su propio tipo, y el tipo
		// repetido se cuenta una vez en ChoiceTypes.
		wantStrings(t, "ChoiceTypes(Patient.partner)", m.ChoiceTypes("Patient.partner"), "Reference", "canonical")
		wantStrings(t, "Patient.partnerReference", m.ReferenceTargets("Patient.partnerReference"), "RelatedPerson", "Organization")
		wantStrings(t, "Patient.partnerCanonical", m.ReferenceTargets("Patient.partnerCanonical"), "Observation")
		if got := m.ReferenceTargets("Patient.gender"); got != nil {
			t.Errorf("ReferenceTargets(Patient.gender) = %q, want nil", got)
		}
	})
}

// H10: min y max del snapshot.
func TestModelCardinality(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		for _, c := range []struct {
			path     string
			min, max int
		}{
			{"Patient.name", 0, Unbounded}, {"Patient.gender", 0, 1}, {"Observation.status", 1, 1},
			{"Patient.link.other", 1, 1},
			// El elemento con [x] responde por su base y por cada variante.
			{"Patient.deceased", 0, 1}, {"Patient.deceasedBoolean", 0, 1}, {"Patient.deceasedDateTime", 0, 1},
			{"Questionnaire.item", 0, Unbounded},
		} {
			min, max, ok := m.Cardinality(c.path)
			if !ok || min != c.min || max != c.max {
				t.Errorf("Cardinality(%s) = %d..%d %v, want %d..%d", c.path, min, max, ok, c.min, c.max)
			}
		}
		for _, p := range []string{"Patient", "Nope.x", "Patient.deceased[x]"} {
			if _, _, ok := m.Cardinality(p); ok {
				t.Errorf("Cardinality(%s) is known", p)
			}
		}
	})
}

// El padre sale del type del SD de baseDefinition, no del último segmento de su
// URL: aquí el SD base tiene un id (y un nombre en la URL) distinto de su type.
func TestModelParentIsTheTypeNotTheURLSegment(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			base := j("StructureDefinition", "http://example.org/fhir/StructureDefinition/quantity-base", "kind", "complex-type",
				"type", "Quantity2", "abstract", false, "derivation", "specialization", "baseDefinition", sdBase+"Element",
				"status", "active", "version", v.fhir, "snapshot", map[string]any{"element": []any{el("Quantity2", 0, "*")}})
			child := j("StructureDefinition", "http://example.org/fhir/StructureDefinition/child", "kind", "complex-type",
				"type", "Child", "abstract", false, "derivation", "specialization", "baseDefinition", "http://example.org/fhir/StructureDefinition/quantity-base",
				"status", "active", "version", v.fhir, "snapshot", map[string]any{"element": []any{el("Child", 0, "*")}})
			m := buildModelFixture(t, v, base, child).Model()
			wantString(t, "ParentType(Child)", m.ParentType("Child"), "Quantity2")
			if !m.IsSubtype("Child", "Quantity2") || m.HasType("quantity-base") || m.HasType("child") {
				t.Errorf("the URL segment is taken as a type")
			}
		})
	}
}

// El padre es el type del SD de baseDefinition, aunque ese SD sea un perfil: el
// perfil de Patient resuelve a Patient (regla 2).
func TestModelParentOfADefinitionBasedOnAProfile(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			d := j("StructureDefinition", sdBase+"OnProfile", "kind", "resource", "type", "OnProfile", "abstract", false, "derivation", "specialization",
				"baseDefinition", sdBase+"vitalsigns-patient", "status", "active", "version", v.fhir,
				"snapshot", map[string]any{"element": []any{el("OnProfile", 0, "*")}})
			wantString(t, "ParentType", buildModelFixture(t, v, d).Model().ParentType("OnProfile"), "Patient")
		})
	}
}

// Un tipo lo pueden definir packages distintos (regla 1): un SD de un IG con su tipo.
func TestModelTypesFromAnyPackage(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			custom := j("StructureDefinition", "http://example.org/fhir/StructureDefinition/Custom", "kind", "resource",
				"type", "Custom", "abstract", false, "derivation", "specialization", "baseDefinition", sdBase+"DomainResource",
				"status", "active", "version", "1.0.0", "snapshot", map[string]any{"element": []any{
					el("Custom", 0, "*"), el("Custom.thing", 0, "1", ty("string"))}})
			ig := pkg{name: "ig", version: "1.0.0", typ: "IG", canonical: "http://example.org/fhir", fhirVersions: []string{v.fhir},
				deps: map[string]string{"core": v.fhir}, files: fileSet(custom)}
			c, err := tryCtx(t, mcore(v, fixture(v)...), ig)
			if err != nil {
				t.Fatal(err)
			}
			m := c.Model()
			if !m.HasType("Custom") || !m.IsResource("Custom") || m.TypeOf("Custom.thing") != "string" || !m.IsSubtype("Custom", "Resource") {
				t.Errorf("the type of the IG is not in the model")
			}
		})
	}
}

// Dos SD ganadores de URL distinta para un tipo son un error (D4, plan 003 §3.1).
func TestModelTwoDefinitionsOfOneTypeIsAnError(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			other := j("StructureDefinition", "http://example.org/fhir/StructureDefinition/OtherQuantity", "kind", "complex-type",
				"type", "Quantity", "abstract", false, "derivation", "specialization", "baseDefinition", sdBase+"Element",
				"status", "active", "version", v.fhir, "snapshot", map[string]any{"element": []any{el("Quantity", 0, "*")}})
			_, err := tryCtx(t, mcore(v, append(fixture(v), other)...))
			wantErr(t, err, `type "Quantity" is defined by more than one`)
		})
	}
}

// El mismo canónico en dos packages no es un conflicto de tipo: la cascada
// elige uno y el tipo se define una vez.
func TestModelSameCanonicalInTwoPackagesIsOneType(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			dup := fixture(v)[1] // Quantity
			ig := pkg{name: "ig", version: "1.0.0", typ: "IG", canonical: "http://example.org/fhir", fhirVersions: []string{v.fhir},
				deps: map[string]string{"core": v.fhir}, files: fileSet(dup)}
			c, err := tryCtx(t, mcore(v, fixture(v)...), ig)
			if err != nil {
				t.Fatal(err)
			}
			if !c.Model().HasType("Quantity") {
				t.Error("Quantity is not a type")
			}
		})
	}
}

// Fallar cerrado: lo que no se puede leer no construye ningún modelo.
func TestModelBrokenDefinitionsAreErrors(t *testing.T) {
	type brokenCase struct {
		name, sd string
		want     string
	}
	for _, v := range modelVersions {
		bad := func(name string, parent string, elems ...map[string]any) string {
			kv := []any{"kind", "complex-type", "type", name, "abstract", false, "derivation", "specialization", "status", "active", "version", v.fhir,
				"snapshot", map[string]any{"element": elems}}
			if parent != "" {
				kv = append(kv, "baseDefinition", parent)
			}
			return j("StructureDefinition", "http://example.org/fhir/StructureDefinition/"+name, kv...)
		}
		root := el("Bad", 0, "*")
		cases := []brokenCase{
			{"base does not resolve", bad("Bad", "http://example.org/fhir/StructureDefinition/Missing", root), "does not resolve"},
			{"base is a logical model", bad("Bad", sdBase+"Foo", root), "which is not a type"},
			{"base is not a canonical", bad("Bad", "http://x|1|2", root), "base definition"},
			{"path outside the type", bad("Bad", sdBase+"Element", root, el("Other.x", 0, "1", ty("string"))), "not under the type"},
			{"path listed twice", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("string")), el("Bad.x", 0, "1", ty("string"))), "listed twice"},
			{"type without code", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", map[string]any{})), "has no code"},
			{"content reference without a fragment", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "1"), "contentReference", "Bad.y")), "no fragment"},
			{"max is not a count", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "many", ty("string"))), "unbounded mark"},
			{"max is negative", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "-1", ty("string"))), "unbounded mark"},
			{"min is negative", bad("Bad", sdBase+"Element", root, el("Bad.x", -1, "1", ty("string"))), "out of range"},
			{"target profile does not resolve", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("Reference", "NoSuchProfile"))), "target profile"},
			// M1: un snapshot incoherente no construye el modelo (fallar cerrado).
			// elementdefinition.html, contentReference: «an element defined elsewhere
			// in the definition».
			{"content reference to a path that does not exist", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "*"), "contentReference", "#Bad.nope")), "Bad.nope|not an element of this definition"},
			{"content reference to another type", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "*"), "contentReference", "#Patient.link")), "Patient.link|not an element of this definition"},
			{"content reference url of another definition", bad("Bad", sdBase+"Element", root, el("Bad.y", 0, "*", ty("BackboneElement")), with(el("Bad.x", 0, "*"), "contentReference", sdBase+"Questionnaire#Bad.y")), "names another definition|Questionnaire"},
			{"type code that is not a type", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("NoSuchType"))), "NoSuchType|is not a type"},
			{"type code of a profile name", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("vitalsigns-patient"))), "vitalsigns-patient|is not a type"},
			{"choice and an explicit variant", bad("Bad", sdBase+"Element", root, el("Bad.value[x]", 0, "1", ty("string"), ty("integer")), el("Bad.valueString", 0, "1", ty("string"))), "Bad.valueString|also defined"},
			{"explicit variant and then the choice", bad("Bad", sdBase+"Element", root, el("Bad.valueString", 0, "1", ty("string")), el("Bad.value[x]", 0, "1", ty("string"), ty("integer"))), "Bad.valueString|also defined"},
			{"element without type or content reference", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1")), "Bad.x|neither a type nor a content reference"},
			{"choice without types", bad("Bad", sdBase+"Element", root, el("Bad.v[x]", 0, "1")), "Bad.v[x]|choice element has no types"},
			{"min is absent (sdf-3)", bad("Bad", sdBase+"Element", root, without(el("Bad.x", 0, "1", ty("string")), "min")), "Bad.x|sdf-3"},
			{"max is absent (sdf-3)", bad("Bad", sdBase+"Element", root, without(el("Bad.x", 0, "1", ty("string")), "max")), "Bad.x|sdf-3"},
			{"root without cardinality (sdf-3)", bad("Bad", sdBase+"Element", without(el("Bad", 0, "*"), "max")), "Bad|sdf-3"},
			{"slice without cardinality (sdf-3)", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("string")), with(without(el("Bad.x", 0, "1", ty("string")), "max"), "id", "Bad.x:s")), "Bad.x|sdf-3"},
			{"min greater than max (eld-2)", bad("Bad", sdBase+"Element", root, el("Bad.x", 2, "1", ty("string"))), "Bad.x|eld-2"},
			{"snapshot without the root element", bad("Bad", sdBase+"Element", el("Bad.x", 0, "1", ty("string"))), "has no root element"},
			{"orphan path", bad("Bad", sdBase+"Element", root, el("Bad.a.b", 0, "1", ty("string"))), "Bad.a.b|has no parent element"},
			{"snapshot without elements", bad("Bad", sdBase+"Element"), "has no root element"},
			// Ronda 2. sdf-14 (R4, R4B y R5, StructureDefinition): «All element
			// definitions must have an id».
			{"element without an id (sdf-14)", bad("Bad", sdBase+"Element", root, without(el("Bad.x", 0, "1", ty("string")), "id")), "Bad.x|sdf-14"},
			{"element with an empty id (sdf-14)", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "1", ty("string")), "id", "")), "Bad.x|sdf-14"},
			{"slice without an id (sdf-14)", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("string")), without(el("Bad.x", 0, "1", ty("string")), "id")), "sdf-14"},
			{"slice mark in the path", bad("Bad", sdBase+"Element", root, el("Bad.a:b", 0, "1", ty("string"))), "Bad.a:b|slice mark"},
			{"slice mark in the path with a plain id", bad("Bad", sdBase+"Element", root, with(el("Bad.a:b", 0, "1", ty("string")), "id", "Bad.a")), "Bad.a:b|slice mark"},
			{"slice mark in the root path", bad("Bad", sdBase+"Element", with(el("Bad", 0, "*"), "path", "Bad:s", "id", "Bad")), "slice mark"},
			// eld-5: «if the element definition has a contentReference, it cannot
			// have type ...» (R4, R4B y R5, ElementDefinition).
			{"content reference and type (eld-5)", bad("Bad", sdBase+"Element", root, el("Bad.y", 0, "*", ty("BackboneElement")), with(el("Bad.x", 0, "*", ty("string")), "contentReference", "#Bad.y")), "Bad.x|eld-5"},
			// elementdefinition.html: el contentReference aporta las reglas del
			// destino; si el destino es otro contentReference, no aporta nada.
			{"content reference chain", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "*"), "contentReference", "#Bad.y"), with(el("Bad.y", 0, "*"), "contentReference", "#Bad.z"), el("Bad.z", 0, "*", ty("BackboneElement"))), "Bad.x|points to an element that has a content reference"},
			{"content reference cycle", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "*"), "contentReference", "#Bad.y"), with(el("Bad.y", 0, "*"), "contentReference", "#Bad.x")), "has a content reference"},
			{"content reference to itself", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "*"), "contentReference", "#Bad.x")), "Bad.x|has a content reference"},
			// elementdefinition.html, «Choice types»: value y value[x] son el mismo
			// nombre; la cardinalidad de uno pisaría la del otro.
			{"element and its choice", bad("Bad", sdBase+"Element", root, el("Bad.value", 0, "1", ty("string")), el("Bad.value[x]", 1, "1", ty("string"), ty("integer"))), "Bad.value|also defined as an element of its own"},
			{"choice and then its element", bad("Bad", sdBase+"Element", root, el("Bad.value[x]", 1, "1", ty("string"), ty("integer")), el("Bad.value", 0, "1", ty("string"))), "Bad.value|also defined as an element of its own"},
			// extension-structuredefinition-fhir-type.html: los tipos del sistema son
			// los que el núcleo declara en sus primitivos.
			{"system type that the core does not declare", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", sysTy("Bogus", "string"))), "System.Bogus|is not a type"},
			{"system type prefix alone", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", sysTy("", "string"))), "is not a type"},
			// Ronda 3. elementdefinition.html, «Choice types»: el nombre de una
			// variante es el del elemento más el tipo; otro camino o variante con el
			// mismo nombre pisaría su cardinalidad y su tipo.
			{"variant equal to the base of another choice", bad("Bad", sdBase+"Element", root, el("Bad.value[x]", 0, "1", ty("Quantity"), ty("string")), el("Bad.valueQuantity[x]", 0, "*", ty("string"), ty("integer"))), "Bad.valueQuantity[x]|collides"},
			{"base of a choice equal to a variant of another", bad("Bad", sdBase+"Element", root, el("Bad.valueQuantity[x]", 0, "*", ty("string"), ty("integer")), el("Bad.value[x]", 0, "1", ty("Quantity"), ty("string"))), "Bad.value[x]|collides"},
			{"slice without its base element", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "1", ty("string")), "id", "Bad.x:s")), "Bad.x:s|no base element"},
			{"slice before its base element", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "1", ty("string")), "id", "Bad.x:s"), el("Bad.x", 0, "1", ty("string"))), "Bad.x:s|no base element"},
			{"child of a slice without the slice", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("BackboneElement")), with(el("Bad.x.y", 0, "1", ty("string")), "id", "Bad.x:s.y")), "Bad.x:s.y|no base element"},
			{"id unrelated to the path", bad("Bad", sdBase+"Element", root, with(el("Bad.x", 0, "1", ty("string")), "id", "Zzz")), "Bad.x|id \"Zzz\" does not match"},
			{"slice id of another path", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", ty("string")), el("Bad.y", 0, "1", ty("string")), with(el("Bad.x", 0, "1", ty("string")), "id", "Bad.y:s")), "Bad.y:s|does not match"},
			{"system type among several types", bad("Bad", sdBase+"Element", root, el("Bad.x", 0, "1", sysTy("String", "string"), ty("integer"))), "System.String|only in an element of one type"},
			{"system type in a choice", bad("Bad", sdBase+"Element", root, el("Bad.v[x]", 0, "1", sysTy("String", "string"), ty("integer"))), "System.String|only in an element of one type"},
			{"system type alone in a choice", bad("Bad", sdBase+"Element", root, el("Bad.v[x]", 0, "1", sysTy("String", "string"))), "System.String|only in an element of one type"},
			// elementdefinition.html, contentReference: el contenido del elemento es
			// el del destino; con hijos propios el modelo sería ambiguo.
			{"content reference with children", bad("Bad", sdBase+"Element", root, el("Bad.y", 0, "*", ty("BackboneElement")), with(el("Bad.x", 0, "*"), "contentReference", "#Bad.y"), el("Bad.x.z", 0, "1", ty("string"))), "Bad.x|content reference|children"},
			{"content reference to the child of another", bad("Bad", sdBase+"Element", root, el("Bad.y", 0, "*", ty("BackboneElement")), with(el("Bad.a", 0, "*"), "contentReference", "#Bad.y"), el("Bad.a.c", 0, "1", ty("string")), with(el("Bad.x", 0, "*"), "contentReference", "#Bad.a.c")), "Bad.a|content reference|children"},
			{"snapshot is not an object", strings.Replace(bad("Bad", sdBase+"Element", root), `"snapshot":{"element":[`, `"snapshot":{"element":[1,`, 1), "cannot unmarshal"},
		}
		for _, c := range cases {
			t.Run(v.name+"/"+c.name, func(t *testing.T) {
				_, err := tryCtx(t, mcore(v, append(fixture(v), c.sd)...))
				wantErr(t, err, strings.Split(c.want, "|")...)
			})
		}
		t.Run(v.name+"/system type of a package that is not the core", func(t *testing.T) {
			// Un primitivo de un IG no amplía el conjunto de tipos del sistema.
			prim := j("StructureDefinition", "http://example.org/fhir/StructureDefinition/igprim", "kind", "primitive-type", "type", "igprim", "abstract", false,
				"derivation", "specialization", "baseDefinition", sdBase+"Element", "status", "active", "version", v.fhir,
				"snapshot", map[string]any{"element": []any{el("igprim", 0, "*"), el("igprim.value", 0, "1", sysTy("Bogus", "string"))}})
			ig := pkg{name: "ig", version: "1.0.0", typ: "IG", canonical: "http://example.org/fhir", fhirVersions: []string{v.fhir},
				deps: map[string]string{"core": v.fhir}, files: fileSet(prim)}
			_, err := tryCtx(t, mcore(v, fixture(v)...), ig)
			wantErr(t, err, "System.Bogus", "is not a type")
		})
		t.Run(v.name+"/core primitive overridden by another package", func(t *testing.T) {
			// Si un IG gana la URL de un primitivo del núcleo, los tipos del sistema
			// ya no salen del núcleo: el error lo dice, no «is not a type».
			over := j("StructureDefinition", sdBase+"date", "kind", "primitive-type", "type", "date", "abstract", false,
				"derivation", "specialization", "baseDefinition", sdBase+"Element", "status", "active", "version", "99.0.0",
				"snapshot", map[string]any{"element": []any{el("date", 0, "*"), el("date.value", 0, "1", sysTy("Date", "date"))}})
			ig := pkg{name: "ig", version: "1.0.0", typ: "IG", canonical: hl7, fhirVersions: []string{v.fhir},
				deps: map[string]string{"core": v.fhir}, files: fileSet(over)}
			_, err := tryCtx(t, mcore(v, fixture(v)...), ig)
			wantErr(t, err, "primitive", "date", "overridden")
		})
		t.Run(v.name+"/cycle", func(t *testing.T) {
			a := j("StructureDefinition", sdBase+"CycA", "kind", "complex-type", "type", "CycA", "derivation", "specialization", "baseDefinition", sdBase+"CycB",
				"status", "active", "version", v.fhir, "snapshot", map[string]any{"element": []any{el("CycA", 0, "*")}})
			b := j("StructureDefinition", sdBase+"CycB", "kind", "complex-type", "type", "CycB", "derivation", "specialization", "baseDefinition", sdBase+"CycA",
				"status", "active", "version", v.fhir, "snapshot", map[string]any{"element": []any{el("CycB", 0, "*")}})
			_, err := tryCtx(t, mcore(v, append(fixture(v), a, b)...))
			wantErr(t, err, "inheritance cycle")
		})
		t.Run(v.name+"/unresolved definition of a type", func(t *testing.T) {
			// El mismo SD en dos IG, sin nada que los separe: no se puede tomar
			// el tipo de ninguno.
			dup := tsdSameURL(v)
			a := pkg{name: "ig-a", version: "1.0.0", typ: "IG", canonical: hl7, fhirVersions: []string{v.fhir}, deps: map[string]string{"core": v.fhir}, files: fileSet(dup)}
			b := pkg{name: "ig-b", version: "1.0.0", typ: "IG", canonical: hl7, fhirVersions: []string{v.fhir}, deps: map[string]string{"core": v.fhir}, files: fileSet(dup)}
			_, err := tryCtx(t, mcore(v, fixture(v)...), a, b)
			wantErr(t, err, "Shared", "unresolved")
		})
	}
}

// tsdSameURL: una definición de Shared.
func tsdSameURL(v modelVersion) string {
	return j("StructureDefinition", sdBase+"Shared", "kind", "complex-type", "type", "Shared", "abstract", false, "derivation", "specialization",
		"baseDefinition", sdBase+"Element", "status", "active", "snapshot", map[string]any{"element": []any{el("Shared", 0, "*")}})
}

// dump reúne todo lo que el modelo responde, para comparar dos modelos.
func dump(m *Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version %s\n", m.FHIRVersion())
	for _, p := range m.Paths() {
		min, max, ok := m.Cardinality(p)
		fmt.Fprintf(&b, "%s|type=%s|parent=%s|res=%v|has=%v|choice=%v|refs=%v|to=%s|kids=%v|fhir=%s|card=%d,%d,%v\n", p,
			m.TypeOf(p), m.ParentType(p), m.IsResource(p), m.HasType(p), m.ChoiceTypes(p), m.ReferenceTargets(p),
			m.ResolvePath(p), m.ChildElements(p), m.FHIRTypeOf(p), min, max, ok)
	}
	return b.String()
}

// Metamórfico: el orden de los recursos y su reparto en packages no cambia el modelo.
func TestModelDoesNotDependOnOrder(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			rs := fixture(v)
			want := dump(buildModelFixture(t, v).Model())
			for seed := 0; seed < 8; seed++ {
				p := slices.Clone(rs)
				// Orden determinista y distinto en cada vuelta.
				sort.SliceStable(p, func(i, j int) bool { return (i*7+seed*13)%len(p) < (j*7+seed*13)%len(p) })
				if seed%2 == 1 {
					slices.Reverse(p)
				}
				if got := dump(buildModelFixture(t, v, nil...).Model()); got != want {
					t.Fatal("the fixture is not deterministic")
				}
				c, err := tryCtx(t, mcore(v, p...))
				if err != nil {
					t.Fatal(err)
				}
				if got := dump(c.Model()); got != want {
					t.Errorf("seed %d: the model changed with the order of the resources", seed)
				}
			}
			// El mismo contenido repartido en dos packages.
			half := len(rs) / 2
			ig := pkg{name: "ig", version: "1.0.0", typ: "IG", canonical: "http://example.org/fhir", fhirVersions: []string{v.fhir},
				deps: map[string]string{"core": v.fhir}, files: fileSet(rs[half:]...)}
			c, err := tryCtx(t, mcore(v, rs[:half]...), ig)
			if err != nil {
				t.Fatal(err)
			}
			if got := dump(c.Model()); got != want {
				t.Error("the model changed when the definitions were split between two packages")
			}
		})
	}
}

// El Model es inmutable: se usa desde varias goroutines (go test -race).
func TestModelIsConcurrent(t *testing.T) {
	c := buildModelFixture(t, modelVersions[2])
	m := c.Model()
	want := dump(m)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 20; k++ {
				if dump(c.Model()) != want {
					t.Error("the model changed")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// Types es una copia; Paths incluye los tipos y los caminos.
func TestModelTypesAndPaths(t *testing.T) {
	m := buildModelFixture(t, modelVersions[0]).Model()
	ts := m.Types()
	wantStrings(t, "Types", ts, "Age", "BackboneElement", "CodeableConcept", "DomainResource", "Element", "Extension", "HumanName", "Linked", "Meta", "Narrative",
		"Observation", "Organization", "Patient", "Quantity", "Questionnaire", "Reference", "RelatedPerson", "Resource",
		"boolean", "canonical", "code", "date", "dateTime", "decimal", "integer", "string")
	ts[0] = "changed"
	if m.Types()[0] != "Age" {
		t.Error("Types returns the model's own slice")
	}
	if !slices.IsSorted(m.Paths()) || !slices.Contains(m.Paths(), "Patient.name") || !slices.Contains(m.Paths(), "Patient") {
		t.Error("Paths is not sorted or lacks a path")
	}
}

// El modelo no guarda más de lo que usa: el mismo Context da el mismo Model.
func TestContextModelIsBuiltOnce(t *testing.T) {
	c := buildModelFixture(t, modelVersions[1])
	if c.Model() != c.Model() || !reflect.DeepEqual(c.Model().Types(), c.Model().Types()) {
		t.Error("Model() builds a new model on each call")
	}
}

// B2 (R5 sdf-27: «If there's a base definition, there must be a derivation»; la
// misma regla se aplica a R4 y R4B): sin derivation solo define un tipo el que
// no tiene baseDefinition (la raíz de R4 y R4B).
func TestModelBaseDefinitionWithoutDerivationIsNotAType(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			sd := j("StructureDefinition", sdBase+"NoDerivation", "kind", "complex-type", "type", "NoDerivation", "abstract", false,
				"baseDefinition", sdBase+"Element", "status", "active", "version", v.fhir,
				"snapshot", map[string]any{"element": []any{el("NoDerivation", 0, "*")}})
			m := buildModelFixture(t, v, sd).Model()
			if m.HasType("NoDerivation") {
				t.Error("a definition with a base definition and no derivation is a type")
			}
			if !m.HasType("Element") || !m.HasType("Resource") {
				t.Error("the roots, which have neither, must be types")
			}
		})
	}
}

// checkModelInvariants exige los invariantes que valen para todo modelo que se
// construye: lo usan TestRealModelInvariants (los núcleos) y el fuzz (cualquier
// SD que NewContext acepte). Cada tabla debe referirse solo a tipos y caminos
// que el modelo conoce.
func checkModelInvariants(t testing.TB, c *Context) {
	t.Helper()
	m := c.Model()
	if got := m.FHIRVersion(); got != c.set.lock.FHIRVersion {
		t.Errorf("FHIRVersion = %q, want %q", got, c.set.lock.FHIRVersion)
	}
	types := m.Types()
	// Los recursos del modelo son los tipos concretos del 002.
	var resources []string
	for _, ty := range types {
		if m.IsResource(ty) {
			resources = append(resources, ty)
		}
	}
	if !slices.Equal(resources, c.ResourceTypes()) {
		t.Errorf("IsResource gives %d types, ResourceTypes %d", len(resources), len(c.ResourceTypes()))
	}
	for _, ty := range types {
		if p := m.ParentType(ty); p != "" && !m.HasType(p) {
			t.Errorf("the parent %q of %s is not a type", p, ty)
		}
		if !m.IsSubtype(ty, ty) {
			t.Errorf("IsSubtype(%s, %s) = false", ty, ty)
		}
	}
	for _, p := range m.Paths() {
		if strings.Contains(p, ":") {
			t.Errorf("the path %q has a slice mark", p)
		}
		// Todo hijo cuelga de un tipo o de un elemento que el modelo conoce.
		kids := m.ChildElements(p)
		root, _, _ := strings.Cut(p, ".")
		if kids != nil && !m.HasType(root) {
			t.Errorf("%s has children and %s is not a type", p, root)
		}
		if ty := m.TypeOf(p); ty != "" && !strings.HasPrefix(ty, systemTypePrefix) && !m.HasType(ty) {
			t.Errorf("TypeOf(%s) = %q, which is not a type", p, ty)
		}
		for _, target := range m.ReferenceTargets(p) {
			if !m.HasType(target) {
				t.Errorf("ReferenceTargets(%s) has %q, which is not a type", p, target)
			}
		}
		if to := m.ResolvePath(p); to != p {
			// El destino de un contentReference es un elemento del mismo
			// StructureDefinition: tiene cardinalidad (M1).
			if _, _, ok := m.Cardinality(to); !ok && !m.HasType(to) {
				t.Errorf("ResolvePath(%s) = %s, which is not an element of the model", p, to)
			}
		}
		for _, ct := range m.ChoiceTypes(p) {
			if m.TypeOf(p+strings.ToUpper(ct[:1])+ct[1:]) != ct {
				t.Errorf("the variant of %s for %s has another type", p, ct)
			}
		}
	}
}

// Plan 004, D6: IsPrimitive sale de kind primitive-type del StructureDefinition.
func TestModelIsPrimitive(t *testing.T) {
	forVersions(t, func(t *testing.T, v modelVersion, m *Model) {
		for name, want := range map[string]bool{
			"string": true, "decimal": true, "date": true, "boolean": true, "code": true,
			"Quantity": false, "Age": false, "Patient": false, "Resource": false, "Element": false,
			"Foo": false, "Nope": false, "": false, "http://hl7.org/fhirpath/System.String": false,
		} {
			if got := m.IsPrimitive(name); got != want {
				t.Errorf("IsPrimitive(%q) = %v, want %v", name, got, want)
			}
		}
	})
}
