package parity

// Benchmark del normalizador contra gofhir/models (plan 004 §4, paso 4):
// Context.Normalize contra UnmarshalResource + Marshal, por versión y por
// tamaño, con los Bundle-resources de 34 a 41 MB. Tiempo, asignaciones y bytes
// salen de testing.Benchmark; el RSS, de un proceso hijo por medición (el
// máximo de un proceso no se puede separar de lo anterior).

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/gofhir/fhirpkg"
	r4 "github.com/gofhir/models/r4/v2"
	r4b "github.com/gofhir/models/r4b/v2"
	r5 "github.com/gofhir/models/r5/v2"
)

// modelsRoundTrip hace UnmarshalResource + Marshal de la versión.
var modelsRoundTrip = map[string]func([]byte) ([]byte, error){
	"R4": func(b []byte) ([]byte, error) {
		r, err := r4.UnmarshalResource(b)
		if err != nil {
			return nil, err
		}
		return r4.Marshal(r)
	},
	"R4B": func(b []byte) ([]byte, error) {
		r, err := r4b.UnmarshalResource(b)
		if err != nil {
			return nil, err
		}
		return r4b.Marshal(r)
	},
	"R5": func(b []byte) ([]byte, error) {
		r, err := r5.UnmarshalResource(b)
		if err != nil {
			return nil, err
		}
		return r5.Marshal(r)
	},
}

type exampleSize struct {
	Name string
	Size int64
}

var (
	sizesMu    sync.Mutex
	sizesCache = map[string][]exampleSize{}
)

// walkTgz recorre los JSON de primer nivel de un package de ejemplos; con
// want != "" lee el cuerpo de ese archivo y se detiene.
func walkExamples(t testing.TB, ver, want string) (sizes []exampleSize, body []byte) {
	t.Helper()
	dir := os.Getenv("EXAMPLES_PACKAGES")
	if dir == "" {
		t.Skip("EXAMPLES_PACKAGES is not set (make parity)")
	}
	f, err := os.Open(filepath.Join(dir, examplePackages[ver]))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return sizes, nil
		}
		if err != nil {
			t.Fatal(err)
		}
		base := path.Base(h.Name)
		if path.Dir(h.Name) != "package" || !strings.HasSuffix(base, ".json") || base == "package.json" || strings.HasPrefix(base, ".") {
			continue
		}
		if want != "" {
			if base == want {
				b, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				return nil, b
			}
			continue
		}
		sizes = append(sizes, exampleSize{base, h.Size})
	}
}

func exampleSizes(t testing.TB, ver string) []exampleSize {
	sizesMu.Lock()
	defer sizesMu.Unlock()
	if s, ok := sizesCache[ver]; ok {
		return s
	}
	s, _ := walkExamples(t, ver, "")
	sort.Slice(s, func(i, j int) bool { return s[i].Size < s[j].Size || s[i].Size == s[j].Size && s[i].Name < s[j].Name })
	sizesCache[ver] = s
	return s
}

// benchPicks son los ejemplos que se miden: Patient-example (pequeño), el más
// cercano a 100 KB, a 2 MB y el más grande (los Bundle-resources).
func benchPicks(t testing.TB, ver string) []exampleSize {
	s := exampleSizes(t, ver)
	near := func(target int64) exampleSize {
		best := s[0]
		for _, e := range s {
			if abs(e.Size-target) < abs(best.Size-target) {
				best = e
			}
		}
		return best
	}
	out := []exampleSize{near(4 << 10), near(100 << 10), near(2 << 20), s[len(s)-1]}
	// Sin repetidos, por si un tamaño cae en el mismo archivo.
	var uniq []exampleSize
	for _, e := range out {
		if len(uniq) == 0 || uniq[len(uniq)-1].Name != e.Name {
			uniq = append(uniq, e)
		}
	}
	return uniq
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func exampleBody(t testing.TB, ver, name string) []byte {
	_, b := walkExamples(t, ver, name)
	if b == nil {
		t.Fatalf("%s: %s not found", ver, name)
	}
	return b
}

// benchRowN es una fila del benchmark del normalizador.
type benchRowN struct {
	Version, Example, Impl string
	Size                   int64
	NsPerOp, AllocsPerOp   int64
	BytesPerOp             int64
	RSSMB                  int64 // RSS máximo del proceso hijo menos el de su base, en MB
	Err                    string
}

var (
	nbMu   sync.Mutex
	nbRows []benchRowN
)

// childRSS corre este mismo binario con PARITY_CHILD=ver|ejemplo|impl y devuelve
// el RSS máximo en bytes.
func childRSS(t testing.TB, ver, example, impl string) int64 {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestBenchChild$")
	cmd.Env = append(os.Environ(), "PARITY_CHILD="+ver+"|"+example+"|"+impl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child %s %s %s: %v\n%s", ver, example, impl, err, out)
	}
	ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return ru.Maxrss
	}
	return ru.Maxrss << 10
}

// TestBenchChild es el proceso hijo de la medición de RSS: carga el ejemplo, y
// según impl construye el Context (base), o normaliza con fhirpkg o con models.
func TestBenchChild(t *testing.T) {
	spec := os.Getenv("PARITY_CHILD")
	if spec == "" {
		t.Skip("child of the RSS measurement")
	}
	parts := strings.Split(spec, "|")
	ver, example, impl := parts[0], parts[1], parts[2]
	raw := exampleBody(t, ver, example)
	switch impl {
	case "base-models":
		// Solo el ejemplo en memoria.
	case "models":
		if _, err := modelsRoundTrip[ver](raw); err != nil {
			t.Fatal(err)
		}
	case "base-fhirpkg", "fhirpkg":
		var v *version
		for _, x := range loadVersions(t) {
			if x.name == ver {
				v = x
			}
		}
		if impl == "fhirpkg" {
			if _, err := v.ctx.Normalize(raw, fhirpkg.WithMaxBytes(1<<30)); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtime.KeepAlive(raw)
}

// TestNormalizeBenchmark mide los ejemplos escogidos. Informativo, sin umbrales:
// es evidencia del plan 004 §4 (paso 4), no una puerta de `make check-packages`
// (plan 005 §18), que lo omite con PARITY_BENCH=0; `make parity` y
// `make evidence` lo corren.
func TestNormalizeBenchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	if benchOff() {
		t.Skip("PARITY_BENCH=0 (make check-packages); make parity runs it")
	}
	for _, v := range loadVersions(t) {
		for _, pick := range benchPicks(t, v.name) {
			raw := exampleBody(t, v.name, pick.Name)
			ctx := v.ctx
			ours := func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := ctx.Normalize(raw, fhirpkg.WithMaxBytes(1<<30)); err != nil {
						b.Fatal(err)
					}
				}
			}
			theirs := func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := modelsRoundTrip[v.name](raw); err != nil {
						b.Fatal(err)
					}
				}
			}
			for _, c := range []struct {
				impl string
				f    func(*testing.B)
			}{{"models", theirs}, {"fhirpkg", ours}} {
				row := benchRowN{Version: v.name, Example: pick.Name, Impl: c.impl, Size: int64(len(raw))}
				// Un fallo (models con algo que no entiende) se registra, no se esconde.
				if _, err := map[string]func() ([]byte, error){
					"models":  func() ([]byte, error) { return modelsRoundTrip[v.name](raw) },
					"fhirpkg": func() ([]byte, error) { return ctx.Normalize(raw, fhirpkg.WithMaxBytes(1<<30)) },
				}[c.impl](); err != nil {
					row.Err = err.Error()
					if len(row.Err) > 120 {
						row.Err = row.Err[:120]
					}
				} else {
					r := testing.Benchmark(c.f)
					row.NsPerOp, row.AllocsPerOp, row.BytesPerOp = r.NsPerOp(), r.AllocsPerOp(), r.AllocedBytesPerOp()
					base := childRSS(t, v.name, pick.Name, "base-"+c.impl)
					row.RSSMB = max(0, (childRSS(t, v.name, pick.Name, c.impl)-base)>>20)
				}
				t.Logf("%s %s %s: %s", v.name, pick.Name, c.impl, rowText(row))
				nbMu.Lock()
				nbRows = append(nbRows, row)
				nbMu.Unlock()
			}
		}
	}
}

func rowText(r benchRowN) string {
	if r.Err != "" {
		return "fails: " + r.Err
	}
	return fmt.Sprintf("%d bytes, %.2f ms/op, %d allocs/op, %d B/op, +%d MB RSS", r.Size, float64(r.NsPerOp)/1e6, r.AllocsPerOp, r.BytesPerOp, r.RSSMB)
}

// benchOff dice si la corrida omite los benchmarks informativos (y, con ellos, el
// reporte de evidencia, que quedaría incompleto).
func benchOff() bool { return os.Getenv("PARITY_BENCH") == "0" }
