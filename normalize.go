package fhirpkg

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Normalizador de orden (plan 004). Context.Normalize recibe un recurso FHIR
// en JSON y devuelve los mismos datos con las propiedades en el orden del
// modelo (Model.ChildElements), sin espacios y con los arreglos en su orden.
// Se aplica una vez al escribir; se guardan y se sirven los bytes normalizados.
// No valida tipos de valores, cardinalidad ni invariantes (del validador); sí
// la forma JSON que json.html fija y todo lo que un modelo de estructuras
// borraría en silencio.

// Límites por defecto (plan 004, regla 7). La profundidad sale de medir los
// ejemplos oficiales (parity/depth_test.go, plan 004 §9): el máximo de
// anidamiento de objetos y arreglos (la raíz es 1) es 22 en R4, 21 en R4B y 24
// en R5, todos en Bundle o Questionnaire; el defecto deja más del doble de
// margen. gofhir/models limita otra cosa (recursos dentro de recursos, 32) y
// deja la estructura al tope de encoding/json (10000).
const (
	// DefaultMaxDepth es el anidamiento máximo de objetos y arreglos.
	DefaultMaxDepth = 64
	// DefaultMaxBytes es el tamaño máximo de la entrada (los ejemplos oficiales
	// llegan a 41 MB).
	DefaultMaxBytes = 256 << 20
	// DefaultMaxErrors es el tope de errores que se informan.
	DefaultMaxErrors = 100
)

// ErrorKind clasifica un error de Normalize. Es estable: el núcleo lo usa para
// armar el OperationOutcome.
type ErrorKind int

const (
	// KindSyntax: JSON mal formado, UTF-8 inválido, surrogate sin par o número
	// mal escrito (json.html: la codificación es siempre UTF-8).
	KindSyntax ErrorKind = iota + 1
	// KindTooLarge: la entrada supera el tamaño máximo.
	KindTooLarge
	// KindTooDeep: el anidamiento supera la profundidad máxima.
	KindTooDeep
	// KindMissingResourceType: donde va un recurso falta su discriminador.
	KindMissingResourceType
	// KindUnknownResourceType: el discriminador no es un tipo de recurso de la
	// versión (o no corresponde al elemento).
	KindUnknownResourceType
	// KindUnknownProperty: la propiedad no es un elemento del tipo (un modelo
	// de estructuras la borraría).
	KindUnknownProperty
	// KindDuplicateProperty: dos claves iguales una vez decodificadas (json.html:
	// los nombres de propiedad son únicos).
	KindDuplicateProperty
	// KindNotPrimitive: la clave `_x` sobre un elemento que no es un primitivo
	// de FHIR (json.html: solo los primitivos tienen la forma `_x`).
	KindNotPrimitive
	// KindMultipleChoice: dos variantes del mismo elemento con varios tipos.
	KindMultipleChoice
	// KindWrongShape: objeto donde va arreglo o escalar, arreglo donde va un
	// valor, escalar donde va objeto.
	KindWrongShape
	// KindEmpty: objeto o arreglo vacío (json.html: no se permiten).
	KindEmpty
	// KindEmptyString: string vacío (json.html: un string nunca puede estar vacío).
	KindEmptyString
	// KindNull: null fuera de la alineación de los arreglos `x` y `_x`.
	KindNull
	// KindUnknownElement: (NewProjection, plan 005) el nombre de _elements no es
	// un elemento de la raíz del tipo.
	KindUnknownElement
	// KindChoiceElement: (NewProjection) el nombre de _elements es `value[x]` o una
	// variante (`valueQuantity`): search.html pide nombres de elemento base. El
	// núcleo puede ignorar este error (search.html permite ignorar el nombre).
	KindChoiceElement
	// KindConflictingModes: (NewProjection) _summary y _elements a la vez (D5).
	KindConflictingModes
	// KindProjectionMismatch: (Project) la Projection viene de otro Context o su
	// tipo no es el resourceType de la entrada, o el modo no existe.
	KindProjectionMismatch
)

var kindNames = [...]string{
	KindSyntax:              "syntax",
	KindTooLarge:            "too-large",
	KindTooDeep:             "too-deep",
	KindMissingResourceType: "missing-resource-type",
	KindUnknownResourceType: "unknown-resource-type",
	KindUnknownProperty:     "unknown-property",
	KindDuplicateProperty:   "duplicate-property",
	KindNotPrimitive:        "not-primitive",
	KindMultipleChoice:      "multiple-choice",
	KindWrongShape:          "wrong-shape",
	KindEmpty:               "empty",
	KindEmptyString:         "empty-string",
	KindNull:                "null",
	KindUnknownElement:      "unknown-element",
	KindChoiceElement:       "choice-element",
	KindConflictingModes:    "conflicting-modes",
	KindProjectionMismatch:  "projection-mismatch",
}

func (k ErrorKind) String() string {
	if k > 0 && int(k) < len(kindNames) {
		return kindNames[k]
	}
	return fmt.Sprintf("ErrorKind(%d)", int(k))
}

// Error es un defecto de la entrada de Normalize, con su lugar.
type Error struct {
	Kind ErrorKind
	// Path es la ruta FHIRPath con índices del lugar (Patient.name[0].given[1]).
	// Un error en `_given[1]` se informa en la ruta del elemento. En un recurso
	// anidado sigue por la propiedad (Bundle.entry[0].resource.name[0]). Un
	// error de sintaxis trae la misma forma hasta el contenedor donde ocurrió, con
	// las claves decodificadas; su raíz es el resourceType si se leyó antes del
	// error y Resource si no, de modo que nunca está vacía. Un nombre de
	// propiedad que no es un identificador (el vacío, uno con un punto) va
	// delimitado con acentos graves: Patient.`` es la propiedad de nombre vacío.
	// Un guion bajo inicial se quita siempre (`_given` se informa como `given`,
	// y una clave desconocida `_foo` como `foo`), igual en el escáner y en el
	// recorrido.
	Path string
	// Offset es el byte de la entrada donde empieza el valor o la clave.
	Offset int
	// Detail explica el defecto.
	Detail string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %s (offset %d)", e.Kind, e.Detail, e.Offset)
	}
	return fmt.Sprintf("%s at %s: %s (offset %d)", e.Kind, e.Path, e.Detail, e.Offset)
}

// ErrorList son todos los errores de una entrada, ordenados por su lugar en la
// salida (el orden del modelo), de modo que el resultado no depende del orden
// de las claves de la entrada. errors.As(err, &e) con e de tipo *Error da el
// primero; Unwrap devuelve todos.
type ErrorList struct {
	Errors []*Error
	// Truncated indica que había más errores que el tope (WithMaxErrors).
	Truncated bool
}

func (l *ErrorList) Error() string {
	var b strings.Builder
	for i, e := range l.Errors {
		if i == 3 {
			fmt.Fprintf(&b, "; and %d more", len(l.Errors)-3)
			break
		}
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(e.Error())
	}
	if l.Truncated {
		b.WriteString("; more errors were not collected")
	}
	return b.String()
}

// Unwrap da cada error, para errors.As y errors.Is.
func (l *ErrorList) Unwrap() []error {
	out := make([]error, len(l.Errors))
	for i, e := range l.Errors {
		out[i] = e
	}
	return out
}

// NormalizeOption cambia un límite de Normalize.
type NormalizeOption func(*normLimits)

type normLimits struct{ depth, bytes, errs int }

// WithMaxDepth fija el anidamiento máximo de objetos y arreglos (por defecto
// DefaultMaxDepth). Debe ser positivo.
func WithMaxDepth(n int) NormalizeOption { return func(l *normLimits) { l.depth = n } }

// WithMaxBytes fija el tamaño máximo de la entrada (por defecto DefaultMaxBytes).
// Debe ser positivo.
func WithMaxBytes(n int) NormalizeOption { return func(l *normLimits) { l.bytes = n } }

// WithMaxErrors fija cuántos errores se recogen (por defecto DefaultMaxErrors).
// Debe ser positivo.
func WithMaxErrors(n int) NormalizeOption { return func(l *normLimits) { l.errs = n } }

// Normalize devuelve el recurso con sus propiedades en el orden del modelo y
// sin espacios. Si la entrada tiene defectos (ErrorKind) devuelve nil y un
// *ErrorList con todos, en el orden de la salida. La salida es idempotente:
// Normalize(Normalize(x)) == Normalize(x) byte a byte. Los strings se escriben
// con el escape de encoding/json sin escapar HTML (el de gofhir/models) y los
// números con su texto. Es seguro para uso concurrente.
func (c *Context) Normalize(data []byte, opts ...NormalizeOption) ([]byte, error) {
	lim, err := limitsOf(opts)
	if err != nil {
		return nil, err
	}
	return c.model.normalize(data, lim)
}

// limitsOf aplica las opciones sobre los límites por defecto.
func limitsOf(opts []NormalizeOption) (normLimits, error) {
	lim := normLimits{DefaultMaxDepth, DefaultMaxBytes, DefaultMaxErrors}
	for _, o := range opts {
		o(&lim)
	}
	if lim.depth < 1 || lim.bytes < 1 || lim.errs < 1 {
		return lim, errors.New("normalize: the limits must be positive")
	}
	lim.bytes = min(lim.bytes, math.MaxUint32-1)
	return lim, nil
}
