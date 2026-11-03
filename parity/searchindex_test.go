package parity

// Diferencial por consumidor (plan 003 pieza C): searchindex.Extract sobre los
// ejemplos oficiales de HL7 da el mismo resultado con el modelo de models y con
// el de fhirpkg, o cada diferencia está explicada.

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gofhir/searchindex"
	"github.com/gofhir/searchindex/definitions"
)

func definitionsOf(t testing.TB, ver string) searchindex.Definitions {
	t.Helper()
	var (
		s   *definitions.Set
		err error
	)
	switch ver {
	case "R4":
		s, err = definitions.R4()
	case "R4B":
		s, err = definitions.R4B()
	default:
		s, err = definitions.R5()
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// extractors arma un Extractor con cada modelo.
func extractors(t testing.TB, v *version) (ref, ours *searchindex.Extractor) {
	t.Helper()
	defs := definitionsOf(t, v.name)
	var err error
	if ref, err = searchindex.New(searchindex.Config{Definitions: defs, Model: v.ref}); err != nil {
		t.Fatal(err)
	}
	if ours, err = searchindex.New(searchindex.Config{Definitions: defs, Model: v.ours}); err != nil {
		t.Fatal(err)
	}
	return ref, ours
}

// exampleDiff es un ejemplo cuyo resultado difiere entre los dos modelos.
type exampleDiff struct{ Version, Example, Detail string }

var (
	siMu    sync.Mutex
	siStats = map[string]*siStat{}
)

// siStat es lo que el reporte cuenta por versión.
type siStat struct {
	Examples, Extracted, Same int
	Diffs                     []exampleDiff
}

func TestSearchIndexDifferential(t *testing.T) {
	for _, v := range loadVersions(t) {
		t.Run(v.name, func(t *testing.T) {
			ref, ours := extractors(t, v)
			exs := examplesOf(t, v.name)
			if len(exs) < 1000 {
				t.Fatalf("only %d examples", len(exs))
			}
			st := &siStat{Examples: len(exs)}
			for _, ex := range exs {
				a, errA := ref.Extract(ex.resourceType, ex.raw, time.UTC)
				b, errB := ours.Extract(ex.resourceType, ex.raw, time.UTC)
				if (errA == nil) != (errB == nil) {
					st.Diffs = append(st.Diffs, exampleDiff{v.name, ex.name, fmt.Sprintf("error models=%v fhirpkg=%v", errA, errB)})
					continue
				}
				if errA != nil {
					continue
				}
				st.Extracted++
				if reflect.DeepEqual(a, b) {
					st.Same++
					continue
				}
				st.Diffs = append(st.Diffs, exampleDiff{v.name, ex.name, firstDifference(a, b)})
			}
			if !reflect.DeepEqual(ref.Unsupported(), ours.Unsupported()) {
				t.Errorf("Unsupported differs: models %v, fhirpkg %v", ref.Unsupported(), ours.Unsupported())
			}
			siMu.Lock()
			siStats[v.name] = st
			siMu.Unlock()
			t.Logf("%s: %d examples, %d extracted, %d with the same index, %d different", v.name, st.Examples, st.Extracted, st.Same, len(st.Diffs))
			for i, d := range st.Diffs {
				if i < 20 {
					t.Errorf("%s %s: %s", d.Version, d.Example, d.Detail)
				}
			}
		})
	}
}

// firstDifference describe en qué parámetro difieren dos resultados.
func firstDifference(a, b searchindex.Result) string {
	if !reflect.DeepEqual(a.Warnings, b.Warnings) {
		return fmt.Sprintf("warnings: models %v, fhirpkg %v", a.Warnings, b.Warnings)
	}
	va, vb := reflect.ValueOf(a.Index), reflect.ValueOf(b.Index)
	for i := 0; i < va.NumField(); i++ {
		fa, fb := va.Field(i), vb.Field(i)
		if fa.Kind() != reflect.Map {
			continue
		}
		for _, k := range fa.MapKeys() {
			if !reflect.DeepEqual(fa.MapIndex(k).Interface(), fb.MapIndex(k).Interface()) {
				return fmt.Sprintf("%s[%s]: models %v, fhirpkg %v", va.Type().Field(i).Name, k, fa.MapIndex(k).Interface(), fb.MapIndex(k).Interface())
			}
		}
		for _, k := range fb.MapKeys() {
			if !fa.MapIndex(k).IsValid() {
				return fmt.Sprintf("%s[%s]: only fhirpkg has it", va.Type().Field(i).Name, k)
			}
		}
	}
	return "the results differ"
}
