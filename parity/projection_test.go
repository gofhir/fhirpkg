package parity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofhir/fhirpkg"
)

// Paridad exacta de la proyección con el oráculo independiente (plan 005 §13).
// Para cada recurso de los ejemplos oficiales, Synthea R4, los generados y la
// fuente `hand`, y para cada modo (true, text, data, false y una muestra de
// listas de _elements), Normalize(x) va al oráculo (un proceso por versión,
// scripts/projection_oracle.py) y su salida se compara con Project(x) token a
// token, con los miembros en orden (projection_oracle_test.go). Cualquier
// diferencia falla, con la ruta del primer nodo distinto. Una entrada que
// Normalize rechaza no tiene salida esperada: Project debe rechazarla con los
// mismos errores y rutas (regla 9).
//
// testdata/projection.json fija solo cuántos recursos se compararon o se saltaron
// por fuente, versión y modo, y la identidad del corpus (el hash de la lista de
// entradas): controlan que el corpus no se vacíe. Los saltos tienen motivo. Con
// PARITY_DIFFS=<archivo> se escriben todos los desacuerdos (JSONL) para agruparlos.

// oracleMaxBytes es el tamaño máximo de un recurso que se compara (los Bundles de
// búsqueda de decenas de MB se saltan, con motivo).
const oracleMaxBytes = 4 << 20

func projCountsPath() string { return "testdata/projection.json" }

type corpusPin struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Inputs  int    `json:"inputs"`
	Hash    string `json:"hash"`
}

type modePin struct {
	Source   string `json:"source"`
	Version  string `json:"version"`
	Mode     string `json:"mode"` // true, text, data, false, elements o rejected (regla 9)
	Compared int    `json:"compared"`
	Skipped  int    `json:"skipped"`
	Reason   string `json:"skipReason,omitempty"`
}

type projPins struct {
	Corpora []corpusPin `json:"corpora"`
	Modes   []modePin   `json:"modes"`
}

// parityDiff es un desacuerdo entre Project y el oráculo.
type parityDiff struct {
	Source, Version, Name, Mode string
	Elements                    []string
	Detail                      string
	Input                       string `json:",omitempty"`
	Project                     string `json:",omitempty"`
	Oracle                      string `json:",omitempty"`
}

const diffTextMax = 20 << 10

func mkProjection(v *version, rt, mode string, els []string) (*fhirpkg.Projection, error) {
	m := map[string]fhirpkg.SummaryMode{"true": fhirpkg.SummaryTrue, "text": fhirpkg.SummaryText, "data": fhirpkg.SummaryData, "false": fhirpkg.SummaryFalse, "elements": fhirpkg.SummaryFalse}[mode]
	return v.ctx.NewProjection(rt, m, els)
}

// errSig es la firma de un rechazo: tipo y ruta de cada error, en orden.
func errSig(err error) string {
	el, ok := err.(*fhirpkg.ErrorList)
	if !ok {
		return fmt.Sprintf("%T:%v", err, err)
	}
	var b strings.Builder
	for _, e := range el.Errors {
		fmt.Fprintf(&b, "%v@%s;", e.Kind, e.Path)
	}
	if el.Truncated {
		b.WriteString("truncated")
	}
	return b.String()
}

func TestProjectionParity(t *testing.T) {
	// PARITY_CORPUS=fast (scripts/mutations.py): solo los ejemplos oficiales y
	// `hand`, sin Synthea ni generados, parando en el primer desacuerdo y sin
	// comparar los pins (que son del corpus completo).
	fast := os.Getenv("PARITY_CORPUS") == "fast"
	srcs := []source{officialSource(), handSource()}
	if z := os.Getenv("SYNTHEA_ZIP"); z != "" && !fast {
		srcs = append(srcs, syntheaSource(z))
	}
	if d := os.Getenv("GEN_CORPUS"); d != "" && !fast {
		srcs = append(srcs, generatedSource(d))
	}
	var diffs []parityDiff
	var corpora []corpusPin
	modes := map[string]*modePin{}
	ran := map[string]bool{}
	var reports []string
	var pinMu sync.Mutex
	pin := func(src, ver, mode string, f func(*modePin)) {
		pinMu.Lock()
		defer pinMu.Unlock()
		k := src + "|" + ver + "|" + mode
		if modes[k] == nil {
			modes[k] = &modePin{Source: src, Version: ver, Mode: mode}
		}
		f(modes[k])
	}
	nw := oracleWorkers()
	for _, v := range loadVersions(t) {
		core := loadRawCore(t, v)
		pool := make([]*oracleProc, nw)
		for i := range pool {
			pool[i] = startOracle(t, v)
		}
		for _, src := range srcs {
			start := time.Now()
			h := sha256.New()
			inputs := 0
			run := newPoolRun(pool)
			ok := src.each(t, v.name, func(name string, raw []byte) {
				inputs++
				fmt.Fprintf(h, "%s\x00%d\x00%x\n", name, len(raw), sha256.Sum256(raw))
				if len(raw) > oracleMaxBytes {
					pin(src.name, v.name, "true", func(p *modePin) {
						p.Skipped++
						p.Reason = fmt.Sprintf("over %d MB", oracleMaxBytes>>20)
					})
					return
				}
				if !run.submit(func(o *oracleProc) ([]parityDiff, error) {
					return compareResource(v, core, o, src.name, name, raw, pin)
				}) && fast {
					return
				}
			})
			ds, err := run.wait()
			if err != nil {
				t.Fatal(err)
			}
			diffs = append(diffs, ds...)
			if fast && len(diffs) > 0 {
				d := diffs[0]
				t.Fatalf("%s %s %s [%s %v]: %s", d.Source, d.Version, d.Name, d.Mode, d.Elements, d.Detail)
			}
			if ok {
				ran[src.name] = true
				corpora = append(corpora, corpusPin{src.name, v.name, inputs, hex.EncodeToString(h.Sum(nil))})
				t.Logf("%s %s: %d inputs in %s (%d oracle processes)", src.name, v.name, inputs, time.Since(start).Round(time.Second), nw)
			}
		}
		var reps []string
		for _, o := range pool {
			rep, err := o.close()
			if err != nil {
				t.Fatal(err)
			}
			reps = append(reps, rep)
		}
		reports = append(reports, fmt.Sprintf("### %s\n\n```\n%s```\n", v.name, mergeRuleReports(reps)))
	}
	writeRulesReport(t, reports)
	if path := os.Getenv("PARITY_DIFFS"); path != "" {
		var b strings.Builder
		for _, d := range diffs {
			j, _ := json.Marshal(d)
			b.Write(j)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sort.SliceStable(diffs, func(i, j int) bool {
		a, b := diffs[i], diffs[j]
		return a.Version+"|"+a.Source+"|"+a.Name < b.Version+"|"+b.Source+"|"+b.Name
	})
	for i, d := range diffs {
		if i >= 25 {
			t.Errorf("... and %d more disagreements (PARITY_DIFFS=<file> writes them all)", len(diffs)-i)
			break
		}
		t.Errorf("%s %s %s [%s %v]: %s", d.Source, d.Version, d.Name, d.Mode, d.Elements, d.Detail)
	}
	if fast {
		return
	}
	var cur projPins
	cur.Corpora = corpora
	for _, m := range modes {
		cur.Modes = append(cur.Modes, *m)
	}
	sort.Slice(cur.Modes, func(i, j int) bool {
		a, b := cur.Modes[i], cur.Modes[j]
		return a.Source+"|"+a.Version+"|"+a.Mode < b.Source+"|"+b.Version+"|"+b.Mode
	})
	sort.Slice(cur.Corpora, func(i, j int) bool {
		a, b := cur.Corpora[i], cur.Corpora[j]
		return a.Source+"|"+a.Version < b.Source+"|"+b.Version
	})
	if os.Getenv("PARITY_UPDATE") != "" && len(ran) == len(srcs) {
		b, _ := json.MarshalIndent(cur, "", " ")
		if err := os.WriteFile(projCountsPath(), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(projCountsPath())
	if err != nil {
		t.Fatalf("%v (PARITY_UPDATE=1 writes it)", err)
	}
	var pinned projPins
	if err := json.Unmarshal(b, &pinned); err != nil {
		t.Fatal(err)
	}
	wantC := map[string]corpusPin{}
	for _, c := range pinned.Corpora {
		if ran[c.Source] {
			wantC[c.Source+"|"+c.Version] = c
		}
	}
	for _, c := range cur.Corpora {
		w, ok := wantC[c.Source+"|"+c.Version]
		if !ok || w != c {
			t.Errorf("corpus %s %s: %d inputs, hash %s; testdata/projection.json says %+v", c.Source, c.Version, c.Inputs, c.Hash[:12], w)
		}
		delete(wantC, c.Source+"|"+c.Version)
	}
	for k := range wantC {
		t.Errorf("corpus %s is pinned and did not run", k)
	}
	wantM := map[string]modePin{}
	for _, m := range pinned.Modes {
		if ran[m.Source] {
			wantM[m.Source+"|"+m.Version+"|"+m.Mode] = m
		}
	}
	for _, m := range cur.Modes {
		k := m.Source + "|" + m.Version + "|" + m.Mode
		if w, ok := wantM[k]; !ok || w != m {
			t.Errorf("%s: %+v; testdata/projection.json says %+v", k, m, w)
		}
		delete(wantM, k)
	}
	for k, m := range wantM {
		t.Errorf("%s: pinned %+v and none occurs", k, m)
	}
}

// writeRulesReport escribe el informe de reglas del oráculo (no bloqueante, plan
// 005 P13-7) junto al reporte de la paridad: <fecha>-projection-parity.md.
func writeRulesReport(t *testing.T, reports []string) {
	path := os.Getenv("PARITY_REPORT")
	if path == "" {
		return
	}
	path = filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), "-parity.md")+"-projection-parity.md")
	text := "# Projection parity with the independent oracle\n\nGenerated by `make parity` (`TestProjectionParity`, plan 005 section 13). For each version, how many times each rule of the oracle acted (mode, rule, count) over the whole corpus; informative, not blocking.\n\n" + strings.Join(reports, "\n")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// compareResource compara un recurso en todos los modos y devuelve sus
// desacuerdos.
func compareResource(v *version, core rawCore, o *oracleProc, srcName, name string, raw []byte, pin func(src, ver, mode string, f func(*modePin))) ([]parityDiff, error) {
	norm, nerr := v.ctx.Normalize(raw, fhirpkg.WithMaxBytes(1<<30))
	var head struct {
		ResourceType string `json:"resourceType"`
	}
	_ = json.Unmarshal(raw, &head)
	rt := head.ResourceType
	base := parityDiff{Source: srcName, Version: v.name, Name: name}
	if nerr != nil {
		// Regla 9: Project rechaza lo que Normalize rechaza, con los mismos errores.
		p, err := mkProjection(v, rt, "true", nil)
		if err != nil {
			pin(srcName, v.name, "rejected", func(p *modePin) {
				p.Skipped++
				p.Reason = "rejected input without a projectable resourceType"
			})
			return nil, nil
		}
		pin(srcName, v.name, "rejected", func(p *modePin) { p.Compared++ })
		_, perr := v.ctx.Project(raw, p, fhirpkg.WithMaxBytes(1<<30))
		if perr == nil || errSig(perr) != errSig(nerr) {
			d := base
			d.Mode, d.Input = "true", textOf(raw)
			d.Detail = fmt.Sprintf("rule 9: Normalize rejects with %s and Project with %v", errSig(nerr), perr)
			return []parityDiff{d}, nil
		}
		return nil, nil
	}
	in, err := parseUnordered(norm)
	if err != nil {
		return nil, err
	}
	rt, _ = in["resourceType"].(string)
	type job struct {
		mode string
		els  []string
	}
	jobs := []job{{"true", nil}, {"text", nil}, {"data", nil}, {"false", nil}}
	for _, els := range elementLists(core.elementNames(in, rt)) {
		jobs = append(jobs, job{"elements", els})
	}
	reqs := make([]oracleReq, len(jobs))
	for i, j := range jobs {
		mode := j.mode
		if mode == "elements" {
			mode = "false"
		}
		// El oráculo recibe los bytes originales: ordena él mismo por el modelo (plan 004
		// regla 2), así una corrupción del normalizador no queda escondida.
		reqs[i] = oracleReq{ID: fmt.Sprint(i), Mode: mode, Elements: j.els, Resource: string(raw)}
	}
	outs := make([][]byte, len(jobs))
	perrs := make([]error, len(jobs))
	resps, err := o.ask(reqs, func() {
		for i, j := range jobs {
			p, err := mkProjection(v, rt, j.mode, j.els)
			if err != nil {
				perrs[i] = err
				continue
			}
			outs[i], perrs[i] = v.ctx.Project(raw, p, fhirpkg.WithMaxBytes(1<<30))
		}
	})
	if err != nil {
		return nil, err
	}
	var diffs []parityDiff
	for i, j := range jobs {
		d := base
		d.Mode, d.Elements = j.mode, j.els
		pin(srcName, v.name, j.mode, func(p *modePin) { p.Compared++ })
		switch {
		case perrs[i] != nil:
			d.Detail = fmt.Sprintf("Project fails on an input Normalize accepts: %v", perrs[i])
		case resps[i].Error != "":
			// Una entrada que Normalize acepta es juzgable: un `error` del oráculo es un
			// fallo del protocolo, no un desacuerdo (code review o3). La respuesta de
			// error esperada solo existe para las entradas que Normalize rechaza (regla 9),
			// que no pasan por aquí.
			return nil, fmt.Errorf("%s: the oracle cannot judge an input Normalize accepts (%s %s): %s", oracleProtocolMarker, srcName, name, resps[i].Error)
		default:
			if det := compareOutputs(outs[i], []byte(resps[i].Output)); det != "" {
				d.Detail = det
				if len(raw) < diffTextMax && len(outs[i]) < diffTextMax {
					d.Input, d.Project, d.Oracle = string(raw), string(outs[i]), resps[i].Output
				}
			}
		}
		if d.Detail != "" {
			diffs = append(diffs, d)
		}
	}
	return diffs, nil
}

func textOf(b []byte) string {
	if len(b) > diffTextMax {
		return ""
	}
	return string(b)
}

// handSource son recursos a mano para lo que los ejemplos oficiales casi no
// tienen: primitivos repetidos con `_x` desalineado, un obligatorio que el filtro
// vaciaría y un subárbol obligatorio con valores que no pueden cambiar.
func handSource() source {
	ext := `{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]}`
	docs := map[string]string{
		"hand-given-underscore.json":         `{"resourceType":"Patient","name":[{"given":["Peter","James","Jim"],"_given":[null,{"extension":[{"url":"u","valueString":"v"}]},null],"family":"C"}]}`,
		"hand-given-only-null.json":          `{"resourceType":"Patient","name":[{"given":["A"],"_given":[null]}]}`,
		"hand-mandatory-emptied.json":        `{"resourceType":"Provenance","target":[{"reference":"Patient/p"}],"recorded":"2020-01-01T00:00:00Z","agent":[{"who":` + ext + `}]}`,
		"hand-mandatory-modifier.json":       `{"resourceType":"Appointment","status":"proposed","description":"removed","participant":[{"status":"accepted","modifierExtension":[{"url":"https://example.org/mod","valueString":"original"}]}]}`,
		"hand-modifier-component.json":       `{"resourceType":"Observation","status":"final","code":{"text":"test"},"note":[{"text":"removed"}],"component":[{"code":{"text":"test"},"modifierExtension":[{"url":"https://example.org/mod","valueString":"original"}]}]}`,
		"hand-contained-kept.json":           `{"resourceType":"Patient","managingOrganization":{"reference":"#o"},"contained":[{"resourceType":"Organization","id":"o","name":"original","alias":["removed"]}]}`,
		"hand-repeated-mandatory.json":       `{"resourceType":"Provenance","target":[{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]},{"reference":"Patient/p"}],"recorded":"2020-01-01T00:00:00Z","agent":[{"who":{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]}},{"who":{"reference":"Practitioner/p"},"role":[{"text":"removed"}]}]}`,
		"hand-r3-underscore-mandatory.json":  `{"resourceType":"Appointment","status":"proposed","description":"drop","participant":[{"_status":` + ext + `,"actor":{"reference":"Patient/p"}}]}`,
		"hand-r3-summary-descendant.json":    `{"resourceType":"Appointment","status":"proposed","description":"drop","participant":[{"status":"accepted","actor":{"reference":"Patient/p"}}]}`,
		"hand-r3-misaligned-underscore.json": `{"resourceType":"Patient","name":[{"given":["A",null,"B"],"_given":[null,{"extension":[{"url":"https://example.org/drop","valueString":"drop"}]},null]}]}`,
		"hand-r3-contained-chain.json":       `{"resourceType":"Patient","extension":[{"url":"https://example.org/drop","valueString":"drop"}],"managingOrganization":{"reference":"#o"},"contained":[{"resourceType":"Organization","id":"o","name":"O","partOf":{"reference":"#p"}},{"resourceType":"Organization","id":"p","name":"P"}]}`,
		"hand-r3-target-occurrence.json":     `{"resourceType":"Provenance","extension":[{"url":"https://example.org/drop","valueString":"drop"}],"target":[` + ext + `,{"reference":"Patient/p"}],"recorded":"2020-01-01T00:00:00Z","agent":[{"who":{"reference":"Practitioner/p"}}]}`,
		"hand-canonical-contained.json":      `{"resourceType":"Patient","modifierExtension":[{"url":"https://example.org/modifier","valueCanonical":"#vs"}],"contained":[{"resourceType":"ValueSet","id":"vs","status":"draft","modifierExtension":[{"url":"https://example.org/modifier","valueCanonical":"#vs2"}]},{"resourceType":"ValueSet","id":"vs2","status":"draft"}]}`,
		"hand-contained-cycle.json":          `{"resourceType":"Patient","managingOrganization":{"reference":"#a"},"contained":[{"resourceType":"Organization","id":"a","partOf":{"reference":"#b"}},{"resourceType":"Organization","id":"b","partOf":{"reference":"#a"}},{"resourceType":"Organization","id":"c","name":"dropped"}]}`,
		"hand-tag-already-present.json":      `{"resourceType":"Patient","meta":{"tag":[{"system":"http://terminology.hl7.org/CodeSystem/v3-ObservationValue","code":"SUBSETTED"}]},"gender":"male","extension":[{"url":"https://example.org/drop","valueString":"drop"}]}`,
		"hand-null-only-underscore.json":     `{"resourceType":"Patient","name":[{"given":["A"],"_given":[null]}],"birthDate":"1990","_birthDate":{"id":"x"}}`,
		"hand-shuffled-members.json":         `{"gender":"male","name":[{"given":["B","A"],"family":"F","_family":{"id":"f"},"use":"official"}],"birthDate":"1990-05-01","active":true,"id":"p","resourceType":"Patient","extension":[{"valueString":"v","url":"https://example.org/e"}],"meta":{"versionId":"2","lastUpdated":"2020-01-01T00:00:00Z"}}`,
		"hand-rejected-duplicate.json":       `{"resourceType":"Patient","id":"a","id":"b"}`,
		"hand-rejected-shape.json":           `{"resourceType":"Patient","name":{"family":"x"},"active":"yes"}`,
		"hand-rejected-unknown.json":         `{"resourceType":"Patient","nope":1}`,
		"hand-number-lexemes.json":           `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":1.50,"unit":"mg"},"component":[{"code":{"text":"c"},"valueInteger":10}]}`,
		"hand-mandatory-values.json":         `{"resourceType":"Appointment","status":"proposed","description":"Visit","participant":[{"status":"accepted"}]}`,
	}
	return source{"hand", func(t testing.TB, ver string, f func(string, []byte)) bool {
		names := make([]string, 0, len(docs))
		for n := range docs {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			f(n, []byte(docs[n]))
		}
		return true
	}}
}

// oracleWorkers es el número de procesos del oráculo por versión. El oráculo
// (Python, un hilo) es el 94 % del costo de la paridad, así que la comparación
// reparte los recursos entre varios procesos: uno por núcleo, hasta 4 (cada uno
// carga los packages y retiene unos cientos de MB; ver plan 005 §18).
// PARITY_ORACLES=<n> lo fija.
func oracleWorkers() int {
	if s := os.Getenv("PARITY_ORACLES"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	return max(1, min(runtime.NumCPU(), 4))
}

// poolRun reparte trabajos entre los procesos de un grupo, con a lo sumo uno en
// vuelo por proceso y uno esperando (la memoria de los recursos en curso queda
// acotada). El primer error detiene la entrega; los desacuerdos se juntan.
type poolRun struct {
	ch    chan func(*oracleProc) ([]parityDiff, error)
	wg    sync.WaitGroup
	mu    sync.Mutex
	diffs []parityDiff
	err   error
	stop  bool
}

func newPoolRun(pool []*oracleProc) *poolRun {
	r := &poolRun{ch: make(chan func(*oracleProc) ([]parityDiff, error), len(pool))}
	for _, o := range pool {
		r.wg.Add(1)
		go func(o *oracleProc) {
			defer r.wg.Done()
			for job := range r.ch {
				r.mu.Lock()
				stopped := r.stop
				r.mu.Unlock()
				if stopped {
					continue
				}
				ds, err := job(o)
				r.mu.Lock()
				r.diffs = append(r.diffs, ds...)
				if err != nil && r.err == nil {
					r.err = err
				}
				r.stop = r.stop || err != nil
				r.mu.Unlock()
			}
		}(o)
	}
	return r
}

// submit entrega un trabajo; false si ya se detuvo la corrida.
func (r *poolRun) submit(job func(*oracleProc) ([]parityDiff, error)) bool {
	r.mu.Lock()
	stopped := r.stop
	r.mu.Unlock()
	if stopped {
		return false
	}
	r.ch <- job
	return true
}

func (r *poolRun) wait() ([]parityDiff, error) {
	close(r.ch)
	r.wg.Wait()
	return r.diffs, r.err
}

// mergeRuleReports suma los informes de reglas (mode, rule, count) de los
// procesos del oráculo de una versión.
func mergeRuleReports(reps []string) string {
	type k struct{ mode, rule string }
	sum := map[k]int{}
	for _, rep := range reps {
		for i, line := range strings.Split(rep, "\n") {
			f := strings.Split(line, "\t")
			if i == 0 || len(f) != 3 {
				continue
			}
			n, _ := strconv.Atoi(f[2])
			sum[k{f[0], f[1]}] += n
		}
	}
	keys := make([]k, 0, len(sum))
	for key := range sum {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].mode != keys[j].mode {
			return keys[i].mode < keys[j].mode
		}
		return keys[i].rule < keys[j].rule
	})
	var b strings.Builder
	b.WriteString("mode\trule\tcount\n")
	for _, key := range keys {
		fmt.Fprintf(&b, "%s\t%s\t%d\n", key.mode, key.rule, sum[key])
	}
	return b.String()
}
