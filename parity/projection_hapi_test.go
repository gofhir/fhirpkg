package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gofhir/fhirpkg"
)

// Paridad de la proyección con HAPI (plan 005, D7; make hapi-projection, no
// bloqueante). HAPI 8.10.0 proyecta los ejemplos oficiales de cada versión con su
// parser (scripts/hapi/Projection.java) y aquí se compara qué elementos conserva
// cada uno (presencia de claves; los valores los reserializa HAPI y no se
// comparan). Cada diferencia cae en una clase; las cuentas están en
// testdata/hapi-projection.json y una diferencia sin clase falla.

const hapiMaxBytes = 200 << 10

type hapiJob struct{ name, mode string }

var hapiModes = []string{"true", "text", "data", "elements"}

// TestWriteHAPIJobs escribe las entradas y jobs.tsv en HAPI_OUT/<versión>/.
func TestWriteHAPIJobs(t *testing.T) {
	dir := os.Getenv("HAPI_OUT")
	if dir == "" {
		t.Skip("HAPI_OUT is not set (make hapi-projection)")
	}
	for _, v := range loadVersions(t) {
		core := loadRawCore(t, v)
		root := filepath.Join(dir, v.name)
		_ = os.RemoveAll(root)
		for _, d := range []string{"in", "out"} {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		var jobs strings.Builder
		walkAll(t, v.name, func(name string, raw []byte) {
			if len(raw) > hapiMaxBytes {
				return
			}
			norm, err := v.ctx.Normalize(raw)
			if err != nil {
				return
			}
			in, _ := parseUnordered(norm)
			rt, _ := in["resourceType"].(string)
			lists := elementLists(core.elementNames(in, rt))
			if len(lists) == 0 {
				return
			}
			path := filepath.Join(root, "in", name)
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			for _, m := range hapiModes {
				els := ""
				if m == "elements" {
					els = strings.Join(lists[len(lists)-1], ",")
				}
				fmt.Fprintf(&jobs, "%s\t%s\t%s\t%s\n", path, m, els, filepath.Join(root, "out", strings.TrimSuffix(name, ".json")+"."+m+".json"))
			}
		})
		if err := os.WriteFile(filepath.Join(root, "jobs.tsv"), []byte(jobs.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

type hapiCount struct {
	Version string `json:"version"`
	Mode    string `json:"mode"`
	Class   string `json:"class"`
	Count   int    `json:"count"`
}

// hapiWalk compara las claves de dos árboles y anota la clase de cada diferencia.
func (cl *classifier) hapiWalk(scope string, isRes, root bool, ours, hapi jmap) {
	keys := cl.c.keys(scope)
	for k, ov := range ours {
		hv, ok := hapi[k]
		if !ok {
			if k == "resourceType" {
				continue
			}
			cl.mark("fhirpkg only: " + cl.oursOnly(scope, k, root))
			continue
		}
		ki, known := keys[k]
		if !known || ki.under {
			continue
		}
		cs, res := cl.c.resourceOrChild(ki, ov)
		cl.hapiValue(cs, res, ov, hv)
	}
	for k := range hapi {
		if _, ok := ours[k]; ok || k == "resourceType" {
			continue
		}
		ki, known := keys[k]
		switch {
		case !known:
			cl.fail("%s.%s: HAPI keeps an unknown key", scope, k)
		case ki.under:
			cl.mark("HAPI only: _x of a kept primitive")
		default:
			cl.mark("HAPI only: " + cl.hapiOnly(ki, root))
		}
	}
}

// oursOnly explica por qué la especificación respalda a fhirpkg en una clave que
// HAPI quitó: la regla del plan 005 que la conserva o el dato del SD.
func (cl *classifier) oursOnly(scope, k string, root bool) string {
	if c := cl.extraClass(scope, k, root); c != "" {
		return c
	}
	ki := cl.c.keys(scope)[k]
	switch {
	case !root:
		return "inside a whole copy or a recursive projection"
	case cl.mode == "true" && ki.elem.IsSummary:
		return "isSummary"
	case cl.mode == "text" && (ki.name == "text" || ki.name == "id" || ki.name == "meta"):
		return "text, id or meta"
	case cl.mode == "elements" && cl.names[ki.name]:
		return "requested name"
	case cl.mode == "data":
		return "data keeps everything but text"
	}
	return "other"
}

// hapiOnly describe una clave que HAPI conserva y la especificación no pide.
func (cl *classifier) hapiOnly(ki keyInfo, root bool) string {
	switch {
	case !root:
		return "inside a whole copy or a recursive projection"
	case cl.mode == "true":
		return "not isSummary, not min 1, not a modifier"
	case cl.mode == "data":
		return "text kept by data"
	case cl.mode == "elements" && !cl.names[ki.name]:
		return "not requested"
	}
	return "other"
}

func (cl *classifier) hapiValue(scope string, isRes bool, ov, hv any) {
	switch o := ov.(type) {
	case jmap:
		h, ok := hv.(jmap)
		if !ok {
			return
		}
		if isRes {
			scope, _ = o["resourceType"].(string)
		}
		cl.hapiWalk(scope, isRes, false, o, h)
	case []any:
		h, ok := hv.([]any)
		if !ok {
			return
		}
		if len(o) != len(h) {
			cl.mark("array length differs")
			return
		}
		for i := range o {
			cl.hapiValue(scope, isRes, o[i], h[i])
		}
	}
}

func TestHAPIParity(t *testing.T) {
	dir := os.Getenv("HAPI_OUT")
	if dir == "" {
		t.Skip("HAPI_OUT is not set (make hapi-projection)")
	}
	got := map[string]int{}
	for _, v := range loadVersions(t) {
		core := loadRawCore(t, v)
		root := filepath.Join(dir, v.name)
		b, err := os.ReadFile(filepath.Join(root, "jobs.tsv"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			f := strings.Split(line, "\t")
			raw, _ := os.ReadFile(f[0])
			hout, err := os.ReadFile(f[3])
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(string(hout), "ERROR:") {
				got[fmt.Sprintf("%s|%s|HAPI failed to parse or encode", v.name, f[1])]++
				continue
			}
			var head struct{ ResourceType string }
			_ = json.Unmarshal(raw, &head)
			mode, els := fhirpkg.SummaryFalse, []string(nil)
			switch f[1] {
			case "true":
				mode = fhirpkg.SummaryTrue
			case "text":
				mode = fhirpkg.SummaryText
			case "data":
				mode = fhirpkg.SummaryData
			default:
				els = strings.Split(f[2], ",")
			}
			p, err := v.ctx.NewProjection(head.ResourceType, mode, els)
			if err != nil {
				t.Fatal(err)
			}
			ours, err := v.ctx.Project(raw, p)
			if err != nil {
				t.Fatal(err)
			}
			ot, _ := parseUnordered(ours)
			ht, err := parseUnordered(hout)
			if err != nil {
				t.Fatalf("%s: %v", f[3], err)
			}
			ot, _ = stripSubsetted(ot).(jmap)
			ht, _ = stripSubsetted(ht).(jmap)
			cl := &classifier{c: core, mode: f[1], classes: map[string]bool{}, names: map[string]bool{}}
			for _, n := range els {
				cl.names[n] = true
			}
			cl.hapiWalk(head.ResourceType, true, true, ot, ht)
			for _, e := range cl.errs {
				t.Errorf("%s %s %s: unclassified: %s", v.name, filepath.Base(f[0]), f[1], e)
			}
			if len(cl.classes) == 0 {
				cl.classes["same keys"] = true
			}
			for c := range cl.classes {
				got[fmt.Sprintf("%s|%s|%s", v.name, f[1], c)]++
			}
		}
	}
	var rows []hapiCount
	for k, n := range got {
		x := strings.SplitN(k, "|", 3)
		rows = append(rows, hapiCount{x[0], x[1], x[2], n})
	}
	sort.Slice(rows, func(i, j int) bool {
		return fmt.Sprint(rows[i].Version, rows[i].Mode, rows[i].Class) < fmt.Sprint(rows[j].Version, rows[j].Mode, rows[j].Class)
	})
	path := "testdata/hapi-projection.json"
	if os.Getenv("PARITY_UPDATE") != "" {
		out, _ := json.MarshalIndent(rows, "", " ")
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (PARITY_UPDATE=1 writes it)", err)
	}
	var pinned []hapiCount
	_ = json.Unmarshal(b, &pinned)
	want := map[string]int{}
	for _, r := range pinned {
		want[r.Version+"|"+r.Mode+"|"+r.Class] = r.Count
	}
	for k, n := range got {
		if want[k] != n {
			t.Errorf("%s: %d, testdata/hapi-projection.json says %d", k, n, want[k])
		}
		delete(want, k)
	}
	for k, n := range want {
		t.Errorf("%s: pinned %d and none occurs", k, n)
	}
}

// Clasificación de las diferencias con HAPI (informativa): no es la capa literal
// del oráculo, que se eliminó en el plan 005 §13.

type classifier struct {
	c       rawCore
	mode    string // true, text, data, elements
	classes map[string]bool
	errs    []string
	names   map[string]bool // los nombres pedidos (_elements), solo para HAPI
}

func (cl *classifier) mark(class string) { cl.classes[class] = true }

func (cl *classifier) fail(format string, args ...any) {
	cl.errs = append(cl.errs, fmt.Sprintf(format, args...))
}

// extraClass explica una clave que Project conserva y la capa literal no.
func (cl *classifier) extraClass(scope, k string, root bool) string {
	ki, ok := cl.c.keys(scope)[k]
	if !ok {
		return ""
	}
	// En true el literal ya tiene lo que es del resumen: lo que sobra es un
	// obligatorio o un modificador que no lo es. En text y _elements el literal no
	// tiene los modificadores ni, en _elements, los obligatorios.
	extra := cl.mode != "true" || !ki.elem.IsSummary
	switch {
	case ki.name == "contained" && !ki.under:
		return "contained referenced (D9)"
	case cl.mode == "elements" && root && (ki.name == "id" || ki.name == "meta"):
		return "id/meta (D4)"
	case ki.elem.Min > 0 && extra:
		if cl.mode == "elements" {
			return "mandatory (D4)"
		}
		return "mandatory (D8)"
	case ki.elem.IsModifier && extra:
		if cl.mode == "elements" {
			return "modifier (D4)"
		}
		return "modifier (D10)"
	}
	return ""
}

// stripTag quita el tag SUBSETTED del árbol de Project (la capa literal no lo
// agrega) y devuelve si lo había en cada recurso, para comprobarlo aparte.
func stripSubsetted(v any) any {
	switch x := v.(type) {
	case jmap:
		out := jmap{}
		for k, e := range x {
			if k == "meta" {
				if m, ok := e.(jmap); ok {
					mm := jmap{}
					for mk, me := range m {
						if mk == "tag" {
							var keep []any
							for _, tg := range me.([]any) {
								if tm, ok := tg.(jmap); ok && tm["code"] == "SUBSETTED" {
									continue
								}
								keep = append(keep, tg)
							}
							if len(keep) == 0 {
								continue
							}
							me = keep
						}
						mm[mk] = me
					}
					if len(mm) == 0 {
						continue
					}
					out[k] = mm
					continue
				}
			}
			out[k] = stripSubsetted(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = stripSubsetted(e)
		}
		return out
	}
	return v
}
