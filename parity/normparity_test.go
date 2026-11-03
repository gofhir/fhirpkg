package parity

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gofhir/fhirpkg"
	r4 "github.com/gofhir/models/r4/v2"
	r4b "github.com/gofhir/models/r4b/v2"
	r5 "github.com/gofhir/models/r5/v2"
)

// Paridad del normalizador con gofhir/models (plan 004 §3.3, paso 5).

type treeDiff struct {
	Path string // ruta con las claves tal cual, delimitadas como la biblioteca
	Elem string // la misma ruta con el guion bajo de _x quitado en cada paso
	Kind string // deleted (la entrada lo tiene y la salida no), added o changed
	A, B string
}

// pathName delimita un nombre de propiedad como la regla 8 del plan 004
// (appendPathName de la biblioteca, que no se exporta): un identificador va tal
// cual; si no (vacío, con punto, empieza con dígito), entre acentos graves con la
// barra y el acento grave escapados. Los controles van como en la biblioteca (\n, \r,
// \t, \f, \u00XX). TestPathNameAgreesWithTheLibrary comprueba
// que las dos reglas coinciden.
func pathName(name string) string {
	plain := name != ""
	for i := 0; i < len(name) && plain; i++ {
		c := name[i]
		plain = c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9'
	}
	if plain {
		return name
	}
	var b strings.Builder
	b.WriteByte('`')
	for _, r := range name {
		switch {
		case r == '`' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\f':
			b.WriteString(`\f`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('`')
	return b.String()
}

func parseTree(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func scalarText(v any) string {
	switch x := v.(type) {
	case json.Number:
		return x.String()
	case string:
		return fmt.Sprintf("%q", x)
	}
	return fmt.Sprint(v)
}

// diffTrees compara dos árboles JSON ignorando el orden de las claves. path y
// elem son la ruta delimitada (pathName) con las claves tal cual y con el guion
// bajo de _x quitado en cada paso.
func diffTrees(path, elem string, a, b any, out *[]treeDiff) {
	add := func(p, e, kind, x, y string) { *out = append(*out, treeDiff{p, e, kind, x, y}) }
	join := func(base, k string) string {
		if base == "" {
			return k
		}
		return base + "." + k
	}
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			add(path, elem, "changed", "object", scalarText(b))
			return
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, e := join(path, pathName(k)), join(elem, pathName(strings.TrimPrefix(k, "_")))
			if bv, ok := y[k]; ok {
				diffTrees(p, e, x[k], bv, out)
			} else {
				add(p, e, "deleted", scalarText(x[k]), "")
			}
		}
		for k := range y {
			if _, ok := x[k]; !ok {
				add(join(path, pathName(k)), join(elem, pathName(strings.TrimPrefix(k, "_"))), "added", "", scalarText(y[k]))
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			add(path, elem, "changed", "array", scalarText(b))
			return
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			diffTrees(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("%s[%d]", elem, i), x[i], y[i], out)
		}
		if len(x) != len(y) {
			add(path, elem, "changed", fmt.Sprintf("array of %d", len(x)), fmt.Sprintf("array of %d", len(y)))
		}
	default:
		if _, isObj := b.(map[string]any); isObj {
			add(path, elem, "changed", scalarText(a), "object")
		} else if _, isArr := b.([]any); isArr {
			add(path, elem, "changed", scalarText(a), "array")
		} else if scalarText(a) != scalarText(b) {
			add(path, elem, "changed", scalarText(a), scalarText(b))
		}
	}
}

var indexRe = regexp.MustCompile(`\[\d+\]`)

// generic quita los índices y deja la ruta como la ve el modelo.
func generic(p string) string { return indexRe.ReplaceAllString(p, "") }

type modelsResult struct {
	out     []byte
	err     error
	unknown string // el resourceType, si models devolvió UnknownResource
}

func runModels(ver string, raw []byte) modelsResult {
	var res modelsResult
	switch ver {
	case "R4":
		r, err := r4.UnmarshalResource(raw)
		if err != nil {
			res.err = err
			return res
		}
		if u, ok := r.(*r4.UnknownResource); ok {
			res.unknown = u.Type
		}
		res.out, res.err = r4.Marshal(r)
	case "R4B":
		r, err := r4b.UnmarshalResource(raw)
		if err != nil {
			res.err = err
			return res
		}
		if u, ok := r.(*r4b.UnknownResource); ok {
			res.unknown = u.Type
		}
		res.out, res.err = r4b.Marshal(r)
	default:
		r, err := r5.UnmarshalResource(raw)
		if err != nil {
			res.err = err
			return res
		}
		if u, ok := r.(*r5.UnknownResource); ok {
			res.unknown = u.Type
		}
		res.out, res.err = r5.Marshal(r)
	}
	return res
}

// Clases de la paridad (plan 004 §3.3).
const (
	clsUnchanged   = "unchanged"        // la misma secuencia de bytes que Marshal de models
	clsModelsFails = "models-fails"     // models falla (Normalize lo acepta o lo rechaza por su regla)
	clsUnknownRes  = "unknown-resource" // D4: models guarda UnknownResource, Normalize da error
	clsDeletes     = "deletes"          // Normalize da error donde models borra o altera en silencio
	clsRepairs     = "repairs-text"     // regla 1: models cambia UTF-8 inválido o un surrogate suelto por U+FFFD
	clsKeepsBad    = "keeps-invalid"    // Normalize da error donde models conserva lo inválido
	clsTransforms  = "transforms"       // models cambia el valor (Normalize lo copia)
	clsUnclassed   = "unclassified"
)

// verdict es la clase de un recurso y la regla que la explica.
type verdict struct{ Class, Rule string }

// ruleOf: la regla del plan que cada tipo de error hace cumplir.
func ruleOf(k fhirpkg.ErrorKind) string {
	switch k {
	case fhirpkg.KindSyntax:
		return "1"
	case fhirpkg.KindTooLarge, fhirpkg.KindTooDeep:
		return "7"
	case fhirpkg.KindNotPrimitive:
		return "4"
	case fhirpkg.KindUnknownResourceType, fhirpkg.KindMissingResourceType:
		return "D4"
	case fhirpkg.KindEmptyString:
		return "D5"
	}
	return "6"
}

// errorRules son las reglas de los errores, sin repetir y ordenadas.
func errorRules(list *fhirpkg.ErrorList) string {
	seen := map[string]bool{}
	for _, e := range list.Errors {
		seen[e.Kind.String()+" (rule "+ruleOf(e.Kind)+")"] = true
	}
	var out []string
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func hasKind(list *fhirpkg.ErrorList, k fhirpkg.ErrorKind) bool {
	for _, e := range list.Errors {
		if e.Kind == k {
			return true
		}
	}
	return false
}

// covered: ¿algún error está en la ruta de la diferencia o por encima de ella?
// La biblioteca siempre quita el guion bajo de la forma _x en la ruta del error,
// así que solo se compara con Elem (la ruta con el guion bajo quitado en cada
// paso); comparar también con Path sería una rama muerta.
func covered(list *fhirpkg.ErrorList, d treeDiff) bool {
	for _, e := range list.Errors {
		if e.Path == "" || d.Elem == e.Path || strings.HasPrefix(d.Elem, e.Path+".") || strings.HasPrefix(d.Elem, e.Path+"[") {
			return true
		}
	}
	return false
}

// rootName es el resourceType de un árbol.
func rootName(tree any) string {
	if m, ok := tree.(map[string]any); ok {
		s, _ := m["resourceType"].(string)
		return s
	}
	return ""
}

// classifyOne corre las dos implementaciones sobre un recurso y lo clasifica.
func classifyOne(v *version, raw []byte) verdict {
	out, err := v.ctx.Normalize(raw, fhirpkg.WithMaxBytes(1<<30))
	mr := runModels(v.name, raw)
	var list *fhirpkg.ErrorList
	if err != nil && !errors.As(err, &list) {
		return verdict{clsUnclassed, "not an ErrorList: " + err.Error()}
	}
	switch {
	case mr.err != nil:
		rule := "normalize accepts"
		if list != nil {
			rule = "normalize rejects: " + errorRules(list)
		}
		return verdict{clsModelsFails, rule}
	case err == nil && bytes.Equal(out, mr.out):
		return verdict{clsUnchanged, "-"}
	}
	mt, _ := parseTree(mr.out)
	if list == nil {
		// Normalize acepta y models cambia algo.
		ot, _ := parseTree(out)
		var d []treeDiff
		diffTrees(rootName(ot), rootName(ot), ot, mt, &d)
		if len(d) == 0 {
			return verdict{clsUnclassed, "same tree, different bytes (order)"}
		}
		return verdict{clsTransforms, transformRule(d)}
	}
	// Normalize rechaza: ¿models borra o altera, o conserva?
	in, perr := parseTree(raw)
	if perr != nil {
		return verdict{clsUnclassed, "the input is not parseable by encoding/json"}
	}
	var d []treeDiff
	diffTrees(rootName(in), rootName(in), in, mt, &d)
	switch {
	case hasKind(list, fhirpkg.KindUnknownResourceType):
		// D4: models guarda el recurso que la versión no define; Normalize lo rechaza.
		for _, x := range d {
			if !covered(list, x) {
				return verdict{clsUnclassed, "models alters " + generic(x.Path) + " and no error covers it"}
			}
		}
		return verdict{clsUnknownRes, errorRules(list)}
	case hasKind(list, fhirpkg.KindSyntax):
		// El UTF-8 inválido y los surrogates sueltos que encoding/json cambia por U+FFFD.
		if bytes.Contains(mr.out, []byte("\ufffd")) {
			return verdict{clsRepairs, errorRules(list)}
		}
		return verdict{clsUnclassed, "syntax error that models does not repair"}
	case len(d) == 0:
		return verdict{clsKeepsBad, errorRules(list)}
	}
	for _, x := range d {
		if !covered(list, x) {
			return verdict{clsUnclassed, "models alters " + generic(x.Path) + " and no error covers it"}
		}
	}
	return verdict{clsDeletes, errorRules(list)}
}

// transformRule describe cómo models cambió un valor que Normalize copia.
func transformRule(d []treeDiff) string {
	set := map[string]bool{}
	for _, x := range d {
		switch {
		case x.Kind == "changed" && strings.HasPrefix(x.A, `"`) && !strings.HasPrefix(x.B, `"`):
			set["a string is written as a number (5)"] = true
		case x.Kind == "changed" && !strings.HasPrefix(x.A, `"`) && strings.HasPrefix(x.B, `"`):
			set["a number is written as a string (5)"] = true
		default:
			set["other: "+x.Kind+" "+generic(x.Path)] = true
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, "; ")
}

// probe es una entrada escrita a mano para cada clase de §3.3: ningún corpus
// trae lo que models borra, transforma o conserva (todos son «unchanged»), así que
// estas entradas la ejercen. Los veredictos esperados están escritos desde la
// especificación y la lectura de models, no copiados de una corrida.
type probe struct {
	name, in string
	want     verdict
	r5       *verdict // el veredicto en R5, si es otro
}

var (
	vUnchanged = verdict{clsUnchanged, "-"}
	vUnknown   = verdict{clsUnknownRes, "unknown-resource-type (rule D4)"}
)

var probeCases = []probe{
	{name: "null-in-array-of-objects", in: `{"resourceType":"Patient","name":[null,{"family":"F"}]}`,
		want: verdict{clsDeletes, "null (rule 6)"}},
	{name: "decimal-as-string", in: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":"1.5"}}`,
		want: verdict{clsTransforms, "a string is written as a number (5)"}},
	{name: "integer-as-float", in: `{"resourceType":"Patient","multipleBirthInteger":2.0}`,
		want: verdict{clsModelsFails, "normalize accepts"}},
	{name: "number-out-of-range", in: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":1E400}}`,
		want: verdict{clsModelsFails, "normalize accepts"}},
	{name: "object-for-array", in: `{"resourceType":"Patient","name":{"family":"F"}}`,
		want: verdict{clsModelsFails, "normalize rejects: wrong-shape (rule 6)"}},
	{name: "array-for-object", in: `{"resourceType":"Patient","meta":[{"versionId":"1"}]}`,
		want: verdict{clsModelsFails, "normalize rejects: wrong-shape (rule 6)"}},
	{name: "empty-array", in: `{"resourceType":"Patient","name":[]}`, want: verdict{clsDeletes, "empty (rule 6)"}},
	{name: "empty-object", in: `{"resourceType":"Patient","meta":{}}`, want: verdict{clsKeepsBad, "empty (rule 6)"}},
	{name: "two-variants", in: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueString":"a","valueInteger":1}`,
		want: verdict{clsKeepsBad, "multiple-choice (rule 6)"}},
	{name: "duplicate-key", in: `{"resourceType":"Patient","id":"a","id":"b"}`, want: verdict{clsKeepsBad, "duplicate-property (rule 6)"}},
	{name: "unknown-property", in: `{"resourceType":"Patient","name":[{"family":"F","givenx":["a"]}]}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "fhir-comments", in: `{"resourceType":"Patient","fhir_comments":["c"]}`, want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "underscore-on-complex", in: `{"resourceType":"Patient","_name":[{"family":"F"}]}`,
		want: verdict{clsDeletes, "not-primitive (rule 4)"}},
	{name: "underscore-array-shorter", in: `{"resourceType":"Patient","name":[{"given":["a","b"],"_given":[{"id":"x"}]}]}`,
		want: verdict{clsKeepsBad, "length-mismatch (rule 6)"}},
	// Rutas con la regla de acentos graves (plan 004 regla 8) y la de _x desconocido.
	{name: "property-name-with-a-dot", in: `{"resourceType":"Patient","a.b":1}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "empty-property-name", in: `{"resourceType":"Patient","":1}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "property-name-starting-with-a-digit", in: `{"resourceType":"Patient","1a":1}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "unknown-underscore-property", in: `{"resourceType":"Patient","_foo":{"id":"x"}}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "empty-string", in: `{"resourceType":"Patient","name":[{"family":""}]}`, want: verdict{clsKeepsBad, "empty-string (rule D5)"}},
	{name: "null-property", in: `{"resourceType":"Patient","gender":null}`, want: verdict{clsDeletes, "null (rule 6)"}},
	{name: "invalid-utf8", in: "{\"resourceType\":\"Patient\",\"name\":[{\"family\":\"\xff\"}]}", want: verdict{clsRepairs, "syntax (rule 1)"}},
	{name: "lone-surrogate", in: `{"resourceType":"Patient","name":[{"family":"\ud800"}]}`, want: verdict{clsRepairs, "syntax (rule 1)"}},
	{name: "unknown-resource-type", in: `{"resourceType":"Foo","id":"x"}`, want: vUnknown},
	{name: "unknown-resource-type-in-bundle", in: `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Foo","id":"x"}}]}`, want: vUnknown},
	// El value dentro de _x: models lo borra. En un primitivo que deriva de otro
	// (code, id: string; canonical: uri; positiveInt: integer) y en uno que no.
	{name: "value-inside-underscore", in: `{"resourceType":"Patient","_active":{"value":false}}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "value-inside-underscore-of-a-code", in: `{"resourceType":"Patient","gender":"male","_gender":{"value":"female"}}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "value-inside-underscore-of-an-extension-code", in: `{"resourceType":"Patient","extension":[{"url":"u","valueCode":"a","_valueCode":{"value":"b"}}]}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "value-inside-underscore-of-a-canonical", in: `{"resourceType":"Patient","meta":{"profile":["http://e/p"],"_profile":[{"value":"http://e/q"}]}}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	{name: "value-inside-underscore-of-a-positive-int", in: `{"resourceType":"Patient","extension":[{"url":"u","valuePositiveInt":1,"_valuePositiveInt":{"value":2}}]}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}},
	// U+2028 y U+2029 crudos: models (encoding/json) los escribe escapados, como Normalize (D2).
	{name: "raw-separators", in: "{\"resourceType\":\"Patient\",\"name\":[{\"family\":\"a\u2028b\u2029c\"}]}", want: vUnchanged},
	// models 2.10.0 sí escribe _div (Narrative.DivExt, un Element): el plan 004 §3.3
	// suponía que no. La diferencia que queda es que el SD de xhtml prohíbe extension
	// (max 0) y models no.
	{name: "underscore-div", in: `{"resourceType":"Patient","text":{"status":"generated","div":"<div/>","_div":{"id":"d"}}}`, want: vUnchanged},
	{name: "underscore-div-extension", in: `{"resourceType":"Patient","text":{"status":"generated","div":"<div/>","_div":{"extension":[{"url":"u","valueString":"v"}]}}}`,
		want: verdict{clsKeepsBad, "unknown-property (rule 6)"}},
	{name: "valid-reordered", in: `{"gender":"male","resourceType":"Patient","id":"a"}`, want: vUnchanged},
	{name: "valid-big-integer", in: `{"resourceType":"Patient","multipleBirthInteger":1234567890123456789}`, want: vUnchanged},
	{name: "valid-null-alignment", in: `{"resourceType":"Patient","name":[{"given":["a",null],"_given":[null,{"id":"x"}]}]}`, want: vUnchanged},
	{name: "bundle-in-bundle", in: `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Bundle","type":"collection"}}]}`, want: vUnchanged},
	// Bundle.issues existe solo en R5 (bundle.html de R5).
	{name: "r5-only-element", in: `{"resourceType":"Bundle","type":"collection","issues":{"resourceType":"OperationOutcome","issue":[{"severity":"error","code":"invalid"}]}}`,
		want: verdict{clsDeletes, "unknown-property (rule 6)"}, r5: &vUnchanged},
}

// source es un corpus de la paridad.
type source struct {
	name string
	// each recorre los recursos de la versión; false si la fuente no tiene la versión.
	each func(t testing.TB, ver string, f func(name string, raw []byte)) bool
}

func officialSource() source {
	return source{"official", func(t testing.TB, ver string, f func(string, []byte)) bool {
		walkAll(t, ver, f)
		return true
	}}
}

func syntheaSource(zipPath string) source {
	return source{"synthea", func(t testing.TB, ver string, f func(string, []byte)) bool {
		if ver != "R4" {
			return false
		}
		zr, err := zip.OpenReader(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		defer zr.Close()
		for _, zf := range zr.File {
			if !strings.HasSuffix(zf.Name, ".json") {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			f(zf.Name, b)
		}
		return true
	}}
}

func generatedSource(dir string) source {
	return source{"generated", func(t testing.TB, ver string, f func(string, []byte)) bool {
		files, err := os.ReadDir(filepath.Join(dir, ver))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range files {
			b, err := os.ReadFile(filepath.Join(dir, ver, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			f(e.Name(), b)
		}
		return true
	}}
}

func probeSource() source {
	return source{"probes", func(t testing.TB, ver string, f func(string, []byte)) bool {
		for _, p := range probeCases {
			f(p.name, []byte(p.in))
		}
		return true
	}}
}

// parityCount es una fila de testdata/normalizer.json.
type parityCount struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	Class   string `json:"class"`
	Rule    string `json:"rule"`
	Count   int    `json:"count"`
}

// normStat es lo que el reporte cuenta por fuente y versión.
type normStat struct {
	Source, Version string
	Resources       int
	Reordered       int // la salida difiere de la entrada (hubo algo que normalizar)
	Bytes           int64
	MaxDepth        int
}

var (
	npMu    sync.Mutex
	npStats []normStat
)

// maxNesting es el anidamiento máximo de objetos y arreglos de un JSON válido.
func maxNesting(b []byte) int {
	depth, max := 0, 0
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{' || c == '[':
			depth++
			if depth > max {
				max = depth
			}
		case c == '}' || c == ']':
			depth--
		}
	}
	return max
}

func parityCountsPath() string { return "testdata/normalizer.json" }

func loadParityCounts(t testing.TB) []parityCount {
	b, err := os.ReadFile(parityCountsPath())
	if err != nil {
		t.Fatal(err)
	}
	var out []parityCount
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNormalizerParity compara Context.Normalize con models (UnmarshalResource +
// Marshal) sobre los ejemplos oficiales, Synthea R4, los generados y las entradas
// a mano (plan 004 §3.3). Cada recurso cae en una clase con su regla; las cuentas
// por fuente, versión, clase y regla están fijadas en testdata/normalizer.json
// (PARITY_UPDATE=1 las reescribe: revisar el diff). No puede haber un recurso
// sin clasificar, ni un recurso que Normalize acepte y no sea idempotente.
func TestNormalizerParity(t *testing.T) {
	srcs := []source{officialSource(), probeSource()}
	if z := os.Getenv("SYNTHEA_ZIP"); z != "" {
		srcs = append(srcs, syntheaSource(z))
	}
	if d := os.Getenv("GEN_CORPUS"); d != "" {
		srcs = append(srcs, generatedSource(d))
	}
	got := map[string]int{}
	ran := map[string]bool{}
	for _, v := range loadVersions(t) {
		for _, src := range srcs {
			stat := normStat{Source: src.name, Version: v.name}
			ok := src.each(t, v.name, func(name string, raw []byte) {
				vd := classifyOne(v, raw)
				if vd.Class == clsUnclassed {
					t.Errorf("%s %s %s: unclassified: %s", src.name, v.name, name, vd.Rule)
				}
				got[fmt.Sprintf("%s|%s|%s|%s", src.name, v.name, vd.Class, vd.Rule)]++
				stat.Resources++
				stat.Bytes += int64(len(raw))
				if src.name != "probes" {
					if d := maxNesting(raw); d > stat.MaxDepth {
						stat.MaxDepth = d
					}
				}
				if vd.Class == clsUnchanged {
					out, err := v.ctx.Normalize(raw, fhirpkg.WithMaxBytes(1<<30))
					if err != nil {
						t.Errorf("%s %s %s: %v", src.name, v.name, name, err)
						return
					}
					if !bytes.Equal(out, raw) {
						stat.Reordered++
					}
					if again, err := v.ctx.Normalize(out, fhirpkg.WithMaxBytes(1<<30)); err != nil || !bytes.Equal(again, out) {
						t.Errorf("%s %s %s: Normalize is not idempotent", src.name, v.name, name)
					}
				}
				if src.name == "probes" {
					for _, p := range probeCases {
						if p.name == name {
							want := p.want
							if v.name == "R5" && p.r5 != nil {
								want = *p.r5
							}
							if vd != want {
								t.Errorf("probe %s %s: %v, want %v", v.name, name, vd, want)
							}
						}
					}
				}
			})
			if ok {
				ran[src.name] = true
				npMu.Lock()
				npStats = append(npStats, stat)
				npMu.Unlock()
				t.Logf("%s %s: %d resources, %d MB, %d reordered, max nesting %d", src.name, v.name, stat.Resources, stat.Bytes>>20, stat.Reordered, stat.MaxDepth)
				if src.name != "probes" && stat.MaxDepth > fhirpkg.DefaultMaxDepth/2 {
					t.Errorf("%s %s: nesting %d leaves less than 2x margin under the default limit %d", src.name, v.name, stat.MaxDepth, fhirpkg.DefaultMaxDepth)
				}
			}
		}
	}
	// Las cuentas fijadas.
	var rows []parityCount
	for k, n := range got {
		f := strings.SplitN(k, "|", 4)
		rows = append(rows, parityCount{f[0], f[1], f[2], f[3], n})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		return fmt.Sprint(a.Source, a.Version, a.Class, a.Rule) < fmt.Sprint(b.Source, b.Version, b.Class, b.Rule)
	})
	if os.Getenv("PARITY_UPDATE") != "" && len(ran) == len(srcs) {
		b, _ := json.MarshalIndent(rows, "", " ")
		if err := os.WriteFile(parityCountsPath(), append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := map[string]int{}
	for _, r := range loadParityCounts(t) {
		if ran[r.Source] {
			want[fmt.Sprintf("%s|%s|%s|%s", r.Source, r.Version, r.Class, r.Rule)] = r.Count
		}
	}
	for k, n := range got {
		if want[k] != n {
			t.Errorf("%s: %d resources, testdata/normalizer.json says %d", k, n, want[k])
		}
		delete(want, k)
	}
	for k, n := range want {
		t.Errorf("%s: testdata/normalizer.json says %d and none occurs", k, n)
	}
	npMu.Lock()
	npRows = rows
	npMu.Unlock()
}

var npRows []parityCount

// pathName (que arma las rutas de las diferencias) y la biblioteca (que arma las
// de los errores) delimitan igual: para cada clave desconocida, el error de
// Normalize está en Patient.<pathName(clave sin el guion bajo inicial)>.
func TestPathNameAgreesWithTheLibrary(t *testing.T) {
	v := loadVersions(t)[0]
	keys := []string{"", "a.b", "1a", "_foo", "_", "__x", "a b", "a`b", "a\\b", "a\nb", "a\tb", "a\rb", "a\fb", "a\x01b", "a\x1fb", "a\u2028b", "a\u00a0b", "é", "ñ", "zz", "a-b", "9"}
	for _, k := range keys {
		kb, _ := json.Marshal(k)
		raw := []byte(`{"resourceType":"Patient",` + string(kb) + `:1}`)
		_, err := v.ctx.Normalize(raw)
		var list *fhirpkg.ErrorList
		if !errors.As(err, &list) || len(list.Errors) != 1 {
			t.Fatalf("key %q: %v", k, err)
		}
		want := "Patient." + pathName(strings.TrimPrefix(k, "_"))
		if got := list.Errors[0].Path; got != want {
			t.Errorf("key %q: library path %q, test path %q", k, got, want)
		}
	}
}
