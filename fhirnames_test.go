package fhirpkg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// formatLiterals son los literales de una sola palabra permitidos fuera de
// fhirnames.go: separadores y los nombres de las enumeraciones de esta
// librería (Step, Kind). Ninguno es un nombre de recurso, elemento o parámetro
// de FHIR; uno nuevo hace fallar el test hasta decidirlo.
var formatLiterals = map[string]bool{
	`"|"`: true, `"#"`: true, `":"`: true, `"+"`: true, `"-"`: true, `"."`: true, `""`: true, `"0"`: true,
	`"series"`:             true, // Step: paso de la cascada
	`"publication-status"`: true, // Step
	`"publisher"`:          true, // Step
	`"version-order"`:      true, // Step
	`"last-updated"`:       true, // Step
	`"not-experimental"`:   true, // Step
	`"core-first"`:         true, // Step
	`"unresolved"`:         true, // Step
	`"single"`:             true, // Step
	`"canonical"`:          true, // Kind
	`"base-code"`:          true, // Kind
	`"01"`:                 true, // fhirDateKey: mes y día por defecto
	`"0000"`:               true, // fhirDateKey: el año 0000 no es una fecha FHIR (datatypes.html)
	`")"`:                  true, // separador en un mensaje
	// Incremento 001 y CLI: vocabulario de la especificación de packages
	// (packages.html), del formato del lock y de la línea de comandos; ninguno
	// es un nombre de recurso o elemento de FHIR.
	`"core"`:                         true, // manifest.go: package.json type Core / fhir.core (packages.html)
	`"current"`:                      true, // version.go: versiones de npm que se rechazan
	`"dev"`:                          true, // ídem
	`"latest"`:                       true, // ídem
	`"x"`:                            true, // ídem: comodín M.m.x
	`".x"`:                           true, // ídem
	`"empty"`:                        true, // manifest.go: mensaje
	`"(root)"`:                       true, // graph.go y resolve.go: quien pide un package
	`" (asks "`:                      true, // graph.go: mensaje
	`"none"`:                         true, // cmd/fhirpkg/graph.go: reporte
	`"(none)"`:                       true, // ídem
	`" (fhir-version-list)"`:         true, // ídem: campo legado de packages.html
	`"\n### Verdict\n\n"`:            true, // ídem: título del reporte
	`"%s conflicts: %s\n"`:           true, // cmd/fhirpkg/main.go: salida de context
	`"- Problem: %s.\n"`:             true, // cmd/fhirpkg/graph.go: reporte
	`"%-48s %6d resources\n"`:        true, // cmd/fhirpkg/main.go: salida de verify
	`"fhir.core"`:                    true, // manifest.go: type del núcleo R4 (packages.html)
	`"https://packages.fhir.org"`:    true, // registry.go: el registro oficial
	`"package.json"`:                 true, // tgz.go: manifiesto de un package (packages.html)
	`"scripts/graph-candidates.txt"`: true, // cmd/fhirpkg/graph.go: valor por defecto de un flag
	`"fhirpkg.lock"`:                 true, // cmd/fhirpkg: archivo por defecto
	`".cache/packages"`:              true, // cmd/fhirpkg: carpeta por defecto
	`"report"`:                       true, // ídem
	`"root %s#%s: %w"`:               true, // graph.go: formato de un error de raíz
	`"%s: HTTP %s"`:                  true, // registry.go: formato de un error HTTP
	`"entry %q: %w"`:                 true, // tgz.go: formato de un error de entrada
	`"## %s\n\nRoots: %s\n\n"`:       true, // cmd/fhirpkg/graph.go: encabezado del reporte
	`"exception: %w"`:                true, // resolve.go: prefijo de un error de excepción (formato propio del lock)
	`".json"`:                        true, // tgz.go: archivos de un package
	`"package/"`:                     true, // tgz.go: carpeta de un .tgz (packages.html)
	`".tgz"`:                         true, // registry.go: archivos de packages
	`"fhirpkg:"`:                     true, // cmd/fhirpkg: prefijo de los errores
	`"lock"`:                         true, // cmd/fhirpkg: subcomando y flag
	`"verify"`:                       true, // ídem
	`"graph"`:                        true, // ídem
	`"context"`:                      true, // ídem
	`"registry"`:                     true, // ídem
	`"candidates"`:                   true, // ídem
	`"except"`:                       true, // ídem
	`"dir"`:                          true, // ídem
	`"o"`:                            true, // ídem
}

// proseAllowed son los mensajes de varias palabras que nombran un identificador
// de FHIR a propósito, cada uno con su motivo.
var proseAllowed = map[string]bool{
	`"invalid lock: empty fhirVersion"`:                                        true, // lock.go: nombre del campo del lock (formato propio, ADR-001)
	`"invalid lock: package %s: excluded and excludedReason must go together"`: true, // lock.go: campos del lock (formato propio)
}

type lit struct{ pos, val string }

// lockFormatFields son los campos del formato del lock y del resto de la
// librería (no son de FHIR): las etiquetas json de lock.go, graph.go y
// overlap.go. Los demás nombres de campo salen de jsonFieldSources.
var lockFormatFields = map[string]bool{
	"schema": true, "core": true, "roots": true, "packages": true, "order": true, "exceptions": true,
	"sha256": true, "legacyVersionList": true, "via": true, "excluded": true, "excludedReason": true,
	"package": true, "reason": true,
	// Protocolo del registro npm (registry.go): metadata de un package.
	"versions": true, "dist": true, "tarball": true,
}

// verbs son los verbos de formato de fmt (%s, %q, %w, %d, %v, %-5s...).
var verbs = regexp.MustCompile(`%[#+\- 0]*[0-9.]*[a-zA-Z%]`)

// camelCase reconoce un identificador de FHIR en camelCase (valueQuantity).
var camelCase = regexp.MustCompile(`^[a-z]+[A-Z]`)

// words parte un literal en palabras: secuencias de letras, dígitos, "-" y "_".
func words(v string) []string {
	var out []string
	v = verbs.ReplaceAllString(v, " ")
	for _, w := range strings.FieldsFunc(v, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_')
	}) {
		if strings.ContainsFunc(w, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
			out = append(out, w)
		}
	}
	return out
}

// forbiddenWords arma el conjunto de palabras que no pueden aparecer en un
// literal de varias palabras (un mensaje): los nombres de tipos de recurso, los
// identificadores de FHIR en camelCase o con guion de jsonFieldSources, y los
// valores con mayúscula de fhirnames.go. Una palabra corriente como "status" o
// "code" sí puede aparecer en un mensaje en inglés; como literal de una sola
// palabra, no (formatLiterals). Los tipos de recurso salen de los datos, no de
// una lista escrita a mano: del dorado de testdata (columna resourceType), que
// siempre está, y, con los packages (make check-packages), de los
// StructureDefinition de las tres versiones (Context.ResourceTypes).
func forbiddenWords(t testing.TB, resourceTypes []string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, rt := range resourceTypes {
		out[rt] = true
	}
	for name := range jsonFieldSources {
		if strings.ContainsFunc(name, unicode.IsUpper) || strings.Contains(name, "-") {
			out[name] = true
		}
	}
	for _, v := range []string{typeSearchParameter, typeStructureDefinition} {
		out[v] = true
	}
	// Todos los tipos de R4, R4B y R5 (abstractos incluidos), generados de los
	// packages (make golden) y comprobados contra ellos en make check-packages:
	// así make check, sin packages, también los conoce.
	b, err := os.ReadFile(filepath.Join("testdata", "resource-types.txt"))
	if err != nil {
		t.Fatalf("%v (make golden)", err)
	}
	for _, rt := range strings.Fields(string(b)) {
		out[rt] = true
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "golden", "*.tsv"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Split(line, "\t"); len(f) > 2 && !strings.HasPrefix(line, "#") && f[1] != "" {
				out[f[1]] = true
			}
		}
	}
	return out
}

// sourceViolations devuelve los literales de un fuente que rompen la regla 5:
//   - uno de una sola palabra (con o sin espacios alrededor) que no está en la
//     lista de formato;
//   - uno de varias palabras con alguna palabra prohibida;
//   - una etiqueta json cuyo nombre no está documentado.
func sourceViolations(t testing.TB, name string, src []byte, forbidden map[string]bool) []lit {
	t.Helper()
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, name, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []lit
	skip := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ImportSpec:
			skip[n.Path] = true
		case *ast.Field:
			if n.Tag != nil {
				skip[n.Tag] = true
				tag, _ := strconv.Unquote(n.Tag.Value)
				if jsonName, ok := reflect.StructTag(tag).Lookup("json"); ok {
					field, _, _ := strings.Cut(jsonName, ",")
					if _, known := jsonFieldSources[field]; !known && !lockFormatFields[field] && field != "" && field != "-" {
						out = append(out, lit{fs.Position(n.Tag.Pos()).String(), n.Tag.Value})
					}
				}
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		l, ok := n.(*ast.BasicLit)
		if !ok || l.Kind != token.STRING || skip[l] {
			return true
		}
		v, err := strconv.Unquote(l.Value)
		if err != nil {
			t.Fatal(err)
		}
		ws := words(v)
		switch {
		case len(ws) == 0:
			// Solo puntuación o formato: no puede nombrar nada.
		case len(ws) >= 2:
			// Un mensaje (dos o más palabras sin contar los verbos de formato):
			// ninguna puede ser un nombre de FHIR, ni una palabra camelCase, y no
			// todas pueden ser campos de FHIR (una lista armada como datos).
			// Límite: una lista con al menos una palabra corriente («url version
			// and status») pasa; el guardián no es un analizador de lenguaje.
			if proseAllowed[l.Value] {
				break
			}
			bad, allFields := false, true
			for _, w := range ws {
				if forbidden[w] || camelCase.MatchString(w) {
					bad = true
				}
				if _, ok := jsonFieldSources[w]; !ok {
					allFields = false
				}
			}
			if bad || allFields {
				out = append(out, lit{fs.Position(l.Pos()).String(), l.Value})
			}
		default:
			// Una sola pieza (con o sin espacios alrededor o verbos de formato):
			// debe estar en la lista de formato.
			if !formatLiterals[l.Value] && !(len(ws) == 1 && formatLiterals[strconv.Quote(ws[0])] && strings.TrimSpace(v) == v && !strings.Contains(v, "%")) {
				out = append(out, lit{fs.Position(l.Pos()).String(), l.Value})
			}
		}
		return true
	})
	return out
}

// productionFiles son los archivos de producción de la raíz y de cmd/: todos,
// los del 001 también; solo fhirnames.go queda fuera.
func productionFiles(t testing.TB) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"*.go", filepath.Join("cmd", "*", "*.go")} {
		files, err := filepath.Glob(pat)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if !strings.HasSuffix(f, "_test.go") && f != "fhirnames.go" {
				out = append(out, f)
			}
		}
	}
	return out
}

func guardFiles(t *testing.T, forbidden map[string]bool) {
	t.Helper()
	files := productionFiles(t)
	if len(files) < 12 {
		t.Fatalf("scanned only %d files; the guard is not looking where it should", len(files))
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range sourceViolations(t, f, src, forbidden) {
			t.Errorf("%s: literal %s outside fhirnames.go (rule 5): move it there with its source or add it to the format list", v.pos, v.val)
		}
	}
}

// TestFHIRNamesOnlyInFHIRNames hace cumplir la regla 5 en todo el código de
// producción: ningún literal con un nombre de FHIR fuera de fhirnames.go, y toda
// etiqueta json con su nombre y su fuente. Con los tipos de recurso del dorado.
func TestFHIRNamesOnlyInFHIRNames(t *testing.T) {
	guardFiles(t, forbiddenWords(t, nil))
}

// TestRealFHIRNamesOnlyInFHIRNames repite el guardián con todos los tipos de
// recurso de R4, R4B y R5 (make check-packages).
func TestRealFHIRNamesOnlyInFHIRNames(t *testing.T) {
	var all []string
	for _, rc := range realCases {
		c, _ := realContext(t, rc.lock)
		// Todos los tipos, abstractos incluidos: ResourceTypes no los lista.
		all = append(all, c.types...)
	}
	if len(all) < 300 {
		t.Fatalf("only %d resource types", len(all))
	}
	guardFiles(t, forbiddenWords(t, all))
}

// El guardián se prueba con fuentes sintéticos, incluidos los rodeos.
func TestFHIRNamesGuard(t *testing.T) {
	src := "package p\n\nimport \"strings\"\n\n" +
		"type T struct {\n\tA string `json:\"status\"`\n\tB string `json:\"made-up-field,omitempty\"`\n\tC string `json:\"-\"`\n}\n\n" +
		"const a = \"SearchParameter\"\nconst b = `kind`\nvar c = \"two words are prose\"\nvar d = \"%s is fine\"\nvar e = \"|\"\n" +
		"var f = strings.ToLower(\"Patient\")\n" +
		"var g = strings.Fields(\"Patient Observation\")\n" +
		"var h = \" SearchParameter\"\n" +
		"var i = \"%s lastUpdated\"\n" +
		"var j = \"a\\tPatient\"\n" +
		"var k = \"status \"\n" +
		"var l = \"no match for %s in the status\"\n" +
		"var m = strings.TrimSuffix(\"status %s\", \" %s\")\n" +
		"var n = strings.Fields(\"valueQuantity effectiveDateTime\")\n" +
		"var o = strings.Fields(\"url version status\")\n" +
		"var q = \"see DomainResource and Resource\"\n"
	forbidden := forbiddenWords(t, []string{"Patient", "Observation", "DomainResource", "Resource"})
	var got []string
	for _, v := range sourceViolations(t, "synthetic.go", []byte(src), forbidden) {
		got = append(got, v.val)
	}
	want := []string{"`json:\"made-up-field,omitempty\"`", `"SearchParameter"`, "`kind`", `"Patient"`, `"Patient Observation"`, `" SearchParameter"`, `"%s lastUpdated"`, `"a\tPatient"`, `"status "`, `"status %s"`, `"valueQuantity effectiveDateTime"`, `"url version status"`, `"see DomainResource and Resource"`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("violations = %q, want %q", got, want)
	}
}

// Toda entrada de jsonFieldSources tiene su fuente escrita.
func TestFieldSourcesAreDocumented(t *testing.T) {
	for name, src := range jsonFieldSources {
		if strings.TrimSpace(src) == "" || name == "" {
			t.Errorf("field %q has no source", name)
		}
	}
}

// TestResourceTypesListIsComplete: sin packages, el guardián conoce los tipos
// de recurso de las tres versiones por testdata/resource-types.txt.
func TestResourceTypesListIsComplete(t *testing.T) {
	f := forbiddenWords(t, nil)
	for _, rt := range []string{"Patient", "Observation", "Resource", "DomainResource", "CanonicalResource", "MetadataResource", "Bundle"} {
		if !f[rt] {
			t.Errorf("%s is not a forbidden word without packages", rt)
		}
	}
}
