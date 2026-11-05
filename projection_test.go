package fhirpkg

import (
	"errors"
	"strings"
	"testing"
)

// Los casos a mano de la proyección (plan 005 §5.4). Cada uno cita la sección de
// la especificación o la regla del plan que lo origina; el valor esperado se
// escribió a mano desde ella, sin ejecutar el código. Corren sobre la fixture y
// sobre los tres núcleos oficiales (make check-packages) cuando el caso no
// depende de flags que difieren entre ambos.

const narr = `{"status":"generated","div":"<div xmlns=\"http://www.w3.org/1999/xhtml\">x</div>"}`

// expectedTag arma el tag SUBSETTED esperado desde el CodeSystem del Set, sin
// pasar por subsettedTag: el display es el del concepto del package.
func expectedTag(t testing.TB, c *Context) string {
	t.Helper()
	r, err := c.Resolve(typeCodeSystem, systemSubsetted)
	if err != nil {
		t.Fatal(err)
	}
	return tagOf(t, r.Resource.Raw)
}

func tagOf(t testing.TB, raw []byte) string {
	t.Helper()
	tree, err := parseOrdered(raw)
	if err != nil {
		t.Fatal(err)
	}
	var find func(v any) string
	find = func(v any) string {
		o, _ := v.(jobj)
		for _, cc := range func() []any { a, _ := o.getArr("concept"); return a }() {
			co := cc.(jobj)
			if code, _ := co.get("code"); code == codeSubsetted {
				d, _ := co.get("display")
				return d.(string)
			}
			if s := find(co); s != "" {
				return s
			}
		}
		return ""
	}
	return `{"system":"` + systemSubsetted + `","code":"SUBSETTED","display":` + strconvQuote(find(tree)) + `}`
}

func (o jobj) getArr(k string) ([]any, bool) {
	v, ok := o.get(k)
	a, ok2 := v.([]any)
	return a, ok && ok2
}

func (pt projTarget) project(t testing.TB, rt string, mode SummaryMode, elements []string, in string) (string, error) {
	t.Helper()
	p, err := pt.ctx.NewProjection(rt, mode, elements)
	if err != nil {
		t.Fatalf("%s: NewProjection(%s, %d, %v): %v", pt.name, rt, mode, elements, err)
	}
	out, err := pt.ctx.Project([]byte(in), p)
	return string(out), err
}

type projCase struct {
	name     string
	rt       string
	mode     SummaryMode
	elements []string
	in       string
	want     string // «TAG» es el tag SUBSETTED esperado
}

func runProjCases(t *testing.T, cases []projCase) {
	t.Helper()
	for _, pt := range projTargets(t) {
		tag := expectedTag(t, pt.ctx)
		for _, tc := range cases {
			t.Run(pt.name+"/"+tc.name, func(t *testing.T) {
				got, err := pt.project(t, tc.rt, tc.mode, tc.elements, strings.ReplaceAll(tc.in, "NARR", narr))
				if err != nil {
					t.Fatal(err)
				}
				want := strings.ReplaceAll(strings.ReplaceAll(tc.want, "TAG", tag), "NARR", narr)
				if got != want {
					t.Errorf("\n got %s\nwant %s", got, want)
				}
			})
		}
	}
}

const patientFull = `{"resourceType":"Patient","id":"p","extension":[{"url":"u","valueString":"v"}],"text":NARR,` +
	`"contained":[{"resourceType":"Patient","id":"o","active":true},{"resourceType":"Patient","id":"q","active":false}],` +
	`"name":[{"extension":[{"url":"u","valueString":"z"}],"family":"A","given":["x"]}],"gender":"male","_gender":{"extension":[{"url":"u","valueString":"v"}]},` +
	`"birthDate":"1990","managingOrganization":{"reference":"#o","display":"D"}}`

// search.html (R4 §3.1.1.5.8, R5 §3.2.1.7.5): los modos de _summary sobre un
// Patient. Plan 005 reglas 1 a 6 y 9.
func TestProjectSummaryModes(t *testing.T) {
	runProjCases(t, []projCase{
		// true: solo lo marcado isSummary en cada nivel (D1): extension, text y el
		// `_gender` caen; el contenido o, referido por managingOrganization, se queda
		// (D9, ref-1) y q, no referido, cae.
		{"true", "Patient", SummaryTrue, nil, patientFull,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]},"contained":[{"resourceType":"Patient","id":"o","active":true}],` +
				`"name":[{"family":"A","given":["x"]}],"gender":"male","birthDate":"1990","managingOrganization":{"reference":"#o","display":"D"}}`},
		// text: «Return only the "text" element, the 'id' element, the 'meta' element,
		// and only top-level mandatory elements». Patient no tiene obligatorios; nada
		// conservado referencia un contenido.
		{"text", "Patient", SummaryText, nil, patientFull,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]},"text":NARR}`},
		// data: «Remove the text element».
		{"data", "Patient", SummaryData, nil, patientFull,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]},"contained":[{"resourceType":"Patient","id":"o","active":true},{"resourceType":"Patient","id":"q","active":false}],` +
				`"extension":[{"url":"u","valueString":"v"}],"name":[{"extension":[{"url":"u","valueString":"z"}],"family":"A","given":["x"]}],"gender":"male",` +
				`"_gender":{"extension":[{"url":"u","valueString":"v"}]},"birthDate":"1990","managingOrganization":{"reference":"#o","display":"D"}}`},
		// false: todo, normalizado y sin tag (nada se quitó).
		{"false", "Patient", SummaryFalse, nil, patientFull,
			`{"resourceType":"Patient","id":"p","text":NARR,"contained":[{"resourceType":"Patient","id":"o","active":true},{"resourceType":"Patient","id":"q","active":false}],` +
				`"extension":[{"url":"u","valueString":"v"}],"name":[{"extension":[{"url":"u","valueString":"z"}],"family":"A","given":["x"]}],"gender":"male",` +
				`"_gender":{"extension":[{"url":"u","valueString":"v"}]},"birthDate":"1990","managingOrganization":{"reference":"#o","display":"D"}}`},
		// Quitar nada no es un subconjunto: sin tag (D6).
		{"true-nothing-lost", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","id":"p","gender":"male"}`, `{"resourceType":"Patient","id":"p","gender":"male"}`},
		{"data-no-text", "Patient", SummaryData, nil, `{"resourceType":"Patient","id":"p","gender":"male"}`, `{"resourceType":"Patient","id":"p","gender":"male"}`},
	})
}

// search.html, _elements (R4 §3.1.1.5.9, R5 §3.2.1.7.6): una lista de nombres
// de la raíz. Los elementos pedidos van enteros (con su `_x`); id y meta
// (decisión D4), los obligatorios y los modificadores con valor, siempre.
func TestProjectElements(t *testing.T) {
	runProjCases(t, []projCase{
		{"gender-with-underscore", "Patient", SummaryFalse, []string{"gender"}, patientFull,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]},"gender":"male","_gender":{"extension":[{"url":"u","valueString":"v"}]}}`},
		// D9: el contenido referido por un elemento conservado se conserva, entero.
		{"referenced-contained", "Patient", SummaryFalse, []string{"managingOrganization"}, patientFull,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]},"contained":[{"resourceType":"Patient","id":"o","active":true}],"managingOrganization":{"reference":"#o","display":"D"}}`},
		{"underscore-without-value", "Patient", SummaryFalse, []string{"birthDate"},
			`{"resourceType":"Patient","_birthDate":{"extension":[{"url":"u","valueString":"v"}]},"gender":"male"}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"_birthDate":{"extension":[{"url":"u","valueString":"v"}]}}`},
		// Observation.status y .code son obligatorios (min 1): vuelven aunque no se pidan.
		{"mandatory-added", "Observation", SummaryFalse, []string{"subject"},
			`{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"subject":{"reference":"Patient/1"},"valueString":"s"}`,
			`{"resourceType":"Observation","id":"o","meta":{"tag":[TAG]},"status":"final","code":{"text":"c"},"subject":{"reference":"Patient/1"}}`},
		// `value` nombra el elemento base: sale la variante presente.
		{"choice-base-name", "Observation", SummaryFalse, []string{"value"},
			`{"resourceType":"Observation","status":"final","code":{"text":"c"},"subject":{"reference":"Patient/1"},"valueString":"s"}`,
			`{"resourceType":"Observation","meta":{"tag":[TAG]},"status":"final","code":{"text":"c"},"valueString":"s"}`},
		// R5: los modificadores con valor vuelven (D4/D10): active y deceased[x].
		{"modifiers-added", "Patient", SummaryFalse, []string{"gender"},
			`{"resourceType":"Patient","active":true,"deceasedBoolean":false,"gender":"male","birthDate":"1990"}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"active":true,"gender":"male","deceasedBoolean":false}`},
		// Un Bundle con _elements=entry: los recursos de las entradas van enteros, sin
		// tag (no perdieron nada); la raíz perdió total y lleva el tag (regla 8 y 10).
		{"bundle-entry-whole", "Bundle", SummaryFalse, []string{"entry"}, bundleIn,
			`{"resourceType":"Bundle","meta":{"tag":[TAG]},"type":"searchset","entry":[{"fullUrl":"u1","resource":{"resourceType":"Patient","id":"a","text":NARR,"gender":"male"}},{"fullUrl":"u2","resource":{"resourceType":"Patient","id":"b","gender":"female"}}]}`},
	})
}

const bundleIn = `{"resourceType":"Bundle","type":"searchset","total":2,"entry":[` +
	`{"fullUrl":"u1","resource":{"resourceType":"Patient","id":"a","text":NARR,"gender":"male"}},` +
	`{"fullUrl":"u2","resource":{"resourceType":"Patient","id":"b","gender":"female"}}]}`

// Plan 005 reglas 8 y 10 (D2, D6, H5): con true y data los recursos anidados se
// proyectan con el mismo modo y cada uno que perdió algo lleva su tag; la raíz
// también cuando solo perdió uno de sus anidados.
func TestProjectNestedResources(t *testing.T) {
	bundleOut := `{"resourceType":"Bundle","meta":{"tag":[TAG]},"type":"searchset","total":2,"entry":[` +
		`{"fullUrl":"u1","resource":{"resourceType":"Patient","id":"a","meta":{"tag":[TAG]},"gender":"male"}},` +
		`{"fullUrl":"u2","resource":{"resourceType":"Patient","id":"b","gender":"female"}}]}`
	paramsIn := `{"resourceType":"Parameters","parameter":[{"name":"p","resource":{"resourceType":"Patient","id":"a","text":NARR,"gender":"male"}}]}`
	paramsOut := `{"resourceType":"Parameters","meta":{"tag":[TAG]},"parameter":[{"name":"p","resource":{"resourceType":"Patient","id":"a","meta":{"tag":[TAG]},"gender":"male"}}]}`
	runProjCases(t, []projCase{
		{"bundle-true", "Bundle", SummaryTrue, nil, bundleIn, bundleOut},
		{"bundle-data", "Bundle", SummaryData, nil, bundleIn, bundleOut},
		// text: type es obligatorio; total y entry caen y las entradas no se miran.
		{"bundle-text", "Bundle", SummaryText, nil, bundleIn, `{"resourceType":"Bundle","meta":{"tag":[TAG]},"type":"searchset"}`},
		{"parameters-true", "Parameters", SummaryTrue, nil, paramsIn, paramsOut},
		{"parameters-data", "Parameters", SummaryData, nil, paramsIn, paramsOut},
		// Un contenido anidado que perdió algo lleva su tag (regla 10).
		{"contained-text-data", "Patient", SummaryData, nil,
			`{"resourceType":"Patient","contained":[{"resourceType":"Patient","id":"c","text":NARR,"gender":"male"}],"gender":"male"}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"contained":[{"resourceType":"Patient","id":"c","meta":{"tag":[TAG]},"gender":"male"}],"gender":"male"}`},
	})
}

// composition.html: Composition.section.text es un Narrative que no es el text del
// recurso; _summary=data «Remove the text element» del recurso y deja el de la
// sección (plan 005 regla 5).
func TestProjectDataKeepsSectionText(t *testing.T) {
	in := `{"resourceType":"Composition","status":"final","title":"t","text":NARR,"section":[{"title":"s","text":NARR}]}`
	runProjCases(t, []projCase{
		{"data", "Composition", SummaryData, nil, in,
			`{"resourceType":"Composition","meta":{"tag":[TAG]},"status":"final","title":"t","section":[{"title":"s","text":NARR}]}`},
		// true: Composition.section no está en el resumen (R4, R4B y R5).
		{"true", "Composition", SummaryTrue, nil, in,
			`{"resourceType":"Composition","meta":{"tag":[TAG]},"status":"final","title":"t"}`},
	})
}

// Plan 005 regla 2 y H4: `_x` de los primitivos. Element.id y Element.extension
// no están en el resumen, así que con true el `_x` cae; en un arreglo, la
// posición cuyo valor es null y cuyo `_x` cae se quita de los dos arreglos.
func TestProjectPrimitiveUnderscore(t *testing.T) {
	in := `{"resourceType":"Patient","name":[{"given":["a",null],"_given":[null,{"id":"i"}],"family":"F","_family":{"extension":[{"url":"u","valueString":"v"}]}}],` +
		`"_birthDate":{"extension":[{"url":"u","valueString":"v"}]}}`
	runProjCases(t, []projCase{
		{"true", "Patient", SummaryTrue, nil, in, `{"resourceType":"Patient","meta":{"tag":[TAG]},"name":[{"family":"F","given":["a"]}]}`},
		// data no filtra: la salida es la normalizada, sin tag.
		{"data", "Patient", SummaryData, nil, in,
			`{"resourceType":"Patient","name":[{"family":"F","_family":{"extension":[{"url":"u","valueString":"v"}]},"given":["a",null],"_given":[null,{"id":"i"}]}],"_birthDate":{"extension":[{"url":"u","valueString":"v"}]}}`},
		{"text", "Patient", SummaryText, nil, in, `{"resourceType":"Patient","meta":{"tag":[TAG]}}`},
		// Un valor con `_x` parcial: la posición 1 conserva el valor y pierde el `_x`.
		{"value-kept-underscore-dropped", "Patient", SummaryTrue, nil,
			`{"resourceType":"Patient","name":[{"given":["a","b"],"_given":[{"id":"i"},null]}]}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"name":[{"given":["a","b"]}]}`},
	})
}

// Plan 005 regla 3 (H2): la salida nunca tiene {} ni [] (ele-1).
func TestProjectEmptyContainers(t *testing.T) {
	ext := `[{"url":"u","valueString":"z"}]`
	runProjCases(t, []projCase{
		{"element-and-whole-array", "Patient", SummaryTrue, nil,
			`{"resourceType":"Patient","name":[{"extension":` + ext + `},{"family":"A"}],"managingOrganization":{"extension":` + ext + `}}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"name":[{"family":"A"}]}`},
		{"only-empties", "Patient", SummaryTrue, nil,
			`{"resourceType":"Patient","id":"p","name":[{"extension":` + ext + `}]}`,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]}}`},
	})
}

// Plan 005 regla 10 (D6): dónde y cómo se pone el tag.
func TestProjectSubsettedTag(t *testing.T) {
	sys := `"system":"` + systemSubsetted + `"`
	runProjCases(t, []projCase{
		// Sin meta ni id: meta va primero, tras resourceType.
		{"no-meta", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","gender":"male","text":NARR}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"gender":"male"}`},
		// meta antes de implicitRules, que sigue a id y meta en Resource.
		{"before-implicit-rules", "Patient", SummaryData, nil, `{"resourceType":"Patient","implicitRules":"http://x","text":NARR}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"implicitRules":"http://x"}`},
		// Al final de meta.tag, tras los que ya hay.
		{"after-other-tags", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","meta":{"tag":[{"system":"s","code":"c"}]},"text":NARR}`,
			`{"resourceType":"Patient","meta":{"tag":[{"system":"s","code":"c"},TAG]}}`},
		// Ya presente: no se duplica.
		{"already-present", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","meta":{"tag":[{` + sys + `,"code":"SUBSETTED"}]},"text":NARR}`,
			`{"resourceType":"Patient","meta":{"tag":[{` + sys + `,"code":"SUBSETTED"}]}}`},
		// Meta con otros elementos y sin tag: tag al final, en su lugar del orden.
		{"meta-without-tag", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","meta":{"versionId":"1","lastUpdated":"2020-01-01T00:00:00Z","profile":["http://p"]},"text":NARR}`,
			`{"resourceType":"Patient","meta":{"versionId":"1","lastUpdated":"2020-01-01T00:00:00Z","profile":["http://p"],"tag":[TAG]}}`},
		// meta.extension no está en el resumen: cae, y el recurso perdió algo.
		{"meta-extension-dropped", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","meta":{"extension":[{"url":"u","valueString":"v"}],"versionId":"1"}}`,
			`{"resourceType":"Patient","meta":{"versionId":"1","tag":[TAG]}}`},
	})
}

// Plan 005 reglas 1 y 4 (D10, H3): un modificador con valor se conserva en todos
// los modos, también donde isSummary es falso (DomainResource.modifierExtension
// en R4 y R4B); implicitRules con text (resource.html: es un modificador).
func TestProjectModifiers(t *testing.T) {
	ext := `[{"url":"u","valueString":"v"}]`
	runProjCases(t, []projCase{
		{"root-modifier-extension-true", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","modifierExtension":` + ext + `,"gender":"male"}`,
			`{"resourceType":"Patient","modifierExtension":` + ext + `,"gender":"male"}`},
		{"root-modifier-extension-text", "Patient", SummaryText, nil, `{"resourceType":"Patient","modifierExtension":` + ext + `,"gender":"male"}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"modifierExtension":` + ext + `}`},
		{"implicit-rules-text", "Patient", SummaryText, nil, `{"resourceType":"Patient","implicitRules":"http://x","language":"es","text":NARR,"gender":"male"}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"implicitRules":"http://x","text":NARR}`},
		{"implicit-rules-true", "Patient", SummaryTrue, nil, `{"resourceType":"Patient","implicitRules":"http://x","language":"es","text":NARR,"gender":"male"}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"implicitRules":"http://x","gender":"male"}`},
		{"active-and-deceased-text", "Patient", SummaryText, nil, `{"resourceType":"Patient","active":true,"deceasedBoolean":false,"multipleBirthInteger":2}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"active":true,"deceasedBoolean":false}`},
		// Un modifierExtension de un backbone (Bundle.entry): del resumen, entero (D1).
		{"backbone-modifier-extension-true", "Bundle", SummaryTrue, nil,
			`{"resourceType":"Bundle","type":"collection","entry":[{"modifierExtension":[{"url":"u","extension":[{"url":"i","valueString":"n"}]}],"extension":[{"url":"e","valueString":"x"}],"fullUrl":"f"}]}`,
			`{"resourceType":"Bundle","meta":{"tag":[TAG]},"type":"collection","entry":[{"modifierExtension":[{"extension":[{"url":"i","valueString":"n"}],"url":"u"}],"fullUrl":"f"}]}`},
	})
}

// Plan 005 regla 1 y D8: un obligatorio de la raíz sin isSummary se suma con true
// (Observation.status en la fixture; en los núcleos, ver TestRealMandatoryWithoutSummary).
func TestProjectMandatoryAdded(t *testing.T) {
	in := `{"resourceType":"Observation","status":"final","code":{"text":"c"},"subject":{"reference":"Patient/1"},"valueString":"s","extension":[{"url":"u","valueString":"v"}]}`
	runProjCases(t, []projCase{
		{"true", "Observation", SummaryTrue, nil, in,
			`{"resourceType":"Observation","meta":{"tag":[TAG]},"status":"final","code":{"text":"c"},"subject":{"reference":"Patient/1"},"valueString":"s"}`},
		{"text", "Observation", SummaryText, nil, in,
			`{"resourceType":"Observation","meta":{"tag":[TAG]},"status":"final","code":{"text":"c"}}`},
	})
}

// Plan 005 regla 9 (D9, H1): un contenido al que apunta una referencia local
// conservada se conserva, también por otro contenido conservado; el que nadie
// referencia cae.
func TestProjectContainedReferenced(t *testing.T) {
	in := `{"resourceType":"Patient","id":"p","contained":[` +
		`{"resourceType":"Patient","id":"a","managingOrganization":{"reference":"#b"}},` +
		`{"resourceType":"Patient","id":"b","gender":"male"},` +
		`{"resourceType":"Patient","id":"c","gender":"female"}],"managingOrganization":{"reference":"#a"}}`
	runProjCases(t, []projCase{
		// a lo referencia la raíz; b lo referencia a; c, nadie.
		{"transitive", "Patient", SummaryTrue, nil, in,
			`{"resourceType":"Patient","id":"p","meta":{"tag":[TAG]},"contained":[{"resourceType":"Patient","id":"a","managingOrganization":{"reference":"#b"}},{"resourceType":"Patient","id":"b","gender":"male"}],"managingOrganization":{"reference":"#a"}}`},
		// Una referencia no local no conserva nada; «#» solo es el propio recurso.
		{"not-local", "Patient", SummaryTrue, nil,
			`{"resourceType":"Patient","contained":[{"resourceType":"Patient","id":"a"}],"managingOrganization":{"reference":"Patient/a"}}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]},"managingOrganization":{"reference":"Patient/a"}}`},
		// Con data nada se quita del contenido.
		{"data-keeps-all", "Patient", SummaryData, nil,
			`{"resourceType":"Patient","contained":[{"resourceType":"Patient","id":"a"}]}`,
			`{"resourceType":"Patient","contained":[{"resourceType":"Patient","id":"a"}]}`},
		// Una referencia de un elemento que se quita no conserva el contenido.
		{"reference-of-dropped-element", "Patient", SummaryTrue, nil,
			`{"resourceType":"Patient","contained":[{"resourceType":"Patient","id":"a"}],"extension":[{"url":"u","valueReference":{"reference":"#a"}}]}`,
			`{"resourceType":"Patient","meta":{"tag":[TAG]}}`},
	})
}

// Plan 005 regla 2: `_x` junto a `x` en el orden del modelo.
func TestProjectOrderIsNormalized(t *testing.T) {
	runProjCases(t, []projCase{
		{"reordered", "Patient", SummaryTrue, nil, `{"gender":"male","name":[{"given":["g"],"family":"f"}],"resourceType":"Patient","id":"x"}`,
			`{"resourceType":"Patient","id":"x","name":[{"family":"f","given":["g"]}],"gender":"male"}`},
	})
}

func TestProjectDropsEveryElementKind(t *testing.T) {
	// Un Patient con un recurso anidado en un elemento que se quita: el anidado
	// se valida (regla 11) pero no sale ni marca nada.
	runProjCases(t, []projCase{
		{"dropped-nested-resource", "Bundle", SummaryText, nil,
			`{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Patient","text":NARR}}]}`,
			`{"resourceType":"Bundle","meta":{"tag":[TAG]},"type":"collection"}`},
	})
}

// NewProjection: los errores de §3 reglas 6 y 7 (D3, D5).
func TestNewProjectionErrors(t *testing.T) {
	for _, pt := range projTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			for _, tc := range []struct {
				name     string
				rt       string
				mode     SummaryMode
				elements []string
				kind     ErrorKind
			}{
				{"unknown-element", "Observation", SummaryFalse, []string{"nope"}, KindUnknownElement},
				{"base-with-suffix", "Observation", SummaryFalse, []string{"value[x]"}, KindChoiceElement},
				{"variant", "Observation", SummaryFalse, []string{"valueQuantity"}, KindChoiceElement},
				{"path", "Patient", SummaryFalse, []string{"name.family"}, KindUnknownElement},
				{"resource-type-key", "Patient", SummaryFalse, []string{"resourceType"}, KindUnknownElement},
				{"empty-name", "Patient", SummaryFalse, []string{""}, KindUnknownElement},
				{"summary-and-elements", "Patient", SummaryTrue, []string{"gender"}, KindConflictingModes},
				{"text-and-elements", "Patient", SummaryText, []string{"gender"}, KindConflictingModes},
				{"abstract-type", "DomainResource", SummaryFalse, nil, KindUnknownResourceType},
				{"not-a-type", "Nope", SummaryFalse, nil, KindUnknownResourceType},
				{"bad-mode", "Patient", SummaryMode(9), nil, KindProjectionMismatch},
			} {
				_, err := pt.ctx.NewProjection(tc.rt, tc.mode, tc.elements)
				var e *Error
				if !errors.As(err, &e) || e.Kind != tc.kind {
					t.Errorf("%s: %v, want kind %v", tc.name, err, tc.kind)
				}
			}
			// summary=false con _elements vale como _elements; los nombres base se aceptan.
			if _, err := pt.ctx.NewProjection("Observation", SummaryFalse, []string{"value", "id", "meta", "subject"}); err != nil {
				t.Error(err)
			}
			// La lista se copia: cambiar la del llamador no cambia la proyección.
			els := []string{"gender"}
			p, _ := pt.ctx.NewProjection("Patient", SummaryFalse, els)
			els[0] = "birthDate"
			out, err := pt.ctx.Project([]byte(`{"resourceType":"Patient","gender":"male","birthDate":"1990"}`), p)
			if err != nil || strings.Contains(string(out), "birthDate") {
				t.Errorf("the projection followed the caller's slice: %s %v", out, err)
			}
		})
	}
}

// Plan 005 §2 y H8: la Projection es de un Context y de un tipo.
func TestProjectRejectsForeignProjection(t *testing.T) {
	a, b := projContext(t, modelVersions[0], true), projContext(t, modelVersions[0], true)
	p, err := a.NewProjection("Patient", SummaryTrue, nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() error{
		"other-context": func() error { _, err := b.Project([]byte(`{"resourceType":"Patient"}`), p); return err },
		"other-type":    func() error { _, err := a.Project([]byte(`{"resourceType":"Observation"}`), p); return err },
		"nil":           func() error { _, err := a.Project([]byte(`{"resourceType":"Patient"}`), nil); return err },
		"bad-limit": func() error {
			_, err := a.Project([]byte(`{"resourceType":"Patient"}`), p, WithMaxBytes(0))
			return err
		},
	} {
		err := run()
		var e *Error
		if name == "bad-limit" {
			if err == nil {
				t.Errorf("%s: no error", name)
			}
			continue
		}
		if !errors.As(err, &e) || e.Kind != KindProjectionMismatch {
			t.Errorf("%s: %v, want KindProjectionMismatch", name, err)
		}
	}
	// El tipo de un recurso anidado no se compara: la proyección es del tipo de la raíz.
	pb, _ := a.NewProjection("Bundle", SummaryTrue, nil)
	if _, err := a.Project([]byte(`{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Patient"}}]}`), pb); err != nil {
		t.Error(err)
	}
}

// Plan 005 regla 11: Project rechaza lo que rechaza Normalize, con la misma ruta,
// también en lo que la proyección quita.
func TestProjectRejectsWhatNormalizeRejects(t *testing.T) {
	bad := []string{
		`{"resourceType":"Patient","extension":[{"url":"u","bogus":1}]}`,                                                          // en un elemento que true quita
		`{"resourceType":"Patient","text":{"status":"generated","div":"<div/>","nope":1}}`,                                        // en el text que data quita
		`{"resourceType":"Patient","contained":[{"resourceType":"Patient","gender":null}]}`,                                       // en un contenido no referido
		`{"resourceType":"Patient","name":[{"given":["a"],"_given":[]}]}`,                                                         // arreglo vacío
		`{"resourceType":"Patient","name":[{"given":["a",null],"_given":[null]}]}`,                                                // largo distinto
		`{"resourceType":"Patient","gender":"","name":[{}]}`,                                                                      // string y objeto vacíos
		`{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Patient","bogus":1}}]}`,               // en un recurso anidado
		`{"resourceType":"Patient","managingOrganization":{"reference":"#o"},"contained":[{"resourceType":"Patient","bogus":1}]}`, // en un contenido conservado
		`{"resourceType":"Patient"`,
	}
	for _, pt := range projTargets(t) {
		for i, in := range bad {
			rt := "Patient"
			if strings.Contains(in, `"Bundle"`) {
				rt = "Bundle"
			}
			_, nerr := pt.ctx.Normalize([]byte(in))
			if nerr == nil {
				t.Fatalf("%s #%d: Normalize accepts %s", pt.name, i, in)
			}
			for _, mode := range []SummaryMode{SummaryFalse, SummaryTrue, SummaryText, SummaryData} {
				_, perr := pt.project(t, rt, mode, nil, in)
				if perr == nil || perr.Error() != nerr.Error() {
					t.Errorf("%s #%d mode %d:\n Project   %v\n Normalize %v", pt.name, i, mode, perr, nerr)
				}
			}
			els := []string{"gender"}
			if rt == "Bundle" {
				els = []string{"entry"}
			}
			if _, perr := pt.project(t, rt, SummaryFalse, els, in); perr == nil || perr.Error() != nerr.Error() {
				t.Errorf("%s #%d _elements:\n Project   %v\n Normalize %v", pt.name, i, perr, nerr)
			}
		}
	}
}

// D6: un lock sin el CodeSystem del tag lo pone sin display; con el CodeSystem
// sin el concepto, NewProjection falla (nunca un tag sin verificar).
func TestProjectTagWithoutCodeSystem(t *testing.T) {
	for _, v := range modelVersions {
		t.Run(v.name, func(t *testing.T) {
			c := projContext(t, v, false)
			p, err := c.NewProjection("Patient", SummaryTrue, nil)
			if err != nil {
				t.Fatal(err)
			}
			out, err := c.Project([]byte(`{"resourceType":"Patient","text":`+narr+`}`), p)
			want := `{"resourceType":"Patient","meta":{"tag":[{"system":"` + systemSubsetted + `","code":"SUBSETTED"}]}}`
			if err != nil || string(out) != want {
				t.Errorf("got %s %v, want %s", out, err, want)
			}
			bad, err := tryCtx(t, mcore(v, append(projFixture(v), j("CodeSystem", systemSubsetted, "status", "active", "content", "complete",
				"concept", []any{map[string]any{"code": "OTHER", "display": "x"}}))...))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := bad.NewProjection("Patient", SummaryTrue, nil); err == nil || !strings.Contains(err.Error(), "no concept") {
				t.Errorf("NewProjection with a code system without the concept: %v", err)
			}
		})
	}
}

// El display con un carácter que encoding/json escaparía como HTML sale tal cual,
// como lo escribe Normalize: Normalize(Project(x)) == Project(x).
func TestProjectTagDisplayIsNormalized(t *testing.T) {
	v := modelVersions[0]
	cs := j("CodeSystem", systemSubsetted, "status", "active", "content", "complete",
		"concept", []any{map[string]any{"code": codeSubsetted, "display": "a<b> & \"c\"\u2028"}})
	c, err := tryCtx(t, mcore(v, append(projFixture(v), cs)...))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.NewProjection("Patient", SummaryTrue, nil)
	out, err := c.Project([]byte(`{"resourceType":"Patient","text":`+narr+`}`), p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := c.Normalize(out)
	if err != nil || string(again) != string(out) {
		t.Errorf("Normalize(Project(x)) differs:\n %s\n %s (%v)", out, again, err)
	}
}

// D8, ampliada el 04-11-2026 (aprobada por el usuario): un primitivo con min>0
// está presente por su valor o por su `_x` (json.html, «Representation of
// primitive elements»: un primitivo sin valor puede tener solo extensiones). El
// elemento es el par `x` y `_x`: si se conserva por min>0, se conserva entero, en
// todo modo que suma obligatorios y en todo nivel conservado. Los `_x` de los
// primitivos que no son obligatorios se siguen filtrando (TestProjectPrimitiveUnderscore).
func TestProjectMandatoryPrimitiveKeepsUnderscore(t *testing.T) {
	ext := `{"extension":[{"url":"u","valueString":"v"}]}`
	both := `{"resourceType":"Composition","status":"final","_status":{"id":"i"},"title":"t","_title":` + ext + `}`
	only := `{"resourceType":"Composition","status":"final","_title":` + ext + `}`
	runProjCases(t, []projCase{
		// valor y `_x` a la vez: se conservan los dos, y no se perdió nada.
		{"true-value-and-underscore", "Composition", SummaryTrue, nil, both, both},
		// solo el `_x`: el obligatorio sigue presente.
		{"true-only-underscore", "Composition", SummaryTrue, nil, only, only},
		{"text-only-underscore", "Composition", SummaryText, nil, only, only},
		{"elements-only-underscore", "Composition", SummaryFalse, []string{"status"}, only, only},
	})
}

// json.html y search.html: sin _summary ni _elements la proyección es idéntica a
// Normalize, también con un `_x` que solo trae null (que el filtro de los
// primitivos no debe tocar cuando no hay nada que filtrar).
func TestProjectFalseEqualsNormalizeWithNullUnderscore(t *testing.T) {
	for _, pt := range projTargets(t) {
		for _, in := range []string{
			`{"resourceType":"Patient","name":[{"given":["A"],"_given":[null]}]}`,
			`{"resourceType":"Patient","name":[{"given":["A",null],"_given":[null,{"id":"i"}]}]}`,
		} {
			want, err := pt.ctx.Normalize([]byte(in))
			if err != nil {
				t.Fatal(err)
			}
			got, err := pt.project(t, "Patient", SummaryFalse, nil, in)
			if err != nil || got != string(want) {
				t.Errorf("%s: got %s (%v), want %s", pt.name, got, err, want)
			}
		}
	}
}

// Plan 005 §13.6 regla 4 (veredicto D-1, 04-11-2026): en un nivel que filtra, el
// `_x` de un primitivo se filtra como Element y desaparece si queda solo de null;
// sin contenido que quitar, tampoco hay tag SUBSETTED. En un nivel que no filtra
// (false sin _elements, data) el `_x` no se toca, tampoco uno solo de null.
func TestProjectNullOnlyUnderscore(t *testing.T) {
	in := `{"resourceType":"Patient","name":[{"given":["A"],"_given":[null]}]}`
	for _, pt := range projTargets(t) {
		got, err := pt.project(t, "Patient", SummaryTrue, nil, in)
		if want := `{"resourceType":"Patient","name":[{"given":["A"]}]}`; err != nil || got != want {
			t.Errorf("%s true: got %s (%v), want %s", pt.name, got, err, want)
		}
		for _, mode := range []SummaryMode{SummaryFalse, SummaryData} {
			got, err := pt.project(t, "Patient", mode, nil, in)
			if err != nil || got != in {
				t.Errorf("%s mode %v: got %s (%v), want the input", pt.name, mode, got, err)
			}
		}
	}
}

// json.html R5 §2.1.6.4.2 (el sufijo de nulls del arreglo más corto se infiere) y
// §13.6 regla 4: el relleno se hace antes de filtrar. Con `true`, un `_x` que se
// rellena y queda solo de null desaparece; con `false` sin _elements sale
// alineado como Normalize.
func TestProjectPaddedUnderscoreArrays(t *testing.T) {
	cases := []struct{ name, in, mode, want string }{
		{"true-null-only-after-padding", `{"resourceType":"Patient","name":[{"given":["a","b"],"_given":[null]}]}`, "true",
			`{"resourceType":"Patient","name":[{"given":["a","b"]}]}`},
		{"false-keeps-the-padded-pair", `{"resourceType":"Patient","name":[{"given":["a","b"],"_given":[null]}]}`, "false",
			`{"resourceType":"Patient","name":[{"given":["a","b"],"_given":[null,null]}]}`},
		{"false-shorter-value-array", `{"resourceType":"Patient","name":[{"given":["a"],"_given":[null,{"id":"x"}]}]}`, "false",
			`{"resourceType":"Patient","name":[{"given":["a",null],"_given":[null,{"id":"x"}]}]}`},
	}
	for _, pt := range projTargets(t) {
		for _, c := range cases {
			mode := map[string]SummaryMode{"true": SummaryTrue, "false": SummaryFalse}[c.mode]
			got, err := pt.project(t, "Patient", mode, nil, c.in)
			if err != nil || got != c.want {
				t.Errorf("%s %s: got %s (%v), want %s", pt.name, c.name, got, err, c.want)
			}
		}
		// Un null más allá del arreglo más corto sigue siendo un error, en la ruta del valor.
		_, err := pt.project(t, "Patient", SummaryTrue, nil, `{"resourceType":"Patient","name":[{"given":["a",null],"_given":[{"id":"x"}]}]}`)
		if err == nil || !strings.Contains(err.Error(), "given[1]") {
			t.Errorf("%s: a null beyond the shorter array: %v", pt.name, err)
		}
	}
}

// Plan 005 regla 2: el `_x` de un primitivo se filtra con la regla de cualquier
// Element. En los núcleos oficiales Element.id y Element.extension no son
// isSummary, así que nada sobrevive; con una fixture donde Element.extension sí lo
// es, el `_x` conservado sale alineado con su arreglo y sin perder valores.
func TestProjectUnderscoreSurvivesWhenElementExtensionIsSummary(t *testing.T) {
	projExtraSummary = map[string]bool{"Element.extension": true, "string.extension": true, "date.extension": true}
	defer func() { projExtraSummary = nil }()
	ext := `{"extension":[{"url":"u","valueString":"v"}]}`
	for _, v := range modelVersions {
		c := projContext(t, v, true)
		pt := projTarget{"fixture-ext-" + v.name, v.name, false, c}
		in := `{"resourceType":"Patient","name":[{"given":["a","b","c"],"_given":[null,` + ext + `,null]}],"birthDate":"1990","_birthDate":` + ext + `}`
		got, err := pt.project(t, "Patient", SummaryTrue, nil, in)
		want := `{"resourceType":"Patient","name":[{"given":["a","b","c"],"_given":[null,` + ext + `,null]}],"birthDate":"1990","_birthDate":` + ext + `}`
		if err != nil || got != want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", pt.name, got, err, want)
		}
	}
}
