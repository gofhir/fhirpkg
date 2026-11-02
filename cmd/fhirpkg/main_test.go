package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofhir/fhirpkg"
)

func tgz(t *testing.T, manifest map[string]any, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	add := func(name, body string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	m, _ := json.Marshal(manifest)
	add("package/package.json", string(m))
	for n, b := range files {
		add("package/"+n, b)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func fixture(t *testing.T) string {
	dir := t.TempDir()
	write := func(name, version string, manifest map[string]any, files map[string]string) {
		manifest["name"], manifest["version"] = name, version
		if err := os.WriteFile(filepath.Join(dir, fhirpkg.PackageFile(name, version)), tgz(t, manifest, files), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("core.b", "4.3.0", map[string]any{"type": "Core", "fhirVersions": []string{"4.3.0"}},
		map[string]string{"a.json": `{"resourceType":"ValueSet","url":"http://x/a","version":"1.0.0"}`})
	write("core.a", "4.0.1", map[string]any{"type": "fhir.core", "fhir-version-list": []string{"4.0.1"}},
		map[string]string{"a.json": `{"resourceType":"ValueSet","url":"http://x/a","version":"1.0.0"}`})
	write("ig", "1.0.0", map[string]any{"type": "IG", "fhirVersions": []string{"4.0.1"}, "dependencies": map[string]string{"core.a": "4.0.1"}},
		map[string]string{"b.json": `{"resourceType":"CodeSystem","url":"http://x/b"}`})
	write("igb", "1.0.0", map[string]any{"type": "IG", "fhirVersions": []string{"4.3.0"}, "dependencies": map[string]string{"core.b": "4.3.0", "ig": "1.0.0"}}, nil)
	return dir
}

func TestLockVerifyAndExceptionsAreKept(t *testing.T) {
	dir := fixture(t)
	lockFile := filepath.Join(t.TempDir(), "x.lock")
	// Sin la excepción falla y no escribe nada.
	err := runLock([]string{"-registry", dir, "-o", lockFile, "core.b#4.3.0", "igb#1.0.0"})
	if err == nil || !strings.Contains(err.Error(), "another FHIR series") {
		t.Fatalf("error %v", err)
	}
	if _, err := os.Stat(lockFile); err == nil {
		t.Fatal("a failed lock wrote a file")
	}
	if err := runLock([]string{"-registry", dir, "-o", lockFile, "-except", "ig#1.0.0=needs core.a, not read", "core.b#4.3.0", "igb#1.0.0"}); err != nil {
		t.Fatal(err)
	}
	// Una segunda corrida sin -except conserva las excepciones del lock existente.
	if err := runLock([]string{"-registry", dir, "-o", lockFile, "igb#1.0.0", "core.b#4.3.0"}); err != nil {
		t.Fatal(err)
	}
	l, err := fhirpkg.ReadLockFile(lockFile)
	if err != nil || len(l.Exceptions) != 1 || l.Exceptions[0].Reason != "needs core.a, not read" {
		t.Fatalf("%+v %v", l.Exceptions, err)
	}
	if err := runVerify([]string{"-lock", lockFile, "-dir", dir}); err != nil {
		t.Fatal(err)
	}
	// Un archivo cambiado hace fallar verify.
	f := filepath.Join(dir, fhirpkg.PackageFile("ig", "1.0.0"))
	b, _ := os.ReadFile(f)
	b[len(b)-2] ^= 1
	_ = os.WriteFile(f, b, 0o644)
	if err := runVerify([]string{"-lock", lockFile, "-dir", dir}); err == nil {
		t.Fatal("verify accepted a changed file")
	}
	if err := runLock([]string{"-registry", dir, "-o", lockFile, "-except", "bad", "core.b#4.3.0"}); err == nil {
		t.Fatal("bad -except accepted")
	}
}

func TestGraphReportIsDeterministicAndShowsTheFindings(t *testing.T) {
	dir := fixture(t)
	cands := filepath.Join(t.TempDir(), "c.txt")
	_ = os.WriteFile(cands, []byte("# comment\n\nB | core.b#4.3.0 igb#1.0.0\nA | core.a#4.0.1 ig#1.0.0\nMissing | core.a#4.0.1 nope#1.0.0\n"), 0o644)
	out1 := filepath.Join(t.TempDir(), "r1.md")
	out2 := filepath.Join(t.TempDir(), "r2.md")
	for _, o := range []string{out1, out2} {
		if err := runGraph([]string{"-registry", dir, "-candidates", cands, "-o", o}); err != nil {
			t.Fatal(err)
		}
	}
	r1, _ := os.ReadFile(out1)
	r2, _ := os.ReadFile(out2)
	if string(r1) != string(r2) {
		t.Fatal("the report is not deterministic")
	}
	for _, want := range []string{
		"## B", "Cores of another series (D1, excluded): `core.a#4.0.1`",
		"Non-core packages of another series (need an exception): `ig#1.0.0`",
		"Lock without exceptions: error:", "Lock with an exception for each non-core package of another series: valid",
		"| `core.a#4.0.1` | `core.b#4.3.0` | 1 | 1 | 1 | 0 | 0 | ValueSet 1 |",
		"## A", "Lock without exceptions: valid.", "Packages in two versions (D2): none.",
		"## Missing", "The graph cannot be explored",
	} {
		if !strings.Contains(string(r1), want) {
			t.Errorf("report lacks %q", want)
		}
	}
}

func TestReadCandidatesRejectsBadLines(t *testing.T) {
	for _, in := range []string{"no separator", " | a#1", "label |", "label | "} {
		f := filepath.Join(t.TempDir(), "c.txt")
		_ = os.WriteFile(f, []byte(in), 0o644)
		if _, err := readCandidates(f); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}
