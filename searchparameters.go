package fhirpkg

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// buildTypes lee los tipos de recurso de los StructureDefinition y su herencia
// (baseDefinition), sin ningún nombre de recurso: un tipo es un
// StructureDefinition de kind resource que no es un perfil (derivation
// constraint). Un modelo lógico tampoco lo es. La cadena termina cuando el
// baseDefinition resuelve a algo que no es un tipo de recurso (en R5, Resource
// hereda de Base, un tipo complejo).
func (c *Context) buildTypes(keys []key, docs map[*cand]parsed) error {
	type def struct {
		url      string
		baseDef  string
		cand     *cand
		abstract bool
	}
	defs := map[string]def{}
	for _, k := range keys {
		if k.rt != typeStructureDefinition {
			continue
		}
		g := c.groups[k]
		if g.dec.winner == nil {
			for _, cd := range g.cands {
				if d := docs[cd].sd; isResourceType(d) {
					return fmt.Errorf("%s: the definition of resource type %q is unresolved (%s): no type can be taken from it", k.url, d.Type, explainTrace(g.dec.trace))
				}
			}
			continue
		}
		d := docs[g.dec.winner].sd
		if !isResourceType(d) {
			continue
		}
		if prev, ok := defs[d.Type]; ok {
			return fmt.Errorf("resource type %q is defined by more than one structure definition (%s and %s)", d.Type, prev.url, k.url)
		}
		defs[d.Type] = def{url: k.url, baseDef: d.BaseDefinition, cand: g.dec.winner, abstract: d.Abstract}
	}
	parent := map[string]string{}
	for name, d := range defs {
		c.types = append(c.types, name)
		if !d.abstract {
			c.concrete = append(c.concrete, name)
		}
		if d.baseDef == "" {
			continue
		}
		cn, err := ParseCanonical(d.baseDef)
		if err != nil {
			return fmt.Errorf("%s: %s: base definition: %w", d.cand.pkgID, name, err)
		}
		cd, err := c.pick(nil, typeStructureDefinition, cn)
		if err != nil {
			return fmt.Errorf("%s: resource type %s: base definition %s does not resolve: %w", d.cand.pkgID, name, d.baseDef, err)
		}
		bd := docs[cd].sd
		if !isResourceType(bd) {
			continue // fin de la cadena (R5: Base)
		}
		parent[name] = bd.Type
	}
	sort.Strings(c.types)
	sort.Strings(c.concrete)
	chains := map[string][]string{}
	for _, name := range c.types {
		chain := []string{name}
		for t := name; ; {
			p, ok := parent[t]
			if !ok {
				break
			}
			if slices.Contains(chain, p) {
				return fmt.Errorf("inheritance cycle through resource type %s: %s", p, strings.Join(append(chain, p), " -> "))
			}
			chain = append(chain, p)
			t = p
		}
		chains[name] = chain
	}
	c.chains = chains
	return nil
}

func isResourceType(d *sdDoc) bool {
	return d != nil && d.Kind == kindResource && d.Derivation != derivationConstraint && d.Type != ""
}

type baseCode struct{ base, code string }

// buildSearchParameters aplica la regla de base:code de ADR-003 (plan 002
// §3.2) y arma la lista de cada tipo. Parte de un SearchParameter por URL (el
// ganador de su canónico).
func (c *Context) buildSearchParameters(keys []key, docs map[*cand]parsed) error {
	byBC := map[baseCode][]*cand{}
	// unresolvedBC son los candidatos de los SearchParameter cuyo canónico no se
	// resolvió, por cada base:code que declaran (la unión de todos): ese
	// base:code queda sin resolver, no se decide con otros SearchParameter.
	unresolvedBC := map[baseCode][]*cand{}
	disagree := map[baseCode]bool{}
	for _, k := range keys {
		if k.rt != typeSearchParameter {
			continue
		}
		w := c.groups[k].dec.winner
		if w == nil {
			if err := collectUnresolved(k.url, c.groups[k].cands, docs, unresolvedBC, disagree); err != nil {
				return err
			}
			continue
		}
		d := docs[w].sp
		if len(d.Base) == 0 {
			// Sin base no aplica a ningún tipo. hl7.fhir.r4.core y
			// hl7.fhir.r4b.core traen 10 así (codesystem-extensions-*, draft): no es
			// error, no se indexan.
			continue
		}
		if d.Code == "" {
			return fmt.Errorf("%s: %s: search parameter %s has no code", w.pkgID, w.res.File, k.url)
		}
		seen := map[string]bool{}
		for _, b := range d.Base {
			if !seen[b] {
				seen[b] = true
				byBC[baseCode{b, d.Code}] = append(byBC[baseCode{b, d.Code}], w)
			}
		}
	}

	resolved := map[baseCode]*cand{} // nil: sin resolver
	codesOf := map[string][]string{} // base -> codes
	bcs := make([]baseCode, 0, len(byBC)+len(unresolvedBC))
	for bc := range byBC {
		bcs = append(bcs, bc)
	}
	for bc := range unresolvedBC {
		if _, ok := byBC[bc]; !ok {
			bcs = append(bcs, bc)
		}
	}
	sort.Slice(bcs, func(i, j int) bool {
		if bcs[i].base != bcs[j].base {
			return bcs[i].base < bcs[j].base
		}
		return bcs[i].code < bcs[j].code
	})
	for _, bc := range bcs {
		cs := slices.Clone(byBC[bc])
		if blocked := unresolvedBC[bc]; len(blocked) > 0 {
			cs = append(cs, blocked...)
		}
		slices.SortFunc(cs, func(a, b *cand) int {
			if x := strings.Compare(a.pkgID, b.pkgID); x != 0 {
				return x
			}
			if x := strings.Compare(a.res.URL, b.res.URL); x != 0 {
				return x
			}
			return strings.Compare(a.res.File, b.res.File)
		})
		codesOf[bc.base] = append(codesOf[bc.base], bc.code)
		if len(unresolvedBC[bc]) > 0 {
			detail := "a search parameter canonical is unresolved: its base:code cannot be decided with the others"
			if disagree[bc] {
				detail += "; its candidates disagree on base or code, so every base:code of their union is unresolved"
			}
			cf := Conflict{Kind: KindSearchParameter, Key: bc.base + ":" + bc.code, Winner: -1, DecidedBy: StepUnresolved,
				Trace: []StepTrace{{Step: StepUnresolved, In: len(cs), Out: len(cs), Detail: detail}}}
			for _, cd := range cs {
				cf.Candidates = append(cf.Candidates, candidateOf(cd))
			}
			resolved[bc] = nil
			c.conflicts = append(c.conflicts, cf)
			continue
		}
		if len(cs) == 1 {
			resolved[bc] = cs[0]
			continue
		}
		winner, trace, by := decideBaseCode(cs, docs, c.core)
		resolved[bc] = winner
		cf := Conflict{Kind: KindSearchParameter, Key: bc.base + ":" + bc.code, Winner: -1, DecidedBy: by, Trace: trace}
		for i, cd := range cs {
			cf.Candidates = append(cf.Candidates, candidateOf(cd))
			if cd == winner {
				cf.Winner = i
			}
		}
		c.conflicts = append(c.conflicts, cf)
	}

	c.spByType = make(map[string][]SearchParameter, len(c.types))
	for _, t := range c.types {
		var out []SearchParameter
		seen := map[string]bool{}
		for _, level := range c.chains[t] {
			for _, code := range codesOf[level] {
				if seen[code] {
					continue
				}
				seen[code] = true // un base:code sin resolver bloquea el code en los niveles siguientes
				w := resolved[baseCode{level, code}]
				if w == nil {
					continue
				}
				d := docs[w].sp
				out = append(out, SearchParameter{
					URL: w.res.URL, Version: w.res.Version, Code: d.Code, Status: d.Status, Experimental: d.Experimental,
					Base: slices.Clone(d.Base), Package: w.pkgID, Raw: w.res.Raw,
				})
			}
		}
		c.spByType[t] = out
	}
	return nil
}

// decideBaseCode elige entre SearchParameter de URL distinta con el mismo
// base:code: el no experimental, luego el del núcleo; si no, sin resolver.
func decideBaseCode(cs []*cand, docs map[*cand]parsed, core string) (*cand, []StepTrace, Step) {
	var trace []StepTrace
	cur := cs
	steps := []struct {
		step Step
		keep func(*cand) bool
	}{
		{StepNotExperimental, func(cd *cand) bool { return !docs[cd].sp.Experimental }},
		{StepCore, func(cd *cand) bool { return cd.pkgID == core }},
	}
	for _, s := range steps {
		var next []*cand
		for _, cd := range cur {
			if s.keep(cd) {
				next = append(next, cd)
			}
		}
		if len(next) == 0 {
			next = cur // todos pierden: el paso no separa
		}
		trace = append(trace, StepTrace{Step: s.step, In: len(cur), Out: len(next)})
		cur = next
		if len(cur) == 1 {
			return cur[0], trace, s.step
		}
	}
	return nil, trace, StepUnresolved
}

// collectUnresolved agrega los candidatos de un SearchParameter sin resolver a
// cada base:code que declaran. Un candidato sin base no aplica a ningún tipo y no
// aporta, como uno resuelto; uno con base y sin code es un error, como uno
// resuelto. Cada candidato aporta sus propios pares (base × code), sin producto
// cruzado entre candidatos; si sus pares difieren, se marcan como discrepantes.
func collectUnresolved(url string, cs []*cand, docs map[*cand]parsed, into map[baseCode][]*cand, disagree map[baseCode]bool) error {
	var withBase []*cand
	var pairs []map[baseCode]bool
	for _, cd := range cs {
		d := docs[cd].sp
		if len(d.Base) == 0 {
			continue
		}
		if d.Code == "" {
			return fmt.Errorf("%s: %s: search parameter %s has no code", cd.pkgID, cd.res.File, url)
		}
		ps := map[baseCode]bool{}
		for _, b := range d.Base {
			ps[baseCode{b, d.Code}] = true
		}
		withBase = append(withBase, cd)
		pairs = append(pairs, ps)
	}
	for i, ps := range pairs {
		for bc := range ps {
			into[bc] = append(into[bc], withBase[i])
		}
	}
	for i := 1; i < len(pairs); i++ {
		if !maps.Equal(pairs[0], pairs[i]) {
			for _, ps := range pairs {
				for bc := range ps {
					disagree[bc] = true
				}
			}
			break
		}
	}
	return nil
}
