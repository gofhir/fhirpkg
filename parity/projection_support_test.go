package parity

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Apoyo común de los tests de la proyección: la lectura en crudo de los
// StructureDefinition del núcleo (solo para elegir nombres de `_elements` y para
// la paridad con HAPI) y el parseo de un JSON a mapas. La comparación contra el
// oráculo no usa nada de esto: ver projection_oracle_test.go.

type rawElem struct {
	Path       string `json:"path"`
	ID         string `json:"id"`
	Min        int    `json:"min"`
	IsSummary  bool   `json:"isSummary"`
	IsModifier bool   `json:"isModifier"`
	Cref       string `json:"contentReference"`
	Type       []struct {
		Code string `json:"code"`
	} `json:"type"`
}

type rawSD struct {
	name, kind string
	byParent   map[string][]*rawElem
	byPath     map[string]*rawElem
}

type rawCore map[string]*rawSD

func loadRawCore(t testing.TB, v *version) rawCore {
	t.Helper()
	core := rawCore{}
	for _, r := range v.set.Resources(v.set.Lock().Core) {
		if r.ResourceType != "StructureDefinition" {
			continue
		}
		var sd struct {
			Kind           string `json:"kind"`
			Type           string `json:"type"`
			Derivation     string `json:"derivation"`
			BaseDefinition string `json:"baseDefinition"`
			Snapshot       *struct {
				Element []*rawElem `json:"element"`
			} `json:"snapshot"`
		}
		if err := json.Unmarshal(r.Raw, &sd); err != nil {
			t.Fatal(err)
		}
		if sd.Kind == "logical" || sd.Snapshot == nil || (sd.Derivation != "specialization" && (sd.Derivation != "" || sd.BaseDefinition != "")) {
			continue
		}
		s := &rawSD{name: sd.Type, kind: sd.Kind, byParent: map[string][]*rawElem{}, byPath: map[string]*rawElem{}}
		for _, e := range sd.Snapshot.Element {
			if strings.Contains(e.ID, ":") {
				continue
			}
			s.byPath[e.Path] = e
			if i := strings.LastIndex(e.Path, "."); i >= 0 {
				s.byParent[e.Path[:i]] = append(s.byParent[e.Path[:i]], e)
			}
		}
		core[sd.Type] = s
	}
	return core
}

// sdOf devuelve el SD que define un alcance (un tipo o la ruta de un backbone).
func (c rawCore) sdOf(scope string) *rawSD {
	root, _, _ := strings.Cut(scope, ".")
	return c[root]
}

// slot dice si el alcance de un elemento es el de un recurso anidado.
func (c rawCore) isResourceSlot(code string) bool {
	return code == "Resource" || code == "DomainResource"
}

type keyInfo struct {
	elem   *rawElem
	code   string // el tipo de la variante
	name   string // el nombre del elemento sin [x]
	under  bool
	choice bool
}

// keys devuelve las claves JSON de un alcance.
func (c rawCore) keys(scope string) map[string]keyInfo {
	out := map[string]keyInfo{}
	sd := c.sdOf(scope)
	if sd == nil {
		return out
	}
	for _, e := range sd.byParent[scope] {
		name := e.Path[strings.LastIndex(e.Path, ".")+1:]
		if base, ok := strings.CutSuffix(name, "[x]"); ok {
			for _, ty := range e.Type {
				k := base + strings.ToUpper(ty.Code[:1]) + ty.Code[1:]
				out[k] = keyInfo{e, ty.Code, base, false, true}
				out["_"+k] = keyInfo{e, ty.Code, base, true, true}
			}
			continue
		}
		code := ""
		if len(e.Type) == 1 {
			code = e.Type[0].Code
		}
		out[name] = keyInfo{e, code, name, false, false}
		out["_"+name] = keyInfo{e, code, name, true, false}
	}
	return out
}

// child es el alcance de los hijos de un valor: "" si es un escalar.
func (c rawCore) child(ki keyInfo) string {
	e := ki.elem
	if e.Cref != "" {
		_, to, _ := strings.Cut(e.Cref, "#")
		return to
	}
	if len(c.sdOf(e.Path).byParent[e.Path]) > 0 {
		return e.Path
	}
	if _, ok := c[ki.code]; ok && ki.code != "" {
		return ki.code
	}
	return ""
}

// ---- Capa literal ----

type jmap = map[string]any

func parseUnordered(b []byte) (jmap, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var m jmap
	err := d.Decode(&m)
	return m, err
}

// elementNames devuelve, para una entrada, los nombres de elemento de la raíz que
// tiene, ordenados.
func (c rawCore) elementNames(in jmap, rt string) []string {
	seen := map[string]bool{}
	for k := range in {
		if ki, ok := c.keys(rt)[k]; ok {
			seen[ki.name] = true
		}
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// elementLists es una muestra determinista de listas de _elements de una entrada:
// el primer nombre, el último y los de posición par.
func elementLists(names []string) [][]string {
	if len(names) == 0 {
		return nil
	}
	var even []string
	for i, n := range names {
		if i%2 == 0 {
			even = append(even, n)
		}
	}
	lists := [][]string{{names[0]}, {names[len(names)-1]}}
	if len(even) > 1 {
		lists = append(lists, even)
	}
	return lists
}

// resourceOrChild dice qué hay bajo un elemento: un recurso (de tipo Resource o
// DomainResource) o los hijos de un alcance.
func (c rawCore) resourceOrChild(ki keyInfo, v any) (scope string, isRes bool) {
	if len(ki.elem.Type) == 1 && c.isResourceSlot(ki.elem.Type[0].Code) {
		if m, ok := v.(jmap); ok {
			rt, _ := m["resourceType"].(string)
			return rt, true
		}
		return "", true
	}
	return c.child(ki), false
}
