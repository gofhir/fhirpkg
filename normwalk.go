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
}

func (m *Model) normalize(data []byte, lim normLimits) ([]byte, error) {
	if len(data) > lim.bytes {
		return nil, &ErrorList{Errors: []*Error{{Kind: KindTooLarge, Detail: "the input is " + strconv.Itoa(len(data)) + " bytes, over the limit of " + strconv.Itoa(lim.bytes)}}}
	}
	s := scanner{data: data, maxDepth: lim.depth, nodes: make([]node, 0, len(data)/32+16)}
	root := s.scan()
	if root == 0 {
		return nil, &ErrorList{Errors: []*Error{s.err}}
	}
	w := &walker{m: m, data: data, nodes: s.nodes, out: make([]byte, 0, len(data)), max: lim.errs}
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
func (w *walker) resource(n uint32, slot string) {
	nd := &w.nodes[n]
	switch nd.kind {
	case kObject:
	case kNull:
		w.report(KindNull, n, "", "a resource is expected, not null")
		return
	default:
		w.report(KindWrongShape, n, "", "an object is expected for a resource")
		return
	}
	var dn uint32
	count := 0
	for c := nd.first; c != 0; c = w.nodes[c].next {
		if string(w.keyBytes(c)) == propResourceType {
			count++
			if dn == 0 {
				dn = c
			}
		}
	}
	switch {
	case count == 0:
		w.report(KindMissingResourceType, n, "", "the discriminator is missing")
		return
	case count > 1:
		w.report(KindDuplicateProperty, dn, propResourceType, "the discriminator is repeated")
		return
	case w.nodes[dn].kind != kString:
		w.report(KindWrongShape, dn, propResourceType, "the discriminator must be a string")
		return
	}
	rt := w.stringOf(dn)
	if !w.m.IsResource(rt) || (slot != "" && !w.m.IsSubtype(rt, slot)) {
		w.report(KindUnknownResourceType, dn, propResourceType, "not a resource type of this version")
		return
	}
	if len(w.path) == 0 {
		w.push(rt)
		defer w.pop()
	}
	w.object(n, w.m.spec(rt), rt)
}

// object escribe el objeto n con las propiedades en el orden de sp. rt, si no
// es "", es el discriminador que va primero.
func (w *walker) object(n uint32, sp *objSpec, rt string) {
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
			w.field(sp, ms[i:j], &first)
			i = j
		}
	}
	w.out = append(w.out, '}')
	w.scratch = w.scratch[:base]
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

// field escribe las propiedades de un elemento: el valor y su `_x`.
func (w *walker) field(sp *objSpec, g []member, first *bool) {
	fs := &sp.fields[g[0].ref.field]
	for _, x := range g {
		if x.ref.variant != g[0].ref.variant {
			w.push(fs.name)
			w.report(KindMultipleChoice, x.n, "", "only one variant of an element with several types may appear")
			w.pop()
			return
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
		w.primitive(v, xm, um, first)
		return
	}
	if xm != 0 {
		w.sep(first)
		w.writeKey("", v.key)
		w.value(v, xm)
	}
}

// value escribe el valor de un elemento: un arreglo si se repite.
func (w *walker) value(v *variantSpec, n uint32) {
	nd := &w.nodes[n]
	if !v.repeat {
		if nd.kind == kArray {
			w.report(KindWrongShape, n, "", "the element does not repeat: an array is not allowed")
			return
		}
		w.element(v, n)
		return
	}
	if nd.kind != kArray {
		w.report(KindWrongShape, n, "", "the element repeats: an array is expected")
		return
	}
	if nd.first == 0 {
		w.report(KindEmpty, n, "", "an empty array is not allowed")
		return
	}
	w.out = append(w.out, '[')
	i := 0
	for c := nd.first; c != 0 && !w.trunc; c = w.nodes[c].next {
		if i > 0 {
			w.out = append(w.out, ',')
		}
		w.setIdx(i)
		w.element(v, c)
		i++
	}
	w.setIdx(-1)
	w.out = append(w.out, ']')
}

func (w *walker) element(v *variantSpec, n uint32) {
	switch v.kind {
	case vkScalar, vkPrimitive:
		w.scalar(n)
	case vkObject:
		w.objectValue(n, w.m.spec(v.scope))
	case vkResource:
		w.resource(n, v.slotType)
	}
}

// objectValue comprueba la forma de un objeto y lo escribe.
func (w *walker) objectValue(n uint32, sp *objSpec) {
	nd := &w.nodes[n]
	switch {
	case nd.kind == kNull:
		w.report(KindNull, n, "", "null is not allowed here")
	case nd.kind != kObject:
		w.report(KindWrongShape, n, "", "an object is expected")
	case nd.first == 0:
		w.report(KindEmpty, n, "", "an empty object is not allowed")
	default:
		w.object(n, sp, "")
	}
}

// scalar escribe un string, número o booleano.
func (w *walker) scalar(n uint32) {
	nd := &w.nodes[n]
	switch nd.kind {
	case kString:
		if nd.end-nd.start == 2 {
			w.report(KindEmptyString, n, "", "a string can never be empty")
			return
		}
		if !nd.dirt {
			w.out = append(w.out, w.data[nd.start:nd.end]...)
			return
		}
		w.out = append(w.out, '"')
		w.out = appendEscaped(w.out, w.data[nd.start+1:nd.end-1])
		w.out = append(w.out, '"')
	case kNumber, kTrue, kFalse:
		w.out = append(w.out, w.data[nd.start:nd.end]...)
	case kNull:
		w.report(KindNull, n, "", "null is not allowed here")
	default:
		w.report(KindWrongShape, n, "", "a value is expected, not an object or an array")
	}
}

// primitive escribe `x` y `_x` de un primitivo (json.html §2.6.2.3): los dos
// arreglos de un elemento que se repite se alinean con null.
func (w *walker) primitive(v *variantSpec, xm, um uint32, first *bool) {
	ext := w.m.extSpec(v.prim)
	if !v.repeat {
		if xm != 0 {
			w.sep(first)
			w.writeKey("", v.key)
			w.value(v, xm)
		}
		if um != 0 {
			w.sep(first)
			w.writeKey(primitiveExtPrefix, v.key)
			w.objectValue(um, ext)
		}
		return
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
		return
	}
	if xm != 0 && um != 0 && w.count(xm) != w.count(um) {
		w.report(KindLengthMismatch, um, "", "the value array and its underscore array must have the same length")
		return
	}
	if xm != 0 {
		w.sep(first)
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
		w.out = append(w.out, ']')
	}
	if um != 0 {
		w.sep(first)
		w.writeKey(primitiveExtPrefix, v.key)
		w.out = append(w.out, '[')
		for c, i := w.nodes[um].first, 0; c != 0 && !w.trunc; c, i = w.nodes[c].next, i+1 {
			if i > 0 {
				w.out = append(w.out, ',')
			}
			w.setIdx(i)
			if w.nodes[c].kind == kNull {
				if xm == 0 {
					w.report(KindNull, c, "", "null needs a value in the value array at the same position")
				} else {
					w.out = append(w.out, "null"...)
				}
			} else {
				w.objectValue(c, ext)
			}
		}
		w.setIdx(-1)
		w.out = append(w.out, ']')
	}
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
