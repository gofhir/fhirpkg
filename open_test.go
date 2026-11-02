package fhirpkg

import (
	"archive/tar"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
)

// lockedSet resuelve el grafo de R4B y escribe los .tgz en una carpeta.
func lockedSet(t *testing.T) (Lock, string, memRegistry) {
	reg := r4bGraph(t)
	l := mustResolve(t, []string{"core.r4b#4.3.0", "ext.r4b#5.3.0"}, reg,
		Options{Exceptions: []Exception{{"term.r4#7.1.0", "dependency"}, {"ext.r4#5.2.0", "dependency"}}})
	return l, reg.writeDir(t), reg
}

func TestOpen(t *testing.T) {
	l, dir, _ := lockedSet(t)
	s, err := Open(l, dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.FHIRVersion() != "4.3.0" {
		t.Fatalf("version %s", s.FHIRVersion())
	}
	var ids []string
	for _, p := range s.Packages() {
		ids = append(ids, p.ID)
		if p.SHA256 == "" || p.Manifest.ID() != p.ID {
			t.Fatalf("package %+v", p)
		}
	}
	if !reflect.DeepEqual(ids, l.Order) {
		t.Fatalf("packages %v, order %v", ids, l.Order)
	}
	rs := s.Resources("core.r4b#4.3.0")
	if len(rs) != 1 || rs[0].URL != "http://x/sp-a" || rs[0].Version != "4.3.0" {
		t.Fatalf("resources %+v", rs)
	}
	if got := s.Resources("ext.r4b#5.3.0"); len(got) != 1 || got[0].ResourceType != "ValueSet" {
		t.Fatalf("resources %+v", got)
	}
	if s.Lock().Core != l.Core {
		t.Fatal("Lock()")
	}
	// Packages() devuelve una copia.
	ps := s.Packages()
	ps[0].ID = "mutated"
	if s.Packages()[0].ID == "mutated" {
		t.Fatal("Packages exposes internal state")
	}
}

func TestOpenFailsClosed(t *testing.T) {
	cases := map[string]struct {
		mutate func(t *testing.T, dir string, l *Lock)
		want   string
	}{
		"missing file": {func(t *testing.T, dir string, l *Lock) {
			_ = removeFile(dir, PackageFile("ext.r4", "5.2.0"))
		}, "is missing"},
		"different sha": {func(t *testing.T, dir string, l *Lock) {
			for i := range l.Packages {
				if l.Packages[i].Name == "ext.r4b" {
					l.Packages[i].SHA256 = strings.Repeat("0", 64)
				}
			}
		}, "SHA-256"},
		"file changed on disk": {func(t *testing.T, dir string, l *Lock) {
			f := filepath.Join(dir, PackageFile("term.r4", "7.1.0"))
			b, _ := os.ReadFile(f)
			b[len(b)-1] ^= 1
			_ = os.WriteFile(f, b, 0o644)
		}, "SHA-256"},
		"another package under the name": {func(t *testing.T, dir string, l *Lock) {
			// El archivo es de otro package, con su SHA fijado en el lock: lo detecta el manifiesto.
			other := ig("other", "1.0.0", []string{"4.3.0"}, nil).tgz(t)
			_ = os.WriteFile(filepath.Join(dir, PackageFile("ext.r4b", "5.3.0")), other, 0o644)
			for i := range l.Packages {
				if l.Packages[i].Name == "ext.r4b" {
					l.Packages[i].SHA256 = sha(other)
				}
			}
		}, "declares itself"},
		"manifest differs from the lock": {func(t *testing.T, dir string, l *Lock) {
			p := ig("ext.r4b", "5.3.0", []string{"4.3.0"}, map[string]string{"core.r4b": "4.3.0"}) // sin term.r4
			b := p.tgz(t)
			_ = os.WriteFile(filepath.Join(dir, PackageFile("ext.r4b", "5.3.0")), b, 0o644)
			for i := range l.Packages {
				if l.Packages[i].Name == "ext.r4b" {
					l.Packages[i].SHA256 = sha(b)
				}
			}
		}, "dependencies"},
		"zip-slip tgz": {func(t *testing.T, dir string, l *Lock) {
			b := buildTgz(t, []entry{{name: "package/package.json", body: ig("ext.r4", "5.2.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}).manifestJSON()},
				{name: "package/../../evil.json", body: "{}"}})
			_ = os.WriteFile(filepath.Join(dir, PackageFile("ext.r4", "5.2.0")), b, 0o644)
			for i := range l.Packages {
				if l.Packages[i].Name == "ext.r4" {
					l.Packages[i].SHA256 = sha(b)
				}
			}
		}, "escapes"},
		"exceptions removed": {func(t *testing.T, dir string, l *Lock) { l.Exceptions = l.Exceptions[:1] }, "exceptions"},
		"invalid lock":       {func(t *testing.T, dir string, l *Lock) { l.Order = nil }, "order"},
	}
	for name, c := range cases {
		l, dir, _ := lockedSet(t)
		c.mutate(t, dir, &l)
		s, err := Open(l, dir)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", name, err, c.want)
		}
		if s != nil {
			t.Errorf("%s: a Set was returned with the error", name)
		}
	}
}

func TestOpenSecondCoreInLock(t *testing.T) {
	// Un lock editado a mano que quita la marca de excluido a un núcleo de otra serie.
	l, dir, _ := lockedSet(t)
	for i := range l.Packages {
		if l.Packages[i].Name == "core.r4" {
			l.Packages[i].Excluded, l.Packages[i].ExcludedReason = false, ""
		}
	}
	l.Order = []string{"core.r4#4.0.1", "core.r4b#4.3.0", "ext.r4#5.2.0", "term.r4#7.1.0", "ext.r4b#5.3.0"}
	_, err := Open(l, dir)
	wantErr(t, err, "second core")
}

func TestOpenKeepsOnlyResourcesNotTarballs(t *testing.T) {
	l, dir, _ := lockedSet(t)
	s, err := Open(l, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Los bytes de un recurso son una copia propia, no una ventana del .tgz.
	for _, p := range s.Packages() {
		for _, r := range s.Resources(p.ID) {
			if cap(r.Raw) != len(r.Raw) {
				t.Fatalf("%s %s retains %d extra bytes", p.ID, r.File, cap(r.Raw)-len(r.Raw))
			}
		}
	}
}

func TestOpenIgnoresExtraFiles(t *testing.T) {
	l, dir, _ := lockedSet(t)
	_ = os.WriteFile(filepath.Join(dir, "unrelated-1.0.0.tgz"), []byte("junk"), 0o644)
	if _, err := Open(l, dir); err != nil {
		t.Fatal(err)
	}
}

// Metamórfico: el orden de los archivos dentro de cada .tgz no cambia el Set.
func TestOpenArchiveEntryOrderDoesNotMatter(t *testing.T) {
	reg := r4bGraph(t)
	exc := Options{Exceptions: []Exception{{"term.r4#7.1.0", "d"}, {"ext.r4#5.2.0", "d"}}}
	roots := []string{"core.r4b#4.3.0", "ext.r4b#5.3.0"}
	l := mustResolve(t, roots, reg, exc)
	s1, err := Open(l, reg.writeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	// Reescribe cada package con los archivos al revés (otro SHA, por eso otro lock).
	rev := memRegistry{}
	for id, b := range reg {
		c, _ := scanTgz(b, DefaultLimits, true)
		entries := []entry{}
		for i := len(c.resources) - 1; i >= 0; i-- {
			entries = append(entries, entry{name: "package/" + c.resources[i].File, body: string(c.resources[i].Raw)})
		}
		entries = append(entries, entry{name: "package/package.json", body: c.manifest.rawForTest()})
		rev[id] = buildTgz(t, entries)
	}
	l2 := mustResolve(t, roots, rev, exc)
	s2, err := Open(l2, rev.writeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range s1.Packages() {
		if !reflect.DeepEqual(s1.Resources(p.ID), s2.Resources(p.ID)) {
			t.Fatalf("%s: resources differ", p.ID)
		}
	}
}

// handLock arma, sin pasar por Resolve, un lock sin dependencias con los
// packages dados (el primero es el núcleo) y su carpeta. Los que están en
// excluded quedan marcados como excluidos y fuera del orden. Simula un lock
// editado a mano.
func handLock(t *testing.T, fhirVersion string, excluded map[string]bool, pkgs ...pkg) (Lock, string) {
	t.Helper()
	dir := t.TempDir()
	l := Lock{Schema: LockSchema, FHIRVersion: fhirVersion, Core: pkgs[0].id(), Roots: []string{pkgs[0].id()}}
	for _, p := range pkgs {
		b := p.tgz(t)
		if err := os.WriteFile(filepath.Join(dir, PackageFile(p.name, p.version)), b, 0o644); err != nil {
			t.Fatal(err)
		}
		lp := LockedPackage{Name: p.name, Version: p.version, SHA256: sha(b), FHIRVersions: p.fhirVersions}
		if p.legacyList != nil {
			lp.FHIRVersions, lp.LegacyVersionList = p.legacyList, true
		}
		if excluded[p.id()] {
			lp.Excluded, lp.ExcludedReason = true, "edited by hand"
		} else {
			l.Order = append(l.Order, p.id())
		}
		l.Packages = append(l.Packages, lp)
	}
	sort.Slice(l.Packages, func(i, j int) bool { return l.Packages[i].ID() < l.Packages[j].ID() })
	return l, dir
}

// H1: un lock editado no puede excluir un package que no sea el núcleo de otra
// serie, ni declarar como núcleo un package que no lo es.
func TestOpenRejectsExcludingANonCorePackage(t *testing.T) {
	l, dir, _ := lockedSet(t)
	for i := range l.Packages {
		if l.Packages[i].Name == "term.r4" {
			l.Packages[i].Excluded, l.Packages[i].ExcludedReason = true, "edited by hand"
		}
	}
	l.Order = slices.DeleteFunc(l.Order, func(id string) bool { return id == "term.r4#7.1.0" })
	l.Exceptions = slices.DeleteFunc(l.Exceptions, func(e Exception) bool { return e.Package == "term.r4#7.1.0" })
	s, err := Open(l, dir)
	wantErr(t, err, "term.r4#7.1.0", "excluded", "not a core package")
	if s != nil {
		t.Fatal("a Set was returned with the error")
	}
}

func TestOpenRejectsExcludingACoreOfTheLocksSeries(t *testing.T) {
	core := coreR4B()
	other := pkg{name: "core.other", version: "4.3.1", typ: "Core", fhirVersions: []string{"4.3.1"}}
	l, dir := handLock(t, "4.3.0", map[string]bool{other.id(): true}, core, other)
	_, err := Open(l, dir)
	wantErr(t, err, "core.other#4.3.1", "excluded", "series 4.3")
}

func TestOpenRejectsALockCoreThatIsNotACore(t *testing.T) {
	l, dir := handLock(t, "4.0.1", nil, ig("ig.a", "1.0.0", []string{"4.0.1"}, nil))
	_, err := Open(l, dir)
	wantErr(t, err, "ig.a#1.0.0", "not a core package")
}

func TestOpenVerifiesExcludedPackages(t *testing.T) {
	cases := map[string]struct {
		mutate func(dir string, l *Lock)
		want   string
	}{
		"missing file": {func(dir string, l *Lock) { _ = removeFile(dir, PackageFile("core.r4", "4.0.1")) }, "is missing"},
		"different sha": {func(dir string, l *Lock) {
			f := filepath.Join(dir, PackageFile("core.r4", "4.0.1"))
			b, _ := os.ReadFile(f)
			b[len(b)-1] ^= 1
			_ = os.WriteFile(f, b, 0o644)
		}, "SHA-256"},
		"manifest differs from the lock": {func(dir string, l *Lock) {
			for i := range l.Packages {
				if l.Packages[i].Name == "core.r4" {
					l.Packages[i].FHIRVersions = []string{"4.0.0"}
				}
			}
		}, "FHIR versions"},
	}
	for name, c := range cases {
		l, dir, _ := lockedSet(t)
		c.mutate(dir, &l)
		s, err := Open(l, dir)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "core.r4#4.0.1") {
			t.Errorf("%s: error %v, want core.r4#4.0.1 and %q", name, err, c.want)
		}
		if s != nil {
			t.Errorf("%s: a Set was returned with the error", name)
		}
	}
}

// El núcleo excluido se verifica pero no se guarda: sus recursos no están en el Set.
func TestOpenDoesNotKeepExcludedResources(t *testing.T) {
	l, dir, _ := lockedSet(t)
	s, err := Open(l, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Resources("core.r4#4.0.1")) != 0 || len(s.Packages()) != len(l.Order) {
		t.Fatalf("excluded core kept: %d resources, %d packages", len(s.Resources("core.r4#4.0.1")), len(s.Packages()))
	}
}

// H5: el lock y el manifiesto declaran las mismas dependencias, una vez cada
// una. [ig.a, ig.a] contra {ig.a, ig.b} tiene la misma cantidad y no puede
// ocultar a ig.b.
func TestMatchLockedRequiresEveryManifestDependencyOnce(t *testing.T) {
	m := Manifest{Name: "ig.c", Version: "1.0.0", Dependencies: map[string]string{"ig.a": "1.0.0", "ig.b": "1.0.0"}}
	lp := LockedPackage{Name: "ig.c", Version: "1.0.0", Dependencies: []string{"ig.a#1.0.0", "ig.a#1.0.0"}}
	wantErr(t, matchLocked(m, lp), "depends on ig.b", "does not list")
	lp.Dependencies = []string{"ig.a#1.0.0", "ig.b#1.0.0"}
	if err := matchLocked(m, lp); err != nil {
		t.Fatal(err)
	}
}

// H6: un excluido sin versión FHIR no declara una serie distinta de la del lock.
func TestOpenRejectsExcludingACoreWithoutFHIRVersion(t *testing.T) {
	core := coreR4B()
	other := pkg{name: "core.none", version: "1.0.0", typ: "Core"}
	l, dir := handLock(t, "4.3.0", map[string]bool{other.id(): true}, core, other)
	_, err := Open(l, dir)
	wantErr(t, err, "core.none#1.0.0", "excluded", "other than the lock's series 4.3")
}

// H7: los excluidos se verifican antes de leer los demás (falla temprano): con
// un package roto en el orden y un excluido sin archivo, el error es el del
// excluido.
func TestOpenVerifiesExcludedPackagesFirst(t *testing.T) {
	l, dir, _ := lockedSet(t)
	if err := removeFile(dir, PackageFile("core.r4", "4.0.1")); err != nil {
		t.Fatal(err)
	}
	for i := range l.Packages {
		if l.Packages[i].ID() == l.Order[0] {
			l.Packages[i].SHA256 = strings.Repeat("0", 64)
		}
	}
	_, err := Open(l, dir)
	wantErr(t, err, "core.r4#4.0.1", "is missing")
}

// H8: Open recorre el .tgz entero de un núcleo excluido: las entradas que
// siguen al manifiesto también se validan.
func TestOpenRejectsAnExcludedCoreWithABadTgz(t *testing.T) {
	m := entry{name: "package/package.json", body: coreR4().manifestJSON()}
	good := buildTgz(t, []entry{m})
	cases := map[string][]byte{
		"repeated package.json": buildTgz(t, []entry{m, m}),
		"symlink after the manifest": buildTgz(t, []entry{m,
			{name: "package/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}}),
		"truncated gzip": good[:len(good)-8],
	}
	for name, b := range cases {
		l, dir, _ := lockedSet(t)
		for i := range l.Packages {
			if l.Packages[i].Name == "core.r4" {
				l.Packages[i].SHA256 = sha(b)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, PackageFile("core.r4", "4.0.1")), b, 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := Open(l, dir)
		if err == nil || !strings.Contains(err.Error(), "core.r4#4.0.1") || s != nil {
			t.Errorf("%s: Open returned %v, %v", name, s, err)
		}
	}
}
