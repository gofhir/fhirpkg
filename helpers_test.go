package fhirpkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// entry es una entrada de un .tgz sintético.
type entry struct {
	name     string
	typeflag byte // 0 es un archivo regular
	body     string
	link     string
}

// buildTgz arma un .tgz con las entradas, en ese orden.
func buildTgz(t testing.TB, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Typeflag: tf, Mode: 0o644, Linkname: e.link}
		if tf == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if tf == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pkg describe un package sintético.
type pkg struct {
	name, version, typ string
	canonical          string // package.json canonical
	fhirVersions       []string
	legacyList         []string
	deps               map[string]string
	files              map[string]string // nombre dentro de package/ -> contenido
}

func (p pkg) id() string { return p.name + "#" + p.version }

func (p pkg) manifestJSON() string {
	m := map[string]any{"name": p.name, "version": p.version}
	if p.typ != "" {
		m["type"] = p.typ
	}
	if p.canonical != "" {
		m["canonical"] = p.canonical
	}
	if p.fhirVersions != nil {
		m["fhirVersions"] = p.fhirVersions
	}
	if p.legacyList != nil {
		m["fhir-version-list"] = p.legacyList
	}
	if p.deps != nil {
		m["dependencies"] = p.deps
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (p pkg) tgz(t testing.TB) []byte {
	t.Helper()
	entries := []entry{{name: "package/package.json", body: p.manifestJSON()}}
	names := make([]string, 0, len(p.files))
	for n := range p.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		entries = append(entries, entry{name: "package/" + n, body: p.files[n]})
	}
	return buildTgz(t, entries)
}

// resource es el JSON de un recurso de conformidad mínimo.
func resource(rt, url, version string) string {
	if version == "" {
		return fmt.Sprintf(`{"resourceType":%q,"url":%q}`, rt, url)
	}
	return fmt.Sprintf(`{"resourceType":%q,"url":%q,"version":%q}`, rt, url, version)
}

// memRegistry es un Registry en memoria; el valor es el .tgz.
type memRegistry map[string][]byte

func newMem(t testing.TB, pkgs ...pkg) memRegistry {
	m := memRegistry{}
	for _, p := range pkgs {
		m[p.id()] = p.tgz(t)
	}
	return m
}

func (m memRegistry) Versions(_ context.Context, name string) ([]string, error) {
	var out []string
	for id := range m {
		n, v, _ := strings.Cut(id, "#")
		if n == name {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	sort.Strings(out)
	return out, nil
}

func (m memRegistry) Fetch(_ context.Context, name, version string) ([]byte, error) {
	b, ok := m[name+"#"+version]
	if !ok {
		return nil, fmt.Errorf("%s#%s: %w", name, version, ErrNotFound)
	}
	return b, nil
}

// writeDir escribe los .tgz de un registro en memoria a una carpeta.
func (m memRegistry) writeDir(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	for id, b := range m {
		n, v, _ := strings.Cut(id, "#")
		if err := os.WriteFile(filepath.Join(dir, PackageFile(n, v)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Núcleos y packages de ejemplo para los escenarios.
func coreR4() pkg {
	return pkg{name: "core.r4", version: "4.0.1", typ: "fhir.core", legacyList: []string{"4.0.1"},
		files: map[string]string{"SearchParameter-a.json": resource("SearchParameter", "http://x/sp-a", "4.0.1")}}
}

func coreR4B() pkg {
	return pkg{name: "core.r4b", version: "4.3.0", typ: "Core", fhirVersions: []string{"4.3.0"},
		files: map[string]string{"SearchParameter-a.json": resource("SearchParameter", "http://x/sp-a", "4.3.0")}}
}

func ig(name, version string, fhir []string, deps map[string]string) pkg {
	return pkg{name: name, version: version, typ: "IG", fhirVersions: fhir, deps: deps,
		files: map[string]string{"ValueSet-" + name + ".json": resource("ValueSet", "http://x/vs/"+name, version)}}
}

func mustResolve(t testing.TB, roots []string, reg Registry, opts Options) Lock {
	t.Helper()
	l, err := Resolve(context.Background(), roots, reg, opts)
	if err != nil {
		t.Fatalf("Resolve(%v): %v", roots, err)
	}
	return l
}

func wantErr(t testing.TB, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want one containing %q", parts)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("error %q does not contain %q", err, p)
		}
	}
}

func removeFile(dir, name string) error { return os.Remove(filepath.Join(dir, name)) }

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// rawForTest reconstruye un package.json equivalente al manifiesto leído.
func (m Manifest) rawForTest() string {
	p := pkg{name: m.Name, version: m.Version, typ: m.Type, deps: m.Dependencies}
	if m.LegacyVersionList {
		p.legacyList = m.FHIRVersions
	} else {
		p.fhirVersions = m.FHIRVersions
	}
	return p.manifestJSON()
}
