package fhirpkg

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

// La fixture de la proyección (plan 005): la del normalizador con los flags
// isSummary e isModifier que los núcleos oficiales ponen, más los elementos que
// los casos a mano necesitan (Meta.tag, un modificador que no es del resumen, un
// recurso con Narrative fuera de la raíz). Los flags imitan a los de R4, R4B y R5
// (plan 005, revisión previa §2): Resource.id y meta del resumen, language, text,
// contained y extension no; DomainResource.modifierExtension del resumen solo en
// R5; Element.id y .extension nunca; un obligatorio de la raíz sin isSummary
// (Observation.status aquí; en los núcleos, Appointment.participant).

var projSummaryPaths = []string{
	"Patient.active", "Patient.name", "Patient.gender", "Patient.birthDate", "Patient.deceased[x]",
	"Patient.multipleBirth[x]", "Patient.managingOrganization", "Patient.contact.name", "Patient.contact.modifierExtension",
	"Observation.code", "Observation.subject", "Observation.value[x]",
	"Bundle.type", "Bundle.total", "Bundle.entry", "Bundle.entry.fullUrl", "Bundle.entry.resource", "Bundle.entry.response",
	"Bundle.entry.response.status", "Bundle.entry.response.outcome", "Bundle.entry.modifierExtension", "Bundle.entry.response.modifierExtension",
	"Bundle.issues",
	"Parameters.parameter", "Parameters.parameter.name", "Parameters.parameter.value[x]", "Parameters.parameter.resource",
	"Parameters.parameter.part", "Parameters.parameter.modifierExtension",
	"Composition.status", "Composition.title", "Composition.section.modifierExtension",
	"BackboneElement.modifierExtension",
	"HumanName.use", "HumanName.text", "HumanName.family", "HumanName.given", "HumanName.prefix",
	"Quantity.value", "Quantity.unit", "Quantity.system", "Quantity.code",
	"Coding.system", "Coding.code", "Coding.display", "CodeableConcept.coding", "CodeableConcept.text",
	"Reference.reference", "Reference.display",
	"Meta.versionId", "Meta.lastUpdated", "Meta.profile", "Meta.source", "Meta.security", "Meta.tag",
}

var projModifierPaths = []string{
	"Patient.active", "Patient.deceased[x]", "Patient.contact.flag", "Patient.contact.modifierExtension",
	"Bundle.entry.modifierExtension", "Bundle.entry.response.modifierExtension", "Parameters.parameter.modifierExtension",
	"Composition.section.modifierExtension", "BackboneElement.modifierExtension", "Observation.status",
}

// projDomainTypes son los recursos que descienden de DomainResource en la fixture.
var projDomainTypes = []string{"DomainResource", "Patient", "Observation", "Composition"}

// projExtraSummary marca como isSummary caminos extra de la fixture, solo mientras
// un test arma su propio Context (por defecto Element.extension no lo es).
var projExtraSummary map[string]bool

func projFlags(v modelVersion, path string) (summary, modifier bool) {
	in := func(list []string) bool {
		for _, p := range list {
			if p == path {
				return true
			}
		}
		return false
	}
	summary, modifier = in(projSummaryPaths) || projExtraSummary[path], in(projModifierPaths)
	root, rest, _ := strings.Cut(path, ".")
	switch rest {
	case "id", "meta", "implicitRules":
		if strings.Contains(" Resource DomainResource Patient Observation Bundle Parameters Composition ", " "+root+" ") {
			summary = true
			modifier = rest == "implicitRules"
		}
	case "modifierExtension":
		for _, d := range projDomainTypes {
			if d == root {
				summary, modifier = v.name == "R5", true
			}
		}
	}
	return
}

// projFixture es la fixture del normalizador con los flags y los extras.
func projFixture(v modelVersion) []string {
	var out []string
	for _, s := range normFixture(v) {
		var sd map[string]any
		if err := json.Unmarshal([]byte(s), &sd); err != nil {
			panic(err)
		}
		snap, ok := sd["snapshot"].(map[string]any)
		if !ok {
			out = append(out, s)
			continue
		}
		els := snap["element"].([]any)
		switch sd["type"] {
		case "Meta":
			els = append(els, el("Meta.source", 0, "1", ty("string")), el("Meta.security", 0, "*", ty("Coding")), el("Meta.tag", 0, "*", ty("Coding")))
		case "Patient":
			els = append(els, el("Patient.contact.flag", 0, "1", ty("boolean")))
		}
		for i, e := range els {
			m, ok := e.(map[string]any)
			if !ok {
				m = map[string]any(e.(map[string]any))
			}
			sum, mod := projFlags(v, m["path"].(string))
			m["isSummary"], m["isModifier"] = sum, mod
			els[i] = m
		}
		snap["element"] = els
		b, _ := json.Marshal(sd)
		out = append(out, string(b))
	}
	// Composition: Narrative fuera de la raíz (Composition.section.text).
	inh := func(name string) []any {
		var es []any
		for _, e := range []map[string]any{
			el(name+".id", 0, "1", sysTy("String", idType(v))), el(name+".meta", 0, "1", ty("Meta")),
			el(name+".implicitRules", 0, "1", ty("uri")), el(name+".language", 0, "1", ty("code")),
			el(name+".text", 0, "1", ty("Narrative")), el(name+".contained", 0, "*", ty("Resource")),
			el(name+".extension", 0, "*", ty("Extension")), el(name+".modifierExtension", 0, "*", ty("Extension")),
		} {
			es = append(es, e)
		}
		return es
	}
	els := append([]any{el("Composition", 0, "*")}, inh("Composition")...)
	for _, e := range []map[string]any{
		el("Composition.status", 1, "1", ty("code")), el("Composition.title", 1, "1", ty("string")),
		el("Composition.section", 0, "*", ty("BackboneElement")),
		el("Composition.section.id", 0, "1", sysTy("String", idType(v))), el("Composition.section.extension", 0, "*", ty("Extension")),
		el("Composition.section.modifierExtension", 0, "*", ty("Extension")),
		el("Composition.section.title", 0, "1", ty("string")), el("Composition.section.text", 0, "1", ty("Narrative")),
	} {
		els = append(els, e)
	}
	for _, e := range els {
		m := e.(map[string]any)
		m["isSummary"], m["isModifier"] = func() (bool, bool) { return projFlags(v, m["path"].(string)) }()
	}
	kv := []any{"kind", "resource", "type", "Composition", "abstract", false, "status", "active", "version", v.fhir,
		"snapshot", map[string]any{"element": els}, "derivation", "specialization", "baseDefinition", sdBase + "DomainResource"}
	out = append(out, j("StructureDefinition", sdBase+"Composition", kv...))
	return out
}

// subsettedCodeSystem es el CodeSystem del tag, con el concepto anidado como en
// la terminología de HL7 (v3-ObservationValue).
func subsettedCodeSystem() string {
	return j("CodeSystem", systemSubsetted, "status", "active", "version", "1.0.0", "content", "complete",
		"concept", []any{map[string]any{"code": "_ActCode", "display": "act code", "concept": []any{
			map[string]any{"code": codeSubsetted, "display": "Resource subsetted (fixture)"}}}})
}

// projContext arma el Context de la fixture; withTags=false omite el CodeSystem.
func projContext(t testing.TB, v modelVersion, withCodeSystem bool) *Context {
	t.Helper()
	rs := projFixture(v)
	if withCodeSystem {
		rs = append(rs, subsettedCodeSystem())
	}
	c, err := tryCtx(t, mcore(v, rs...))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// projTarget es un Context contra el que corren los casos: la fixture o un
// núcleo oficial (con FHIRPKG_PACKAGES).
type projTarget struct {
	name, ver string
	real      bool
	ctx       *Context
}

var (
	projTargetsOnce sync.Once
	projTargetsAll  []projTarget
)

// projTargets arma una vez los Context (son inmutables): los de la fixture y,
// con los packages, los de los tres núcleos.
func projTargets(t testing.TB) []projTarget {
	t.Helper()
	projTargetsOnce.Do(func() {
		for _, v := range modelVersions {
			projTargetsAll = append(projTargetsAll, projTarget{"fixture-" + v.name, v.name, false, projContext(t, v, true)})
		}
		if realPackages() {
			for _, rc := range realModelCases {
				c, _ := realContext(t, rc.lock)
				projTargetsAll = append(projTargetsAll, projTarget{"real-" + rc.name, rc.name, true, c})
			}
		}
	})
	return projTargetsAll
}

func realPackages() bool { return os.Getenv("FHIRPKG_PACKAGES") != "" }
