package fhirpkg

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchExamples lee los ejemplos oficiales de R4 y se queda con los que se
// normalizan sin error, hasta max. Se omite sin los packages.
func benchExamples(b *testing.B, max int) [][]byte {
	b.Helper()
	dir := os.Getenv("FHIRPKG_PACKAGES")
	if dir == "" {
		b.Skip("FHIRPKG_PACKAGES is not set (make check-packages)")
	}
	f, err := os.Open(filepath.Join(dir, "hl7.fhir.r4.examples-4.0.1.tgz"))
	if err != nil {
		b.Skip(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		b.Fatal(err)
	}
	tr := tar.NewReader(zr)
	c, _ := realContext(b, "r4.lock")
	var out [][]byte
	for len(out) < max {
		h, err := tr.Next()
		if err != nil {
			break
		}
		rest, ok := strings.CutPrefix(h.Name, "package/")
		if !ok || strings.Contains(rest, "/") || !strings.HasSuffix(rest, ".json") || h.Size > 200<<10 {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := c.Normalize(body); err == nil {
			out = append(out, body)
		}
	}
	return out
}

// BenchmarkNormalize mide Normalize sobre ejemplos oficiales de R4; los
// BenchmarkProject* miden Project con cada modo, que pasa por los mismos caminos
// del recorrido (campos, primitivos y sus extensiones filtrados).
func BenchmarkNormalize(b *testing.B) {
	c, _ := realContext(b, "r4.lock")
	in := benchExamples(b, 400)
	var n int64
	for _, x := range in {
		n += int64(len(x))
	}
	b.SetBytes(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, x := range in {
			if _, err := c.Normalize(x); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func benchProject(b *testing.B, mode SummaryMode, elements []string) {
	c, _ := realContext(b, "r4.lock")
	in := benchExamples(b, 400)
	type item struct {
		data []byte
		p    *Projection
	}
	projs := map[string]*Projection{}
	var items []item
	var n int64
	for _, x := range in {
		rt := resourceTypeOf(x)
		p, ok := projs[rt]
		if !ok {
			var err error
			p, err = c.NewProjection(rt, mode, elements)
			if err != nil {
				p = nil
			}
			projs[rt] = p
		}
		if p == nil {
			continue
		}
		if _, err := c.Project(x, p); err != nil {
			continue
		}
		items = append(items, item{x, p})
		n += int64(len(x))
	}
	if len(items) == 0 {
		b.Skip("no example projects")
	}
	b.SetBytes(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, it := range items {
			if _, err := c.Project(it.data, it.p); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkProjectSummaryTrue(b *testing.B) { benchProject(b, SummaryTrue, nil) }
func BenchmarkProjectSummaryData(b *testing.B) { benchProject(b, SummaryData, nil) }
func BenchmarkProjectElements(b *testing.B) {
	benchProject(b, SummaryFalse, []string{"id", "meta", "text"})
}

func resourceTypeOf(data []byte) string {
	s := string(data)
	i := strings.Index(s, `"resourceType"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i+14:], `"`)
	k := strings.Index(s[i+14+j+1:], `"`)
	return s[i+14+j+1 : i+14+j+1+k]
}
