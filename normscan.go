package fhirpkg

import (
	"fmt"
	"unicode/utf8"
)

// Escáner de JSON propio (plan 004, regla 1). encoding/json cambia el UTF-8
// inválido y los surrogates sin par por U+FFFD sin error, pasa los números por
// float64 y no ve las claves duplicadas: aquí cada byte se comprueba y los
// números conservan su texto. El escáner no copia: arma un arreglo de nodos con
// los rangos de la entrada.

type nodeKind uint8

const (
	kNull nodeKind = iota + 1
	kTrue
	kFalse
	kNumber
	kString
	kObject
	kArray
)

// node es un valor JSON. El índice 0 no es un nodo (sin hijo o sin hermano).
type node struct {
	start, end   uint32 // el valor completo en la entrada (un string, con sus comillas)
	key, keyEnd  uint32 // la clave sin comillas, si el nodo es miembro de un objeto
	first, next  uint32 // primer hijo y siguiente hermano
	kind         nodeKind
	keyEsc, dirt bool // la clave tiene una barra; el string necesita reescribirse
}

// seg es un paso de la ruta cruda hasta donde falló el escáner.
type seg struct {
	key, keyEnd uint32
	idx         int32 // índice en un arreglo, o -1 si es una clave
	esc         bool  // la clave tiene una barra
}

type scanner struct {
	data     []byte
	pos      int
	nodes    []node
	maxDepth int
	stack    []seg
	err      *Error
}

func (s *scanner) fail(kind ErrorKind, at int, format string, args ...any) uint32 {
	if s.err == nil {
		e := &Error{Kind: kind, Offset: at, Detail: fmt.Sprintf(format, args...)}
		e.Path = s.rawPath()
		s.err = e
	}
	return 0
}

// rawPath arma la ruta FHIRPath (regla 8) hasta el error: el tipo del recurso
// raíz, las claves decodificadas y los índices. El tipo es el resourceType de
// la raíz si el escáner ya lo leyó antes del error; si no (falta, está después
// o no es un string), la raíz se llama Resource, que es lo único que se sabe
// de cualquier instancia. Así la ruta nunca queda vacía.
func (s *scanner) rawPath() string {
	b := append([]byte(nil), s.rootType()...)
	for _, g := range s.stack {
		if g.idx >= 0 {
			b = fmt.Appendf(b, "[%d]", g.idx)
			continue
		}
		raw := s.data[g.key:g.keyEnd]
		if g.esc {
			raw = appendUnescaped(nil, raw)
		}
		b = append(b, '.')
		b = appendPropertyPath(b, string(raw))
	}
	return string(b)
}

// rootType es el resourceType de la raíz si ya se leyó completo.
func (s *scanner) rootType() string {
	if len(s.nodes) < 2 || s.nodes[1].kind != kObject {
		return rootFallback
	}
	for c := s.nodes[1].first; c != 0; c = s.nodes[c].next {
		nd := &s.nodes[c]
		raw := s.data[nd.key:nd.keyEnd]
		if nd.keyEsc {
			raw = appendUnescaped(nil, raw)
		}
		if string(raw) == propResourceType && nd.kind == kString && nd.end-nd.start > 2 {
			// Un nombre de tipo siempre es un identificador: si no lo es, no es un tipo.
			t := string(appendUnescaped(nil, s.data[nd.start+1:nd.end-1]))
			if string(appendPathName(nil, t)) != t {
				return rootFallback
			}
			return t
		}
	}
	return rootFallback
}

func (s *scanner) skipWS() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *scanner) newNode(k nodeKind, start int) uint32 {
	s.nodes = append(s.nodes, node{start: uint32(start), kind: k})
	return uint32(len(s.nodes) - 1)
}

// scan lee un documento JSON completo y devuelve el nodo raíz (0 si hay error).
func (s *scanner) scan() uint32 {
	s.nodes = append(s.nodes, node{}) // el índice 0 es «ninguno»
	s.skipWS()
	root := s.value(1)
	if root == 0 {
		return 0
	}
	s.skipWS()
	if s.pos != len(s.data) {
		return s.fail(KindSyntax, s.pos, "unexpected data after the top-level value")
	}
	return root
}

func (s *scanner) value(depth int) uint32 {
	if s.pos >= len(s.data) {
		return s.fail(KindSyntax, s.pos, "unexpected end of input")
	}
	start := s.pos
	switch c := s.data[s.pos]; {
	case c == '{':
		return s.object(depth)
	case c == '[':
		return s.array(depth)
	case c == '"':
		_, _, dirt, ok := s.str()
		if !ok {
			return 0
		}
		n := s.newNode(kString, start)
		s.nodes[n].end = uint32(s.pos)
		s.nodes[n].key, s.nodes[n].keyEnd = 0, 0
		s.nodes[n].dirt = dirt
		return n
	case c == '-' || (c >= '0' && c <= '9'):
		if !s.number() {
			return 0
		}
		n := s.newNode(kNumber, start)
		s.nodes[n].end = uint32(s.pos)
		return n
	case c == 't':
		return s.literal("true", kTrue)
	case c == 'f':
		return s.literal("false", kFalse)
	case c == 'n':
		return s.literal("null", kNull)
	default:
		return s.fail(KindSyntax, s.pos, "invalid character %q looking for the beginning of a value", rune(c))
	}
}

func (s *scanner) literal(word string, k nodeKind) uint32 {
	start := s.pos
	if len(s.data)-s.pos < len(word) || string(s.data[s.pos:s.pos+len(word)]) != word {
		return s.fail(KindSyntax, start, "invalid literal")
	}
	s.pos += len(word)
	n := s.newNode(k, start)
	s.nodes[n].end = uint32(s.pos)
	return n
}

// number comprueba la gramática de un número JSON y avanza.
func (s *scanner) number() bool {
	d, p := s.data, s.pos
	start := p
	if p < len(d) && d[p] == '-' {
		p++
	}
	switch {
	case p < len(d) && d[p] == '0':
		p++
	case p < len(d) && d[p] >= '1' && d[p] <= '9':
		for p < len(d) && d[p] >= '0' && d[p] <= '9' {
			p++
		}
	default:
		s.fail(KindSyntax, start, "invalid number")
		return false
	}
	if p < len(d) && d[p] == '.' {
		p++
		q := p
		for p < len(d) && d[p] >= '0' && d[p] <= '9' {
			p++
		}
		if p == q {
			s.fail(KindSyntax, start, "invalid number: no digits after the decimal point")
			return false
		}
	}
	if p < len(d) && (d[p] == 'e' || d[p] == 'E') {
		p++
		if p < len(d) && (d[p] == '+' || d[p] == '-') {
			p++
		}
		q := p
		for p < len(d) && d[p] >= '0' && d[p] <= '9' {
			p++
		}
		if p == q {
			s.fail(KindSyntax, start, "invalid number: no digits in the exponent")
			return false
		}
	}
	s.pos = p
	return true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) rune {
	switch {
	case c <= '9':
		return rune(c - '0')
	case c >= 'a':
		return rune(c-'a') + 10
	}
	return rune(c-'A') + 10
}

// u4 lee los cuatro dígitos hex de un \u en la posición p (después de «\u»).
func (s *scanner) u4(p int) (rune, bool) {
	if p+4 > len(s.data) {
		return 0, false
	}
	var r rune
	for i := 0; i < 4; i++ {
		c := s.data[p+i]
		if !isHex(c) {
			return 0, false
		}
		r = r<<4 | hexVal(c)
	}
	return r, true
}

// str lee un string que empieza en la comilla de s.pos. Devuelve el rango del
// contenido y si necesita reescribirse (una barra o un U+2028/9 posible). Falla
// ante un control, un escape inválido, un surrogate sin par o UTF-8 inválido.
func (s *scanner) str() (cs, ce int, dirt, ok bool) {
	d := s.data
	start := s.pos
	p := start + 1
	cs = p
	for {
		if p >= len(d) {
			s.fail(KindSyntax, start, "string is not terminated")
			return
		}
		c := d[p]
		switch {
		case c == '"':
			ce = p
			s.pos = p + 1
			return cs, ce, dirt, true
		case c < 0x20:
			s.fail(KindSyntax, p, "control character %#02x in a string", c)
			return
		case c == '\\':
			dirt = true
			if p+1 >= len(d) {
				s.fail(KindSyntax, p, "string is not terminated")
				return
			}
			switch d[p+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				p += 2
			case 'u':
				r, good := s.u4(p + 2)
				if !good {
					s.fail(KindSyntax, p, "invalid \\u escape")
					return
				}
				p += 6
				switch {
				case r >= 0xD800 && r <= 0xDBFF:
					if p+1 < len(d) && d[p] == '\\' && d[p+1] == 'u' {
						if lo, good := s.u4(p + 2); good && lo >= 0xDC00 && lo <= 0xDFFF {
							p += 6
							continue
						}
					}
					s.fail(KindSyntax, p-6, "high surrogate without its low surrogate")
					return
				case r >= 0xDC00 && r <= 0xDFFF:
					s.fail(KindSyntax, p-6, "low surrogate without its high surrogate")
					return
				}
			default:
				s.fail(KindSyntax, p, "invalid escape \\%c", rune(d[p+1]))
				return
			}
		case c < utf8.RuneSelf:
			p++
		default:
			if c == 0xE2 {
				dirt = true // U+2028 y U+2029 se escapan al escribir
			}
			r, w := utf8.DecodeRune(d[p:])
			if r == utf8.RuneError && w <= 1 {
				s.fail(KindSyntax, p, "invalid UTF-8")
				return
			}
			p += w
		}
	}
}

func (s *scanner) object(depth int) uint32 {
	start := s.pos
	if depth > s.maxDepth {
		return s.fail(KindTooDeep, start, "nesting is deeper than %d", s.maxDepth)
	}
	s.pos++
	n := s.newNode(kObject, start)
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == '}' {
		s.pos++
		s.nodes[n].end = uint32(s.pos)
		return n
	}
	var last uint32
	for {
		s.skipWS()
		if s.pos >= len(s.data) || s.data[s.pos] != '"' {
			return s.fail(KindSyntax, s.pos, "object key expected")
		}
		cs, ce, dirt, ok := s.str()
		if !ok {
			return 0
		}
		s.skipWS()
		if s.pos >= len(s.data) || s.data[s.pos] != ':' {
			return s.fail(KindSyntax, s.pos, "colon expected after the key")
		}
		s.pos++
		s.skipWS()
		s.stack = append(s.stack, seg{uint32(cs), uint32(ce), -1, dirt})
		c := s.value(depth + 1)
		if c == 0 {
			return 0
		}
		s.stack = s.stack[:len(s.stack)-1]
		s.nodes[c].key, s.nodes[c].keyEnd, s.nodes[c].keyEsc = uint32(cs), uint32(ce), dirt
		if last == 0 {
			s.nodes[n].first = c
		} else {
			s.nodes[last].next = c
		}
		last = c
		s.skipWS()
		if s.pos >= len(s.data) {
			return s.fail(KindSyntax, s.pos, "unexpected end of input in an object")
		}
		switch s.data[s.pos] {
		case ',':
			s.pos++
		case '}':
			s.pos++
			s.nodes[n].end = uint32(s.pos)
			return n
		default:
			return s.fail(KindSyntax, s.pos, "comma or closing brace expected")
		}
	}
}

func (s *scanner) array(depth int) uint32 {
	start := s.pos
	if depth > s.maxDepth {
		return s.fail(KindTooDeep, start, "nesting is deeper than %d", s.maxDepth)
	}
	s.pos++
	n := s.newNode(kArray, start)
	s.skipWS()
	if s.pos < len(s.data) && s.data[s.pos] == ']' {
		s.pos++
		s.nodes[n].end = uint32(s.pos)
		return n
	}
	var last uint32
	for i := int32(0); ; i++ {
		s.skipWS()
		s.stack = append(s.stack, seg{idx: i})
		c := s.value(depth + 1)
		if c == 0 {
			return 0
		}
		s.stack = s.stack[:len(s.stack)-1]
		if last == 0 {
			s.nodes[n].first = c
		} else {
			s.nodes[last].next = c
		}
		last = c
		s.skipWS()
		if s.pos >= len(s.data) {
			return s.fail(KindSyntax, s.pos, "unexpected end of input in an array")
		}
		switch s.data[s.pos] {
		case ',':
			s.pos++
		case ']':
			s.pos++
			s.nodes[n].end = uint32(s.pos)
			return n
		default:
			return s.fail(KindSyntax, s.pos, "comma or closing bracket expected")
		}
	}
}
