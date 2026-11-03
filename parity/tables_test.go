package parity

// Las nueve tablas de FHIRPathModelData de gofhir/models y su equivalente en
// el Model de fhirpkg, comparables clave a clave (plan 003 §3.4, H5). Las claves
// de models son privadas: se leen con reflect (MapKeys, sin Interface()); los
// valores se piden por los métodos públicos de las dos partes, así que se
// compara el comportamiento y no la representación.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	fhirpath "github.com/gofhir/fhirpath"
	"github.com/gofhir/fhirpkg"
	r4 "github.com/gofhir/models/r4/v2"
	r4b "github.com/gofhir/models/r4b/v2"
	r5 "github.com/gofhir/models/r5/v2"
)

// refModel es lo que se le pide a models: la interfaz de fhirpath y las tres
// opcionales.
type refModel interface {
	fhirpath.Model
	fhirpath.VersionedModel
	fhirpath.TypeRegistry
	fhirpath.ElementOrder
	fhirpath.FHIRTypes
}

// version es una versión FHIR con sus dos modelos.
type version struct {
	name, lock string
	ref        refModel
	ctx        *fhirpkg.Context
	ours       *fhirpkg.Model
	set        *fhirpkg.Set
}

var versions = []struct {
	name, lock string
	ref        refModel
}{
	{"R4", "r4.lock", r4.FHIRPathModel()},
	{"R4B", "r4b.lock", r4b.FHIRPathModel()},
	{"R5", "r5.lock", r5.FHIRPathModel()},
}

var (
	loadOnce sync.Once
	loaded   []*version
	loadErr  error
)

// packagesDir es la caché de packages de fhirpkg (make parity la fija).
func packagesDir(t testing.TB) string {
	t.Helper()
	dir := os.Getenv("FHIRPKG_PACKAGES")
	if dir == "" {
		t.Skip("FHIRPKG_PACKAGES is not set (make parity)")
	}
	return dir
}

// loadVersions construye una sola vez el Context de cada versión.
func loadVersions(t testing.TB) []*version {
	t.Helper()
	dir := packagesDir(t)
	loadOnce.Do(func() {
		for _, v := range versions {
			l, err := fhirpkg.ReadLockFile(filepath.Join("..", "testdata", v.lock))
			if err != nil {
				loadErr = err
				return
			}
			set, err := fhirpkg.Open(l, dir)
			if err != nil {
				loadErr = err
				return
			}
			c, err := fhirpkg.NewContext(set)
			if err != nil {
				loadErr = err
				return
			}
			loaded = append(loaded, &version{name: v.name, lock: v.lock, ref: v.ref, ctx: c, ours: c.Model(), set: set})
		}
	})
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

// table es una de las nueve tablas: sus claves en models y cómo se pide el
// valor a cada parte (un valor, o la lista unida con "|").
type table struct {
	name  string // nombre del campo privado de models
	value func(m interface{}, key string) string
}

func join(xs []string) string {
	if xs == nil {
		return ""
	}
	return "[" + strings.Join(xs, "|") + "]"
}

func present(b bool) string {
	if b {
		return "yes"
	}
	return ""
}

// model es lo común a models y a fhirpkg.Model para pedir un valor.
type model interface {
	ChoiceTypes(string) []string
	TypeOf(string) string
	ReferenceTargets(string) []string
	ParentType(string) string
	ResolvePath(string) string
	ChildElements(string) []string
	FHIRTypeOf(string) string
	HasType(string) bool
	IsResource(string) bool
}

var tables = []table{
	{"choiceTypePaths", func(m interface{}, k string) string { return join(m.(model).ChoiceTypes(k)) }},
	{"path2Type", func(m interface{}, k string) string { return m.(model).TypeOf(k) }},
	{"path2RefType", func(m interface{}, k string) string { return join(m.(model).ReferenceTargets(k)) }},
	{"type2Parent", func(m interface{}, k string) string { return m.(model).ParentType(k) }},
	{"pathsDefinedElsewhere", func(m interface{}, k string) string {
		if r := m.(model).ResolvePath(k); r != k {
			return r
		}
		return ""
	}},
	{"childElements", func(m interface{}, k string) string { return join(m.(model).ChildElements(k)) }},
	{"fhirTypes", func(m interface{}, k string) string { return m.(model).FHIRTypeOf(k) }},
	{"resources", func(m interface{}, k string) string { return present(m.(model).IsResource(k)) }},
	{"types", func(m interface{}, k string) string { return present(m.(model).HasType(k)) }},
}

// refKeys son las claves de una tabla privada de models.
func refKeys(t testing.TB, ref refModel, field string) []string {
	t.Helper()
	v := reflect.ValueOf(ref)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		t.Fatalf("%T is not a pointer to a struct", ref)
	}
	f := v.Elem().FieldByName(field)
	if !f.IsValid() || f.Kind() != reflect.Map {
		t.Fatalf("%T has no map field %q: models changed its layout", ref, field)
	}
	var out []string
	for _, k := range f.MapKeys() {
		out = append(out, k.String())
	}
	sort.Strings(out)
	return out
}

// diff es el resultado de comparar una tabla.
type diff struct {
	OnlyRef  []string             // claves solo de models
	OnlyOurs []string             // claves solo de fhirpkg
	Differ   map[string][2]string // clave -> {models, fhirpkg}
	Keys     [2]int               // cuántas claves tiene cada parte
}

// compare compara una tabla. Las claves de fhirpkg son las del universo que
// responden con un valor no vacío: Paths() más las claves de models.
func compare(t testing.TB, v *version, tb table) diff {
	t.Helper()
	ref := refKeys(t, v.ref, tb.name)
	universe := map[string]bool{}
	for _, k := range ref {
		universe[k] = true
	}
	for _, k := range v.ours.Paths() {
		universe[k] = true
	}
	refSet := map[string]bool{}
	for _, k := range ref {
		refSet[k] = true
	}
	d := diff{Differ: map[string][2]string{}}
	keys := make([]string, 0, len(universe))
	for k := range universe {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a, b := tb.value(v.ref, k), tb.value(v.ours, k)
		inRef := refSet[k]
		inOurs := b != ""
		if inRef {
			d.Keys[0]++
		}
		if inOurs {
			d.Keys[1]++
		}
		switch {
		case inRef && !inOurs:
			d.OnlyRef = append(d.OnlyRef, k)
		case !inRef && inOurs:
			d.OnlyOurs = append(d.OnlyOurs, k)
		case inRef && inOurs && a != b:
			d.Differ[k] = [2]string{a, b}
		}
	}
	return d
}

func (d diff) String() string {
	return fmt.Sprintf("keys %d/%d, only models %d, only fhirpkg %d, different %d", d.Keys[0], d.Keys[1], len(d.OnlyRef), len(d.OnlyOurs), len(d.Differ))
}
