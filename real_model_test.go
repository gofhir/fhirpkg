package fhirpkg

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Pruebas del modelo de tipos sobre los núcleos oficiales (make check-packages).
// Los valores esperados a mano salen de la especificación de cada versión y
// están citados; las cuentas de tipos, del plan 003 §1.4 y H9.

var realModelCases = []struct {
	lock, name, golden string
	types              int // tipos que define el núcleo (plan 003 §1.4: 209, 206 y 231)
}{
	{"r4.lock", "R4", "r4-types.txt", 209},
	{"r4b.lock", "R4B", "r4b-types.txt", 206},
	{"r5.lock", "R5", "r5-types.txt", 231},
}

// H9: el dorado fija los tipos por versión; un tipo nuevo cambia el dorado.
func TestRealModelTypesGolden(t *testing.T) {
	for _, rc := range realModelCases {
		c, _ := realContext(t, rc.lock)
		got := c.Model().Types()
		if len(got) != rc.types {
			t.Errorf("%s: %d types, plan 003 §1.4 says %d", rc.name, len(got), rc.types)
		}
		text := strings.Join(got, "\n") + "\n"
		path := filepath.Join("testdata", "golden", rc.golden)
		if *update {
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (make golden)", err)
		}
		if text != string(want) {
			t.Errorf("%s: the types differ from %s (make golden, then review the diff)", rc.name, path)
		}
	}
}

// Los casos de la especificación, uno por regla de §3.2.
func TestRealModelSpecCases(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			m := c.Model()
			// structuredefinition.html, baseDefinition: la jerarquía.
			wantString(t, "ParentType(Age)", m.ParentType("Age"), "Quantity")
			wantString(t, "ParentType(Patient)", m.ParentType("Patient"), "DomainResource")
			wantString(t, "ParentType(DomainResource)", m.ParentType("DomainResource"), "Resource")
			if !m.IsSubtype("Patient", "Resource") || m.IsSubtype("Resource", "Patient") {
				t.Error("IsSubtype across Patient and Resource")
			}
			// R4 y R4B: Resource y Element no declaran base; R5: Base (structuredefinition.html R5).
			if rc.name == "R5" {
				wantString(t, "ParentType(Resource)", m.ParentType("Resource"), "Base")
				wantString(t, "ParentType(Element)", m.ParentType("Element"), "Base")
			} else {
				wantString(t, "ParentType(Resource)", m.ParentType("Resource"), "")
				wantString(t, "ParentType(Element)", m.ParentType("Element"), "")
			}
			// Un tipo abstracto es tipo pero no recurso concreto.
			if !m.HasType("Resource") || m.IsResource("Resource") || m.IsResource("DomainResource") || !m.IsResource("Patient") || m.IsResource("Quantity") {
				t.Error("IsResource of Resource, DomainResource, Patient or Quantity")
			}
			// observation.html: Observation.value[x], en el orden de la definición.
			choices := []string{"Quantity", "CodeableConcept", "string", "boolean", "integer", "Range", "Ratio", "SampledData", "time", "dateTime", "Period"}
			if rc.name == "R5" {
				choices = append(choices, "Attachment", "Reference") // R5 agrega valueAttachment y valueReference
			}
			wantStrings(t, "ChoiceTypes(Observation.value)", m.ChoiceTypes("Observation.value"), choices...)
			wantString(t, "TypeOf(Observation.valueQuantity)", m.TypeOf("Observation.valueQuantity"), "Quantity")
			wantString(t, "TypeOf(Observation.valueDateTime)", m.TypeOf("Observation.valueDateTime"), "dateTime")
			// questionnaire.html: item.item es contentReference a #Questionnaire.item.
			wantString(t, "ResolvePath(Questionnaire.item.item)", m.ResolvePath("Questionnaire.item.item"), "Questionnaire.item")
			// datatypes.html: un primitivo tiene id, extension y value.
			wantStrings(t, "ChildElements(date)", m.ChildElements("date"), "id", "extension", "value")
			wantString(t, "TypeOf(date.extension)", m.TypeOf("date.extension"), "Extension")
			wantString(t, "FHIRTypeOf(date.value)", m.FHIRTypeOf("date.value"), "")
			// plan 003 §4: Patient.id es string en R4 e id en R5.
			switch rc.name {
			case "R4":
				wantString(t, "FHIRTypeOf(Patient.id)", m.FHIRTypeOf("Patient.id"), "string")
			case "R5":
				wantString(t, "FHIRTypeOf(Patient.id)", m.FHIRTypeOf("Patient.id"), "id")
			}
			wantString(t, "FHIRTypeOf(Extension.url)", m.FHIRTypeOf("Extension.url"), "uri")
			// R4 referencelist: Reference(Any) es Resource.
			wantStrings(t, "ReferenceTargets(Observation.focus)", m.ReferenceTargets("Observation.focus"), "Resource")
			// patient.html: subject de Observation y managingOrganization.
			wantStrings(t, "ReferenceTargets(Patient.managingOrganization)", m.ReferenceTargets("Patient.managingOrganization"), "Organization")
			// patient.html: link.other es Reference(Patient | RelatedPerson).
			wantStrings(t, "ReferenceTargets(Patient.link.other)", m.ReferenceTargets("Patient.link.other"), "Patient", "RelatedPerson")
			// patient.html, bundle.html: cardinalidades.
			for _, cc := range []struct {
				path     string
				min, max int
			}{{"Patient.name", 0, Unbounded}, {"Patient.birthDate", 0, 1}, {"Observation.status", 1, 1}, {"Observation.code", 1, 1}, {"Bundle.entry", 0, Unbounded}, {"Patient.link.other", 1, 1}} {
				if min, max, ok := m.Cardinality(cc.path); !ok || min != cc.min || max != cc.max {
					t.Errorf("Cardinality(%s) = %d..%d %v, want %d..%d", cc.path, min, max, ok, cc.min, cc.max)
				}
			}
			// D1: SimpleQuantity y MoneyQuantity son perfiles de Quantity, no tipos (ADR-004).
			if m.HasType("SimpleQuantity") || m.HasType("MoneyQuantity") {
				t.Error("SimpleQuantity or MoneyQuantity is a type")
			}
			// D3: DataType, el tipo abstracto de R4B y R5, es un tipo.
			if rc.name != "R4" && !m.HasType("DataType") {
				t.Error("DataType is not a type")
			}
		})
	}
}

// Invariantes que valen en toda versión.
func TestRealModelInvariants(t *testing.T) {
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			checkModelInvariants(t, c)
		})
	}
}

// El costo de construir el modelo dentro de NewContext (plan 003 D4, §2 pieza
// D): lo que tarda NewContext (que incluye el modelo) y el heap que retiene el
// modelo, por versión. make evidence recoge estas líneas.
func TestRealModelCost(t *testing.T) {
	for _, rc := range realModelCases {
		set := realSet(t, rc.lock)
		start := time.Now()
		c, err := NewContext(set)
		took := time.Since(start)
		if err != nil {
			t.Fatal(err)
		}
		m := c.model
		nTypes, nPaths := len(m.names), len(m.Paths())
		var with, without runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&with)
		// Sin ninguna referencia al modelo, el heap baja lo que él retenía.
		c.model, m = nil, nil
		runtime.GC()
		runtime.ReadMemStats(&without)
		t.Logf("%s model: NewContext (with the model) took %s, %d types, %d paths, the model retains %d KB of heap", rc.name,
			took.Round(100_000), nTypes, nPaths, (int64(with.HeapAlloc)-int64(without.HeapAlloc))>>10)
		runtime.KeepAlive(c)
	}
}

// Oráculo independiente (plan 003, ronda 1, M2): scripts/model_oracle.py lee el
// package del núcleo sin pasar por modelbuild.go y deriva, de la especificación,
// los destinos de referencia, la cardinalidad y el tipo FHIR de cada camino de
// cada tipo; el modelo debe coincidir exacto, en los dos sentidos. Necesita
// python3 (make check-packages).
func TestRealModelOracle(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := os.Getenv("FHIRPKG_PACKAGES")
	for _, rc := range realModelCases {
		t.Run(rc.name, func(t *testing.T) {
			c, _ := realContext(t, rc.lock)
			m := c.Model()
			name, version, _ := strings.Cut(c.set.lock.Core, "#")
			start := time.Now()
			out, err := exec.Command(py, "-I", filepath.Join("scripts", "model_oracle.py"), filepath.Join(dir, name+"-"+version+".tgz")).Output()
			if err != nil {
				t.Fatalf("model_oracle.py: %v", err)
			}
			took := time.Since(start)
			refs, ftypes := map[string]string{}, map[string]string{}
			cards := map[string][2]int{}
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				f := strings.Split(line, "\t")
				switch f[0] {
				case "ref":
					refs[f[1]] = f[2]
				case "ftype":
					ftypes[f[1]] = f[2]
				case "card":
					lo, _ := strconv.Atoi(f[2])
					hi, _ := strconv.Atoi(f[3])
					cards[f[1]] = [2]int{lo, hi}
				default:
					t.Fatalf("model_oracle.py: unexpected line %q", line)
				}
			}
			seenRef, seenCard, seenFType := 0, 0, 0
			for _, p := range m.Paths() {
				if got := m.ReferenceTargets(p); len(got) > 0 {
					seenRef++
					if want, ok := refs[p]; !ok || strings.Join(got, ",") != want {
						t.Errorf("ReferenceTargets(%s) = %q, the oracle says %q", p, got, want)
					}
				}
				if lo, hi, ok := m.Cardinality(p); ok {
					seenCard++
					if want, k := cards[p]; !k || want != [2]int{lo, hi} {
						t.Errorf("Cardinality(%s) = %d..%d, the oracle says %v", p, lo, hi, want)
					}
				}
				if got := m.FHIRTypeOf(p); got != "" {
					seenFType++
					if want := ftypes[p]; got != want {
						t.Errorf("FHIRTypeOf(%s) = %q, the oracle says %q", p, got, want)
					}
				}
			}
			if seenRef != len(refs) || seenCard != len(cards) || seenFType != len(ftypes) {
				t.Errorf("the model has %d reference, %d cardinality and %d fhir-type paths; the oracle has %d, %d and %d",
					seenRef, seenCard, seenFType, len(refs), len(cards), len(ftypes))
			}
			t.Logf("%s oracle: %d reference, %d cardinality and %d fhir-type paths, python took %s", rc.name, len(refs), len(cards), len(ftypes), took.Round(10*time.Millisecond))
		})
	}
}

// Plan 004, D6: los tipos primitivos de cada versión (datatypes.html, «Primitive
// Types»): 20 en R4 y R4B, y R5 agrega integer64.
func TestRealModelIsPrimitive(t *testing.T) {
	r4 := []string{"base64Binary", "boolean", "canonical", "code", "date", "dateTime", "decimal", "id", "instant", "integer",
		"markdown", "oid", "positiveInt", "string", "time", "unsignedInt", "uri", "url", "uuid", "xhtml"}
	want := map[string][]string{"R4": r4, "R4B": r4, "R5": append(append([]string{}, r4...), "integer64")}
	for _, rc := range realModelCases {
		c, _ := realContext(t, rc.lock)
		m := c.Model()
		var got []string
		for _, ty := range m.Types() {
			if m.IsPrimitive(ty) {
				got = append(got, ty)
			}
		}
		w := append([]string(nil), want[rc.name]...)
		sort.Strings(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s: primitives = %v, want %v", rc.name, got, w)
		}
	}
}
