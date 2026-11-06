package fhirpkg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Proyección _summary / _elements (plan 005, ADR-005). Project es la pasada de
// Normalize con un filtro por elemento: hereda el orden, la resolución de las
// variantes de un [x], el `_x` junto a `x`, los recursos anidados y los errores
// con ruta. Lo que se conserva sale de ElementDefinition.isSummary, min e
// isModifier del Model; los pocos nombres de elemento que ningún dato marca
// están en fhirnames.go.

// SummaryMode es el valor de _summary que una Projection aplica. _summary=count
// no proyecta un recurso: es del núcleo.
type SummaryMode uint8

const (
	// SummaryFalse devuelve todo el recurso (con _elements, solo lo pedido).
	SummaryFalse SummaryMode = iota
	// SummaryTrue conserva lo marcado isSummary (search.html, _summary=true).
	SummaryTrue
	// SummaryText conserva text, id, meta y los elementos obligatorios de la raíz.
	SummaryText
	// SummaryData quita el elemento text de cada recurso.
	SummaryData
)

// Projection es una proyección validada para un tipo de recurso: no tiene
// campos exportados y solo se arma con Context.NewProjection. Es inmutable y
// seguro para uso concurrente.
type Projection struct {
	c        *Context
	rt       string
	mode     SummaryMode
	elements []string
	names    map[string]bool
	tag      []byte // el Coding SUBSETTED, en JSON
}

// subsettedTag es el Coding del tag, armado una vez por Context.
type subsettedTag struct {
	once sync.Once
	json []byte
	err  error
}

// NewProjection valida _summary y _elements para un tipo de recurso. Un nombre
// de _elements es el de un elemento de la raíz sin [x] ni variante (`value`
// sí; `value[x]` y `valueQuantity` no): lo que el tipo no tiene es un *Error
// KindUnknownElement y una variante, KindChoiceElement, que el núcleo puede
// ignorar (search.html permite ignorar un nombre). _summary distinto de false
// junto con _elements es KindConflictingModes. Devuelve el primer *Error.
//
// El tag SUBSETTED se arma aquí: si el Set trae el CodeSystem de su concepto,
// se verifica el código y el display sale de ahí; si no lo trae (un lock solo
// con el núcleo), el tag va sin display, que es opcional en Coding (ADR-005, D6).
func (c *Context) NewProjection(resourceType string, summary SummaryMode, elements []string) (*Projection, error) {
	if !c.model.IsResource(resourceType) {
		return nil, &Error{Kind: KindUnknownResourceType, Path: resourceType, Detail: "not a resource type of this version"}
	}
	if summary > SummaryData {
		return nil, &Error{Kind: KindProjectionMismatch, Path: resourceType, Detail: fmt.Sprintf("unknown summary mode %d", summary)}
	}
	if summary != SummaryFalse && len(elements) > 0 {
		return nil, &Error{Kind: KindConflictingModes, Path: resourceType, Detail: "the summary mode and the elements list cannot be combined"}
	}
	p := &Projection{c: c, rt: resourceType, mode: summary, elements: slices.Clone(elements)}
	if len(elements) > 0 {
		sp := c.model.spec(resourceType)
		p.names = map[string]bool{}
		for _, name := range elements {
			if err := rootElement(sp, resourceType, name); err != nil {
				return nil, err
			}
			p.names[name] = true
		}
	}
	tag, err := c.subsettedTag()
	if err != nil {
		return nil, err
	}
	p.tag = tag
	return p, nil
}

// rootElement comprueba que name es un elemento de la raíz sin sufijo.
func rootElement(sp *objSpec, rt, name string) error {
	if strings.HasSuffix(name, choiceSuffix) {
		return &Error{Kind: KindChoiceElement, Path: rt + "." + name, Detail: "an element with several types is named without its suffix or variant"}
	}
	for i := range sp.fields {
		f := &sp.fields[i]
		if f.name == name {
			return nil
		}
		for _, v := range f.variants {
			if v.key == name {
				return &Error{Kind: KindChoiceElement, Path: rt + "." + name, Detail: "a variant of an element with several types is not a base element name"}
			}
		}
	}
	return &Error{Kind: KindUnknownElement, Path: rt + "." + name, Detail: "the type has no such element"}
}

type conceptDoc struct {
	Code    string       `json:"code"`
	Display string       `json:"display"`
	Concept []conceptDoc `json:"concept"`
}

func findConcept(cs []conceptDoc, code string) *conceptDoc {
	for i := range cs {
		if cs[i].Code == code {
			return &cs[i]
		}
		if c := findConcept(cs[i].Concept, code); c != nil {
			return c
		}
	}
	return nil
}

func (c *Context) subsettedTag() ([]byte, error) {
	c.subsetted.once.Do(func() {
		display := ""
		r, err := c.Resolve(typeCodeSystem, systemSubsetted)
		switch {
		case errors.Is(err, ErrNotFound):
		case err != nil:
			c.subsetted.err = fmt.Errorf("the tag system cannot be resolved: %w", err)
			return
		default:
			var doc struct {
				Concept []conceptDoc `json:"concept"`
			}
			if err := json.Unmarshal(r.Resource.Raw, &doc); err != nil {
				c.subsetted.err = fmt.Errorf("%s: %w", systemSubsetted, err)
				return
			}
			cd := findConcept(doc.Concept, codeSubsetted)
			if cd == nil {
				c.subsetted.err = fmt.Errorf("the code system %s (%s) has no concept %s", systemSubsetted, r.Package, codeSubsetted)
				return
			}
			display = cd.Display
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		str := func(s string) string {
			b.Reset()
			_ = enc.Encode(s) //nolint:errcheck // codificar un string en un bytes.Buffer no puede fallar
			return strings.TrimSuffix(b.String(), "\n")
		}
		out := `{"` + elemSystem + `":` + str(systemSubsetted) + `,"` + elemCode + `":` + str(codeSubsetted)
		if display != "" {
			out += `,"` + elemDisplay + `":` + str(display)
		}
		c.subsetted.json = []byte(out + "}")
	})
	return c.subsetted.json, c.subsetted.err
}

// Project devuelve el recurso normalizado (como Normalize) y proyectado por p,
// en una sola pasada. Rechaza lo que rechaza Normalize, con la misma ruta, y es
// error (KindProjectionMismatch) que p venga de otro Context o que el
// resourceType de la entrada no sea el de p. Cada recurso que perdió algo,
// raíz o anidado, lleva el tag SUBSETTED al final de meta.tag; con SummaryFalse
// y sin _elements, la salida es la de Normalize. Es seguro para uso concurrente.
func (c *Context) Project(data []byte, p *Projection, opts ...NormalizeOption) ([]byte, error) {
	lim, err := limitsOf(opts)
	if err != nil {
		return nil, err
	}
	if p == nil || p.c != c {
		return nil, &ErrorList{Errors: []*Error{{Kind: KindProjectionMismatch, Detail: "the projection was made by another context"}}}
	}
	return c.model.run(data, lim, p)
}

// resFrame es el estado de un recurso mientras se escribe.
type resFrame struct {
	parent *resFrame
	start  int // dónde empieza el recurso en la salida
	lost   bool
	refs   map[string]bool // los ids locales que su contenido conservado referencia
	id     string          // su id, si es un recurso contenido

	metaStart, metaEnd int    // el valor de meta en la salida; -1 si no se escribió
	metaPos            int    // dónde iría meta si no está
	metaSpec           string // el alcance de los hijos de meta

	collect bool // los recursos hijos se juntan en cl, no se escriben
	contPos int  // dónde iría contained
	cl      []collected
}

// collected es un recurso contenido proyectado, a la espera de saber si algo
// lo referencia.
type collected struct {
	id   string
	b    []byte
	refs map[string]bool
	lost bool
}

// loss anota que la proyección quitó algo del recurso que se escribe.
func (w *walker) loss() {
	if w.p != nil && w.dry == 0 && w.fr != nil {
		w.fr.lost = true
	}
}

// noteRef anota las referencias locales (#id) conservadas: Reference.reference y
// los canonical locales (references.html), también en un arreglo.
func (w *walker) noteRef(n uint32) {
	if w.nodes[n].kind == kArray {
		for c := w.nodes[n].first; c != 0; c = w.nodes[c].next {
			w.noteRef(c)
		}
		return
	}
	if w.nodes[n].kind != kString {
		return
	}
	s := w.stringOf(n)
	if len(s) > len(localRefPrefix) && strings.HasPrefix(s, localRefPrefix) {
		if w.fr.refs == nil {
			w.fr.refs = map[string]bool{}
		}
		w.fr.refs[s[len(localRefPrefix):]] = true
	}
}

// filtersUnderscore dice si la proyección filtra el `_x` de los primitivos: con
// false sin _elements y con data no quita nada, y la salida debe ser la de Normalize.
func (p *Projection) filtersUnderscore() bool {
	return p.mode == SummaryTrue || p.mode == SummaryText || p.mode == SummaryFalse && p.names != nil
}

type action uint8

const (
	actKeep      action = iota // se conserva y se filtra por dentro
	actWhole                   // se conserva y se copia entero
	actDrop                    // se quita
	actContained               // se quita, salvo los contenidos que algo conservado referencia
)

// decide dice qué hacer con un elemento de un objeto (plan 005, §3).
func (p *Projection) decide(m *Model, sp *objSpec, fs *fieldSpec, resLevel, root bool) action {
	path := sp.scope + "." + fs.name
	switch p.mode {
	case SummaryTrue:
		if summaryKeeps(m, path) {
			if v := &fs.variants[0]; v.kind == vkObject && v.scope == typeExtension {
				return actWhole // D1: una Extension conservada va entera
			}
			return actKeep
		}
	case SummaryData:
		if resLevel && fs.name == elemText {
			return actDrop
		}
		return actKeep
	case SummaryFalse:
		if p.names == nil {
			return actKeep
		}
		fallthrough
	case SummaryText:
		if !root {
			return actKeep
		}
		if p.keepsWhole(m, fs, path) {
			return actWhole
		}
	}
	if resLevel && fs.name == elemContained {
		return actContained
	}
	return actDrop
}

// summaryKeeps dice si _summary=true conserva el elemento: es de resumen,
// obligatorio o modificador.
func summaryKeeps(m *Model, path string) bool {
	return m.IsSummary(path) || mandatory(m, path) || m.IsModifier(path)
}

// keepsWhole dice si _elements/_summary=text piden el elemento: lo nombran, es
// uno de los que el modo conserva siempre, o es obligatorio o modificador.
func (p *Projection) keepsWhole(m *Model, fs *fieldSpec, path string) bool {
	return p.names[fs.name] || p.mode == SummaryText && (fs.name == elemText || fs.name == elemID || fs.name == elemMeta) ||
		p.mode == SummaryFalse && (fs.name == elemID || fs.name == elemMeta) ||
		mandatory(m, path) || m.IsModifier(path)
}

func mandatory(m *Model, path string) bool {
	lo, _, ok := m.Cardinality(path)
	return ok && lo > 0
}

// projField escribe un elemento según la proyección.
func (w *walker) projField(sp *objSpec, g []member, first *bool, resLevel bool) bool {
	if w.p == nil || w.copy > 0 {
		return w.field(sp, g, first)
	}
	fs := &sp.fields[g[0].ref.field]
	switch w.p.decide(w.m, sp, fs, resLevel, resLevel && w.fr.parent == nil) {
	case actKeep:
		// D8 por ocurrencia: ver occurrence en normwalk.go.
		return w.field(sp, g, first)
	case actWhole:
		w.copy++
		ok := w.field(sp, g, first)
		w.copy--
		return ok
	case actContained:
		fr := w.fr
		mark, wasFirst := len(w.out), *first
		fr.contPos, fr.collect = mark, true
		whole := w.p.mode != SummaryTrue
		if whole {
			w.copy++
		}
		w.field(sp, g, first)
		if whole {
			w.copy--
		}
		fr.collect = false
		w.out, *first = w.out[:mark], wasFirst
		return false
	}
	// Se quita, pero se recorre entero: Project rechaza lo que rechaza Normalize.
	mark, wasFirst := len(w.out), *first
	w.copy++
	w.dry++
	w.field(sp, g, first)
	w.dry--
	w.copy--
	w.out, *first = w.out[:mark], wasFirst
	w.loss()
	return false
}

// resolveContained decide qué recursos contenidos se conservan (D9): los que
// una referencia local conservada nombra, directa o por otro contenido
// conservado. Los demás se pierden.
func (w *walker) resolveContained(fr *resFrame) {
	if len(fr.cl) == 0 {
		return
	}
	kept := containedClosure(fr)
	ins := []byte(`,"` + elemContained + `":[`)
	n := 0
	for i, c := range fr.cl {
		switch {
		case !kept[i]:
			fr.lost = true
		default:
			if n > 0 {
				ins = append(ins, ',')
			}
			ins = append(ins, c.b...)
			n++
			fr.lost = fr.lost || c.lost
		}
	}
	if n > 0 {
		w.out = slices.Insert(w.out, fr.contPos, append(ins, ']')...)
	}
	fr.cl = nil
}

// containedClosure marca los contenidos que una referencia local conservada
// nombra, directa o por otro contenido conservado.
func containedClosure(fr *resFrame) []bool {
	kept := make([]bool, len(fr.cl))
	if fr.refs == nil {
		fr.refs = map[string]bool{}
	}
	// Cierre transitivo con una lista de trabajo: cada id se visita una vez.
	byID := make(map[string][]int, len(fr.cl))
	for i, c := range fr.cl {
		if c.id != "" {
			byID[c.id] = append(byID[c.id], i)
		}
	}
	work := make([]string, 0, len(fr.refs))
	for id := range fr.refs {
		work = append(work, id)
	}
	for len(work) > 0 {
		id := work[len(work)-1]
		work = work[:len(work)-1]
		for _, i := range byID[id] {
			if kept[i] {
				continue
			}
			kept[i] = true
			for r := range fr.cl[i].refs {
				if !fr.refs[r] {
					fr.refs[r] = true
					work = append(work, r)
				}
			}
		}
	}
	return kept
}

// finishFrame cierra un recurso: le pone el tag si perdió algo y avisa al
// recurso que lo contiene.
func (w *walker) finishFrame(fr *resFrame) {
	if len(w.errs) > 0 {
		return
	}
	if fr.lost {
		w.addTag(fr)
	}
	if par := fr.parent; par != nil {
		if par.collect {
			par.cl = append(par.cl, collected{id: fr.id, b: slices.Clone(w.out[fr.start:]), refs: fr.refs, lost: fr.lost})
			w.out = w.out[:fr.start]
			return
		}
		par.lost = par.lost || fr.lost
	}
}

// hasSubsettedTag informa si el arreglo meta.tag ya trae el tag SUBSETTED.
func hasSubsettedTag(s *scanner, meta []byte, arr uint32) bool {
	for c := s.nodes[arr].first; c != 0; c = s.nodes[c].next {
		var sys, code string
		for m := s.nodes[c].first; m != 0; m = s.nodes[m].next {
			nd := &s.nodes[m]
			if nd.kind != kString {
				continue
			}
			switch string(meta[nd.key:nd.keyEnd]) {
			case elemSystem:
				sys = string(meta[nd.start+1 : nd.end-1])
			case elemCode:
				code = string(meta[nd.start+1 : nd.end-1])
			}
		}
		if sys == systemSubsetted && code == codeSubsetted {
			return true
		}
	}
	return false
}

// addTag agrega el tag SUBSETTED al final de meta.tag del recurso, sin
// duplicarlo, y crea meta o tag en su lugar del orden si faltan.
func (w *walker) addTag(fr *resFrame) {
	tag := w.p.tag
	if fr.metaStart < 0 {
		w.out = slices.Insert(w.out, fr.metaPos, []byte(`,"`+elemMeta+`":{"`+elemTag+`":[`+string(tag)+`]}`)...)
		return
	}
	meta := w.out[fr.metaStart:fr.metaEnd]
	s := scanner{data: meta, maxDepth: len(meta) + 1, nodes: make([]node, 0, 16)}
	root := s.scan()
	if root == 0 {
		return
	}
	sp := w.m.spec(fr.metaSpec)
	tagField := int(sp.keys[elemTag].field)
	var arr uint32
	before := -1
	for c := s.nodes[root].first; c != 0; c = s.nodes[c].next {
		nd := &s.nodes[c]
		key := string(meta[nd.key:nd.keyEnd])
		if key == elemTag {
			arr = c
		} else if ref, ok := sp.keys[key]; ok && int(ref.field) > tagField && before < 0 {
			before = int(nd.key) - 1
		}
	}
	switch {
	case arr != 0:
		if hasSubsettedTag(&s, meta, arr) {
			return
		}
		w.out = slices.Insert(w.out, fr.metaStart+int(s.nodes[arr].end)-1, append([]byte{','}, tag...)...)
	case before >= 0:
		w.out = slices.Insert(w.out, fr.metaStart+before, []byte(`"`+elemTag+`":[`+string(tag)+`],`)...)
	default:
		w.out = slices.Insert(w.out, fr.metaEnd-1, []byte(`,"`+elemTag+`":[`+string(tag)+`]`)...)
	}
}
