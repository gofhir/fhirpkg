package parity

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gofhir/fhirpkg"
)

// Validez de la proyección con el validador de HL7 (plan 005 §5.3). Una muestra
// fija y determinista de los ejemplos oficiales de cada versión se proyecta en
// cuatro modos; el validador (validator_cli, scripts/validator.sha256) juzga la
// entrada y cada salida, y todo error de la salida que no esté en la entrada
// falla. Los veredictos se guardan en testdata/projection-hl7/ y se comprueban
// aquí sin Java; scripts/validator_projection.py (make hl7-projection) los
// actualiza.

// projectionSampleSize es cuántos ejemplos de cada versión entran en la muestra;
// se fijó midiendo el costo por recurso (docs/evidence, plan 005 §9).
const projectionSampleSize = 100

// projectionSampleMaxBytes deja fuera los ejemplos grandes: el costo del
// validador crece con el tamaño y la muestra busca variedad de tipos.
const projectionSampleMaxBytes = 60 << 10

type sampleFile struct {
	Version  string   `json:"version"`
	Name     string   `json:"name"`
	Mode     string   `json:"mode"` // input, true, text, data o elements
	Elements []string `json:"elements,omitempty"`
	File     string   `json:"file"`
	SHA256   string   `json:"sha256"`
	Type     string   `json:"type"`
}

type sampleBody struct {
	sampleFile
	body []byte
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// projectionSample elige la muestra de la versión y arma sus archivos: la entrada
// original y las cuatro proyecciones. La elección ordena los ejemplos por el
// SHA-256 de versión y nombre y toma a lo sumo dos por tipo de recurso.
func projectionSample(t testing.TB, v *version) []sampleBody {
	t.Helper()
	type cand struct {
		key, name, rt string
		raw           []byte
	}
	var cands []cand
	walkAll(t, v.name, func(name string, raw []byte) {
		if len(raw) > projectionSampleMaxBytes {
			return
		}
		var head struct{ ResourceType string }
		_ = json.Unmarshal(raw, &head)
		cands = append(cands, cand{sha([]byte(v.name + "/" + name)), name, head.ResourceType, append([]byte(nil), raw...)})
	})
	sort.Slice(cands, func(i, j int) bool { return cands[i].key < cands[j].key })
	core := loadRawCore(t, v)
	perType := map[string]int{}
	var out []sampleBody
	picked := 0
	for _, c := range cands {
		if picked == projectionSampleSize {
			break
		}
		if perType[c.rt] >= 2 || !v.ctx.Model().IsResource(c.rt) {
			continue
		}
		norm, err := v.ctx.Normalize(c.raw)
		if err != nil {
			continue
		}
		perType[c.rt]++
		picked++
		in, _ := parseUnordered(norm)
		add := func(mode string, els []string, body []byte) {
			out = append(out, sampleBody{sampleFile{Version: v.name, Name: c.name, Mode: mode, Elements: els, SHA256: sha(body), Type: c.rt,
				File: strings.TrimSuffix(c.name, ".json") + "." + mode + ".json"}, body})
		}
		add("input", nil, c.raw)
		for _, mode := range []fhirpkg.SummaryMode{fhirpkg.SummaryTrue, fhirpkg.SummaryText, fhirpkg.SummaryData} {
			p, err := v.ctx.NewProjection(c.rt, mode, nil)
			if err != nil {
				t.Fatal(err)
			}
			body, err := v.ctx.Project(c.raw, p)
			if err != nil {
				t.Fatal(err)
			}
			add(map[fhirpkg.SummaryMode]string{fhirpkg.SummaryTrue: "true", fhirpkg.SummaryText: "text", fhirpkg.SummaryData: "data"}[mode], nil, body)
		}
		if lists := elementLists(core.elementNames(in, c.rt)); len(lists) > 0 {
			els := lists[len(lists)-1]
			p, err := v.ctx.NewProjection(c.rt, fhirpkg.SummaryFalse, els)
			if err != nil {
				t.Fatal(err)
			}
			body, err := v.ctx.Project(c.raw, p)
			if err != nil {
				t.Fatal(err)
			}
			add("elements", els, body)
		}
	}
	return out
}

// TestWriteProjectionSample escribe la muestra para el validador (make
// hl7-projection): PROJECTION_SAMPLE_OUT es la carpeta; sin ella se omite.
func TestWriteProjectionSample(t *testing.T) {
	dir := os.Getenv("PROJECTION_SAMPLE_OUT")
	if dir == "" {
		t.Skip("PROJECTION_SAMPLE_OUT is not set (make hl7-projection)")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	var manifest []sampleFile
	for _, v := range loadVersions(t) {
		if err := os.MkdirAll(filepath.Join(dir, v.name), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, s := range projectionSample(t, v) {
			s.File = v.name + "/" + s.File
			if err := os.WriteFile(filepath.Join(dir, s.File), s.body, 0o644); err != nil {
				t.Fatal(err)
			}
			manifest = append(manifest, s.sampleFile)
		}
	}
	f, err := os.Create(filepath.Join(dir, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, m := range manifest {
		b, _ := json.Marshal(m)
		fmt.Fprintf(f, "%s\n", b)
	}
	t.Logf("%d files", len(manifest))
}

// hl7Verdict es una línea de testdata/projection-hl7/<versión>.jsonl: los errores
// que el validador dio a un archivo, identificados por su SHA-256.
type hl7Verdict struct {
	SHA256 string   `json:"sha256"`
	Name   string   `json:"name"`
	Mode   string   `json:"mode"`
	Errors []string `json:"errors"` // «severidad|código|ruta sin índices|texto», ordenados y sin repetir
}

var indexRe2 = regexp.MustCompile(`\[\d+\]`)

func readVerdicts(t testing.TB, ver string) map[string]hl7Verdict {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "testdata", "projection-hl7", ver+".jsonl"))
	if err != nil {
		t.Fatalf("%v (make hl7-projection)", err)
	}
	defer f.Close()
	out := map[string]hl7Verdict{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var v hl7Verdict
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		out[v.SHA256] = v
	}
	return out
}

type divergence struct {
	Rule   string `json:"rule"`   // la regla del plan 005 que lo explica
	Match  string `json:"match"`  // expresión regular sobre «severidad|código|ruta|texto»
	Reason string `json:"reason"` // por qué la salida tiene un error que la entrada no tiene
	Count  int    `json:"count"`  // en cuántos archivos ocurre
	re     *regexp.Regexp
}

// TestProjectionHL7Verdicts (sin Java): la muestra de cada versión se proyecta de
// nuevo, cada archivo tiene su veredicto guardado (por SHA-256, así que un cambio
// en la proyección lo invalida), y todo error de una salida que no esté en su
// entrada está clasificado en testdata/projection-hl7/divergences.json, con su
// cuenta. Una divergencia sin clase o una clase que ya no ocurre falla.
func TestProjectionHL7Verdicts(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "testdata", "projection-hl7", "divergences.json"))
	if err != nil {
		t.Fatal(err)
	}
	var divs []*divergence
	if err := json.Unmarshal(b, &divs); err != nil {
		t.Fatal(err)
	}
	for _, d := range divs {
		d.re = regexp.MustCompile(d.Match)
	}
	got := map[string]int{}
	files, newFiles := 0, 0
	for _, v := range loadVersions(t) {
		verdicts := readVerdicts(t, v.name)
		var inputErrs map[string]bool
		var curName string
		for _, s := range projectionSample(t, v) {
			ve, ok := verdicts[s.SHA256]
			if !ok {
				t.Errorf("%s %s %s: no verdict for sha256 %s (make hl7-projection)", v.name, s.Name, s.Mode, s.SHA256[:12])
				continue
			}
			files++
			if s.Mode == "input" {
				inputErrs, curName = map[string]bool{}, s.Name
				for _, e := range ve.Errors {
					inputErrs[e] = true
				}
				continue
			}
			if s.Name != curName {
				t.Fatalf("sample order: %s after %s", s.Name, curName)
			}
			for _, e := range ve.Errors {
				if inputErrs[e] {
					continue
				}
				newFiles++
				matched := false
				for _, d := range divs {
					if d.re.MatchString(e) {
						got[d.Rule+"|"+d.Match]++
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("%s %s %s: an error of the output that the input does not have: %s", v.name, s.Name, s.Mode, e)
				}
			}
		}
	}
	for _, d := range divs {
		k := d.Rule + "|" + d.Match
		if got[k] != d.Count {
			t.Errorf("divergence %q: %d occurrences, divergences.json says %d", d.Match, got[k], d.Count)
		}
	}
	t.Logf("%d files with a verdict, %d new errors, all classified", files, newFiles)
}
