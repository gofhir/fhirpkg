package fhirpkg

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Los tests con los packages oficiales corren con FHIRPKG_PACKAGES=<carpeta con
// los .tgz> (make check-packages los descarga con SHA-256 fijado). Sin esa
// variable se omiten: make check no necesita red.
func packagesDir(t *testing.T) string {
	dir := os.Getenv("FHIRPKG_PACKAGES")
	if dir == "" {
		t.Skip("FHIRPKG_PACKAGES is not set (make check-packages)")
	}
	return dir
}

var realLocks = map[string]string{ // lock -> versión FHIR esperada
	"r4-core.lock":             "4.0.1",
	"r4.lock":                  "4.0.1",
	"r4b.lock":                 "4.3.0",
	"r4b-terminology-r4b.lock": "4.3.0",
	"r5.lock":                  "5.0.0",
}

func readRealLock(t *testing.T, name string) Lock {
	l, err := ReadLockFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Dorado: resolver otra vez las raíces y las excepciones de cada lock da los
// mismos bytes.
func TestRealLocksAreReproducible(t *testing.T) {
	reg := DirRegistry{Dir: packagesDir(t)}
	for name := range realLocks {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		l := readRealLock(t, name)
		got, err := Resolve(context.Background(), l.Roots, reg, Options{Exceptions: l.Exceptions})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, _ := got.Marshal()
		if string(b) != string(want) {
			t.Errorf("%s: resolving again gives another lock (fhirpkg lock, review the diff)", name)
		}
	}
}

func TestRealOpen(t *testing.T) {
	dir := packagesDir(t)
	for name, version := range realLocks {
		l := readRealLock(t, name)
		start := time.Now()
		s, err := Open(l, dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.FHIRVersion() != version {
			t.Errorf("%s: version %s, want %s", name, s.FHIRVersion(), version)
		}
		sps := 0
		for _, r := range s.Resources(l.Core) {
			if r.ResourceType == "SearchParameter" {
				sps++
			}
		}
		if sps < 1000 {
			t.Errorf("%s: the core has %d SearchParameter, want over 1000", name, sps)
		}
		t.Logf("%s: FHIR %s, %d packages read in %s, %d core resources, %d SearchParameter", name, s.FHIRVersion(), len(s.Packages()), time.Since(start).Round(time.Millisecond), len(s.Resources(l.Core)), sps)
	}
}

// El núcleo R4 declara su versión solo en el campo legado fhir-version-list.
func TestRealR4CoreUsesTheLegacyVersionList(t *testing.T) {
	dir := packagesDir(t)
	l := readRealLock(t, "r4-core.lock")
	core, _ := l.Package(l.Core)
	if !core.LegacyVersionList || !slices.Equal(core.FHIRVersions, []string{"4.0.1"}) || l.FHIRVersion != "4.0.1" {
		t.Fatalf("%+v", core)
	}
	s, err := Open(l, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Packages()[0].Manifest.LegacyVersionList || s.Packages()[0].Manifest.Type != "fhir.core" {
		t.Fatalf("%+v", s.Packages()[0].Manifest)
	}
}

// D1 sobre el grafo real de R4B: terminology.r4 trae r4.core, de otra serie.
func TestRealR4BGraphExcludesTheR4Core(t *testing.T) {
	dir := packagesDir(t)
	reg := DirRegistry{Dir: dir}
	roots := []string{"hl7.fhir.r4b.core#4.3.0", "hl7.fhir.uv.extensions.r4b#5.3.0"}
	_, err := Resolve(context.Background(), roots, reg, Options{})
	wantErr(t, err, "hl7.terminology.r4#7.1.0", "hl7.fhir.uv.extensions.r4#5.2.0", "another FHIR series")

	l := readRealLock(t, "r4b.lock")
	core4, ok := l.Package("hl7.fhir.r4.core#4.0.1")
	if !ok || !core4.Excluded || !slices.Equal(core4.Via, []string{"hl7.terminology.r4#7.1.0"}) {
		t.Fatalf("%+v", core4)
	}
	// El núcleo excluido no se lee, pero se verifica: sin su .tgz el lock no abre.
	tmp := t.TempDir()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == PackageFile("hl7.fhir.r4.core", "4.0.1") {
			continue
		}
		if err := os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(tmp, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Open(l, tmp); err == nil {
		t.Fatal("Open accepted a lock without the excluded core's file")
	}
	if err := os.Symlink(filepath.Join(dir, PackageFile("hl7.fhir.r4.core", "4.0.1")), filepath.Join(tmp, PackageFile("hl7.fhir.r4.core", "4.0.1"))); err != nil {
		t.Fatal(err)
	}
	s, err := Open(l, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if s.FHIRVersion() != "4.3.0" || len(s.Resources("hl7.fhir.r4.core#4.0.1")) != 0 {
		t.Fatalf("version %s", s.FHIRVersion())
	}
}

// D2 sobre el grafo real: terminology.r4 7.0.1 como raíz y extensions.r4b, que
// pide la 7.1.0.
func TestRealDoubleTerminologyVersion(t *testing.T) {
	reg := DirRegistry{Dir: packagesDir(t)}
	roots := []string{"hl7.fhir.r4b.core#4.3.0", "hl7.fhir.uv.extensions.r4b#5.3.0", "hl7.terminology.r4#7.0.1"}
	_, err := Resolve(context.Background(), roots, reg, Options{})
	wantErr(t, err, "more than one version", "hl7.terminology.r4:",
		"7.0.1 requested by (root)", "7.1.0 requested by hl7.fhir.uv.extensions.r4b#5.3.0")
}

func TestRealRootOrderDoesNotChangeTheLock(t *testing.T) {
	reg := DirRegistry{Dir: packagesDir(t)}
	l := readRealLock(t, "r4b.lock")
	want, _ := l.Marshal()
	for i := 0; i < len(l.Roots); i++ {
		roots := append(slices.Clone(l.Roots[i:]), l.Roots[:i]...)
		got, err := Resolve(context.Background(), roots, reg, Options{Exceptions: l.Exceptions})
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := got.Marshal(); string(b) != string(want) {
			t.Fatalf("roots %v change the lock", roots)
		}
	}
}

// Los SHA-256 de los locks son los de scripts/packages.sha256: lo que se
// resuelve es lo que se descarga y verifica.
func TestRealLocksArePinnedByThePackagesFile(t *testing.T) {
	f, err := os.Open(filepath.Join("scripts", "packages.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pinned := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		sum, file, ok := strings.Cut(sc.Text(), "  ")
		if ok {
			pinned[file] = sum
		}
	}
	for name := range realLocks {
		for _, p := range readRealLock(t, name).Packages {
			if got := pinned[PackageFile(p.Name, p.Version)]; got != p.SHA256 {
				t.Errorf("%s: %s is %s in the lock and %q in scripts/packages.sha256", name, p.ID(), p.SHA256, got)
			}
		}
	}
}

// Contraste con la medición del plan (§1.3): el grafo de R4B trae un segundo
// núcleo que comparte 3.113 canónicos con r4b.core, 1.333 de ellos
// SearchParameter, con 18 de la misma versión.
func TestRealCoreOverlapOfTheR4BGraph(t *testing.T) {
	dir := packagesDir(t)
	reg := DirRegistry{Dir: dir}
	recs := map[string][]URLRecord{}
	for _, id := range []string{"hl7.fhir.r4.core#4.0.1", "hl7.fhir.r4b.core#4.3.0"} {
		name, version, _ := splitID(id)
		b, err := reg.Fetch(context.Background(), name, version)
		if err != nil {
			t.Fatal(err)
		}
		recs[id], _, err = ReadRecords(b)
		if err != nil {
			t.Fatal(err)
		}
	}
	ov := Overlaps(recs)
	if len(ov) != 1 {
		t.Fatalf("overlaps: %+v", ov)
	}
	p := ov[0]
	t.Logf("shared %d, SearchParameter %d, same version %d", p.Shared, p.ByType["SearchParameter"], p.SameVersion)
	if p.Shared != 3113 || p.ByType["SearchParameter"] != 1333 || p.SameVersion != 18 {
		t.Errorf("shared %d (want 3113), SearchParameter %d (want 1333), same version %d (want 18)", p.Shared, p.ByType["SearchParameter"], p.SameVersion)
	}
}

// Decisión del conjunto base (ADR-002, opción C): R4 y R4B usan la misma
// terminología (hl7.terminology.r4#7.1.0, el mismo .tgz).
func TestRealR4AndR4BShareTheTerminology(t *testing.T) {
	r4, ok4 := readRealLock(t, "r4.lock").Package("hl7.terminology.r4#7.1.0")
	r4b, ok4b := readRealLock(t, "r4b.lock").Package("hl7.terminology.r4#7.1.0")
	if !ok4 || !ok4b || r4.SHA256 != r4b.SHA256 || r4.Excluded || r4b.Excluded {
		t.Fatalf("r4: %+v, r4b: %+v", r4, r4b)
	}
}
