package fhirpkg

import (
	"slices"
	"strings"
	"sync"
)

// El orden de un objeto y el tipo de cada hijo, leídos del Model (plan 004,
// reglas 2 a 4). Se arman por demanda, una vez por alcance (un tipo o un
// elemento con hijos propios), y se guardan en el Model.

// normState es la caché del normalizador dentro del Model.
type normState struct {
	specs sync.Map // alcance -> *objSpec
	exts  sync.Map // tipo primitivo -> *objSpec de su `_x`

	slotOnce sync.Once
	slots    map[string]bool
}

// valueKind es la forma que el modelo da a un hijo.
type valueKind uint8

const (
	vkScalar    valueKind = iota + 1 // tipo del sistema (Element.id, Extension.url): un escalar sin `_x`
	vkPrimitive                      // primitivo de FHIR: un escalar y su `_x`
	vkObject                         // tipo complejo o elemento con hijos propios
	vkResource                       // un recurso, discriminado por su resourceType
)

// variantSpec es un hijo con un tipo: un elemento, o una variante de un [x].
type variantSpec struct {
	key       string // clave JSON
	kind      valueKind
	repeat    bool   // max distinto de 1
	scope     string // vkObject: el alcance de sus hijos (se resuelve al usarlo: los tipos son recursivos)
	prim      string // vkPrimitive: el tipo, de donde salen los hijos de `_x`
	slotType  string // vkResource: el tipo (abstracto) que acota el discriminador
	fieldName string // el nombre del elemento (sin la variante), para el error de un [x]
}

type fieldSpec struct {
	name     string // el nombre del elemento sin [x]
	variants []variantSpec
}

// keyRef identifica una clave JSON de un objeto.
type keyRef struct {
	field, variant uint16
	under          bool // la clave `_x`
	discriminator  bool // la clave del tipo de recurso
}

type objSpec struct {
	fields []fieldSpec
	keys   map[string]keyRef
}

// slotTypes son los tipos que acotan un elemento que contiene un recurso: los
// tipos de los que desciende algún recurso concreto (Resource, DomainResource),
// salvo los que también son ancestros de un primitivo (la raíz común de R5, que
// es de los tipos de datos y de los recursos a la vez). Un tipo abstracto sin
// descendientes (los que R5 agrega a la herencia de recursos) no es un tipo de
// elemento.
func (m *Model) slotTypes() map[string]bool {
	m.norm.slotOnce.Do(func() {
		ancestors := func(keep func(typeInfo) bool) map[string]bool {
			out := map[string]bool{}
			for _, t := range m.names {
				if keep(m.types[t]) {
					for cur := t; cur != "" && !out[cur]; cur = m.types[cur].parent {
						out[cur] = true
					}
				}
			}
			return out
		}
		res := ancestors(func(i typeInfo) bool { return i.resource })
		for t := range ancestors(func(i typeInfo) bool { return i.primitive }) {
			delete(res, t)
		}
		m.norm.slots = res
	})
	return m.norm.slots
}

// spec devuelve el objSpec de un alcance: un tipo o la ruta de un elemento con
// hijos propios.
func (m *Model) spec(scope string) *objSpec {
	if v, ok := m.norm.specs.Load(scope); ok {
		return v.(*objSpec)
	}
	sp := m.buildSpec(scope, nil)
	v, _ := m.norm.specs.LoadOrStore(scope, sp)
	return v.(*objSpec)
}

// extSpec devuelve el objSpec de los hijos de `_x` de un primitivo: los del
// tipo menos los que hereda de su primer ancestro que no es un primitivo (el
// value, regla 4). Un primitivo puede derivar de otro (code e id de string,
// canonical de uri, positiveInt de integer) y la base primitiva también trae
// value: se sube hasta el tipo que no es primitivo (Element, o PrimitiveType en R5).
func (m *Model) extSpec(prim string) *objSpec {
	if v, ok := m.norm.exts.Load(prim); ok {
		return v.(*objSpec)
	}
	base := m.ParentType(prim)
	for m.IsPrimitive(base) {
		base = m.ParentType(base)
	}
	inherited := m.ChildElements(base)
	sp := m.buildSpec(prim, func(name string) bool { return !slices.Contains(inherited, name) })
	v, _ := m.norm.exts.LoadOrStore(prim, sp)
	return v.(*objSpec)
}

func (m *Model) buildSpec(scope string, skip func(string) bool) *objSpec {
	sp := &objSpec{keys: map[string]keyRef{}}
	for _, cn := range m.ChildElements(scope) {
		if skip != nil && skip(cn) {
			continue
		}
		base, choice := strings.CutSuffix(cn, choiceSuffix)
		path := scope + "." + base
		if _, hi, ok := m.Cardinality(path); ok && hi == 0 {
			continue // max 0: el tipo no admite el elemento
		}
		f := fieldSpec{name: base}
		if choice {
			for _, code := range m.ChoiceTypes(path) {
				if v, ok := m.variant(path+upperFirst(code), base+upperFirst(code), base); ok {
					f.variants = append(f.variants, v)
				}
			}
		} else if v, ok := m.variant(path, base, base); ok {
			f.variants = append(f.variants, v)
		}
		if len(f.variants) == 0 {
			continue
		}
		fi := uint16(len(sp.fields))
		sp.fields = append(sp.fields, f)
		for vi, v := range f.variants {
			sp.keys[v.key] = keyRef{field: fi, variant: uint16(vi)}
			sp.keys[primitiveExtPrefix+v.key] = keyRef{field: fi, variant: uint16(vi), under: true}
		}
	}
	if m.IsResource(scope) {
		sp.keys[propResourceType] = keyRef{discriminator: true}
	}
	return sp
}

// variant describe un hijo por su ruta; ok es false si el modelo no le da tipo.
func (m *Model) variant(path, key, field string) (variantSpec, bool) {
	v := variantSpec{key: key, fieldName: field}
	if _, hi, ok := m.Cardinality(path); ok {
		v.repeat = hi == Unbounded || hi > 1
	}
	rp := m.ResolvePath(path) // contentReference: los hijos y el tipo son los del destino
	t := m.TypeOf(rp)
	switch {
	case len(m.ChildElements(rp)) > 0:
		v.kind, v.scope = vkObject, rp
	case t == "":
		return v, false
	case !m.HasType(t):
		v.kind = vkScalar
	case m.IsPrimitive(t):
		v.kind, v.prim = vkPrimitive, t
	case m.slotTypes()[t]:
		v.kind, v.slotType = vkResource, t
	default:
		v.kind, v.scope = vkObject, t
	}
	return v, true
}
