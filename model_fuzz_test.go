package fhirpkg

import (
	"testing"
)

// FuzzModelFromStructureDefinition: un SD sintético con una ruta, un código de
// tipo, un contentReference, un targetProfile, un min y un max arbitrarios. El
// modelo se construye o da error; nunca entra en pánico, y si se construye
// cumple los invariantes de checkModelInvariants (los mismos que se exigen a los
// núcleos oficiales).
func FuzzModelFromStructureDefinition(f *testing.F) {
	f.Add("Fuzz.value[x]", "Quantity", "", "", 0, "1", "")
	f.Add("Fuzz.value[x]", "dateTime", "", "", 0, "1", "")
	f.Add("Fuzz.item", "", "#Fuzz", "", 0, "*", "")
	f.Add("Fuzz.item", "", sdBase+"Fuzz#Fuzz", "", 0, "*", "")
	f.Add("Fuzz.item", "", "#Fuzz.nope", "", 0, "*", "")
	f.Add("Fuzz.ref", "Reference", "", sdBase+"Element", 1, "1", "")
	f.Add("Fuzz.ref", "Reference", "", sdBase+"Patient", 0, "*", "")
	f.Add("Fuzz.ref", "Reference", "", "not a canonical|", 0, "*", "")
	f.Add("Fuzz.", "x", "http://x#", "", -5, "z", "id")
	f.Add("Fuzz.[x]", "", "#", "|", 0, "", "Fuzz.a:b")
	f.Add("Other.value", "string", "", "", 0, "0", "")
	// Ronda 2: sin id ("!" lo omite: sdf-14) y con «:» en el camino.
	f.Add("Fuzz.a", "string", "", "", 0, "1", "!")
	f.Add("Fuzz.a:b", "string", "", "", 0, "1", "")
	f.Add("Fuzz.a:b", "string", "", "", 0, "1", "Fuzz.a")
	f.Add("Fuzz:s", "string", "", "", 0, "1", "Fuzz")
	f.Add("Fuzz.a", "http://hl7.org/fhirpath/System.Bogus", "", "", 0, "1", "")
	f.Add("Fuzz.item", "string", "#Fuzz", "", 0, "*", "")
	f.Add("Fuzz.a.b.c.d", "BackboneElement", "", "", 1<<31, "99999999999999", "Fuzz.a")
	f.Fuzz(func(t *testing.T, path, code, cref, target string, min int, max, id string) {
		v := modelVersions[2]
		e := map[string]any{"path": path, "min": min, "max": max}
		switch id {
		case "!": // sin id
		case "": // el id por defecto es el camino
			e["id"] = path
		default:
			e["id"] = id
		}
		if cref != "" {
			e["contentReference"] = cref
		}
		if code != "" || target != "" {
			ty := map[string]any{"code": code}
			if target != "" {
				ty["targetProfile"] = []string{target}
			}
			e["type"] = []any{ty}
		}
		d := tsd(v, "Fuzz", "complex-type", "Element", false, el("Fuzz", 0, "*"), e)
		c, err := tryCtx(t, mcore(v, append(fixture(v), d)...))
		if err != nil {
			return
		}
		m := c.Model()
		checkModelInvariants(t, c)
		for _, p := range m.Paths() {
			m.TypeOf(p)
			m.ChoiceTypes(p)
			m.ReferenceTargets(p)
			m.ResolvePath(p)
			m.ChildElements(p)
			m.FHIRTypeOf(p)
			m.ParentType(p)
			m.IsSubtype(p, "Element")
			m.Cardinality(p)
		}
	})
}
