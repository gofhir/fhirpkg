package parity

// Diferencial directo de fhirpath (plan 003 H6): is, as, type(), children() y
// Analyze con el modelo de models y con el de fhirpkg, sobre los ejemplos
// oficiales y sobre las expresiones de los packages (los invariantes de los
// StructureDefinition, que incluyen dom-2 y bdl-16 de H1, y los SearchParameter).

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	fhirpath "github.com/gofhir/fhirpath"
)

// evalExpressions se evalúan con los dos modelos sobre cada ejemplo. is, as,
// type() y children() usan la jerarquía, el tipo de cada camino y el orden de
// los hijos del modelo.
var evalExpressions = func() []string {
	out := []string{
		"type().name",
		"children().type().name",
		"descendants().type().name",
		"children().select($this.type().name)",
	}
	for _, ty := range []string{"Element", "BackboneElement", "Resource", "DomainResource", "Quantity", "Reference", "Extension", "Coding", "string", "id", "uri", "code", "dateTime", "canonical"} {
		out = append(out,
			"descendants().where($this.is("+ty+")).count()",
			"descendants().select($this.as("+ty+")).count()",
			"is("+ty+")",
		)
	}
	return out
}()

// examplesPerType es la muestra por tipo de recurso: los ejemplos son miles
// y descendants() los recorre enteros con cada expresión.
const examplesPerType = 8

// patched es el modelo de models con el TypeOf de algunas rutas cambiado: sirve
// para demostrar que una clase de differences.json explica toda la diferencia
// observable (con la corrección aplicada, el resultado es el de fhirpkg).
type patched struct {
	refModel
	typeOf map[string]string
}

func (p patched) TypeOf(path string) string {
	if t, ok := p.typeOf[path]; ok {
		return t
	}
	return p.refModel.TypeOf(path)
}

// nestedBackboneFix son las rutas de la clase nested-backbone-type con el tipo
// de fhirpkg.
func nestedBackboneFix(t testing.TB, v *version) map[string]string {
	t.Helper()
	fix := map[string]string{}
	for _, c := range loadClasses(t) {
		if c.ID != "nested-backbone-type" {
			continue
		}
		for _, d := range allDifferences(t, v) {
			if c.matches(v, d) {
				fix[d.Key] = d.Ours
			}
		}
	}
	if len(fix) == 0 {
		t.Fatal("the class nested-backbone-type is empty")
	}
	return fix
}

func evalString(e *fhirpath.Expression, raw []byte, m fhirpath.Model) string {
	col, err := e.EvaluateWithOptions(raw, fhirpath.WithModel(m))
	if err != nil {
		return "error: " + err.Error()
	}
	return col.String()
}

// fpStat es lo que el reporte cuenta del diferencial de fhirpath.
type fpStat struct {
	Examples, Evaluations, Different, Explained int
	AnalyzeExpressions, AnalyzeDifferent        int
}

var (
	fpMu    sync.Mutex
	fpStats = map[string]*fpStat{}
)

func fpStatOf(ver string) *fpStat {
	fpMu.Lock()
	defer fpMu.Unlock()
	if fpStats[ver] == nil {
		fpStats[ver] = &fpStat{}
	}
	return fpStats[ver]
}

// TestFHIRPathEvaluationDifferential: cada diferencia observable entre los dos
// modelos desaparece al corregir en models las rutas de la clase
// nested-backbone-type; no queda ninguna sin explicar.
func TestFHIRPathEvaluationDifferential(t *testing.T) {
	t.Parallel() // plan 005 §18: las tres pruebas largas e independientes corren juntas
	compiled := make([]*fhirpath.Expression, len(evalExpressions))
	for i, src := range evalExpressions {
		compiled[i] = fhirpath.MustCompile(src)
	}
	for _, v := range loadVersions(t) {
		t.Run(v.name, func(t *testing.T) {
			fixed := patched{v.ref, nestedBackboneFix(t, v)}
			st := fpStatOf(v.name)
			per := map[string]int{}
			for _, ex := range examplesOf(t, v.name) {
				if per[ex.resourceType] >= examplesPerType {
					continue
				}
				per[ex.resourceType]++
				st.Examples++
				for i, e := range compiled {
					st.Evaluations++
					a, b := evalString(e, ex.raw, v.ref), evalString(e, ex.raw, v.ours)
					if a == b {
						continue
					}
					st.Different++
					if c := evalString(e, ex.raw, fixed); c != b {
						t.Errorf("%s [%s]: models=%.150s fixed=%.150s fhirpkg=%.150s", ex.name, evalExpressions[i], a, c, b)
						continue
					}
					st.Explained++
				}
			}
			if st.Different == 0 {
				t.Error("no difference at all: the class nested-backbone-type no longer shows in the evaluation")
			}
			t.Logf("%s: %d examples, %d evaluations, %d differ, %d explained by nested-backbone-type", v.name, st.Examples, st.Evaluations, st.Different, st.Explained)
		})
	}
}

// ---- Analyze ----

type constraintDoc struct {
	Key        string `json:"key"`
	Expression string `json:"expression"`
}

type snapElement struct {
	Path       string          `json:"path"`
	Constraint []constraintDoc `json:"constraint"`
}

// expression es una expresión de un package con el tipo de su contexto.
type expression struct {
	Origin  string // de dónde sale: SD nombre elemento clave, o SP tipo code
	Context string
	Expr    string
}

// packageExpressions reúne los invariantes de los StructureDefinition y las
// expresiones de los SearchParameter del núcleo de la versión.
func packageExpressions(t testing.TB, v *version) []expression {
	t.Helper()
	var out []expression
	for _, r := range v.set.Resources(v.set.Lock().Core) {
		switch r.ResourceType {
		case "StructureDefinition":
			var sd struct {
				Name     string `json:"name"`
				Snapshot struct {
					Element []snapElement `json:"element"`
				} `json:"snapshot"`
			}
			if err := json.Unmarshal(r.Raw, &sd); err != nil {
				t.Fatal(err)
			}
			for _, e := range sd.Snapshot.Element {
				for _, c := range e.Constraint {
					if c.Expression != "" {
						out = append(out, expression{fmt.Sprintf("SD %s %s %s", sd.Name, e.Path, c.Key), e.Path, c.Expression})
					}
				}
			}
		case "SearchParameter":
			var sp struct {
				Code       string   `json:"code"`
				Base       []string `json:"base"`
				Expression string   `json:"expression"`
			}
			if err := json.Unmarshal(r.Raw, &sp); err != nil {
				t.Fatal(err)
			}
			if sp.Expression == "" {
				continue
			}
			for _, b := range sp.Base {
				out = append(out, expression{fmt.Sprintf("SP %s %s", b, sp.Code), b, sp.Expression})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Origin < out[j].Origin })
	return out
}

func analyzeErr(e *fhirpath.Expression, m fhirpath.Model, ctx string) string {
	if err := e.Analyze(m, ctx); err != nil {
		return err.Error()
	}
	return ""
}

// TestFHIRPathAnalyzeDifferential: Analyze de cada invariante y de cada
// expresión de SearchParameter de los núcleos con los dos modelos. Toda
// diferencia está en una clase de testdata/differences.json (H1: la pérdida
// conocida) y cada clase ocurre las veces que declara.
func TestFHIRPathAnalyzeDifferential(t *testing.T) {
	classes := loadDifferences(t).Analyze
	for _, v := range loadVersions(t) {
		t.Run(v.name, func(t *testing.T) {
			got := map[string]int{}
			st := fpStatOf(v.name)
			for _, x := range packageExpressions(t, v) {
				e, err := fhirpath.Compile(x.Expr)
				if err != nil {
					continue
				}
				st.AnalyzeExpressions++
				a, b := analyzeErr(e, v.ref, x.Context), analyzeErr(e, v.ours, x.Context)
				if a == b {
					continue
				}
				st.AnalyzeDifferent++
				i := slices.IndexFunc(classes, func(c *analyzeClass) bool { return slices.Contains(c.Expressions, x.Expr) })
				if i < 0 || a != "" || !strings.Contains(b, "has no element") {
					t.Errorf("%s [%s]: models=%q fhirpkg=%q: unclassified", x.Origin, x.Expr, a, b)
					continue
				}
				got[classes[i].ID]++
			}
			for _, c := range classes {
				if got[c.ID] != c.Counts[v.name] {
					t.Errorf("class %s: %d expressions, the file declares %d", c.ID, got[c.ID], c.Counts[v.name])
				}
			}
			t.Logf("%s: %d expressions, %d differ", v.name, st.AnalyzeExpressions, st.AnalyzeDifferent)
		})
	}
}

// TestKnownLossAnalyzeAbstractTypes fija la pérdida de H1: con los elementos de
// los tipos abstractos en el modelo, fhirpath.Analyze rechaza navegaciones que
// son válidas, y con models no. Es un defecto de fhirpath
// (docs/issues/fhirpath-analyze-abstract-types.md); el modelo no lo rodea. Si
// fhirpath lo corrige, este test falla: se quita la clase y este test.
func TestKnownLossAnalyzeAbstractTypes(t *testing.T) {
	cases := []struct{ context, expr string }{
		{"Patient", "contained.contained.empty()"}, // dom-2 del núcleo
		{"Bundle", "Bundle.entry.resource.name"},
		{"Patient", "contained.text.empty()"},
	}
	for _, v := range loadVersions(t) {
		for _, c := range cases {
			e := fhirpath.MustCompile(c.expr)
			if got := analyzeErr(e, v.ref, c.context); got != "" {
				t.Errorf("%s: models: %s [%s]: %s (the loss is no longer measured against models)", v.name, c.context, c.expr, got)
			}
			if got := analyzeErr(e, v.ours, c.context); !strings.Contains(got, "has no element") {
				t.Errorf("%s: fhirpkg: %s [%s]: %q, want 'has no element' (fhirpath fixed it? remove the class analyze-abstract-type-elements)", v.name, c.context, c.expr, got)
			}
		}
	}
}
