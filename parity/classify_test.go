package parity

// Clasificación de las diferencias con gofhir/models (plan 003 §3.4). Cada
// diferencia entre una tabla de models y la de fhirpkg tiene que caer en
// exactamente una clase de testdata/differences.json, y cada clase tiene que
// ocurrir exactamente las veces que declara: el test falla ante una diferencia
// sin clasificar, ante una con dos clases y ante una clasificada que ya no
// ocurre o que ocurre otra cantidad de veces.

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// Clases de diferencia (el campo "class" de differences.json).
const (
	classOwnDefect      = "own-defect"      // defecto de fhirpkg: se corrige
	classModelsDefect   = "models-defect"   // defecto de models: se reporta con evidencia, sin rodearlo
	classFHIRPathDefect = "fhirpath-defect" // defecto de fhirpath: se reporta con evidencia, sin rodearlo
	classDeliberate     = "deliberate"      // decisión de fhirpkg, con su fuente
)

// Tipos de diferencia.
const (
	kindOnlyModels = "only-models"
	kindOnlyOurs   = "only-fhirpkg"
	kindDiffers    = "differs"
)

type diffClass struct {
	ID        string         `json:"id"`
	Class     string         `json:"class"`
	Reason    string         `json:"reason"`
	Reference string         `json:"reference"`
	Tables    []string       `json:"tables"`
	Kind      string         `json:"kind"`
	KeyRegex  string         `json:"keyRegex,omitempty"`
	Keys      []string       `json:"keys,omitempty"`
	ModelsRe  string         `json:"modelsValueRegex,omitempty"`
	OursRe    string         `json:"fhirpkgValueRegex,omitempty"`
	TypeOf    string         `json:"typeOf,omitempty"`        // el TypeOf de fhirpkg para la clave
	Variant   *bool          `json:"choiceVariant,omitempty"` // la clave es la variante de un elemento con varios tipos
	Counts    map[string]int `json:"counts"`

	keyRe, mRe, oRe *regexp.Regexp
}

type difference struct {
	Version, Table, Kind, Key string
	Models, Ours              string
}

// analyzeClass es una clase de diferencia de Analyze: las expresiones donde
// fhirpath da un error con el modelo de fhirpkg y no con el de models.
type analyzeClass struct {
	ID          string         `json:"id"`
	Class       string         `json:"class"`
	Reason      string         `json:"reason"`
	Reference   string         `json:"reference"`
	Expressions []string       `json:"expressions"`
	Counts      map[string]int `json:"counts"`
}

type differencesFile struct {
	Classes []*diffClass    `json:"classes"`
	Analyze []*analyzeClass `json:"analyze"`
}

func loadDifferences(t testing.TB) differencesFile {
	t.Helper()
	b, err := os.ReadFile("testdata/differences.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc differencesFile
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range doc.Analyze {
		if c.ID == "" || c.Reason == "" || c.Reference == "" || len(c.Expressions) == 0 ||
			!slices.Contains([]string{classOwnDefect, classModelsDefect, classFHIRPathDefect, classDeliberate}, c.Class) {
			t.Fatalf("analyze class %q is incomplete", c.ID)
		}
	}
	return doc
}

func loadClasses(t testing.TB) []*diffClass {
	t.Helper()
	doc := loadDifferences(t)
	ids := map[string]bool{}
	for _, c := range doc.Classes {
		if c.ID == "" || ids[c.ID] {
			t.Fatalf("class %q: empty or repeated id", c.ID)
		}
		ids[c.ID] = true
		if !slices.Contains([]string{classOwnDefect, classModelsDefect, classFHIRPathDefect, classDeliberate}, c.Class) {
			t.Fatalf("class %s: unknown class %q", c.ID, c.Class)
		}
		if c.Reason == "" || c.Reference == "" {
			t.Fatalf("class %s: a classification needs its reason and reference", c.ID)
		}
		if !slices.Contains([]string{kindOnlyModels, kindOnlyOurs, kindDiffers}, c.Kind) {
			t.Fatalf("class %s: unknown kind %q", c.ID, c.Kind)
		}
		for _, tb := range c.Tables {
			if !slices.ContainsFunc(tables, func(x table) bool { return x.name == tb }) {
				t.Fatalf("class %s: unknown table %q", c.ID, tb)
			}
		}
		if c.KeyRegex != "" {
			c.keyRe = regexp.MustCompile(c.KeyRegex)
		}
		if c.ModelsRe != "" {
			c.mRe = regexp.MustCompile(c.ModelsRe)
		}
		if c.OursRe != "" {
			c.oRe = regexp.MustCompile(c.OursRe)
		}
	}
	return doc.Classes
}

// isChoiceVariant: la clave es base+Tipo de un elemento con varios tipos, o
// sea, ChoiceTypes(base) la incluye.
func isChoiceVariant(v *version, key string) bool {
	for i := 1; i < len(key); i++ {
		for _, code := range v.ours.ChoiceTypes(key[:i]) {
			if key[i:] == strings.ToUpper(code[:1])+code[1:] {
				return true
			}
		}
	}
	return false
}

func (c *diffClass) matches(v *version, d difference) bool {
	if c.Kind != d.Kind || !slices.Contains(c.Tables, d.Table) {
		return false
	}
	if c.keyRe != nil && !c.keyRe.MatchString(d.Key) {
		return false
	}
	if c.Keys != nil && !slices.Contains(c.Keys, d.Key) {
		return false
	}
	if c.mRe != nil && !c.mRe.MatchString(d.Models) {
		return false
	}
	if c.oRe != nil && !c.oRe.MatchString(d.Ours) {
		return false
	}
	if c.TypeOf != "" && v.ours.TypeOf(d.Key) != c.TypeOf {
		return false
	}
	if c.Variant != nil && isChoiceVariant(v, d.Key) != *c.Variant {
		return false
	}
	return true
}

// allDifferences compara las nueve tablas de una versión.
func allDifferences(t testing.TB, v *version) []difference {
	t.Helper()
	var out []difference
	for _, tb := range tables {
		d := compare(t, v, tb)
		for _, k := range d.OnlyRef {
			out = append(out, difference{v.name, tb.name, kindOnlyModels, k, tb.value(v.ref, k), ""})
		}
		for _, k := range d.OnlyOurs {
			out = append(out, difference{v.name, tb.name, kindOnlyOurs, k, "", tb.value(v.ours, k)})
		}
		keys := make([]string, 0, len(d.Differ))
		for k := range d.Differ {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, difference{v.name, tb.name, kindDiffers, k, d.Differ[k][0], d.Differ[k][1]})
		}
	}
	return out
}

// classified es el resultado de clasificar todas las versiones.
type classified struct {
	classes []*diffClass
	got     map[string]map[string]int // clase -> versión -> cuántas
	totals  map[string]map[string]int // versión -> tabla -> diferencias
	keys    map[string]map[string][2]int
	errs    []string
}

func classify(t testing.TB) *classified {
	t.Helper()
	cl := &classified{classes: loadClasses(t), got: map[string]map[string]int{}, totals: map[string]map[string]int{}, keys: map[string]map[string][2]int{}}
	for _, v := range loadVersions(t) {
		cl.totals[v.name] = map[string]int{}
		cl.keys[v.name] = map[string][2]int{}
		for _, tb := range tables {
			cl.keys[v.name][tb.name] = compare(t, v, tb).Keys
		}
		for _, d := range allDifferences(t, v) {
			cl.totals[v.name][d.Table]++
			var hit []string
			for _, c := range cl.classes {
				if c.matches(v, d) {
					hit = append(hit, c.ID)
					if cl.got[c.ID] == nil {
						cl.got[c.ID] = map[string]int{}
					}
					cl.got[c.ID][v.name]++
				}
			}
			switch len(hit) {
			case 1:
			case 0:
				cl.errs = append(cl.errs, fmt.Sprintf("%s %s %s %s: unclassified (models=%q fhirpkg=%q)", d.Version, d.Table, d.Kind, d.Key, d.Models, d.Ours))
			default:
				cl.errs = append(cl.errs, fmt.Sprintf("%s %s %s %s: in several classes %v", d.Version, d.Table, d.Kind, d.Key, hit))
			}
		}
	}
	for _, c := range cl.classes {
		total := 0
		for _, v := range versions {
			got, want := cl.got[c.ID][v.name], c.Counts[v.name]
			total += got
			if got != want {
				cl.errs = append(cl.errs, fmt.Sprintf("class %s, %s: %d differences, the file declares %d", c.ID, v.name, got, want))
			}
		}
		if total == 0 {
			cl.errs = append(cl.errs, fmt.Sprintf("class %s no longer occurs in any version: remove it", c.ID))
		}
	}
	return cl
}
