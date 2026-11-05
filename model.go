package fhirpkg

import "sort"

// Model es el modelo de tipos de una versión de FHIR, construido desde los
// StructureDefinition ganadores del Context (plan 003). Es inmutable y seguro
// para uso concurrente. Implementa por tipado estructural las interfaces de
// gofhir/fhirpath (Model, VersionedModel, TypeRegistry, ElementOrder y
// FHIRTypes) sin importarlas.
//
// Los slices que devuelve son del modelo: el llamador no los modifica.
type Model struct {
	version   string
	types     map[string]typeInfo
	names     []string // los tipos, ordenados
	choices   map[string][]string
	pathType  map[string]string
	refs      map[string][]string
	elsewhere map[string]string
	children  map[string][]string
	fhirType  map[string]string
	card      map[string]cardinality
	summary   map[string]bool // plan 005: los caminos con isSummary verdadero
	modifier  map[string]bool // plan 005: los caminos con isModifier verdadero
	norm      normState       // plan 004: la caché del normalizador
}

type typeInfo struct {
	parent    string
	resource  bool
	primitive bool
}

type cardinality struct{ min, max int32 }

// Unbounded es el max de Cardinality cuando el elemento no tiene tope («*»).
const Unbounded = -1

// FHIRVersion es el fhirVersion del núcleo del lock.
func (m *Model) FHIRVersion() string { return m.version }

// HasType informa si el nombre es un tipo que define algún StructureDefinition
// ganador (plan 003 §3.2, regla 1): primitivos, complejos, recursos y los
// abstractos de la herencia. Distingue mayúsculas.
func (m *Model) HasType(typeName string) bool {
	_, ok := m.types[typeName]
	return ok
}

// IsResource informa si el tipo es de kind resource y no es abstracto.
func (m *Model) IsResource(typeName string) bool { return m.types[typeName].resource }

// IsPrimitive informa si el tipo es un tipo primitivo de FHIR: el
// StructureDefinition es de kind primitive-type (plan 004, D6). Los tipos del
// sistema de FHIRPath (System.String...) no son tipos del modelo y dan false,
// igual que un nombre desconocido.
func (m *Model) IsPrimitive(typeName string) bool { return m.types[typeName].primitive }

// ParentType es el tipo del StructureDefinition de baseDefinition, o "" si el
// tipo no existe o no tiene base.
func (m *Model) ParentType(typeName string) string { return m.types[typeName].parent }

// IsSubtype informa si child es parent o desciende de él por baseDefinition.
func (m *Model) IsSubtype(child, parent string) bool {
	for cur := child; cur != ""; cur = m.types[cur].parent {
		if cur == parent {
			return true
		}
	}
	return false
}

// ChoiceTypes son los códigos de tipo de un elemento con varios tipos, dado su
// camino sin el sufijo ("Observation.value"), en el orden del snapshot. Es nil
// si el camino no es un elemento de ese tipo.
func (m *Model) ChoiceTypes(path string) []string { return m.choices[path] }

// TypeOf es el código de tipo único del elemento. Un elemento con varios tipos
// se pide por su variante ("Observation.valueQuantity"). Es "" si el camino no
// existe o el elemento tiene varios tipos sin variante.
func (m *Model) TypeOf(path string) string { return m.pathType[path] }

// ReferenceTargets son los tipos de destino de un elemento de referencia o
// canónico: el tipo del StructureDefinition de cada targetProfile, en orden y
// sin repetir. Para un elemento con varios tipos, se pide la variante.
func (m *Model) ReferenceTargets(path string) []string { return m.refs[path] }

// ResolvePath devuelve el camino al que apunta el contentReference del
// elemento, o el mismo camino si no redirige.
func (m *Model) ResolvePath(path string) string {
	if to, ok := m.elsewhere[path]; ok {
		return to
	}
	return path
}

// ChildElements son los nombres de los hijos de un tipo o de un elemento, en el
// orden del snapshot; un elemento con varios tipos conserva su sufijo. Es nil
// si el camino no tiene hijos conocidos.
func (m *Model) ChildElements(path string) []string { return m.children[path] }

// FHIRTypeOf es el tipo FHIR de un elemento cuyo código es un tipo del sistema
// de FHIRPath, según la extensión fhir-type; "" si no la tiene. El value de un
// primitivo no figura.
func (m *Model) FHIRTypeOf(path string) string { return m.fhirType[path] }

// Cardinality son el min y el max del elemento; max es Unbounded si no tiene
// tope. Un elemento con varios tipos responde tanto por su camino sin sufijo
// como por cada variante. ok es false si el camino no tiene cardinalidad.
func (m *Model) Cardinality(path string) (min, max int, ok bool) {
	c, ok := m.card[path]
	return int(c.min), int(c.max), ok
}

// IsSummary informa si el elemento tiene ElementDefinition.isSummary verdadero
// en el snapshot del tipo que lo define (plan 005): el de un hijo de un tipo de
// datos está en el tipo ("HumanName.family"), no en el elemento que lo usa. Un
// elemento con varios tipos responde por su camino sin sufijo y por cada
// variante, como Cardinality. Es false si el camino no existe.
func (m *Model) IsSummary(path string) bool { return m.summary[path] }

// IsModifier informa si el elemento tiene ElementDefinition.isModifier
// verdadero (plan 005); se lee igual que IsSummary.
func (m *Model) IsModifier(path string) bool { return m.modifier[path] }

// Types son los nombres de los tipos, ordenados. Es una copia.
func (m *Model) Types() []string { return append([]string(nil), m.names...) }

// Paths son todos los caminos y nombres de tipo que el modelo conoce en alguna
// de sus tablas, ordenados. Sirve a las comparaciones, no a la evaluación.
func (m *Model) Paths() []string {
	seen := map[string]struct{}{}
	add := func(k string) { seen[k] = struct{}{} }
	for k := range m.types {
		add(k)
	}
	for k := range m.choices {
		add(k)
	}
	for k := range m.pathType {
		add(k)
	}
	for k := range m.refs {
		add(k)
	}
	for k := range m.elsewhere {
		add(k)
	}
	for k := range m.children {
		add(k)
	}
	for k := range m.fhirType {
		add(k)
	}
	for k := range m.card {
		add(k)
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Model devuelve el modelo de tipos del Set. Se construyó en NewContext.
func (c *Context) Model() *Model { return c.model }
