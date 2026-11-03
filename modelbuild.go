package fhirpkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// snapshotDoc es lo que se lee del snapshot de un StructureDefinition.
type snapshotDoc struct {
	Snapshot *struct {
		Element []elementDoc `json:"element"`
	} `json:"snapshot"`
}

type elementDoc struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	Min              *int   `json:"min"`
	Max              string `json:"max"`
	ContentReference string `json:"contentReference"`
	Base             struct {
		Path string `json:"path"`
	} `json:"base"`
	Type []elementTypeDoc `json:"type"`
}

type elementTypeDoc struct {
	Code          string   `json:"code"`
	TargetProfile []string `json:"targetProfile"`
	Extension     []struct {
		URL      string `json:"url"`
		ValueURL string `json:"valueUrl"`
	} `json:"extension"`
}

// definesType informa si el StructureDefinition define un tipo propio, cuyo
// snapshot lista los elementos de ese tipo (plan 003 §3.2, regla 1; tomado de
// definesType de gofhir/models): no es un modelo lógico, tiene snapshot y es
// una especialización, o una raíz, que no declara derivation ni
// baseDefinition (R5 sdf-27: «If there's a base definition, there must be a
// derivation»; la raíz de R4 y R4B no declara ninguna). Un perfil
// (constraint) repite los elementos de otro tipo bajo sus caminos.
func definesType(d *sdDoc) bool {
	return d != nil && d.Kind != kindLogical && d.Snapshot != nil &&
		(d.Derivation == derivationSpecialization || (d.Derivation == "" && d.BaseDefinition == ""))
}

// modelBuilder es el estado de la construcción; se descarta al terminar.
type modelBuilder struct {
	c       *Context
	m       *Model
	docs    map[*cand]parsed
	defs    map[string]*typeDef
	targets map[string]string // targetProfile -> type
	// system son los códigos de tipo del sistema de FHIRPath que el núcleo
	// declara en sus primitivos (systemTypes).
	system map[string]bool
}

type typeDef struct {
	url    string
	cand   *cand
	doc    *sdDoc
	parent string
}

// buildModel construye el modelo de tipos desde los StructureDefinition
// ganadores. Es error que un tipo lo definan dos StructureDefinition con URL
// distinta, que la definición de un tipo quede sin resolver, que una
// baseDefinition no resuelva a un tipo, que la herencia tenga un ciclo y
// cualquier snapshot mal formado.
func (c *Context) buildModel(keys []key, docs map[*cand]parsed) (*Model, error) {
	defs := map[string]*typeDef{}
	for _, k := range keys {
		if k.rt != typeStructureDefinition {
			continue
		}
		g := c.groups[k]
		if g.dec.winner == nil {
			for _, cd := range g.cands {
				if d := docs[cd].sd; definesType(d) {
					return nil, fmt.Errorf("%s: the definition of type %q is unresolved (%s): no type can be taken from it", k.url, d.Type, explainTrace(g.dec.trace))
				}
			}
			continue
		}
		d := docs[g.dec.winner].sd
		if !definesType(d) {
			continue
		}
		if d.Type == "" {
			return nil, fmt.Errorf("%s: %s: the definition of a type has no type", g.dec.winner.pkgID, k.url)
		}
		if prev, ok := defs[d.Type]; ok {
			return nil, fmt.Errorf("type %q is defined by more than one structure definition (%s and %s)", d.Type, prev.url, k.url)
		}
		defs[d.Type] = &typeDef{url: k.url, cand: g.dec.winner, doc: d}
	}

	m := &Model{
		version:   c.set.lock.FHIRVersion,
		types:     make(map[string]typeInfo, len(defs)),
		choices:   map[string][]string{},
		pathType:  map[string]string{},
		refs:      map[string][]string{},
		elsewhere: map[string]string{},
		children:  map[string][]string{},
		fhirType:  map[string]string{},
		card:      map[string]cardinality{},
	}
	for name := range defs {
		m.names = append(m.names, name)
	}
	sort.Strings(m.names)

	// Jerarquía: el padre es el type del StructureDefinition de baseDefinition.
	for _, name := range m.names {
		d := defs[name]
		if d.doc.BaseDefinition != "" {
			cn, err := ParseCanonical(d.doc.BaseDefinition)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: base definition: %w", d.cand.pkgID, name, err)
			}
			cd, err := c.pick(nil, typeStructureDefinition, cn)
			if err != nil {
				return nil, fmt.Errorf("%s: type %s: base definition %s does not resolve: %w", d.cand.pkgID, name, d.doc.BaseDefinition, err)
			}
			d.parent = docs[cd].sd.Type
			if _, ok := defs[d.parent]; !ok {
				return nil, fmt.Errorf("%s: type %s: base definition %s is %q, which is not a type", d.cand.pkgID, name, d.doc.BaseDefinition, d.parent)
			}
		}
		m.types[name] = typeInfo{parent: d.parent, resource: d.doc.Kind == kindResource && !d.doc.Abstract}
	}
	for _, name := range m.names {
		chain := []string{name}
		for t := m.types[name].parent; t != ""; t = m.types[t].parent {
			if slices.Contains(chain, t) {
				return nil, fmt.Errorf("inheritance cycle through type %s: %s", t, strings.Join(append(chain, t), " -> "))
			}
			chain = append(chain, t)
		}
	}

	b := &modelBuilder{c: c, m: m, docs: docs, defs: defs, targets: map[string]string{}}
	var err error
	if b.system, err = c.systemTypes(keys, docs); err != nil {
		return nil, err
	}
	for _, name := range m.names {
		if err := b.addElements(defs[name]); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// systemTypes reúne los códigos de tipo del sistema de FHIRPath que el núcleo
// declara en los elementos de sus tipos primitivos (el value de cada uno:
// extension-structuredefinition-fhir-type.html). Es el conjunto de tipos del
// sistema que un elemento puede declarar: un código fuera de él no es un tipo
// (plan 003, ronda 2). Solo cuentan los primitivos del package núcleo; si
// contaran los de un IG, este podría ampliar el conjunto. Si otro package gana
// la URL de un primitivo del núcleo, el conjunto quedaría incompleto sin que el
// error lo diga: es error y nombra al primitivo (plan 003, ronda 3).
func (c *Context) systemTypes(keys []key, docs map[*cand]parsed) (map[string]bool, error) {
	out := map[string]bool{}
	for _, k := range keys {
		if k.rt != typeStructureDefinition {
			continue
		}
		g := c.groups[k]
		for _, cd := range g.cands {
			if cd.pkgID != c.core {
				continue
			}
			d := docs[cd].sd
			if !definesType(d) || d.Kind != kindPrimitive {
				continue
			}
			if g.dec.winner != cd {
				by := "no package"
				if g.dec.winner != nil {
					by = g.dec.winner.pkgID
				}
				return nil, fmt.Errorf("%s: the primitive %q of the core package is overridden by %s: the system types cannot be taken from the core", k.url, d.Type, by)
			}
			var doc snapshotDoc
			if err := json.Unmarshal(cd.res.Raw, &doc); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", cd.pkgID, cd.res.File, err)
			}
			for _, e := range doc.Snapshot.Element {
				for _, t := range e.Type {
					if strings.HasPrefix(t.Code, systemTypePrefix) {
						out[t.Code] = true
					}
				}
			}
		}
	}
	return out, nil
}

// targetType es el type del StructureDefinition que resuelve un targetProfile.
func (b *modelBuilder) targetType(profile string) (string, error) {
	if t, ok := b.targets[profile]; ok {
		return t, nil
	}
	cn, err := ParseCanonical(profile)
	if err != nil {
		return "", err
	}
	cd, err := b.c.pick(nil, typeStructureDefinition, cn)
	if err != nil {
		return "", err
	}
	sd := b.docs[cd].sd
	if sd == nil || sd.Type == "" {
		return "", fmt.Errorf("%s is not a structure definition with a type", profile)
	}
	b.targets[profile] = sd.Type
	return sd.Type, nil
}

// addElements recorre el snapshot de un tipo y llena las tablas del modelo.
func (b *modelBuilder) addElements(d *typeDef) error {
	m := b.m
	var doc snapshotDoc
	if err := json.Unmarshal(d.cand.res.Raw, &doc); err != nil {
		return fmt.Errorf("%s: %s: %w", d.cand.pkgID, d.cand.res.File, err)
	}
	typeName := d.doc.Type
	where := d.cand.pkgID + ": " + d.url + " (" + typeName + ")"

	// Primera pasada: los caminos del snapshot, sin rebanadas. Todo lo que
	// apunta a otro elemento (contentReference, padre, variante de [x]) se
	// comprueba contra este conjunto, sea cual sea el orden de los elementos.
	seen := map[string]bool{}
	crs := map[string]bool{}       // caminos con contentReference
	claimed := map[string]string{} // camino indexado -> elemento que lo reclama
	ids := map[string]bool{}       // los id vistos, rebanadas incluidas
	for _, e := range doc.Snapshot.Element {
		// sdf-14: «All element definitions must have an id». El id es lo que
		// distingue una rebanada (path:nombre); sin él no se sabe si lo es.
		if e.ID == "" {
			return fmt.Errorf("%s: the element %q has no id (sdf-14)", where, e.Path)
		}
		// El path nombra el elemento (elementdefinition.html: «a "."-separated
		// list of ancestor elements»); el nombre de una rebanada va en el id y en
		// sliceName. eld-19 admite sintácticamente «:nombre» en un componente,
		// pero ningún núcleo lo usa en un path y el modelo indexa por path: una
		// marca de rebanada en él rompe que ninguna clave la lleve.
		if strings.Contains(e.Path, ":") {
			return fmt.Errorf("%s: the path %q has a slice mark: the slice name belongs in the id", where, e.Path)
		}
		// elementdefinition.html, «id» (Element ids): el id de un elemento es su path
		// con el nombre de cada rebanada tras «:». Un id que no corresponde al path
		// no dice qué elemento es ni si es una rebanada.
		if stripSliceNames(e.ID) != e.Path {
			return fmt.Errorf("%s: the element %q: the id %q does not match the path", where, e.Path, e.ID)
		}
		if isSlice(e) {
			// Una rebanada restringe un elemento ya visto: la raíz de una rebanada
			// (id que termina en :nombre), el elemento sin el nombre; un
			// descendiente, la rebanada de la que cuelga.
			if !ids[sliceParentID(e.ID)] {
				return fmt.Errorf("%s: the slice %q has no base element %q before it", where, e.ID, sliceParentID(e.ID))
			}
			ids[e.ID] = true
			continue
		}
		ids[e.ID] = true
		if e.ContentReference != "" {
			crs[e.Path] = true
		}
		top, _, _ := strings.Cut(e.Path, ".")
		if top != typeName {
			return fmt.Errorf("%s: the path %q is not under the type", where, e.Path)
		}
		if seen[e.Path] {
			return fmt.Errorf("%s: the path %q is listed twice", where, e.Path)
		}
		seen[e.Path] = true
	}
	if !seen[typeName] {
		return fmt.Errorf("%s: the snapshot has no root element", where)
	}
	// elementdefinition.html, contentReference: el contenido del elemento es el
	// del destino. Con hijos propios en el snapshot el modelo sería ambiguo
	// (ResolvePath iría al destino y ChildElements a los hijos), y un
	// contentReference a un hijo de otro eludiría la regla de las cadenas.
	for _, e := range doc.Snapshot.Element {
		i := strings.LastIndex(e.Path, ".")
		if i >= 0 && crs[e.Path[:i]] {
			return fmt.Errorf("%s: %q: the element %q has a content reference and children", where, e.Path, e.Path[:i])
		}
	}

	for _, e := range doc.Snapshot.Element {
		// sdf-3 y eld-2 valen para todo elemento del snapshot, rebanadas incluidas.
		lo, hi, err := cardinalityOf(e)
		if err != nil {
			return fmt.Errorf("%s: %q: %w", where, e.Path, err)
		}
		if isSlice(e) {
			continue
		}
		i := strings.LastIndex(e.Path, ".")
		if i < 0 {
			continue // la raíz del tipo
		}
		parent, name := e.Path[:i], e.Path[i+1:]
		m.children[parent] = append(m.children[parent], name)

		if !seen[parent] {
			return fmt.Errorf("%s: %q has no parent element %q in the snapshot", where, e.Path, parent)
		}
		// Cada camino que el modelo indexa (el del elemento, la base de un [x] y
		// cada variante) lo reclama un solo elemento del snapshot: otro con el
		// mismo nombre pisaría su cardinalidad, su tipo y sus destinos
		// (elementdefinition.html, «Choice types»).
		add := func(path string) error {
			if from, dup := claimed[path]; dup {
				return fmt.Errorf("%s: %q: the path %q collides with the one of %q", where, e.Path, path, from)
			}
			claimed[path] = e.Path
			m.card[path] = cardinality{lo, hi}
			return nil
		}
		if e.ContentReference != "" {
			// eld-5: «if the element definition has a contentReference, it cannot
			// have type ...».
			if len(e.Type) > 0 {
				return fmt.Errorf("%s: %q: the element has a content reference and a type (eld-5)", where, e.Path)
			}
			from, to, ok := strings.Cut(e.ContentReference, "#")
			if !ok || to == "" {
				return fmt.Errorf("%s: %q: the content reference %q has no fragment", where, e.Path, e.ContentReference)
			}
			// elementdefinition.html: el destino es «an element defined elsewhere
			// in the definition»: la url, si la hay, es la de este mismo
			// StructureDefinition (R5 admite url#ruta).
			if from != "" {
				if u, _, _ := strings.Cut(from, "|"); u != d.url {
					return fmt.Errorf("%s: %q: the content reference %q names another definition (%s)", where, e.Path, e.ContentReference, u)
				}
			}
			if !seen[to] {
				return fmt.Errorf("%s: %q: the content reference %q is not an element of this definition", where, e.Path, e.ContentReference)
			}
			// elementdefinition.html: el destino aporta «all the rules that are in
			// the ElementDefinition for the element» (y siempre «the non-constrained
			// definition»): si fuera a su vez un contentReference, no aportaría tipo
			// ni hijos. Una cadena (y un ciclo) es error.
			if crs[to] {
				return fmt.Errorf("%s: %q: the content reference %q points to an element that has a content reference", where, e.Path, e.ContentReference)
			}
			m.elsewhere[e.Path] = to
			if err := add(e.Path); err != nil {
				return err
			}
			continue
		}

		var codes []string
		for _, t := range e.Type {
			if t.Code == "" {
				return fmt.Errorf("%s: %q: a type has no code", where, e.Path)
			}
			// Un código es un tipo del modelo o un tipo del sistema de FHIRPath
			// (extension-structuredefinition-fhir-type.html).
			if _, ok := b.defs[t.Code]; !ok && !b.system[t.Code] {
				return fmt.Errorf("%s: %q: the type code %q is not a type", where, e.Path, t.Code)
			}
			// Un tipo del sistema es el de un valor primitivo: solo vale en un
			// elemento de un solo tipo, no como variante de un [x] (cuyo nombre se
			// formaría con la URL) ni junto a otros tipos.
			if _, ok := b.defs[t.Code]; !ok && (len(e.Type) > 1 || strings.HasSuffix(name, choiceSuffix)) {
				return fmt.Errorf("%s: %q: the system type %q is valid only in an element of one type", where, e.Path, t.Code)
			}
			if !slices.Contains(codes, t.Code) {
				codes = append(codes, t.Code)
			}
		}
		refsOf := func(match func(string) bool) ([]string, error) {
			var out []string
			for _, t := range e.Type {
				if !match(t.Code) {
					continue
				}
				for _, p := range t.TargetProfile {
					tt, err := b.targetType(p)
					if err != nil {
						return nil, fmt.Errorf("%s: %q: target profile %q: %w", where, e.Path, p, err)
					}
					if !slices.Contains(out, tt) {
						out = append(out, tt)
					}
				}
			}
			return out, nil
		}
		base, isChoice := strings.CutSuffix(name, choiceSuffix)
		if len(e.Type) == 0 {
			if isChoice {
				return fmt.Errorf("%s: %q: the choice element has no types", where, e.Path)
			}
			return fmt.Errorf("%s: %q has neither a type nor a content reference", where, e.Path)
		}
		if isChoice {
			basePath := parent + "." + base
			if seen[basePath] {
				return fmt.Errorf("%s: %q: the element %q is also defined as an element of its own", where, e.Path, basePath)
			}
			m.choices[basePath] = codes
			if err := add(basePath); err != nil {
				return err
			}
			for _, code := range codes {
				variant := basePath + upperFirst(code)
				if seen[variant] {
					return fmt.Errorf("%s: %q: the variant %q is also defined as an element of its own", where, e.Path, variant)
				}
				m.pathType[variant] = code
				if err := add(variant); err != nil {
					return err
				}
				rs, err := refsOf(func(x string) bool { return x == code })
				if err != nil {
					return err
				}
				if len(rs) > 0 {
					m.refs[variant] = rs
				}
			}
			if len(e.Type) == 1 {
				b.addFHIRType(d, e, basePath+upperFirst(codes[0]))
			}
			continue
		}
		if err := add(e.Path); err != nil {
			return err
		}
		if len(codes) == 1 {
			m.pathType[e.Path] = codes[0]
		}
		rs, err := refsOf(func(string) bool { return true })
		if err != nil {
			return err
		}
		if len(rs) > 0 {
			m.refs[e.Path] = rs
		}
		if len(e.Type) == 1 {
			b.addFHIRType(d, e, e.Path)
		}
	}
	return nil
}

// addFHIRType guarda el tipo FHIR de un elemento de un solo tipo. Se excluyen
// los elementos de un primitivo cuyo base.path empieza en un tipo primitivo
// (el value): es el valor del sistema de FHIRPath con que se lee el primitivo
// (plan 003, H3).
func (b *modelBuilder) addFHIRType(d *typeDef, e elementDoc, path string) {
	if d.doc.Kind == kindPrimitive {
		root, _, _ := strings.Cut(e.Base.Path, ".")
		if p, ok := b.defs[root]; ok && p.doc.Kind == kindPrimitive {
			return
		}
	}
	for _, x := range e.Type[0].Extension {
		if x.URL == urlFHIRTypeExtension && x.ValueURL != "" {
			b.m.fhirType[path] = x.ValueURL
			return
		}
	}
}

// upperFirst pone en mayúscula la primera letra: el sufijo de la variante de un
// elemento con varios tipos (value[x] con Quantity es valueQuantity;
// elementdefinition.html, «Choice types»).
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// cardinalityOf lee min y max. Que falten es error (sdf-3: «Each element
// definition in a snapshot must have a formal definition and cardinalities») y
// también que min supere a max (eld-2: «Min <= Max»).
func cardinalityOf(e elementDoc) (lo, hi int32, err error) {
	if e.Min == nil || e.Max == "" {
		return 0, 0, errors.New("min or max is absent (sdf-3)")
	}
	if *e.Min < 0 || *e.Min > 1<<30 {
		return 0, 0, fmt.Errorf("min %d is out of range", *e.Min)
	}
	if e.Max == maxUnbounded {
		return int32(*e.Min), Unbounded, nil
	}
	n, err := strconv.Atoi(e.Max)
	if err != nil || n < 0 || n > 1<<30 {
		return 0, 0, fmt.Errorf("max %q is not a count or the unbounded mark", e.Max)
	}
	if *e.Min > n {
		return 0, 0, fmt.Errorf("min %d is greater than max %d (eld-2)", *e.Min, n)
	}
	return int32(*e.Min), int32(n), nil
}

// stripSliceNames quita del id el nombre de cada rebanada («:nombre», hasta el
// siguiente «.»): lo que queda es el path (elementdefinition.html, «id»).
func stripSliceNames(id string) string {
	var b strings.Builder
	for i := 0; i < len(id); i++ {
		if id[i] != ':' {
			b.WriteByte(id[i])
			continue
		}
		for i+1 < len(id) && id[i+1] != '.' {
			i++
		}
	}
	return b.String()
}

// sliceParentID es el id del elemento que una rebanada restringe: para la raíz
// de una rebanada (id que termina en :nombre), el id sin el nombre; para un
// descendiente de una rebanada (X.x:s.y), la rebanada (X.x:s).
func sliceParentID(id string) string {
	last := strings.LastIndex(id, ":")
	if end := strings.Index(id[last:], "."); end >= 0 {
		return id[:last+end]
	}
	return id[:last]
}

// isSlice informa si el elemento es una rebanada: su id lleva el nombre de la
// rebanada tras «:» (elementdefinition.html, sliceName).
func isSlice(e elementDoc) bool { return strings.Contains(e.ID, ":") }
