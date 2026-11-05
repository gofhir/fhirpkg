package fhirpkg

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Request es una petición de un package: quién lo pidió y con qué texto.
type Request struct {
	// From es el package que lo pide ("" si es una raíz).
	From string
	// Spec es lo pedido, tal como está escrito (versión exacta o comodín).
	Spec string
}

// Node es un package del grafo, con lo que se sabe de él sin leer sus recursos.
type Node struct {
	ID       string
	Manifest Manifest
	SHA256   string
	// Size es el tamaño del .tgz.
	Size int
	// Requests son todas las peticiones de esta versión exacta.
	Requests []Request
	// Via es el camino más corto desde una raíz hasta quien lo pide, sin
	// incluirlo.
	Via []string
	// Deps son sus dependencias con la versión exacta resuelta, ordenadas.
	Deps []string
}

// Graph es el grafo transitivo de dependencias de unas raíces. No aplica
// ninguna política (D1, D2): solo lo describe; Analyze lo juzga.
type Graph struct {
	// Roots son las raíces resueltas a versión exacta, ordenadas.
	Roots []string
	Nodes map[string]*Node
}

// IDs devuelve los name#version del grafo, ordenados.
func (g *Graph) IDs() []string {
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Explore resuelve el grafo transitivo de las raíces (name#version o
// name#M.m.x) contra un registro. Recorre en anchura y en orden determinista:
// el resultado no depende del orden de las raíces. Una dependencia que el
// registro no tiene es error. Si onPackage no es nil, se llama una vez por
// package con su .tgz (para quien necesita leer los recursos sin descargar
// dos veces).
func Explore(ctx context.Context, roots []string, reg Registry, onPackage func(id string, tgz []byte) error) (*Graph, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("no roots")
	}
	sorted := append([]string(nil), roots...)
	sort.Strings(sorted)
	g := &Graph{Nodes: map[string]*Node{}}
	var queue []string
	versions := map[string][]string{}

	request := func(from, name, spec string) (string, error) {
		vs, err := parseSpec(spec)
		if err != nil {
			if from == "" {
				return "", fmt.Errorf("root %s#%s: %w", name, spec, err)
			}
			return "", fmt.Errorf("%s depends on %s: %w", from, name, err)
		}
		version := vs.exact
		if vs.wildcard {
			avail, ok := versions[name]
			if !ok {
				avail, err = reg.Versions(ctx, name)
				if err != nil {
					return "", fmt.Errorf("%s: listing versions of %s for %q: %w", orRoot(from), name, spec, err)
				}
				versions[name] = avail
			}
			v, ok := vs.pick(avail)
			if !ok {
				return "", fmt.Errorf("%s: no published version of %s matches %q", orRoot(from), name, spec)
			}
			version = v
		}
		id := name + "#" + version
		n := g.Nodes[id]
		if n == nil {
			n = &Node{ID: id}
			if from != "" {
				n.Via = append(append([]string(nil), g.Nodes[from].Via...), from)
			}
			g.Nodes[id] = n
			queue = append(queue, id)
		}
		n.Requests = append(n.Requests, Request{From: from, Spec: spec})
		return id, nil
	}

	seenRoot := map[string]bool{}
	for i, r := range sorted {
		if i > 0 && r == sorted[i-1] {
			continue
		}
		name, spec, err := splitID(r)
		if err != nil {
			return nil, err
		}
		id, err := request("", name, spec)
		if err != nil {
			return nil, err
		}
		if !seenRoot[id] {
			seenRoot[id] = true
			g.Roots = append(g.Roots, id)
		}
	}
	sort.Strings(g.Roots)

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		n := g.Nodes[id]
		name, version, _ := strings.Cut(id, "#")
		tgz, err := reg.Fetch(ctx, name, version)
		if err != nil {
			return nil, fmt.Errorf("%s (requested by %s): %w", id, orRoot(n.Requests[0].From), err)
		}
		sum := sha256.Sum256(tgz)
		n.SHA256, n.Size = hex.EncodeToString(sum[:]), len(tgz)
		c, err := scanTgz(tgz, DefaultLimits, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", id, err)
		}
		if c.manifest.ID() != id {
			return nil, fmt.Errorf("%s: the package declares itself as %s", id, c.manifest.ID())
		}
		n.Manifest = c.manifest
		if onPackage != nil {
			if err := onPackage(id, tgz); err != nil {
				return nil, err
			}
		}
		deps := make([]string, 0, len(c.manifest.Dependencies))
		for d := range c.manifest.Dependencies {
			deps = append(deps, d)
		}
		sort.Strings(deps)
		for _, d := range deps {
			did, err := request(id, d, c.manifest.Dependencies[d])
			if err != nil {
				return nil, err
			}
			n.Deps = append(n.Deps, did)
		}
		sort.Strings(n.Deps)
	}
	return g, nil
}

func orRoot(from string) string {
	if from == "" {
		return "(root)"
	}
	return from
}

// Double es un package pedido en dos o más versiones (D2).
type Double struct {
	Name     string
	Versions []DoubleVersion
}

// DoubleVersion es una de las versiones de un Double y quién la pide.
type DoubleVersion struct {
	Version     string
	RequestedBy []string
}

// Analysis es el juicio del grafo según las reglas de D1 y D2, sin aplicar
// las excepciones del lock.
type Analysis struct {
	// Problems son los defectos que impiden fijar un núcleo (ninguno o varios
	// núcleos entre las raíces, núcleo sin una versión FHIR declarada).
	Problems []string
	// Core es el núcleo declarado (el único núcleo entre las raíces).
	Core string
	// FHIRVersion y Series son los del núcleo.
	FHIRVersion, Series string
	// ForeignCores son los núcleos de otra serie: se excluyen (D1).
	ForeignCores []string
	// SameSeriesCores son otros núcleos de la misma serie que el del lock: error.
	SameSeriesCores []string
	// Foreign son los packages que no son núcleo y declaran solo otras series:
	// necesitan una excepción del lock (D1).
	Foreign []string
	// Undeclared son los packages sin versión FHIR declarada (se leen).
	Undeclared []string
	// Doubles son los packages pedidos en más de una versión (D2).
	Doubles []Double
	// Cycle es el primer ciclo de dependencias, si lo hay.
	Cycle []string
}

// Analyze juzga el grafo.
func (g *Graph) Analyze() Analysis {
	var a Analysis
	ids := g.IDs()

	var rootCores []string
	for _, r := range g.Roots {
		if g.Nodes[r].Manifest.IsCore() {
			rootCores = append(rootCores, r)
		}
	}
	switch len(rootCores) {
	case 0:
		a.Problems = append(a.Problems, "no root is a core package (type Core or fhir.core): the lock needs exactly one declared core")
	case 1:
		a.Core = rootCores[0]
		m := g.Nodes[a.Core].Manifest
		if len(m.FHIRVersions) != 1 {
			a.Problems = append(a.Problems, fmt.Sprintf("core %s declares %d FHIR versions (%v), want exactly one", a.Core, len(m.FHIRVersions), m.FHIRVersions))
			a.Core = ""
		} else if s, ok := series(m.FHIRVersions[0]); !ok {
			a.Problems = append(a.Problems, fmt.Sprintf("core %s declares the FHIR version %q, which is not M.m.p", a.Core, m.FHIRVersions[0]))
			a.Core = ""
		} else {
			a.FHIRVersion, a.Series = m.FHIRVersions[0], s
		}
	default:
		a.Problems = append(a.Problems, fmt.Sprintf("several roots are core packages (%s): the lock has exactly one core", strings.Join(rootCores, ", ")))
	}

	for _, id := range ids {
		n := g.Nodes[id]
		if len(n.Manifest.FHIRVersions) == 0 && !n.Manifest.IsCore() {
			a.Undeclared = append(a.Undeclared, id)
		}
		if a.Core == "" || id == a.Core {
			continue
		}
		serieses := n.Manifest.Series()
		matches := contains(serieses, a.Series)
		switch {
		case n.Manifest.IsCore() && len(n.Manifest.FHIRVersions) == 0:
			a.Problems = append(a.Problems, fmt.Sprintf("core %s declares no FHIR version", id))
		case n.Manifest.IsCore() && matches:
			a.SameSeriesCores = append(a.SameSeriesCores, id)
		case n.Manifest.IsCore():
			a.ForeignCores = append(a.ForeignCores, id)
		case len(n.Manifest.FHIRVersions) > 0 && !matches:
			a.Foreign = append(a.Foreign, id)
		}
	}

	byName := map[string]map[string][]string{}
	for _, id := range ids {
		n := g.Nodes[id]
		name, version, _ := strings.Cut(id, "#")
		if byName[name] == nil {
			byName[name] = map[string][]string{}
		}
		for _, r := range n.Requests {
			by := orRoot(r.From)
			if r.Spec != version {
				by += " (asks " + r.Spec + ")"
			}
			byName[name][version] = append(byName[name][version], by)
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		if len(byName[name]) < 2 {
			continue
		}
		d := Double{Name: name}
		for v, by := range byName[name] {
			sort.Strings(by)
			d.Versions = append(d.Versions, DoubleVersion{Version: v, RequestedBy: by})
		}
		sort.Slice(d.Versions, func(i, j int) bool { return d.Versions[i].Version < d.Versions[j].Version })
		a.Doubles = append(a.Doubles, d)
	}
	a.Cycle = g.cycle()
	return a
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// cycle devuelve el primer ciclo (recorriendo en orden de id), con el primer
// elemento repetido al final, o nil.
func (g *Graph) cycle() []string {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var stack []string
	var found []string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = grey
		stack = append(stack, id)
		for _, d := range g.Nodes[id].Deps {
			switch color[d] {
			case grey:
				for i, s := range stack {
					if s == d {
						found = append(append([]string(nil), stack[i:]...), d)
						return true
					}
				}
			case white:
				if visit(d) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	for _, id := range g.IDs() {
		if color[id] == white && visit(id) {
			return found
		}
	}
	return nil
}

// topoOrder ordena los packages de include con las dependencias primero y los
// empates por id. Las dependencias que no están en include se ignoran. El grafo
// no puede tener ciclos.
func (g *Graph) topoOrder(include func(id string) bool) []string {
	pending := map[string]int{}
	rev := map[string][]string{}
	var ready []string
	for _, id := range g.IDs() {
		if !include(id) {
			continue
		}
		n := 0
		for _, d := range g.Nodes[id].Deps {
			if include(d) {
				n++
				rev[d] = append(rev[d], id)
			}
		}
		pending[id] = n
		if n == 0 {
			ready = append(ready, id)
		}
	}
	var out []string
	for len(ready) > 0 {
		sort.Strings(ready)
		id := ready[0]
		ready = ready[1:]
		out = append(out, id)
		for _, r := range rev[id] {
			pending[r]--
			if pending[r] == 0 {
				ready = append(ready, r)
			}
		}
	}
	return out
}
