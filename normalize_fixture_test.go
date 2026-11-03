package fhirpkg

import (
	"os"
	"testing"
)

// El núcleo sintético del normalizador (plan 004 §4): un subconjunto de FHIR con
// los elementos en el orden del snapshot oficial, de modo que los mismos casos
// a mano corran sobre él en make check y sobre los núcleos oficiales en
// make check-packages. Cada tipo trae sus elementos heredados, como un snapshot.

// normPrims son los primitivos de la fixture y su tipo del sistema.
var normPrims = []struct{ name, system string }{
	{"boolean", "Boolean"}, {"integer", "Integer"}, {"string", "String"}, {"code", "String"},
	{"id", "String"}, {"uri", "String"}, {"canonical", "String"}, {"date", "Date"},
	{"dateTime", "DateTime"}, {"instant", "DateTime"}, {"decimal", "Decimal"}, {"xhtml", "String"},
	{"positiveInt", "Integer"},
}

// normPrimBase es el tipo base de los primitivos que derivan de otro primitivo,
// como en los StructureDefinition oficiales (code e id desde string, canonical
// desde uri, positiveInt desde integer).
var normPrimBase = map[string]string{"code": "string", "id": "string", "canonical": "uri", "positiveInt": "integer"}

func normFixture(v modelVersion) []string {
	r5 := v.name == "R5"
	idTy := func() map[string]any { return sysTy("String", idType(v)) }
	elemBase, resBase, primBase := "", "", "Element"
	if r5 {
		elemBase, resBase, primBase = "Base", "Base", "PrimitiveType"
	}
	var out []string
	if r5 {
		out = append(out,
			tsd(v, "Base", "complex-type", "", true, el("Base", 0, "*")),
			tsd(v, "DataType", "complex-type", "Element", true, el("DataType", 0, "*"),
				el("DataType.id", 0, "1", idTy()), el("DataType.extension", 0, "*", ty("Extension"))),
			tsd(v, "PrimitiveType", "complex-type", "DataType", true, el("PrimitiveType", 0, "*"),
				el("PrimitiveType.id", 0, "1", idTy()), el("PrimitiveType.extension", 0, "*", ty("Extension"))))
	}
	out = append(out,
		tsd(v, "Element", "complex-type", elemBase, true, el("Element", 0, "*"),
			el("Element.id", 0, "1", idTy()), el("Element.extension", 0, "*", ty("Extension"))),
		tsd(v, "BackboneElement", "complex-type", "Element", false, el("BackboneElement", 0, "*"),
			el("BackboneElement.id", 0, "1", idTy()), el("BackboneElement.extension", 0, "*", ty("Extension")),
			el("BackboneElement.modifierExtension", 0, "*", ty("Extension"))))
	for _, p := range normPrims {
		ext := el(p.name+".extension", 0, "*", ty("Extension"))
		if p.name == "xhtml" {
			ext = el(p.name+".extension", 0, "0", ty("Extension")) // xhtml.extension: max 0, como en el SD oficial
		}
		sys := sysTy(p.system, p.name)
		parent := primBase
		if p.name == "xhtml" {
			parent = "Element"
		}
		if b, ok := normPrimBase[p.name]; ok {
			parent = b
		}
		out = append(out, tsd(v, p.name, "primitive-type", parent, false, el(p.name, 0, "*"),
			el(p.name+".id", 0, "1", idTy()), ext, el(p.name+".value", 0, "1", sys)))
	}
	cplx := func(name string, elems ...map[string]any) {
		all := append([]map[string]any{el(name, 0, "*"), el(name+".id", 0, "1", idTy()), el(name+".extension", 0, "*", ty("Extension"))}, elems...)
		out = append(out, tsd(v, name, "complex-type", "Element", false, all...))
	}
	cplx("Extension", el("Extension.url", 1, "1", sysTy("String", "uri")),
		el("Extension.value[x]", 0, "1", ty("boolean"), ty("integer"), ty("string"), ty("Quantity"), ty("Reference"),
			ty("code"), ty("canonical"), ty("positiveInt")))
	cplx("Meta", el("Meta.versionId", 0, "1", ty("id")), el("Meta.lastUpdated", 0, "1", ty("instant")), el("Meta.profile", 0, "*", ty("canonical")))
	cplx("Narrative", el("Narrative.status", 1, "1", ty("code")), el("Narrative.div", 1, "1", ty("xhtml")))
	cplx("Coding", el("Coding.system", 0, "1", ty("uri")), el("Coding.code", 0, "1", ty("code")), el("Coding.display", 0, "1", ty("string")))
	cplx("CodeableConcept", el("CodeableConcept.coding", 0, "*", ty("Coding")), el("CodeableConcept.text", 0, "1", ty("string")))
	cplx("HumanName", el("HumanName.use", 0, "1", ty("code")), el("HumanName.text", 0, "1", ty("string")),
		el("HumanName.family", 0, "1", ty("string")), el("HumanName.given", 0, "*", ty("string")), el("HumanName.prefix", 0, "*", ty("string")))
	cplx("Quantity", el("Quantity.value", 0, "1", ty("decimal")), el("Quantity.unit", 0, "1", ty("string")),
		el("Quantity.system", 0, "1", ty("uri")), el("Quantity.code", 0, "1", ty("code")))
	cplx("Reference", el("Reference.reference", 0, "1", ty("string")), el("Reference.display", 0, "1", ty("string")))

	res := func(name, parent string, abstract bool, elems ...map[string]any) {
		out = append(out, tsd(v, name, "resource", parent, abstract, append([]map[string]any{el(name, 0, "*")}, elems...)...))
	}
	resBaseElems := []map[string]any{
		el("X.id", 0, "1", sysTy("String", idType(v))), el("X.meta", 0, "1", ty("Meta")),
		el("X.implicitRules", 0, "1", ty("uri")), el("X.language", 0, "1", ty("code")),
	}
	domElems := []map[string]any{
		el("X.text", 0, "1", ty("Narrative")), el("X.contained", 0, "*", ty("Resource")),
		el("X.extension", 0, "*", ty("Extension")), el("X.modifierExtension", 0, "*", ty("Extension")),
	}
	rename := func(name string, elems ...[]map[string]any) []map[string]any {
		var all []map[string]any
		for _, es := range elems {
			for _, e := range es {
				c := map[string]any{}
				for k, val := range e {
					c[k] = val
				}
				p := e["path"].(string)
				c["path"], c["id"] = name+p[1:], name+p[1:]
				all = append(all, c)
			}
		}
		return all
	}
	res("Resource", resBase, true, rename("Resource", resBaseElems)...)
	res("DomainResource", "Resource", true, rename("DomainResource", resBaseElems, domElems)...)
	res("Patient", "DomainResource", false, append(rename("Patient", resBaseElems, domElems),
		el("Patient.active", 0, "1", ty("boolean")), el("Patient.name", 0, "*", ty("HumanName")),
		el("Patient.gender", 0, "1", ty("code")), el("Patient.birthDate", 0, "1", ty("date")),
		el("Patient.deceased[x]", 0, "1", ty("boolean"), ty("dateTime")),
		el("Patient.multipleBirth[x]", 0, "1", ty("boolean"), ty("integer")),
		el("Patient.contact", 0, "*", ty("BackboneElement")),
		el("Patient.contact.id", 0, "1", idTy()), el("Patient.contact.extension", 0, "*", ty("Extension")),
		el("Patient.contact.modifierExtension", 0, "*", ty("Extension")),
		el("Patient.contact.name", 0, "1", ty("HumanName")),
		el("Patient.managingOrganization", 0, "1", ty("Reference")))...)
	res("Observation", "DomainResource", false, append(rename("Observation", resBaseElems, domElems),
		el("Observation.status", 1, "1", ty("code")), el("Observation.code", 1, "1", ty("CodeableConcept")),
		el("Observation.subject", 0, "1", ty("Reference")),
		el("Observation.value[x]", 0, "1", ty("Quantity"), ty("CodeableConcept"), ty("string"), ty("integer")))...)
	bundle := append(rename("Bundle", resBaseElems),
		el("Bundle.type", 1, "1", ty("code")), el("Bundle.total", 0, "1", ty("integer")),
		el("Bundle.entry", 0, "*", ty("BackboneElement")),
		el("Bundle.entry.id", 0, "1", idTy()), el("Bundle.entry.extension", 0, "*", ty("Extension")),
		el("Bundle.entry.modifierExtension", 0, "*", ty("Extension")),
		el("Bundle.entry.fullUrl", 0, "1", ty("uri")), el("Bundle.entry.resource", 0, "1", ty("Resource")),
		el("Bundle.entry.response", 0, "1", ty("BackboneElement")),
		el("Bundle.entry.response.id", 0, "1", idTy()), el("Bundle.entry.response.extension", 0, "*", ty("Extension")),
		el("Bundle.entry.response.modifierExtension", 0, "*", ty("Extension")),
		el("Bundle.entry.response.status", 1, "1", ty("string")), el("Bundle.entry.response.outcome", 0, "1", ty("Resource")),
		el("Bundle.domainOnly", 0, "1", ty("DomainResource")))
	if r5 {
		bundle = append(bundle, el("Bundle.issues", 0, "1", ty("Resource")))
	}
	res("Bundle", "Resource", false, bundle...)
	res("Parameters", "Resource", false, append(rename("Parameters", resBaseElems),
		el("Parameters.parameter", 0, "*", ty("BackboneElement")),
		el("Parameters.parameter.id", 0, "1", idTy()), el("Parameters.parameter.extension", 0, "*", ty("Extension")),
		el("Parameters.parameter.modifierExtension", 0, "*", ty("Extension")),
		el("Parameters.parameter.name", 1, "1", ty("string")), el("Parameters.parameter.value[x]", 0, "1", ty("boolean"), ty("string")),
		el("Parameters.parameter.resource", 0, "1", ty("Resource")),
		with(el("Parameters.parameter.part", 0, "*"), "contentReference", "#Parameters.parameter"))...)
	_ = resBase
	return out
}

// normContext arma el Context de la fixture de la versión.
func normContext(t testing.TB, v modelVersion) *Context {
	t.Helper()
	c, err := tryCtx(t, mcore(v, normFixture(v)...))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// normTarget es un Context contra el que corren los casos: la fixture o un
// núcleo oficial.
type normTarget struct {
	name string
	ver  string // R4, R4B o R5
	ctx  *Context
}

// normTargets son la fixture de las tres versiones y, con los packages
// oficiales (make check-packages), los tres núcleos.
func normTargets(t testing.TB) []normTarget {
	t.Helper()
	var out []normTarget
	for _, v := range modelVersions {
		out = append(out, normTarget{"fixture-" + v.name, v.name, normContext(t, v)})
	}
	if os.Getenv("FHIRPKG_PACKAGES") != "" {
		for _, rc := range realModelCases {
			c, _ := realContext(t, rc.lock)
			out = append(out, normTarget{"real-" + rc.name, rc.name, c})
		}
	}
	return out
}
