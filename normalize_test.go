package fhirpkg

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Casos a mano del normalizador (plan 004 §4). Cada uno cita la especificación
// (json.html, el mismo texto en R4, R4B y R5) y su valor esperado está escrito
// desde ella, no corriendo el código. Corren sobre la fixture sintética de las
// tres versiones (make check) y sobre los núcleos oficiales (make
// check-packages), con el mismo orden de elementos.

type normCase struct {
	name, cite string
	in         string
	want       string   // salida esperada si no hay errores
	errs       []string // errores esperados, «tipo@ruta», en el orden de la salida
	vers       []string // versiones en que aplica (todas si está vacío)
	synthetic  bool     // solo la fixture: usa un elemento que los núcleos oficiales no tienen
}

const obsPre = `{"resourceType":"Observation","status":"final","code":{"text":"t"},`

var normCases = []normCase{
	// Regla 2: orden del modelo.
	{name: "properties-in-any-order", cite: "json.html §2.6.2: «Properties can appear in any order»; la salida sigue el orden del snapshot",
		in:   `{"gender":"male","name":[{"given":["Ann","Bo"],"family":"Fox","use":"official"}],"active":true,"id":"p1","resourceType":"Patient"}`,
		want: `{"resourceType":"Patient","id":"p1","active":true,"name":[{"use":"official","family":"Fox","given":["Ann","Bo"]}],"gender":"male"}`},
	{name: "array-order-is-kept", cite: "json.html §2.6.2: «order within an array SHALL be maintained»",
		in:   `{"resourceType":"Patient","name":[{"family":"B"},{"family":"A"},{"family":"C"}]}`,
		want: `{"resourceType":"Patient","name":[{"family":"B"},{"family":"A"},{"family":"C"}]}`},
	{name: "whitespace-is-not-content", cite: "json.html: «JSON whitespace is not part of the contents of a resource»",
		in:   "{ \"resourceType\" : \"Patient\",\n\t\"name\" : [ { \"family\" : \"F\" } ] ,\r\n \"active\" : true }",
		want: `{"resourceType":"Patient","active":true,"name":[{"family":"F"}]}`},
	{name: "choice-variant-takes-the-place-of-the-choice", cite: "elementdefinition.html «Choice types»: value[x] se escribe como valueTipo; models lo deja en el lugar del [x]",
		in:   `{"resourceType":"Patient","multipleBirthInteger":2,"managingOrganization":{"display":"Org"},"deceasedBoolean":false,"birthDate":"2000-01-01","gender":"male"}`,
		want: `{"resourceType":"Patient","gender":"male","birthDate":"2000-01-01","deceasedBoolean":false,"multipleBirthInteger":2,"managingOrganization":{"display":"Org"}}`},
	{name: "underscore-form-follows-its-value", cite: "json.html §2.6.2.3: la propiedad _x lleva el id y las extensiones del primitivo x",
		in:   `{"resourceType":"Patient","_birthDate":{"id":"b"},"gender":"male","birthDate":"2000"}`,
		want: `{"resourceType":"Patient","gender":"male","birthDate":"2000","_birthDate":{"id":"b"}}`},
	{name: "underscore-form-without-value", cite: "json.html §2.6.2.3: sin valor, solo _x",
		in:   `{"resourceType":"Patient","_birthDate":{"extension":[{"valueString":"x","url":"http://e/x"}]}}`,
		want: `{"resourceType":"Patient","_birthDate":{"extension":[{"url":"http://e/x","valueString":"x"}]}}`},
	{name: "repeating-primitive-arrays-aligned-with-null", cite: "json.html §2.6.2.3: los arreglos x y _x se alinean con null",
		in:   `{"resourceType":"Patient","name":[{"_given":[null,{"id":"g2"}],"given":["Peter","James"]}]}`,
		want: `{"resourceType":"Patient","name":[{"given":["Peter","James"],"_given":[null,{"id":"g2"}]}]}`},
	{name: "null-in-the-value-array", cite: "json.html §2.6.2.3: null en el arreglo de valores cuando solo hay extensión",
		in:   `{"resourceType":"Patient","name":[{"_given":[null,{"id":"x"}],"given":["Peter",null]}]}`,
		want: `{"resourceType":"Patient","name":[{"given":["Peter",null],"_given":[null,{"id":"x"}]}]}`},
	{name: "underscore-array-alone", cite: "json.html §2.6.2.3: sin valores, solo el arreglo _x",
		in:   `{"resourceType":"Patient","name":[{"_given":[{"id":"x"},{"id":"y"}]}]}`,
		want: `{"resourceType":"Patient","name":[{"_given":[{"id":"x"},{"id":"y"}]}]}`},
	{name: "primitive-extension-children-in-order", cite: "json.html §2.6.2.3 y element.html: id antes que extension",
		in:   `{"resourceType":"Patient","_birthDate":{"extension":[{"url":"u","valueString":"v"}],"id":"b"}}`,
		want: `{"resourceType":"Patient","_birthDate":{"id":"b","extension":[{"url":"u","valueString":"v"}]}}`},
	{name: "contained-resource-has-its-own-order", cite: "json.html §2.6.2: cada recurso se discrimina por su resourceType",
		in:   `{"resourceType":"Patient","contained":[{"gender":"male","resourceType":"Patient"}],"id":"a"}`,
		want: `{"resourceType":"Patient","id":"a","contained":[{"resourceType":"Patient","gender":"male"}]}`},
	{name: "bundle-entry-resource", cite: "bundle.html: Bundle.entry.resource es un Resource; json.html §2.6.2",
		in:   `{"entry":[{"resource":{"valueInteger":3,"status":"final","resourceType":"Observation","code":{"text":"t"}},"fullUrl":"urn:x"}],"type":"collection","resourceType":"Bundle"}`,
		want: `{"resourceType":"Bundle","type":"collection","entry":[{"fullUrl":"urn:x","resource":{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueInteger":3}}]}`},
	{name: "bundle-response-outcome", cite: "bundle.html: Bundle.entry.response.outcome es un Resource",
		in:   `{"resourceType":"Bundle","type":"transaction-response","entry":[{"response":{"outcome":{"id":"o","resourceType":"Patient"},"status":"200"}}]}`,
		want: `{"resourceType":"Bundle","type":"transaction-response","entry":[{"response":{"status":"200","outcome":{"resourceType":"Patient","id":"o"}}}]}`},
	{name: "r5-bundle-issues-is-a-resource", cite: "R5 bundle.html: Bundle.issues", vers: []string{"R5"},
		in:   `{"resourceType":"Bundle","issues":{"id":"i","resourceType":"Patient"},"type":"collection"}`,
		want: `{"resourceType":"Bundle","type":"collection","issues":{"resourceType":"Patient","id":"i"}}`},
	{name: "slot-typed-by-an-abstract-subtype-rejects-other-resources", synthetic: true, cite: "json.html §2.6.2: el resourceType debe ser un tipo permitido por el elemento (fixture: DomainResource)",
		in:   `{"resourceType":"Bundle","type":"collection","domainOnly":{"resourceType":"Parameters"}}`,
		errs: []string{"unknown-resource-type@Bundle.domainOnly.resourceType"}},
	{name: "slot-typed-by-an-abstract-subtype-accepts-its-descendants", synthetic: true, cite: "json.html §2.6.2",
		in:   `{"resourceType":"Bundle","type":"collection","domainOnly":{"id":"i","resourceType":"Patient"}}`,
		want: `{"resourceType":"Bundle","type":"collection","domainOnly":{"resourceType":"Patient","id":"i"}}`},
	{name: "r4-bundle-has-no-issues", cite: "R4 y R4B bundle.html: Bundle no tiene issues", vers: []string{"R4", "R4B"},
		in:   `{"resourceType":"Bundle","issues":{"resourceType":"Patient"},"type":"collection"}`,
		errs: []string{"unknown-property@Bundle.issues"}},
	{name: "parameters-resource-and-content-reference", cite: "parameters.html: parameter.part repite parameter (contentReference); parameter.resource es un Resource",
		in:   `{"resourceType":"Parameters","parameter":[{"part":[{"valueString":"v","name":"inner"}],"resource":{"gender":"male","resourceType":"Patient"},"valueBoolean":true,"name":"outer"}]}`,
		want: `{"resourceType":"Parameters","parameter":[{"name":"outer","valueBoolean":true,"resource":{"resourceType":"Patient","gender":"male"},"part":[{"name":"inner","valueString":"v"}]}]}`},
	{name: "extension-children-in-order", cite: "extension.html: id, extension, url y value[x], en el orden del snapshot",
		in:   `{"resourceType":"Patient","extension":[{"valueString":"x","url":"http://e/u","extension":[{"url":"http://e/n","valueInteger":2}]}]}`,
		want: `{"resourceType":"Patient","extension":[{"extension":[{"url":"http://e/n","valueInteger":2}],"url":"http://e/u","valueString":"x"}]}`},
	{name: "xhtml-div-is-a-string", cite: "narrative.html: Narrative.div es xhtml, un string en JSON",
		in:   `{"resourceType":"Patient","text":{"div":"<div xmlns=\"http://www.w3.org/1999/xhtml\">hi &amp; bye</div>","status":"generated"}}`,
		want: `{"resourceType":"Patient","text":{"status":"generated","div":"<div xmlns=\"http://www.w3.org/1999/xhtml\">hi &amp; bye</div>"}}`},
	{name: "xhtml-div-underscore-id", cite: "xhtml (datatypes.html): su SD admite id; models no tiene _div",
		in:   `{"resourceType":"Patient","text":{"_div":{"id":"d"},"status":"generated","div":"<div/>"}}`,
		want: `{"resourceType":"Patient","text":{"status":"generated","div":"<div/>","_div":{"id":"d"}}}`},
	{name: "xhtml-has-no-extension", cite: "xhtml (datatypes.html): extension tiene max 0 en su SD",
		in:   `{"resourceType":"Patient","text":{"status":"generated","div":"<div/>","_div":{"extension":[{"url":"u","valueString":"v"}]}}}`,
		errs: []string{"unknown-property@Patient.text.div.extension"}},
	{name: "values-are-not-type-checked", cite: "plan 004 §2: la validación de tipos de valores es del validador",
		in:   `{"resourceType":"Patient","birthDate":5,"active":"yes"}`,
		want: `{"resourceType":"Patient","active":"yes","birthDate":5}`},
	{name: "e1-no-empty-top-level-object-needed", cite: "json.html §2.6.2: un recurso solo con su resourceType es un objeto válido",
		in: `{"resourceType":"Patient"}`, want: `{"resourceType":"Patient"}`},

	// Regla 5: primitivos.
	{name: "string-escapes-like-encoding-json", cite: "json.html: los strings son JSON; D2: el escape de models.Marshal (sin escapar HTML)",
		in:   obsPre + `"valueString":"a\/b \u00e9 \u2028\u2029 \ud83d\ude00 <&> \u007f \u0001 \n\t\b\f \"q\" \\ \u00e9"}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueString":"a/b é \u2028\u2029 😀 <&> ` + "\x7f" + ` \u0001 \n\t\b\f \"q\" \\ é"}`},
	{name: "raw-line-and-paragraph-separators-are-escaped", cite: "D2: models.Marshal (encoding/json) escribe U+2028 y U+2029 como \\u2028 y \\u2029 aunque lleguen crudos",
		in:   obsPre + "\"valueString\":\"a\u2028b\u2029c\"}",
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueString":"a\u2028b\u2029c"}`},
	{name: "string-without-escapes-is-copied", cite: "D2: mismo contenido, mismos bytes",
		in:   obsPre + `"valueString":"plain é 日本語 😀"}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueString":"plain é 日本語 😀"}`},
	{name: "number-keeps-its-text-1", cite: "json.html §2.6.2.1: los decimales conservan su precisión (1.50E+2 no es 150)",
		in:   obsPre + `"valueQuantity":{"value":1.50E+2}}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":1.50E+2}}`},
	{name: "number-keeps-its-text-2", cite: "json.html §2.6.2.1: -0.0",
		in:   obsPre + `"valueQuantity":{"value":-0.0}}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":-0.0}}`},
	{name: "number-keeps-its-text-3", cite: "json.html §2.6.2.1: más de 17 dígitos significativos",
		in:   obsPre + `"valueQuantity":{"value":12345678901234567890.123456789}}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":12345678901234567890.123456789}}`},
	{name: "number-keeps-its-text-4", cite: "json.html §2.6.2.1: exponentes",
		in:   obsPre + `"valueQuantity":{"value":1.00000000000000000E-24}}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":1.00000000000000000E-24}}`},
	{name: "number-keeps-its-text-5", cite: "json.html §2.6.2.1: -1E+245",
		in:   obsPre + `"valueQuantity":{"value":-1E+245}}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":-1E+245}}`},
	{name: "number-out-of-float-range-is-copied", cite: "json.html §2.6.2.1: el rango de un decimal no es el de float64",
		in:   obsPre + `"valueQuantity":{"value":1E400}}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":1E400}}`},
	{name: "integer-with-19-digits", cite: "json.html: los enteros conservan su texto",
		in:   `{"resourceType":"Patient","multipleBirthInteger":1234567890123456789}`,
		want: `{"resourceType":"Patient","multipleBirthInteger":1234567890123456789}`},

	// Regla 6: lo que se rechaza.
	{name: "unknown-property", cite: "plan 004 regla 6: models borra lo desconocido; aquí es un error con su ruta",
		in:   `{"resourceType":"Patient","name":[{"family":"F","givenx":["a"]}]}`,
		errs: []string{"unknown-property@Patient.name[0].givenx"}},
	{name: "fhir-comments-are-unknown", cite: "json.html §2.6.2: «comments SHALL not be in JSON instances»",
		in:   `{"resourceType":"Patient","fhir_comments":["c"]}`,
		errs: []string{"unknown-property@Patient.fhir_comments"}},
	{name: "resource-type-missing-at-the-root", cite: "json.html §2.6.2: «resourceType» discrimina el recurso",
		in: `{"id":"x"}`, errs: []string{"missing-resource-type@"}},
	{name: "resource-type-missing-in-a-bundle-entry", cite: "bundle.html: Bundle.entry.resource es un Resource",
		in:   `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"id":"x"}}]}`,
		errs: []string{"missing-resource-type@Bundle.entry[0].resource"}},
	{name: "resource-type-unknown", cite: "D4: un resourceType que la versión no define es un error",
		in: `{"resourceType":"Foo"}`, errs: []string{"unknown-resource-type@resourceType"}},
	{name: "resource-type-of-a-datatype", cite: "D4: Quantity no es un recurso",
		in:   `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Quantity"}}]}`,
		errs: []string{"unknown-resource-type@Bundle.entry[0].resource.resourceType"}},
	{name: "resource-type-abstract", cite: "D4: DomainResource es abstracto: no hay instancias",
		in: `{"resourceType":"DomainResource"}`, errs: []string{"unknown-resource-type@resourceType"}},
	{name: "resource-type-not-a-string", cite: "json.html §2.6.2: resourceType es un string",
		in: `{"resourceType":5}`, errs: []string{"wrong-shape@resourceType"}},
	{name: "underscore-on-a-complex-element", cite: "json.html §2.6.2.3: solo los primitivos tienen la forma _x",
		in:   `{"resourceType":"Patient","_name":[{"family":"F"}]}`,
		errs: []string{"not-primitive@Patient.name"}},
	{name: "underscore-on-a-system-typed-element", cite: "plan 004 regla 4: Resource.id es System.String, no un primitivo de FHIR",
		in:   `{"resourceType":"Patient","_id":{"id":"x"}}`,
		errs: []string{"not-primitive@Patient.id"}},
	{name: "underscore-on-extension-url", cite: "plan 004 regla 4: Extension.url es System.String",
		in:   `{"resourceType":"Patient","extension":[{"_url":{"id":"x"},"url":"u"}]}`,
		errs: []string{"not-primitive@Patient.extension[0].url"}},
	{name: "value-inside-the-underscore-object", cite: "json.html §2.6.2.3: _x lleva id y extension, no el value",
		in:   `{"resourceType":"Patient","_active":{"value":true}}`,
		errs: []string{"unknown-property@Patient.active.value"}},
	// Un primitivo que deriva de otro primitivo (code, id: string; canonical: uri;
	// positiveInt: integer) tampoco admite el value dentro de _x: sus hijos son los
	// de Element, no los de su primitivo base (que sí traen value).
	{name: "value-inside-the-underscore-of-a-code", cite: "json.html §2.6.2.3: _x lleva id y extension, no el value; code deriva de string",
		in:   `{"resourceType":"Patient","gender":"male","_gender":{"value":"female"}}`,
		errs: []string{"unknown-property@Patient.gender.value"}},
	{name: "value-inside-the-underscore-of-an-extension-code", cite: "json.html §2.6.2.3; extension.html: value[x] admite code",
		in:   `{"resourceType":"Patient","extension":[{"url":"u","valueCode":"a","_valueCode":{"value":"b"}}]}`,
		errs: []string{"unknown-property@Patient.extension[0].valueCode.value"}},
	{name: "value-inside-the-underscore-of-an-id", cite: "json.html §2.6.2.3; Meta.versionId es id, que deriva de string",
		in:   `{"resourceType":"Patient","meta":{"versionId":"1","_versionId":{"value":"2"}}}`,
		errs: []string{"unknown-property@Patient.meta.versionId.value"}},
	{name: "value-inside-the-underscore-of-a-canonical", cite: "json.html §2.6.2.3; Meta.profile es canonical, que deriva de uri",
		in:   `{"resourceType":"Patient","meta":{"profile":["http://e/p"],"_profile":[{"value":"http://e/q"}]}}`,
		errs: []string{"unknown-property@Patient.meta.profile[0].value"}},
	{name: "value-inside-the-underscore-of-a-positive-int", cite: "json.html §2.6.2.3; extension.html: value[x] admite positiveInt, que deriva de integer",
		in:   `{"resourceType":"Patient","extension":[{"url":"u","valuePositiveInt":1,"_valuePositiveInt":{"value":2}}]}`,
		errs: []string{"unknown-property@Patient.extension[0].valuePositiveInt.value"}},
	{name: "underscore-of-a-code-keeps-id-and-extension", cite: "json.html §2.6.2.3: _x lleva id y extension",
		in:   `{"resourceType":"Patient","_gender":{"extension":[{"valueString":"v","url":"u"}],"id":"g"},"gender":"male"}`,
		want: `{"resourceType":"Patient","gender":"male","_gender":{"id":"g","extension":[{"url":"u","valueString":"v"}]}}`},
	{name: "two-variants-of-a-choice", cite: "elementdefinition.html «Choice types»: un solo tipo por instancia",
		in:   obsPre + `"valueString":"a","valueInteger":1}`,
		errs: []string{"multiple-choice@Observation.value"}},
	{name: "variant-and-underscore-of-another-variant", cite: "elementdefinition.html «Choice types»",
		in:   obsPre + `"valueString":"a","_valueInteger":{"id":"x"}}`,
		errs: []string{"multiple-choice@Observation.value"}},
	{name: "variant-with-its-own-underscore-is-fine", cite: "json.html §2.6.2.3",
		in:   obsPre + `"_valueString":{"id":"x"},"valueString":"a"}`,
		want: `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueString":"a","_valueString":{"id":"x"}}`},
	{name: "array-lengths-differ", cite: "json.html §2.6.2.3: los dos arreglos tienen el mismo largo",
		in:   `{"resourceType":"Patient","name":[{"given":["a","b"],"_given":[{"id":"x"}]}]}`,
		errs: []string{"length-mismatch@Patient.name[0].given"}},
	{name: "object-where-an-array-goes", cite: "json.html §2.6.2: un elemento que se repite es un arreglo",
		in: `{"resourceType":"Patient","name":{"family":"F"}}`, errs: []string{"wrong-shape@Patient.name"}},
	{name: "array-where-an-object-goes", cite: "json.html §2.6.2: un elemento que no se repite no es un arreglo",
		in: `{"resourceType":"Patient","meta":[{"versionId":"1"}]}`, errs: []string{"wrong-shape@Patient.meta"}},
	{name: "array-where-a-primitive-goes", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","active":[true]}`, errs: []string{"wrong-shape@Patient.active"}},
	{name: "object-where-a-primitive-goes", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","gender":{"a":"b"}}`, errs: []string{"wrong-shape@Patient.gender"}},
	{name: "scalar-where-an-object-goes", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","text":"x"}`, errs: []string{"wrong-shape@Patient.text"}},
	{name: "scalar-in-an-array-of-objects", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","name":["x"]}`, errs: []string{"wrong-shape@Patient.name[0]"}},
	{name: "scalar-where-a-resource-goes", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","contained":[1]}`, errs: []string{"wrong-shape@Patient.contained[0]"}},
	{name: "object-where-an-array-of-resources-goes", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","contained":{"resourceType":"Patient"}}`, errs: []string{"wrong-shape@Patient.contained"}},
	{name: "underscore-element-is-an-array-for-a-single-value", cite: "json.html §2.6.2.3",
		in: `{"resourceType":"Patient","_birthDate":[{"id":"x"}]}`, errs: []string{"wrong-shape@Patient.birthDate"}},
	{name: "empty-object", cite: "json.html §2.6.2: «objects SHALL have some content»",
		in: `{"resourceType":"Patient","meta":{}}`, errs: []string{"empty@Patient.meta"}},
	{name: "empty-array", cite: "json.html §2.6.2: «Empty arrays are not allowed»",
		in: `{"resourceType":"Patient","name":[]}`, errs: []string{"empty@Patient.name"}},
	{name: "empty-array-of-primitives", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","name":[{"given":[]}]}`, errs: []string{"empty@Patient.name[0].given"}},
	{name: "empty-underscore-object", cite: "json.html §2.6.2.3: _x sin contenido",
		in: `{"resourceType":"Patient","_birthDate":{}}`, errs: []string{"empty@Patient.birthDate"}},
	{name: "empty-string", cite: "json.html §2.6.2.1: «String property values can never be empty»",
		in: `{"resourceType":"Patient","name":[{"family":""}]}`, errs: []string{"empty-string@Patient.name[0].family"}},
	{name: "empty-string-in-an-array", cite: "json.html §2.6.2.1",
		in: `{"resourceType":"Patient","name":[{"given":["a",""]}]}`, errs: []string{"empty-string@Patient.name[0].given[1]"}},
	{name: "null-property", cite: "json.html §2.6.2: «null» solo en la alineación de arreglos",
		in: `{"resourceType":"Patient","gender":null}`, errs: []string{"null@Patient.gender"}},
	{name: "null-object", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","meta":null}`, errs: []string{"null@Patient.meta"}},
	{name: "null-in-an-array-of-objects", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","name":[null]}`, errs: []string{"null@Patient.name[0]"}},
	{name: "null-in-values-without-underscore-array", cite: "json.html §2.6.2.3: el null necesita su extensión en el mismo lugar",
		in: `{"resourceType":"Patient","name":[{"given":["a",null]}]}`, errs: []string{"null@Patient.name[0].given[1]"}},
	{name: "null-in-underscore-without-value-array", cite: "json.html §2.6.2.3",
		in: `{"resourceType":"Patient","name":[{"_given":[null,{"id":"x"}]}]}`, errs: []string{"null@Patient.name[0].given[0]"}},
	{name: "null-in-both-arrays-at-once", cite: "json.html §2.6.2.3: sin valor ni extensión no hay nada que decir",
		in: `{"resourceType":"Patient","name":[{"given":["a",null],"_given":[null,null]}]}`, errs: []string{"null@Patient.name[0].given[1]"}},
	{name: "duplicate-property", cite: "json.html §2.6.2: «Property names SHALL be unique»",
		in: `{"resourceType":"Patient","id":"a","id":"b"}`, errs: []string{"duplicate-property@Patient.id"}},
	{name: "duplicate-property-with-escapes", cite: "json.html §2.6.2: «n\\u0061me» es «name»",
		in:   `{"resourceType":"Patient","name":[{"family":"A"}],"n\u0061me":[{"family":"B"}]}`,
		errs: []string{"duplicate-property@Patient.name"}},
	{name: "duplicate-underscore-property", cite: "json.html §2.6.2",
		in:   `{"resourceType":"Patient","_birthDate":{"id":"a"},"_birthDate":{"id":"b"}}`,
		errs: []string{"duplicate-property@Patient.birthDate"}},
	{name: "duplicate-resource-type", cite: "json.html §2.6.2",
		in: `{"resourceType":"Patient","resourceType":"Patient"}`, errs: []string{"duplicate-property@resourceType"}},
	{name: "duplicate-unknown-property", cite: "json.html §2.6.2",
		in:   `{"resourceType":"Patient","zz":1,"zz":2}`,
		errs: []string{"unknown-property@Patient.zz", "unknown-property@Patient.zz"}},
	{name: "unknown-properties-are-reported-sorted-by-name", cite: "plan 004 regla 8: el resultado no depende del orden de las claves de la entrada",
		in:   `{"resourceType":"Patient","yy":1,"bb":2}`,
		errs: []string{"unknown-property@Patient.bb", "unknown-property@Patient.yy"}},
	{name: "unknown-properties-are-sorted-by-the-decoded-name", cite: "plan 004 regla 8: se ordena por la clave decodificada, no por los bytes de la entrada (\\u0061z es az, y az va después de ab)",
		in:   `{"resourceType":"Patient","\u0061z":1,"ab":2}`,
		errs: []string{"unknown-property@Patient.ab", "unknown-property@Patient.az"}},
	{name: "empty-property-name", cite: "plan 004 regla 8: un nombre que no es un identificador de FHIRPath se escribe delimitado con acentos graves (fhirpath.html, identificadores delimitados); el vacío es ``",
		in:   `{"resourceType":"Patient","":1}`,
		errs: []string{"unknown-property@Patient.``"}},
	{name: "unusual-property-name", cite: "plan 004 regla 8: delimitado con acentos graves; el acento grave y la barra se escapan con barra",
		in:   `{"resourceType":"Patient","a.b":1,"c` + "`" + `d":2}`,
		errs: []string{"unknown-property@Patient.`a.b`", "unknown-property@Patient.`c\\`d`"}},
	{name: "unknown-underscore-property-is-reported-without-the-underscore", cite: "plan 004 regla 8: la ruta de una clave _x no lleva el guion bajo, conozca o no el elemento; el recorrido y el escáner usan la misma regla",
		in:   `{"resourceType":"Patient","_foo":{"id":"x"}}`,
		errs: []string{"unknown-property@Patient.foo"}},
	{name: "errors-follow-the-output-order", cite: "plan 004 regla 8: los errores van en el orden de la salida",
		in:   `{"zzz":1,"resourceType":"Patient","gender":"","name":[{"bad":1}]}`,
		errs: []string{"unknown-property@Patient.name[0].bad", "empty-string@Patient.gender", "unknown-property@Patient.zzz"}},
	{name: "errors-in-nested-resources", cite: "plan 004 regla 8: en recursos anidados la ruta sigue por la propiedad",
		in:   `{"resourceType":"Bundle","type":"collection","entry":[{"fullUrl":"u"},{"resource":{"resourceType":"Patient","name":[{"x":1}]}}]}`,
		errs: []string{"unknown-property@Bundle.entry[1].resource.name[0].x"}},
}

func (c normCase) applies(tg normTarget) bool {
	if c.synthetic && !strings.HasPrefix(tg.name, "fixture") {
		return false
	}
	return len(c.vers) == 0 || slices.Contains(c.vers, tg.ver)
}

func errSummary(err error) []string {
	var list *ErrorList
	if !errors.As(err, &list) {
		return []string{fmt.Sprintf("not an ErrorList: %v", err)}
	}
	out := make([]string, len(list.Errors))
	for i, e := range list.Errors {
		out[i] = e.Kind.String() + "@" + e.Path
	}
	return out
}

func TestNormalizeCases(t *testing.T) {
	for _, tg := range normTargets(t) {
		t.Run(tg.name, func(t *testing.T) {
			for _, c := range normCases {
				if !c.applies(tg) {
					continue
				}
				t.Run(c.name, func(t *testing.T) {
					got, err := tg.ctx.Normalize([]byte(c.in))
					if c.errs != nil {
						if err == nil {
							t.Fatalf("no error, want %q (%s)", c.errs, c.cite)
						}
						if got != nil {
							t.Errorf("output %q alongside errors", got)
						}
						if s := errSummary(err); !slices.Equal(s, c.errs) {
							t.Errorf("errors = %q, want %q (%s)", s, c.errs, c.cite)
						}
						return
					}
					if err != nil {
						t.Fatalf("%v (%s)", err, c.cite)
					}
					if string(got) != c.want {
						t.Errorf("output\n got %s\nwant %s\n(%s)", got, c.want, c.cite)
					}
					// Regla 9: idempotencia.
					again, err := tg.ctx.Normalize(got)
					if err != nil || !bytes.Equal(again, got) {
						t.Errorf("not idempotent: %s, %v", again, err)
					}
				})
			}
		})
	}
}

// Regla 8: errors.As entrega el primer *Error y la lista completa; el tope de
// errores es configurable y se informa.
func TestNormalizeErrorContract(t *testing.T) {
	c := normContext(t, modelVersions[0])
	in := []byte(`{"resourceType":"Patient","gender":"","name":[{"a":1,"b":2}],"zz":3}`)
	_, err := c.Normalize(in)
	var first *Error
	if !errors.As(err, &first) || first.Kind != KindUnknownProperty || first.Path != "Patient.name[0].a" {
		t.Fatalf("errors.As(*Error) = %+v", first)
	}
	if first.Offset != strings.Index(string(in), `"a"`) {
		t.Errorf("offset %d, want the key's byte %d", first.Offset, strings.Index(string(in), `"a"`))
	}
	var list *ErrorList
	if !errors.As(err, &list) || len(list.Errors) != 4 || list.Truncated {
		t.Fatalf("errors.As(*ErrorList) = %+v", list)
	}
	if !strings.Contains(err.Error(), "unknown-property at Patient.name[0].a") {
		t.Errorf("message %q", err)
	}
	_, err = c.Normalize(in, WithMaxErrors(2))
	if !errors.As(err, &list) || len(list.Errors) != 2 || !list.Truncated {
		t.Fatalf("with a limit of 2: %+v", list)
	}
	if got := errSummary(err); !slices.Equal(got, []string{"unknown-property@Patient.name[0].a", "unknown-property@Patient.name[0].b"}) {
		t.Errorf("the truncated list must be the first errors in output order: %q", got)
	}
	for _, o := range []NormalizeOption{WithMaxDepth(0), WithMaxBytes(0), WithMaxErrors(-1)} {
		if _, err := c.Normalize(in, o); err == nil || errors.As(err, &list) {
			t.Errorf("a non-positive limit must be a plain error, got %v", err)
		}
	}
	if ErrorKind(0).String() == "" || KindNull.String() != "null" {
		t.Error("ErrorKind.String")
	}
}

// Regla 7: profundidad y tamaño.
func TestNormalizeLimits(t *testing.T) {
	c := normContext(t, modelVersions[0])
	deep := `{"resourceType":"Patient","extension":[{"url":"a","extension":[{"url":"b","extension":[{"url":"c","valueString":"x"}]}]}]}`
	// Contenedores anidados: {1 extension[2 {3 extension[4 {5 extension[6 {7.
	if _, err := c.Normalize([]byte(deep), WithMaxDepth(7)); err != nil {
		t.Errorf("depth 7 must fit: %v", err)
	}
	_, err := c.Normalize([]byte(deep), WithMaxDepth(6))
	if s := errSummary(err); !slices.Equal(s, []string{"too-deep@Patient.extension[0].extension[0].extension[0]"}) {
		t.Errorf("depth 6: %q", s)
	}
	// El anidamiento de arreglos también cuenta.
	_, err = c.Normalize([]byte(`{"resourceType":"Patient","name":[[[1]]]}`), WithMaxDepth(3))
	if s := errSummary(err); len(s) != 1 || !strings.HasPrefix(s[0], "too-deep@") {
		t.Errorf("array depth: %q", s)
	}
	_, err = c.Normalize([]byte(`{"resourceType":"Patient","name":[[1]]}`), WithMaxDepth(3))
	if s := errSummary(err); !slices.Equal(s, []string{"wrong-shape@Patient.name[0]"}) {
		t.Errorf("depth 3 must fit these arrays and fail in the model: %q", s)
	}
	_, err = c.Normalize([]byte(deep), WithMaxBytes(len(deep)-1))
	if s := errSummary(err); !slices.Equal(s, []string{"too-large@"}) {
		t.Errorf("size: %q", s)
	}
	if _, err = c.Normalize([]byte(deep), WithMaxBytes(len(deep))); err != nil {
		t.Errorf("the exact size must fit: %v", err)
	}
	// Una entrada muy profunda no desborda la pila con el defecto.
	bomb := strings.Repeat("[", 100000)
	if _, err = c.Normalize([]byte(bomb)); err == nil {
		t.Error("bomb accepted")
	}
}

// Un Context sirve a varias goroutines: las cachés se arman por demanda.
func TestNormalizeIsConcurrent(t *testing.T) {
	c := normContext(t, modelVersions[2])
	ins := make([][]byte, 0, len(normCases))
	for _, nc := range normCases {
		if nc.applies(normTarget{name: "fixture-R5", ver: "R5"}) {
			ins = append(ins, []byte(nc.in))
		}
	}
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 20; i++ {
				for _, in := range ins {
					_, _ = c.Normalize(in)
				}
			}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }

// Plan 004: los tipos que acotan un elemento que contiene un recurso salen de la
// herencia, no de nombres: ancestros de algún recurso, salvo los que también lo
// son de un primitivo (la raíz común de R5).
func TestNormalizeSlotTypes(t *testing.T) {
	for _, tg := range normTargets(t) {
		t.Run(tg.name, func(t *testing.T) {
			slots := tg.ctx.model.slotTypes()
			for name, want := range map[string]bool{
				"Resource": true, "DomainResource": true, "Patient": true, "Bundle": true,
				"Base": false, "Element": false, "Quantity": false, "string": false, "BackboneElement": false, "Nope": false,
			} {
				if slots[name] != want {
					t.Errorf("slotTypes()[%s] = %v, want %v", name, slots[name], want)
				}
			}
		})
	}
}

// La salida no retiene el espacio que la entrada tenía de más (code review del
// plan 004, ronda 1): con una entrada llena de espacios, la capacidad de lo que
// se devuelve queda cerca de su largo.
func TestNormalizeOutputDoesNotHoldTheInputsSpace(t *testing.T) {
	c := normContext(t, modelVersions[0])
	pad := strings.Repeat(" ", 4096)
	in := `{` + pad + `"resourceType"` + pad + `:"Patient"` + pad + `,"active":true` + pad + `}`
	out, err := c.Normalize([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"resourceType":"Patient","active":true}` {
		t.Fatalf("output %s", out)
	}
	if limit := len(out) + len(out)/8 + 64; cap(out) > limit {
		t.Errorf("the output holds %d bytes for %d of content (input %d)", cap(out), len(out), len(in))
	}
}
