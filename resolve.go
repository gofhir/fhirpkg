package fhirpkg

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Options configura Resolve.
type Options struct {
	// Exceptions son los packages de otra serie FHIR que se leen igual (D1),
	// cada uno con su motivo. Una excepción que no se usa es error: una
	// excepción vieja no debe quedar escondida en el lock.
	Exceptions []Exception
}

// Resolve resuelve las raíces (name#version o name#M.m.x) contra el registro y
// devuelve el lock. Aplica las reglas del plan 001:
//
//   - un solo núcleo por lock: el único núcleo entre las raíces;
//   - D1: un núcleo de otra serie que trae una dependencia se excluye (queda
//     en el lock, marcado, con el camino que lo trajo); un package de otra
//     serie que no es núcleo es error salvo que esté en Options.Exceptions;
//   - D2: un package pedido en dos versiones es error;
//   - una dependencia faltante o un ciclo son error.
func Resolve(ctx context.Context, roots []string, reg Registry, opts Options) (Lock, error) {
	g, err := Explore(ctx, roots, reg, nil)
	if err != nil {
		return Lock{}, err
	}
	return resolveGraph(g, opts)
}

func resolveGraph(g *Graph, opts Options) (Lock, error) {
	a := g.Analyze()
	if err := checkAnalysis(a); err != nil {
		return Lock{}, err
	}
	if err := checkExceptions(g, a, opts); err != nil {
		return Lock{}, err
	}

	excluded := map[string]bool{}
	for _, id := range a.ForeignCores {
		excluded[id] = true
	}
	l := Lock{
		Schema:      LockSchema,
		FHIRVersion: a.FHIRVersion,
		Core:        a.Core,
		Roots:       append([]string(nil), g.Roots...),
	}
	for _, id := range g.IDs() {
		n := g.Nodes[id]
		p := LockedPackage{
			Name: n.Manifest.Name, Version: n.Manifest.Version, SHA256: n.SHA256,
			FHIRVersions: n.Manifest.FHIRVersions, LegacyVersionList: n.Manifest.LegacyVersionList,
			Dependencies: n.Deps, Via: n.Via,
		}
		if excluded[id] {
			p.Excluded = true
			p.ExcludedReason = fmt.Sprintf("core of FHIR series %s, the lock's core is %s (series %s)", strings.Join(n.Manifest.Series(), ","), a.Core, a.Series)
		}
		l.Packages = append(l.Packages, p)
	}
	l.Order = g.topoOrder(func(id string) bool { return !excluded[id] })
	l.Exceptions = append(l.Exceptions, opts.Exceptions...)
	sort.Slice(l.Exceptions, func(i, j int) bool { return l.Exceptions[i].Package < l.Exceptions[j].Package })
	if err := l.Validate(); err != nil {
		return Lock{}, fmt.Errorf("internal error: %w", err)
	}
	return l, nil
}

// checkAnalysis falla con el primer problema del análisis. Los errores salen en
// un orden fijo: ciclo, versiones dobles, núcleo, series.
func checkAnalysis(a Analysis) error {
	if len(a.Cycle) > 0 {
		return fmt.Errorf("dependency cycle: %s", strings.Join(a.Cycle, " -> "))
	}
	if len(a.Doubles) > 0 {
		return doubleError(a.Doubles)
	}
	if len(a.Problems) > 0 {
		return fmt.Errorf("%s", strings.Join(a.Problems, "; "))
	}
	if len(a.SameSeriesCores) > 0 {
		return fmt.Errorf("the graph has another core of the same FHIR series (%s) as %s: %s", a.Series, a.Core, strings.Join(a.SameSeriesCores, ", "))
	}
	return nil
}

// checkExceptions valida las excepciones y exige que cubran, exactamente, los
// packages de otra serie: ni uno sin excepción ni una excepción sin uso.
func checkExceptions(g *Graph, a Analysis, opts Options) error {
	exc := map[string]string{}
	for _, e := range opts.Exceptions {
		if _, _, err := splitID(e.Package); err != nil {
			return fmt.Errorf("exception: %w", err)
		}
		if e.Reason == "" {
			return fmt.Errorf("exception for %s has no reason", e.Package)
		}
		if _, dup := exc[e.Package]; dup {
			return fmt.Errorf("two exceptions for %s", e.Package)
		}
		exc[e.Package] = e.Reason
	}
	used := map[string]bool{}
	var missing []string
	for _, id := range a.Foreign {
		if _, ok := exc[id]; ok {
			used[id] = true
			continue
		}
		n := g.Nodes[id]
		missing = append(missing, fmt.Sprintf("%s declares FHIR %v, not the core's series %s (reached through %s)",
			id, n.Manifest.FHIRVersions, a.Series, viaText(n)))
	}
	if len(missing) > 0 {
		return fmt.Errorf("packages of another FHIR series than %s %s:\n  %s\nchange the roots or add an exception with its reason",
			a.Core, a.FHIRVersion, strings.Join(missing, "\n  "))
	}
	var unused []string
	for _, e := range opts.Exceptions {
		if !used[e.Package] {
			unused = append(unused, e.Package)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return fmt.Errorf("exceptions that no package needs: %s", strings.Join(unused, ", "))
	}
	return nil
}

func viaText(n *Node) string {
	if len(n.Via) == 0 {
		return "(root)"
	}
	return strings.Join(n.Via, " -> ")
}

func doubleError(ds []Double) error {
	var b strings.Builder
	b.WriteString("a package is requested in more than one version:")
	for _, d := range ds {
		fmt.Fprintf(&b, "\n  %s:", d.Name)
		for _, v := range d.Versions {
			fmt.Fprintf(&b, "\n    %s requested by %s", v.Version, strings.Join(v.RequestedBy, ", "))
		}
	}
	b.WriteString("\nchange the roots so that a single version is requested")
	return fmt.Errorf("%s", b.String())
}

// Lock aplica las reglas de Resolve a un grafo ya explorado.
func (g *Graph) Lock(opts Options) (Lock, error) { return resolveGraph(g, opts) }
