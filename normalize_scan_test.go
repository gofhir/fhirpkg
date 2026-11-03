package fhirpkg

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// El escáner (plan 004, regla 1): lo que encoding/json acepta o cambia en
// silencio, aquí se rechaza o se conserva. json.html: «The character encoding
// is always UTF-8»; RFC 8259 para la gramática.

func scanOnly(t testing.TB, in []byte) (ok bool, e *Error) {
	t.Helper()
	s := scanner{data: in, maxDepth: DefaultMaxDepth}
	return s.scan() != 0, s.err
}

func TestScannerRejects(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"invalid UTF-8 byte in a string", "{\"a\":\"\xff\"}"},
		{"truncated UTF-8 sequence", "{\"a\":\"\xe2\x80\"}"},
		{"overlong UTF-8", "{\"a\":\"\xc0\xaf\"}"},
		{"surrogate encoded in UTF-8", "{\"a\":\"\xed\xa0\x80\"}"},
		{"lone high surrogate escape", `{"a":"\ud800"}`},
		{"lone low surrogate escape", `{"a":"\udc00"}`},
		{"high surrogate followed by a non-surrogate", `{"a":"\ud800\u0041"}`},
		{"high surrogate followed by a high surrogate", `{"a":"\ud800\ud800"}`},
		{"low surrogate before high", `{"a":"\udc00\ud800"}`},
		{"high surrogate at the end", `{"a":"\ud800x"}`},
		{"surrogate escape in a key", `{"\ud800":1}`},
		{"invalid UTF-8 in a key", "{\"\xff\":1}"},
		{"control character in a string", "{\"a\":\"x\x01y\"}"},
		{"raw newline in a string", "{\"a\":\"x\ny\"}"},
		{"invalid escape", `{"a":"\x"}`},
		{"short unicode escape", `{"a":"\u12"}`},
		{"non-hex unicode escape", `{"a":"\u12g4"}`},
		{"unterminated string", `{"a":"x`},
		{"unterminated backslash", `{"a":"x\`},
		{"leading zero", `{"a":01}`},
		{"plus sign", `{"a":+1}`},
		{"no integer part", `{"a":.5}`},
		{"no fraction digits", `{"a":1.}`},
		{"no exponent digits", `{"a":1e}`},
		{"no exponent digits after sign", `{"a":1e+}`},
		{"lone minus", `{"a":-}`},
		{"NaN", `{"a":NaN}`},
		{"Infinity", `{"a":Infinity}`},
		{"hex number", `{"a":0x10}`},
		{"trailing comma in object", `{"a":1,}`},
		{"trailing comma in array", `{"a":[1,]}`},
		{"missing colon", `{"a" 1}`},
		{"missing comma", `{"a":1 "b":2}`},
		{"unquoted key", `{a:1}`},
		{"single quotes", `{'a':1}`},
		{"truncated literal", `{"a":tru}`},
		{"capitalised literal", `{"a":True}`},
		{"nul", `{"a":nul}`},
		{"unclosed object", `{"a":1`},
		{"unclosed array", `{"a":[1`},
		{"mismatched closers", `{"a":[1}`},
		{"trailing data", `{"a":1} x`},
		{"two documents", `{"a":1}{"b":2}`},
		{"empty input", ``},
		{"whitespace only", " \n"},
		{"byte order mark", "\xef\xbb\xbf{\"a\":1}"},
		{"form feed as whitespace", "{\"a\":1}\f"},
		{"non-breaking space as whitespace", "{ \"a\":1}"},
		{"comment", `{"a":1 /* c */}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, e := scanOnly(t, []byte(tc.in))
			if ok || e == nil || e.Kind != KindSyntax {
				t.Fatalf("accepted or wrong error: ok=%v err=%v", ok, e)
			}
			if tc.in != "" && e.Offset > len(tc.in) {
				t.Errorf("offset %d beyond the input", e.Offset)
			}
		})
	}
}

func TestScannerAccepts(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"paired surrogates", `{"a":"\ud83d\ude00"}`},
		{"surrogate pair in a key", `{"\ud83d\ude00":1}`},
		{"U+2028 raw", "{\"a\":\"x y\"}"},
		{"U+2028 escaped", `{"a":"x\u2028y"}`},
		{"U+FFFD raw", "{\"a\":\"�\"}"},
		{"noncharacter raw", "{\"a\":\"\U0010ffff￾\"}"},
		{"NUL escaped", `{"a":"\u0000"}`},
		{"all simple escapes", `{"a":"\"\\\/\b\f\n\r\t"}`},
		{"numbers", `[0,-0,-0.0,1,10,1.5,1E5,1e-5,1E+5,0.5e10,1.00000000000000000E-24,-1E+245,12345678901234567890123,1E400]`},
		{"literals", `[true,false,null]`},
		{"whitespace", " \t\r\n{ \"a\" : [ 1 , 2 ] }\n"},
		{"nested empty", `{"a":{},"b":[],"c":[[]],"d":""}`},
		{"top-level scalar", `5`},
		{"top-level string", `"x"`},
		{"top-level array", `[]`},
		{"duplicate keys are not the scanner's business", `{"a":1,"a":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if ok, e := scanOnly(t, []byte(tc.in)); !ok {
				t.Fatalf("rejected: %v", e)
			}
		})
	}
}

// Los errores de sintaxis traen el lugar: la ruta FHIRPath (regla 8) con el tipo
// del recurso raíz, las claves decodificadas y los índices, y el byte. Si el
// resourceType no se leyó antes del error, la raíz se llama Resource.
func TestScannerErrorLocation(t *testing.T) {
	for _, tc := range []struct {
		name, in, path, at string
	}{
		{"nested, root type known", `{"resourceType":"Patient","name":[{"family":"F","given":[tru]}]}`, "Patient.name[0].given[0]", "tru]"},
		{"decoded key", `{"resourceType":"Patient","n\u0061me":[tru]}`, "Patient.name[0]", "tru]"},
		{"decoded key with a dot", `{"resourceType":"Patient","a\u002eb":tru}`, "Patient.`a.b`", "tru}"},
		{"underscore form loses its underscore", `{"resourceType":"Patient","name":[{"_given":[tru]}]}`, "Patient.name[0].given[0]", "tru]"},
		{"unknown underscore key loses it too", `{"resourceType":"Patient","_foo":tru}`, "Patient.foo", "tru}"},
		{"root type that is not an identifier (dot)", `{"resourceType":"a.b","x":[1,}`, "Resource.x[1]", "}"},
		{"root type that is not an identifier (newline)", `{"resourceType":"a\nb","x":tru}`, "Resource.x", "tru}"},
		{"root type that is not an identifier (backtick)", `{"resourceType":"a\u0060b","x":tru}`, "Resource.x", "tru}"},
		{"error in the root container", `{"resourceType":"Patient",}`, "Patient", "}"},
		{"error in a nested resource", `{"resourceType":"Bundle","entry":[{"resource":{"resourceType":"Patient","x":tru}}]}`, "Bundle.entry[0].resource.x", "tru}"},
		{"root type after the error", `{"a":[1,{"b":[true,tru]}],"resourceType":"Patient"}`, "Resource.a[1].b[1]", "tru]"},
		{"no root type", `{"a":[1,{"b":[true,tru]}],"c":2}`, "Resource.a[1].b[1]", "tru]"},
		{"root type is not a string", `{"resourceType":5,"x":tru}`, "Resource.x", "tru}"},
		{"root is not an object", `[1,tru]`, "Resource[1]", "tru]"},
		{"error in the root scalar", `tru`, "Resource", "tru"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := scanOnly(t, []byte(tc.in))
			if e == nil || e.Path != tc.path || e.Offset != strings.Index(tc.in, tc.at) {
				t.Fatalf("error = %+v, want path %q at byte %d", e, tc.path, strings.Index(tc.in, tc.at))
			}
		})
	}
}

// El escáner es más estricto que encoding/json y nunca más laxo: lo que acepta
// lo acepta json.Valid; y lo que json.Valid acepta y es UTF-8 sin surrogates
// sueltos, él también.
func checkScannerAgainstStdlib(t testing.TB, in []byte) {
	ok, e := scanOnly(t, in)
	if ok && !json.Valid(in) {
		t.Fatalf("accepted what json.Valid rejects: %q", in)
	}
	if !ok && e == nil {
		t.Fatalf("rejected without an error: %q", in)
	}
	if !ok && json.Valid(in) && utf8.Valid(in) && !hasLoneSurrogate(in) {
		// Con encoding/json aceptado, solo la profundidad puede rechazar.
		if e.Kind != KindTooDeep {
			t.Fatalf("rejected a valid document (%v): %q", e, in)
		}
	}
}

// hasLoneSurrogate: ¿el texto tiene un \uD800-\uDFFF que no es un par?
func hasLoneSurrogate(in []byte) bool {
	s := string(in)
	for i := 0; i+5 < len(s); i++ {
		if s[i] != '\\' || (s[i+1] != 'u' && s[i+1] != 'U') {
			continue
		}
		// Una barra precedida de otra barra no abre un escape; se aproxima por exceso.
		var r rune
		for _, c := range []byte(s[i+2 : i+6]) {
			if !isHex(c) {
				r = -1
				break
			}
			r = r<<4 | hexVal(c)
		}
		if r >= 0xD800 && r <= 0xDFFF {
			return true
		}
	}
	return false
}

func TestScannerAgainstStdlib(t *testing.T) {
	for _, in := range []string{
		`{"a":1}`, `[1,2,3]`, `"x"`, `1.5e3`, `{"a":{"b":[null,true,false,"\u00e9"]}}`, "{\"a\":\"\xff\"}",
		`{"a":"\ud800"}`, `{"a":01}`, `{"a":[1,]}`, ` {"a" : 1 } `, `{"a":"\ud83d\ude00"}`, `{}`, `[]`, `{"":""}`,
	} {
		checkScannerAgainstStdlib(t, []byte(in))
	}
}

func FuzzScanner(f *testing.F) {
	for _, tc := range normCases {
		f.Add([]byte(tc.in))
	}
	f.Add([]byte(`{"a":"\ud83d\ude00","b":[1.5e3,-0,true,null]}`))
	f.Add([]byte("{\"a\":\"\xff\"}"))
	f.Fuzz(func(t *testing.T, in []byte) {
		checkScannerAgainstStdlib(t, in)
	})
}

// El escape de los strings (D2) es el de encoding/json sin escapar HTML, para
// todo el rango de Unicode, escrito crudo y escrito con \u.
func TestEscapeMatchesEncodingJSON(t *testing.T) {
	enc := func(s string) string {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		if err := e.Encode(s); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	check := func(r rune) {
		want := enc(string(r))
		raw := []byte(string(r))
		if r == '"' || r == '\\' || r < 0x20 {
			raw = nil // un crudo así no es JSON válido: solo la forma escapada
		}
		if raw != nil {
			if got := "\"" + string(appendEscaped(nil, raw)) + "\""; got != want {
				t.Fatalf("U+%04X raw: %s, want %s", r, got, want)
			}
		}
		var esc string
		if r > 0xFFFF {
			r2 := r - 0x10000
			esc = `\u` + hex4(0xD800+(r2>>10)) + `\u` + hex4(0xDC00+(r2&0x3FF))
		} else {
			esc = `\u` + hex4(r)
		}
		if got := "\"" + string(appendEscaped(nil, []byte(esc))) + "\""; got != want {
			t.Fatalf("U+%04X escaped: %s, want %s", r, got, want)
		}
	}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if r > 0x3000 && r%97 != 0 && r < 0x10FFF0 && r != 0x2028 && r != 0x2029 {
			continue
		}
		check(r)
	}
	// Cadenas con todo mezclado y los escapes simples de entrada.
	in := `a\/b\"c\\d\ne\tf\rg\bh\fi\u0000j\u001fk\u007fl\u2028m\u2029n\u00e9o\ud83d\ude00p`
	var want string
	if err := json.Unmarshal([]byte(`"`+in+`"`), &want); err != nil {
		t.Fatal(err)
	}
	if got := "\"" + string(appendEscaped(nil, []byte(in))) + "\""; got != enc(want) {
		t.Errorf("mixed: %s, want %s", got, enc(want))
	}
	if got := string(appendUnescaped(nil, []byte(in))); got != want {
		t.Errorf("appendUnescaped: %q, want %q", got, want)
	}
}

func hex4(r rune) string {
	const d = "0123456789abcdef"
	return string([]byte{d[r>>12&0xF], d[r>>8&0xF], d[r>>4&0xF], d[r&0xF]})
}
