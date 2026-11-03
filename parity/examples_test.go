package parity

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Los ejemplos oficiales de HL7 (hl7.fhir.r*.examples), los mismos que usa
// searchindex. Salen de EXAMPLES_PACKAGES (make parity lo apunta a la caché de
// packages de ../go-searchindex, que los descarga con su make check-packages).

var examplePackages = map[string]string{
	"R4":  "hl7.fhir.r4.examples-4.0.1.tgz",
	"R4B": "hl7.fhir.r4b.examples-4.3.0.tgz",
	"R5":  "hl7.fhir.r5.examples-5.0.0.tgz",
}

type example struct {
	name, resourceType string
	raw                []byte
}

var (
	exMu    sync.Mutex
	exCache = map[string][]example{}
)

// examplesOf lee una vez los ejemplos de la versión.
func examplesOf(t testing.TB, ver string) []example {
	t.Helper()
	dir := os.Getenv("EXAMPLES_PACKAGES")
	if dir == "" {
		t.Skip("EXAMPLES_PACKAGES is not set (make parity)")
	}
	exMu.Lock()
	defer exMu.Unlock()
	if ex, ok := exCache[ver]; ok {
		return ex
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
	var out []example
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		base := path.Base(h.Name)
		if path.Dir(h.Name) != "package" || !strings.HasSuffix(base, ".json") || base == "package.json" || strings.HasPrefix(base, ".") {
			continue
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		var head struct{ ResourceType string }
		if json.Unmarshal(raw, &head) != nil || head.ResourceType == "" {
			continue
		}
		out = append(out, example{base, head.ResourceType, raw})
	}
	exCache[ver] = out
	return out
}
