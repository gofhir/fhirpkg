package fhirpkg

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// pathSeg es un paso de la ruta del modelo: el nombre de la propiedad y el
// índice si va dentro de un arreglo (-1 si no).
type pathSeg struct {
	name string
	idx  int
}

// member es una propiedad de un objeto de la entrada con su lugar en la salida.
type member struct {
	sk  uint32 // 0: el discriminador; field+1; unknownSK: desconocida
	ref keyRef
	n   uint32
}

const unknownSK = math.MaxUint32

type walker struct {
	m       *Model
	data    []byte
	nodes   []node
	out     []byte
	errs    []*Error
	max     int
	trunc   bool
	path    []pathSeg
	scratch []member
	keybuf  []byte

	// Proyección (plan 005). p es nil en Normalize.
	p    *Projection
	fr   *resFrame // el recurso que se escribe
	copy int       // >0: lo que se escribe se copia entero, sin filtrar
	dry  int       // >0: lo que se escribe se descartará: no cuenta pérdidas ni referencias
}

func (m *Model) normalize(data []byte, lim normLimits) ([]byte, error) {
	return m.run(data, lim, nil)
}

// run es la única pasada de Normalize y de Project (plan 005, §2): con p nil
// normaliza; con p, además proyecta.
func (m *Model) run(data []byte, lim normLimits, p *Projection) ([]byte, error) {
	if len(data) > lim.bytes {
		return nil, &ErrorList{Errors: []*Error{{Kind: KindTooLarge, Detail: "the input is " + strconv.Itoa(len(data)) + " bytes, over the limit of " + strconv.Itoa(lim.bytes)}}}
	}
	s := scanner{data: data, maxDepth: lim.depth, nodes: make([]node, 0, len(data)/32+16)}
	root := s.scan()
	if root == 0 {
		return nil, &ErrorList{Errors: []*Error{s.err}}
	}
	w := &walker{m: m, data: data, nodes: s.nodes, out: make([]byte, 0, len(data)), max: lim.errs, p: p}
	s.nodes = nil
	w.resource(root, "")
	if len(w.errs) > 0 {
		return nil, &ErrorList{Errors: w.errs, Truncated: w.trunc}
	}
	// La salida se reservó con el tamaño de la entrada: si sobra mucho (espacios,
	// sangría), se copia a su tamaño para que quien la guarde no retenga el resto.
	if out := w.out; cap(out)-len(out) > len(out)/8 {
		return slices.Clone(out), nil
	}
	return w.out, nil
}

// pathString arma la ruta con la propiedad extra al final, si la hay.
func (w *walker) pathString(extra string) string {
	var b strings.Builder
	for i, g := range w.path {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(g.name)
		if g.idx >= 0 {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(g.idx))
			b.WriteByte(']')
		}
	}
	if extra != "" {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(extra)
	}
	return b.String()
}

// appendPathName agrega un nombre de propiedad a una ruta FHIRPath. Un nombre
// que no es un identificador (el vacío, uno con un punto o un espacio) va
// delimitado con acentos graves, con la barra y el acento grave escapados, como
// los identificadores delimitados de FHIRPath; así la ruta no es ambigua.
func appendPathName(b []byte, name string) []byte {
	plain := name != ""
	for i := 0; i < len(name) && plain; i++ {
		c := name[i]
		plain = c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9'
	}
	if plain {
		return append(b, name...)
	}
	b = append(b, '`')
	for _, r := range name {
		switch {
		case r == '`' || r == '\\':
			b = append(b, '\\', byte(r))
		case r == '\n':
			b = append(b, '\\', 'n')
		case r == '\r':
			b = append(b, '\\', 'r')
		case r == '\t':
			b = append(b, '\\', 't')
		case r == '\f':
			b = append(b, '\\', 'f')
		case r < 0x20:
			b = append(b, '\\', 'u', '0', '0', hexDigits[r>>4], hexDigits[r&0xF])
		default:
			b = utf8.AppendRune(b, r)
		}
	}
	return append(b, '`')
}

// appendPropertyPath agrega la clave de un objeto JSON a una ruta, con la regla
// de la forma _x (regla 8): el guion bajo inicial no es parte del elemento (el
// error de `_given[1]` se informa en `given[1]`), así que se quita uno. El
// escáner y el recorrido usan esta misma función; una clave desconocida como
// `_foo` se informa entonces en `foo`, igual en ambos.
func appendPropertyPath(b []byte, key string) []byte {
	return appendPathName(b, strings.TrimPrefix(key, primitiveExtPrefix))
}

// off es el byte donde empieza la propiedad o el valor.
func (w *walker) off(n uint32) int {
	if nd := &w.nodes[n]; nd.keyEnd > 0 {
		return int(nd.key) - 1
	}
	return int(w.nodes[n].start)
}

func (w *walker) report(kind ErrorKind, n uint32, extra, detail string) {
	if len(w.errs) >= w.max {
		w.trunc = true
		return
	}
	w.errs = append(w.errs, &Error{Kind: kind, Path: w.pathString(extra), Offset: w.off(n), Detail: detail})
}

func (w *walker) push(name string) { w.path = append(w.path, pathSeg{name, -1}) }
func (w *walker) pop()             { w.path = w.path[:len(w.path)-1] }
func (w *walker) setIdx(i int)     { w.path[len(w.path)-1].idx = i }

// keyBytes es la clave decodificada de un miembro.
func (w *walker) keyBytes(c uint32) []byte {
	nd := &w.nodes[c]
	raw := w.data[nd.key:nd.keyEnd]
	if !nd.keyEsc {
		return raw
	}
	w.keybuf = appendUnescaped(w.keybuf[:0], raw)
	return w.keybuf
}

func (w *walker) stringOf(n uint32) string {
	nd := &w.nodes[n]
	return string(appendUnescaped(nil, w.data[nd.start+1:nd.end-1]))
}

// resource escribe el recurso del nodo n. slot es el tipo abstracto que acota
// el discriminador, o "" si puede ser cualquier recurso (la raíz).
func (w *walker) resource(n uint32, slot string) bool {
	nd := &w.nodes[n]
	switch nd.kind {
	case kObject:
	case kNull:
		w.report(KindNull, n, "", "a resource is expected, not null")
		return false
	default:
		w.report(KindWrongShape, n, "", "an object is expected for a resource")
		return false
	}
	var dn, idn uint32
	count := 0
	for c := nd.first; c != 0; c = w.nodes[c].next {
		switch string(w.keyBytes(c)) {
		case propResourceType:
			count++
			if dn == 0 {
				dn = c
			}
		case elemID:
			idn = c
		}
	}
	switch {
	case count == 0:
		w.report(KindMissingResourceType, n, "", "the discriminator is missing")
		return false
	case count > 1:
		w.report(KindDuplicateProperty, dn, propResourceType, "the discriminator is repeated")
		return false
	case w.nodes[dn].kind != kString:
		w.report(KindWrongShape, dn, propResourceType, "the discriminator must be a string")
		return false
	}
	rt := w.stringOf(dn)
	if !w.m.IsResource(rt) || (slot != "" && !w.m.IsSubtype(rt, slot)) {
		w.report(KindUnknownResourceType, dn, propResourceType, "not a resource type of this version")
		return false
	}
	if len(w.path) == 0 {
		if w.p != nil && rt != w.p.rt {
			w.report(KindProjectionMismatch, dn, propResourceType, "the projection was made for another resource type")
			return false
		}
		w.push(rt)
		defer w.pop()
	}
	if w.p == nil {
		w.object(n, w.m.spec(rt), rt)
		return true
	}
	fr := &resFrame{parent: w.fr, start: len(w.out), metaStart: -1, metaPos: -1, contPos: -1}
	if fr.parent != nil && fr.parent.collect {
		if idn != 0 && w.nodes[idn].kind == kString {
			fr.id = w.stringOf(idn)
		}
	}
	w.fr = fr
	w.object(n, w.m.spec(rt), rt)
	w.fr = fr.parent
	w.finishFrame(fr)
	return true
}

// object escribe el objeto n con las propiedades en el orden de sp. rt, si no
// es "", es el discriminador que va primero. Devuelve cuántos elementos escribió
// (sin contar el discriminador): con una proyección puede ser 0.
func (w *walker) object(n uint32, sp *objSpec, rt string) int {
	base := len(w.scratch)
	for c := w.nodes[n].first; c != 0; c = w.nodes[c].next {
		ref, ok := sp.keys[string(w.keyBytes(c))]
		switch {
		case !ok:
			w.scratch = append(w.scratch, member{sk: unknownSK, n: c})
		case ref.discriminator:
			w.scratch = append(w.scratch, member{sk: 0, ref: ref, n: c})
		default:
			w.scratch = append(w.scratch, member{sk: uint32(ref.field) + 1, ref: ref, n: c})
		}
	}
	ms := w.scratch[base:]
	slices.SortFunc(ms, func(a, b member) int {
		switch {
		case a.sk != b.sk:
			if a.sk < b.sk {
				return -1
			}
			return 1
		case a.sk == unknownSK:
			if c := strings.Compare(string(w.keyBytes(a.n)), string(w.keyBytes(b.n))); c != 0 {
				return c
			}
		}
		if a.n < b.n {
			return -1
		}
		return 1
	})
	w.out = append(w.out, '{')
	first := true
	if rt != "" {
		w.out = append(w.out, '"')
		w.out = append(w.out, propResourceType...)
		w.out = append(w.out, `":"`...)
		w.out = append(w.out, rt...)
		w.out = append(w.out, '"')
		first = false
	}
	var fr *resFrame
	metaIdx := -1
	if w.p != nil && rt != "" {
		fr = w.fr
		if k, ok := sp.keys[elemMeta]; ok && !k.under {
			metaIdx = int(k.field)
			fr.metaSpec = sp.fields[metaIdx].variants[0].scope
		}
	}
	written := 0
	for i := 0; i < len(ms) && !w.trunc; {
		switch ms[i].sk {
		case 0:
			i++
		case unknownSK:
			w.report(KindUnknownProperty, ms[i].n, string(appendPropertyPath(nil, string(w.keyBytes(ms[i].n)))), "the type has no such element")
			i++
		default:
			j := i
			for j < len(ms) && ms[j].sk == ms[i].sk {
				j++
			}
			f := int(ms[i].ref.field)
			if fr != nil && fr.metaPos < 0 && metaIdx >= 0 && f > metaIdx {
				fr.metaPos = len(w.out) // dónde va meta si no está: tras el último elemento anterior
			}
			mark := len(w.out)
			if w.projField(sp, ms[i:j], &first, rt != "") {
				written++
				if fr != nil && f == metaIdx {
					lead := 0
					if w.out[mark] == ',' {
						lead = 1
					}
					fr.metaStart, fr.metaEnd = mark+lead+len(elemMeta)+3, len(w.out)
				}
			}
			i = j
		}
	}
	if fr != nil {
		if fr.metaPos < 0 {
			fr.metaPos = len(w.out)
		}
		w.resolveContained(fr)
	}
	w.out = append(w.out, '}')
	w.scratch = w.scratch[:base]
	return written
}

func (w *walker) sep(first *bool) {
	if !*first {
		w.out = append(w.out, ',')
	}
	*first = false
}

func (w *walker) writeKey(prefix, key string) {
	w.out = append(w.out, '"')
	w.out = append(w.out, prefix...)
	w.out = append(w.out, key...)
	w.out = append(w.out, `":`...)
}

// field escribe las propiedades de un elemento: el valor y su `_x`. Devuelve si
// escribió algo.
func (w *walker) field(sp *objSpec, g []member, first *bool) bool {
	fs := &sp.fields[g[0].ref.field]
	for _, x := range g {
		if x.ref.variant != g[0].ref.variant {
			w.push(fs.name)
			w.report(KindMultipleChoice, x.n, "", "only one variant of an element with several types may appear")
			w.pop()
			return false
		}
	}
	v := &fs.variants[g[0].ref.variant]
	w.push(v.key)
	defer w.pop()
	var xm, um uint32
	for _, x := range g {
		slot := &xm
		if x.ref.under {
			slot = &um
		}
		if *slot != 0 {
			w.report(KindDuplicateProperty, x.n, "", "the property is repeated")
			*slot = math.MaxUint32 // marca: no se baja a ninguno de los dos
			continue
		}
		*slot = x.n
	}
	if xm == math.MaxUint32 {
		xm = 0
	}
	if um == math.MaxUint32 {
		um = 0
	}
	if um != 0 && v.kind != vkPrimitive {
		w.report(KindNotPrimitive, um, "", "only a primitive element has the underscore form")
		um = 0
	}
	if v.kind == vkPrimitive {
		if xm != 0 && w.p != nil && w.dry == 0 && (sp.scope == typeReference && v.key == elemReference || v.prim == typeCanonical) {
			w.noteRef(xm)
		}
		if um != 0 && w.p != nil && w.copy == 0 && mandatory(w.m, sp.scope+"."+fs.name) {
			// D8 (ampliada el 04-11-2026): el par `x`/`_x` de un primitivo min>0
			// se conserva entero: el elemento puede estar presente solo por su `_x`.
			w.copy++
			ok := w.primitive(v, xm, um, first)
			w.copy--
			return ok
		}
		return w.primitive(v, xm, um, first)
	}
	if xm != 0 {
		return w.keyed(v, xm, "", first, w.p != nil && w.copy == 0 && w.dry == 0 && mandatory(w.m, sp.scope+"."+fs.name))
	}
	return false
}

// keyed escribe `clave: valor`; si el valor queda vacío (una proyección quitó
// todo lo que tenía), deshace la clave y cuenta una pérdida.
func (w *walker) keyed(v *variantSpec, n uint32, prefix string, first *bool, mand bool) bool {
	mark, wasFirst := len(w.out), *first
	w.sep(first)
	w.writeKey(prefix, v.key)
	if !w.value(v, n, mand) {
		w.out, *first = w.out[:mark], wasFirst
		w.loss()
		return false
	}
	return true
}

// occurrence escribe una ocurrencia de un elemento. D8 (aclaración del code
// review, ronda 2): si el elemento es obligatorio y el filtro dejaría vacía esta
// ocurrencia (puede existir solo por una extensión data-absent-reason), esa
// ocurrencia se conserva entera; es por ocurrencia, no por elemento.
func (w *walker) occurrence(v *variantSpec, n uint32, mand bool) bool {
	if !mand {
		return w.element(v, n)
	}
	mark, nErr := len(w.out), len(w.errs)
	lost := w.fr.lost
	if w.element(v, n) {
		return true
	}
	if w.trunc || len(w.errs) != nErr {
		return false
	}
	w.out, w.fr.lost = w.out[:mark], lost
	w.copy++
	ok := w.element(v, n)
	w.copy--
	return ok
}

// value escribe el valor de un elemento: un arreglo si se repite. Devuelve
// false si no escribió nada: un error, o una proyección que lo dejó vacío.
func (w *walker) value(v *variantSpec, n uint32, mand bool) bool {
	nd := &w.nodes[n]
	if !v.repeat {
		if nd.kind == kArray {
			w.report(KindWrongShape, n, "", "the element does not repeat: an array is not allowed")
			return false
		}
		return w.occurrence(v, n, mand)
	}
	if nd.kind != kArray {
		w.report(KindWrongShape, n, "", "the element repeats: an array is expected")
		return false
	}
	if nd.first == 0 {
		w.report(KindEmpty, n, "", "an empty array is not allowed")
		return false
	}
	open := len(w.out)
	w.out = append(w.out, '[')
	wrote := 0
	i := 0
	for c := nd.first; c != 0 && !w.trunc; c = w.nodes[c].next {
		mark := len(w.out)
		if wrote > 0 {
			w.out = append(w.out, ',')
		}
		w.setIdx(i)
		if w.occurrence(v, c, mand) {
			wrote++
		} else {
			w.out = w.out[:mark]
			w.loss()
		}
		i++
	}
	w.setIdx(-1)
	if wrote == 0 {
		w.out = w.out[:open]
		return false
	}
	w.out = append(w.out, ']')
	return true
}

func (w *walker) element(v *variantSpec, n uint32) bool {
	switch v.kind {
	case vkScalar, vkPrimitive:
		return w.scalar(n)
	case vkObject:
		return w.objectValue(n, w.m.spec(v.scope))
	case vkResource:
		return w.resource(n, v.slotType)
	}
	return false
}

// objectValue comprueba la forma de un objeto y lo escribe. Devuelve false si
// no escribió nada.
func (w *walker) objectValue(n uint32, sp *objSpec) bool {
	nd := &w.nodes[n]
	switch {
	case nd.kind == kNull:
		w.report(KindNull, n, "", "null is not allowed here")
	case nd.kind != kObject:
		w.report(KindWrongShape, n, "", "an object is expected")
	case nd.first == 0:
		w.report(KindEmpty, n, "", "an empty object is not allowed")
	default:
		mark := len(w.out)
		if w.object(n, sp, "") > 0 {
			return true
		}
		w.out = w.out[:mark]
	}
	return false
}

// scalar escribe un string, número o booleano.
func (w *walker) scalar(n uint32) bool {
	nd := &w.nodes[n]
	switch nd.kind {
	case kString:
		if nd.end-nd.start == 2 {
			w.report(KindEmptyString, n, "", "a string can never be empty")
			return false
		}
		if !nd.dirt {
			w.out = append(w.out, w.data[nd.start:nd.end]...)
			return true
		}
		w.out = append(w.out, '"')
		w.out = appendEscaped(w.out, w.data[nd.start+1:nd.end-1])
		w.out = append(w.out, '"')
	case kNumber, kTrue, kFalse:
		w.out = append(w.out, w.data[nd.start:nd.end]...)
	case kNull:
		w.report(KindNull, n, "", "null is not allowed here")
		return false
	default:
		w.report(KindWrongShape, n, "", "a value is expected, not an object or an array")
		return false
	}
	return true
}

// primitive escribe `x` y `_x` de un primitivo (json.html §2.6.2.3): los dos
// arreglos de un elemento que se repite se alinean con null. Con una proyección
// que filtra, el `_x` se filtra con la regla de cualquier Element y las
// posiciones que quedan sin nada se quitan de los dos arreglos (plan 005, regla 2).
func (w *walker) primitive(v *variantSpec, xm, um uint32, first *bool) bool {
	ext := w.m.extSpec(v.prim)
	if !v.repeat {
		wrote := false
		if xm != 0 {
			wrote = w.keyed(v, xm, "", first, false) || wrote
		}
		if um != 0 {
			mark, wasFirst := len(w.out), *first
			w.sep(first)
			w.writeKey(primitiveExtPrefix, v.key)
			if w.objectValue(um, ext) {
				wrote = true
			} else {
				w.out, *first = w.out[:mark], wasFirst
				w.loss()
			}
		}
		return wrote
	}
	for _, p := range [2]*uint32{&xm, &um} {
		if *p == 0 {
			continue
		}
		switch nd := &w.nodes[*p]; {
		case nd.kind != kArray:
			w.report(KindWrongShape, *p, "", "the element repeats: an array is expected")
			*p = 0
		case nd.first == 0:
			w.report(KindEmpty, *p, "", "an empty array is not allowed")
			*p = 0
		}
	}
	if xm == 0 && um == 0 {
		return false
	}
	// json.html R5 §2.1.6.4.2: «implementations SHALL infer null values in the
	// suffix of the shorter array». R4 y R4B callan; aquí se hace igual en las tres
	// versiones (decisión del 05-11-2026): el arreglo más corto se rellena con null
	// hasta el largo del otro, y las reglas de null siguen igual sobre el relleno.
	nx, nu := 0, 0
	if xm != 0 {
		nx = w.count(xm)
	}
	if um != 0 {
		nu = w.count(um)
	}
	if w.p != nil && w.copy == 0 && um != 0 && w.p.filtersUnderscore() {
		return w.primitiveFiltered(v, xm, um, nx, nu, ext, first)
	}
	wrote := false
	if xm != 0 {
		w.sep(first)
		wrote = true
		w.writeKey("", v.key)
		w.out = append(w.out, '[')
		var p uint32
		if um != 0 {
			p = w.nodes[um].first
		}
		for c, i := w.nodes[xm].first, 0; c != 0 && !w.trunc; c, i = w.nodes[c].next, i+1 {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			w.setIdx(i)
			if w.nodes[c].kind == kNull {
				if p == 0 || w.nodes[p].kind == kNull {
					w.report(KindNull, c, "", "null needs a value in the underscore array at the same position")
				} else {
					w.out = append(w.out, "null"...)
				}
			} else {
				w.scalar(c)
			}
			if p != 0 {
				p = w.nodes[p].next
			}
		}
		w.setIdx(-1)
		if um != 0 {
			// El valor más corto que su `_x`: el relleno es null y un `_x` null en esa
			// posición queda sin ninguno de los dos.
			c := w.nodes[um].first
			for i := 0; c != 0 && i < nx; i++ {
				c = w.nodes[c].next
			}
			for ; c != 0; c = w.nodes[c].next {
				w.out = append(w.out, ",null"...) // el error de un `_x` null va con los de `_x`, en el mismo orden que primitiveFiltered
			}
		}
		w.out = append(w.out, ']')
	}
	if um != 0 {
		w.sep(first)
		wrote = true
		w.writeKey(primitiveExtPrefix, v.key)
		w.out = append(w.out, '[')
		for c, i := w.nodes[um].first, 0; c != 0 && !w.trunc; c, i = w.nodes[c].next, i+1 {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			w.setIdx(i)
			if w.nodes[c].kind == kNull {
				if xm == 0 || i >= nx {
					w.report(KindNull, c, "", "null needs a value in the value array at the same position")
				} else {
					w.out = append(w.out, "null"...)
				}
			} else {
				w.objectValue(c, ext)
			}
		}
		w.setIdx(-1)
		if xm != 0 {
			for i := nu; i < nx && !w.trunc; i++ {
				w.out = append(w.out, ",null"...)
			}
		}
		w.out = append(w.out, ']')
	}
	return wrote
}

// primitiveFiltered es primitive para un arreglo con `_x` cuando la proyección
// filtra: cada `_x[i]` pasa por la regla de Element; una posición cuyo valor es
// null y cuyo `_x` quedó vacío se quita de los dos arreglos, y si ningún `_x`
// sobrevive, el arreglo `_x` desaparece. Valida en el mismo orden que primitive
// (primero los valores, luego los `_x`), para dar los mismos errores.
func (w *walker) primitiveFiltered(v *variantSpec, xm, um uint32, nx, nu int, ext *objSpec, first *bool) bool {
	type pos struct {
		x    []byte // el valor escrito; nil si es null
		null bool
		ext  []byte // el `_x[i]` filtrado; nil si quedó vacío o era null
	}
	ps := make([]pos, max(nx, nu))
	if xm != 0 {
		p := w.nodes[um].first
		for c, i := w.nodes[xm].first, 0; c != 0 && !w.trunc; c, i = w.nodes[c].next, i+1 {
			w.setIdx(i)
			if w.nodes[c].kind == kNull {
				if p == 0 || w.nodes[p].kind == kNull {
					w.report(KindNull, c, "", "null needs a value in the underscore array at the same position")
				}
				ps[i].null = true
			} else {
				mark := len(w.out)
				w.scalar(c)
				ps[i].x = slices.Clone(w.out[mark:])
				w.out = w.out[:mark]
			}
			if p != 0 {
				p = w.nodes[p].next
			}
		}
	}
	for i := nx; xm != 0 && i < len(ps); i++ {
		ps[i].null = true // el sufijo inferido del valor más corto
	}
	keptExt := 0
	for c, i := w.nodes[um].first, 0; c != 0 && !w.trunc; c, i = w.nodes[c].next, i+1 {
		w.setIdx(i)
		if w.nodes[c].kind == kNull {
			if xm == 0 || i >= nx {
				w.report(KindNull, c, "", "null needs a value in the value array at the same position")
			}
			continue
		}
		mark := len(w.out)
		if w.objectValue(c, ext) {
			ps[i].ext = slices.Clone(w.out[mark:])
			keptExt++
		} else {
			w.loss()
		}
		w.out = w.out[:mark]
	}
	w.setIdx(-1)
	// Una posición sin valor y sin `_x` desaparece de los dos arreglos.
	if xm != 0 {
		kept := ps[:0]
		for _, p := range ps {
			if p.null && p.ext == nil {
				w.loss()
				continue
			}
			kept = append(kept, p)
		}
		ps = kept
	}
	wrote := false
	if xm != 0 && len(ps) > 0 {
		w.sep(first)
		wrote = true
		w.writeKey("", v.key)
		w.out = append(w.out, '[')
		for i, p := range ps {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			if p.null {
				w.out = append(w.out, "null"...)
			} else {
				w.out = append(w.out, p.x...)
			}
		}
		w.out = append(w.out, ']')
	}
	if keptExt > 0 {
		w.sep(first)
		wrote = true
		w.writeKey(primitiveExtPrefix, v.key)
		w.out = append(w.out, '[')
		for i, p := range ps {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			if p.ext == nil {
				w.out = append(w.out, "null"...)
			} else {
				w.out = append(w.out, p.ext...)
			}
		}
		w.out = append(w.out, ']')
	}
	return wrote
}

func (w *walker) count(arr uint32) int {
	k := 0
	for c := w.nodes[arr].first; c != 0; c = w.nodes[c].next {
		k++
	}
	return k
}

// unescapeAt lee el escape o el byte en d[i] y devuelve su rune y el siguiente
// índice. d ya pasó el escáner: sus escapes y su UTF-8 son válidos.
func unescapeAt(d []byte, i int) (rune, int) {
	c := d[i]
	if c != '\\' {
		if c < utf8.RuneSelf {
			return rune(c), i + 1
		}
		r, w := utf8.DecodeRune(d[i:])
		return r, i + w
	}
	switch d[i+1] {
	case 'b':
		return '\b', i + 2
	case 'f':
		return '\f', i + 2
	case 'n':
		return '\n', i + 2
	case 'r':
		return '\r', i + 2
	case 't':
		return '\t', i + 2
	case 'u':
		r := hexRune(d[i+2 : i+6])
		if r >= 0xD800 && r <= 0xDBFF {
			lo := hexRune(d[i+8 : i+12])
			return 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00), i + 12
		}
		return r, i + 6
	}
	return rune(d[i+1]), i + 2 // \" \\ \/
}

func hexRune(h []byte) rune {
	var r rune
	for _, c := range h {
		r = r<<4 | hexVal(c)
	}
	return r
}

// appendUnescaped agrega el contenido decodificado de un string del escáner.
func appendUnescaped(dst, raw []byte) []byte {
	for i := 0; i < len(raw); {
		var r rune
		r, i = unescapeAt(raw, i)
		dst = utf8.AppendRune(dst, r)
	}
	return dst
}

const hexDigits = "0123456789abcdef"

// appendEscaped reescribe el contenido de un string con el escape de
// encoding/json sin escapar HTML, el de gofhir/models (plan 004, D2): comillas,
// barra, controles, y U+2028 y U+2029.
func appendEscaped(dst, raw []byte) []byte {
	for i := 0; i < len(raw); {
		var r rune
		r, i = unescapeAt(raw, i)
		switch {
		case r == '"' || r == '\\':
			dst = append(dst, '\\', byte(r))
		case r == '\n':
			dst = append(dst, '\\', 'n')
		case r == '\r':
			dst = append(dst, '\\', 'r')
		case r == '\t':
			dst = append(dst, '\\', 't')
		case r == '\b':
			dst = append(dst, '\\', 'b')
		case r == '\f':
			dst = append(dst, '\\', 'f')
		case r < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hexDigits[r>>4], hexDigits[r&0xF])
		case r == 0x2028 || r == 0x2029:
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[r&0xF])
		default:
			dst = utf8.AppendRune(dst, r)
		}
	}
	return dst
}
