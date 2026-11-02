package fhirpkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

const manifestOK = `{"name":"p","version":"1.0.0","fhirVersions":["4.0.1"]}`

func TestScanTgzReadsConformanceResources(t *testing.T) {
	data := buildTgz(t, []entry{
		{name: "package/package.json", body: manifestOK},
		{name: "package/.index.json", body: `{"index-version":1,"files":[]}`},
		{name: "package/ValueSet-b.json", body: resource("ValueSet", "http://x/b", "1.0.0")},
		{name: "package/SearchParameter-a.json", body: resource("SearchParameter", "http://x/a", "")},
		{name: "package/ig-p.json", body: `{"resourceType":"ImplementationGuide"}`}, // sin url: no es de conformidad
		{name: "package/Bundle-x.json", body: `{"resourceType":"Bundle","type":"collection"}`},
		{name: "package/example/Patient-1.json", body: `{"resourceType":"Patient","url":"http://x/p"}`}, // ejemplos: subcarpeta
		{name: "package/other/readme.json", body: `{}`},
		{name: "package/notes.txt", body: `hello`},
		{name: "openapi/Patient.schema.json", body: `{"not":"read"}`},
		{name: "package/", typeflag: tar.TypeDir},
	})
	c, err := scanTgz(data, DefaultLimits, true)
	if err != nil {
		t.Fatal(err)
	}
	if c.manifest.ID() != "p#1.0.0" {
		t.Fatalf("manifest %+v", c.manifest)
	}
	if len(c.resources) != 2 || c.resources[0].File != "SearchParameter-a.json" || c.resources[1].File != "ValueSet-b.json" {
		t.Fatalf("resources %+v", c.resources)
	}
	if c.skipped != 2 {
		t.Fatalf("skipped %d, want 2", c.skipped)
	}
	r := c.resources[1]
	if r.ResourceType != "ValueSet" || r.URL != "http://x/b" || r.Version != "1.0.0" || !strings.Contains(string(r.Raw), `"url"`) {
		t.Fatalf("resource %+v", r)
	}
	// Sin recursos: solo el manifiesto.
	c, err = scanTgz(data, DefaultLimits, false)
	if err != nil || len(c.resources) != 0 || c.manifest.Name != "p" {
		t.Fatalf("manifest-only scan: %+v %v", c, err)
	}
}

func TestScanTgzRejectsUnsafeOrBrokenArchives(t *testing.T) {
	ok := entry{name: "package/package.json", body: manifestOK}
	cases := []struct {
		name    string
		entries []entry
		want    string
	}{
		{"zip-slip", []entry{ok, {name: "package/../../etc/x.json", body: "{}"}}, "escapes"},
		{"zip-slip in the middle", []entry{ok, {name: "package/a/../../x.json", body: "{}"}}, "escapes"},
		{"absolute path", []entry{ok, {name: "/etc/passwd", body: "x"}}, "absolute"},
		{"backslash", []entry{ok, {name: `package\..\x.json`, body: "{}"}}, "unsafe"},
		{"symlink", []entry{ok, {name: "package/l.json", typeflag: tar.TypeSymlink, link: "/etc/passwd"}}, "links"},
		{"hardlink", []entry{ok, {name: "package/h.json", typeflag: tar.TypeLink, link: "package/package.json"}}, "links"},
		{"char device", []entry{ok, {name: "package/dev", typeflag: tar.TypeChar}}, "special"},
		{"duplicate entry", []entry{ok, {name: "package/a.json", body: "{}"}, {name: "package/a.json", body: "{}"}}, "duplicate"},
		{"duplicate through ./", []entry{ok, {name: "package/a.json", body: "{}"}, {name: "./package/a.json", body: "{}"}}, "duplicate"},
		{"no manifest", []entry{{name: "package/a.json", body: resource("ValueSet", "http://x", "")}}, "missing"},
		{"manifest in a subfolder", []entry{{name: "package/x/package.json", body: manifestOK}}, "missing"},
		{"invalid manifest", []entry{{name: "package/package.json", body: `{"name":"a"}`}}, "version"},
		{"invalid resource JSON", []entry{ok, {name: "package/a.json", body: `{"resourceType":`}}, "invalid JSON"},
		{"resource with a numeric url", []entry{ok, {name: "package/a.json", body: `{"resourceType":"X","url":3}`}}, "invalid JSON"},
		{"empty archive", nil, "missing"},
	}
	for _, c := range cases {
		_, err := scanTgz(buildTgz(t, c.entries), DefaultLimits, true)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", c.name, err, c.want)
		}
	}
}

func TestScanTgzNotGzipOrTruncated(t *testing.T) {
	good := buildTgz(t, []entry{{name: "package/package.json", body: manifestOK}, {name: "package/a.json", body: resource("ValueSet", "http://x/a", "")}})
	for name, data := range map[string][]byte{
		"empty":     nil,
		"plain":     []byte("this is not gzip"),
		"truncated": good[:len(good)/2],
		"tar only":  gunzip(t, good),
		"garbage":   append(good[:10:10], bytes.Repeat([]byte{0xff}, 50)...),
	} {
		if _, err := scanTgz(data, DefaultLimits, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func gunzip(t *testing.T, b []byte) []byte {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestScanTgzLimits(t *testing.T) {
	big := strings.Repeat("a", 1000)
	data := buildTgz(t, []entry{
		{name: "package/package.json", body: manifestOK},
		{name: "package/big.json", body: `{"resourceType":"X","url":"http://x","pad":"` + big + `"}`},
	})
	if _, err := scanTgz(data, Limits{MaxEntry: 500, MaxTotal: 1 << 20, MaxEntries: 10}, true); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("entry limit: %v", err)
	}
	if _, err := scanTgz(data, Limits{MaxEntry: 1 << 20, MaxTotal: 1100, MaxEntries: 10}, true); err == nil || !strings.Contains(err.Error(), "total") {
		t.Errorf("total limit: %v", err)
	}
	if _, err := scanTgz(data, Limits{MaxEntry: 1 << 20, MaxTotal: 1 << 20, MaxEntries: 1}, true); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Errorf("entries limit: %v", err)
	}
	// Una entrada fuera de package/ también cuenta (no se lee, pero pesa).
	data = buildTgz(t, []entry{{name: "package/package.json", body: manifestOK}, {name: "other/big.bin", body: big}})
	if _, err := scanTgz(data, Limits{MaxEntry: 500, MaxTotal: 1 << 20, MaxEntries: 10}, true); err == nil {
		t.Error("entry outside package/ escapes the size limit")
	}
	// Dentro de los límites.
	if _, err := scanTgz(data, DefaultLimits, true); err != nil {
		t.Error(err)
	}
}

// Una bomba de compresión: un archivo de 200 MB de ceros comprime a unos KB y
// se rechaza por el encabezado, sin descomprimirlo.
func TestScanTgzZipBomb(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "package/package.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(manifestOK))})
	_, _ = tw.Write([]byte(manifestOK))
	const size = 200 << 20
	_ = tw.WriteHeader(&tar.Header{Name: "package/bomb.json", Typeflag: tar.TypeReg, Mode: 0o644, Size: size})
	zeros := make([]byte, 1<<20)
	for i := 0; i < size>>20; i++ {
		_, _ = tw.Write(zeros)
	}
	_ = tw.Close()
	_ = zw.Close()
	if buf.Len() > 1<<20 {
		t.Fatalf("the bomb is not small: %d bytes", buf.Len())
	}
	_, err := scanTgz(buf.Bytes(), DefaultLimits, true)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("bomb accepted: %v", err)
	}
}

// Metamórfico: el orden de las entradas no cambia el resultado.
func TestScanTgzEntryOrderDoesNotMatter(t *testing.T) {
	entries := []entry{{name: "package/package.json", body: manifestOK}}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		entries = append(entries, entry{name: "package/" + n + ".json", body: resource("ValueSet", "http://x/"+n, "1.0.0")})
		entries = append(entries, entry{name: "package/sub/" + n + ".json", body: "{}"}, entry{name: "package/" + n + ".txt", body: "t"})
	}
	want, err := scanTgz(buildTgz(t, entries), DefaultLimits, true)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 100; i++ {
		es := append([]entry(nil), entries...)
		rng.Shuffle(len(es), func(a, b int) { es[a], es[b] = es[b], es[a] })
		got, err := scanTgz(buildTgz(t, es), DefaultLimits, true)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order %d changes the contents", i)
		}
	}
}

func FuzzReadTgz(f *testing.F) {
	f.Add(buildTgz(f, []entry{{name: "package/package.json", body: manifestOK}, {name: "package/a.json", body: resource("ValueSet", "http://x/a", "1")}}))
	f.Add(buildTgz(f, []entry{{name: "package/../x", body: "x"}}))
	f.Add(buildTgz(f, []entry{{name: "package/package.json", body: `{"name":"a","version":"1","dependencies":{"b":"1.0.x"}}`}}))
	f.Add([]byte("not gzip"))
	small := Limits{MaxEntry: 1 << 16, MaxTotal: 1 << 18, MaxEntries: 64}
	f.Fuzz(func(t *testing.T, data []byte) {
		c, err := scanTgz(data, small, true)
		if err != nil {
			return
		}
		if _, _, err := splitID(c.manifest.ID()); err != nil {
			t.Fatalf("accepted package with invalid id: %v", err)
		}
		last := ""
		for _, r := range c.resources {
			if r.File <= last || strings.Contains(r.File, "/") || r.URL == "" || r.ResourceType == "" {
				t.Fatalf("bad resource %q after %q", r.File, last)
			}
			last = r.File
		}
	})
}

// H8: sin recursos (el caso de los núcleos excluidos y del grafo) el .tgz se
// recorre entero con las mismas validaciones, aunque no se guarde ningún recurso.
func TestScanWithoutResourcesStillValidatesTheWholeTgz(t *testing.T) {
	m := entry{name: "package/package.json", body: manifestOK}
	cases := map[string][]entry{
		"repeated package.json":      {m, {name: "package/package.json", body: manifestOK}},
		"symlink after the manifest": {m, {name: "package/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}},
		"path escape after it":       {m, {name: "package/../x.json", body: "{}"}},
	}
	for name, es := range cases {
		if _, err := scanTgz(buildTgz(t, es), DefaultLimits, false); err == nil {
			t.Errorf("%s: accepted without resources", name)
		}
	}
	data := buildTgz(t, []entry{m, {name: "package/a.json", body: strings.Repeat(" ", 4096) + `{"resourceType":"X","url":"u"}`}})
	if _, err := scanTgz(data[:len(data)-10], DefaultLimits, false); err == nil {
		t.Error("truncated gzip accepted without resources")
	}
	// tar inválido tras un manifiesto válido, con el gzip íntegro.
	var raw bytes.Buffer
	zw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: m.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(m.body))})
	_, _ = tw.Write([]byte(m.body))
	_ = tw.Flush()
	_, _ = zw.Write(bytes.Repeat([]byte("x"), 512))
	_ = zw.Close()
	if _, err := scanTgz(raw.Bytes(), DefaultLimits, false); err == nil {
		t.Error("corrupt tar header accepted without resources")
	}
	c, err := scanTgz(buildTgz(t, []entry{m, {name: "package/a.json", body: resource("ValueSet", "u", "")}}), DefaultLimits, false)
	if err != nil || len(c.resources) != 0 || c.manifest.Name != "p" {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := scanTgz(buildTgz(t, []entry{{name: "package/x.json", body: "{}"}}), DefaultLimits, false); err == nil {
		t.Fatal("a package without package.json was accepted")
	}
}
