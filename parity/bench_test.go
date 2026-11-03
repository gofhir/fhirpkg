package parity

// Benchmark con los dos modelos ya construidos (plan 003 H6, pieza C): la
// extracción de searchindex sobre una muestra de los ejemplos oficiales y las
// consultas del modelo que más usa fhirpath. Informativo, sin umbrales.

import (
	"sync"
	"testing"
	"time"

	"github.com/gofhir/searchindex"
)

// benchSample es la muestra fija: los primeros ejemplos de cada tipo de recurso.
func benchSample(t testing.TB, ver string) []example {
	t.Helper()
	per := map[string]int{}
	var out []example
	for _, ex := range examplesOf(t, ver) {
		if per[ex.resourceType] < 2 {
			per[ex.resourceType]++
			out = append(out, ex)
		}
	}
	return out
}

func benchExtract(x *searchindex.Extractor, sample []example) func(*testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, ex := range sample {
				if _, err := x.Extract(ex.resourceType, ex.raw, time.UTC); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

// lookupPaths son las rutas que pide la evaluación: tipos y elementos.
var lookupPaths = []string{"Patient", "Patient.name", "Observation.value", "Observation.valueQuantity", "Observation.subject", "Questionnaire.item.item", "Age", "Resource"}

func benchLookups(m model, sub interface{ IsSubtype(string, string) bool }) func(*testing.B) {
	return func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for _, p := range lookupPaths {
				m.TypeOf(p)
				m.ChoiceTypes(p)
				m.ReferenceTargets(p)
				m.ChildElements(p)
				m.ResolvePath(p)
				m.HasType(p)
				sub.IsSubtype(p, "Resource")
			}
		}
	}
}

func BenchmarkExtract(b *testing.B) {
	for _, v := range loadVersions(b) {
		ref, ours := extractors(b, v)
		sample := benchSample(b, v.name)
		b.Run(v.name+"/models", benchExtract(ref, sample))
		b.Run(v.name+"/fhirpkg", benchExtract(ours, sample))
	}
}

func BenchmarkLookups(b *testing.B) {
	for _, v := range loadVersions(b) {
		b.Run(v.name+"/models", benchLookups(v.ref, v.ref))
		b.Run(v.name+"/fhirpkg", benchLookups(v.ours, v.ours))
	}
}

// benchRow es una fila del reporte.
type benchRow struct {
	Name         string
	NsPerOp      int64
	AllocsPerOp  int64
	BytesPerOp   int64
	PerExampleNs int64
}

var (
	benchMu   sync.Mutex
	benchRows []benchRow
)

// TestBenchmarkReport corre los benchmarks con testing.Benchmark para que el
// reporte los traiga (go test -bench los corre también, sueltos).
func TestBenchmarkReport(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, v := range loadVersions(t) {
		ref, ours := extractors(t, v)
		sample := benchSample(t, v.name)
		for _, c := range []struct {
			name string
			f    func(*testing.B)
			n    int
		}{
			{"Extract/" + v.name + "/models", benchExtract(ref, sample), len(sample)},
			{"Extract/" + v.name + "/fhirpkg", benchExtract(ours, sample), len(sample)},
			{"Lookups/" + v.name + "/models", benchLookups(v.ref, v.ref), len(lookupPaths)},
			{"Lookups/" + v.name + "/fhirpkg", benchLookups(v.ours, v.ours), len(lookupPaths)},
		} {
			r := testing.Benchmark(c.f)
			row := benchRow{c.name, r.NsPerOp(), r.AllocsPerOp(), r.AllocedBytesPerOp(), r.NsPerOp() / int64(c.n)}
			benchMu.Lock()
			benchRows = append(benchRows, row)
			benchMu.Unlock()
			t.Logf("%s: %d ns/op, %d allocs/op, %d B/op (%d ns per item)", c.name, row.NsPerOp, row.AllocsPerOp, row.BytesPerOp, row.PerExampleNs)
		}
	}
}
