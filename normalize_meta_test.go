package fhirpkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// Metamórficos del normalizador (plan 004 §4): reordenar las claves, agregar
// espacios o insertar una propiedad desconocida no cambia la salida (o cambia
// los errores de la forma prevista).

// jv es un valor JSON con el orden de las claves de la entrada.
type jv struct {
	kind byte // 'o', 'a' o 's' (escalar: raw es su texto JSON)
	keys []string
	vals []jv
	raw  string
}

func parseJV(t testing.TB, in []byte) jv {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	v, err := readJV(dec)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func readJV(dec *json.Decoder) (jv, error) {
	tok, err := dec.Token()
	if err != nil {
		return jv{}, err
	}
	switch x := tok.(type) {
	case json.Delim:
		if x == '{' {
			v := jv{kind: 'o'}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return jv{}, err
				}
				c, err := readJV(dec)
				if err != nil {
					return jv{}, err
				}
				v.keys, v.vals = append(v.keys, k.(string)), append(v.vals, c)
			}
			_, err := dec.Token()
			return v, err
		}
		v := jv{kind: 'a'}
		for dec.More() {
			c, err := readJV(dec)
			if err != nil {
				return jv{}, err
			}
			v.vals = append(v.vals, c)
		}
		_, err := dec.Token()
		return v, err
	case string:
		b, _ := json.Marshal(x)
		return jv{kind: 's', raw: string(b)}, nil
	case json.Number:
		return jv{kind: 's', raw: x.String()}, nil
	case bool:
		return jv{kind: 's', raw: fmt.Sprint(x)}, nil
	}
	return jv{kind: 's', raw: "null"}, nil
}

// emit escribe el valor; ws agrega espacios entre los tokens, ordenar baraja las claves.
func (v jv) emit(b *strings.Builder, r *rand.Rand, shuffle, ws bool) {
	sp := func() {
		if ws {
			b.WriteString([]string{" ", "\n", "\t ", "\r\n"}[r.Intn(4)])
		}
	}
	switch v.kind {
	case 's':
		b.WriteString(v.raw)
	case 'a':
		b.WriteByte('[')
		sp()
		for i, c := range v.vals {
			if i > 0 {
				b.WriteByte(',')
				sp()
			}
			c.emit(b, r, shuffle, ws)
			sp()
		}
		b.WriteByte(']')
	case 'o':
		idx := r.Perm(len(v.keys))
		if !shuffle {
			for i := range idx {
				idx[i] = i
			}
		}
		b.WriteByte('{')
		sp()
		for n, i := range idx {
			if n > 0 {
				b.WriteByte(',')
				sp()
			}
			kb, _ := json.Marshal(v.keys[i])
			b.Write(kb)
			sp()
			b.WriteByte(':')
			sp()
			v.vals[i].emit(b, r, shuffle, ws)
			sp()
		}
		b.WriteByte('}')
	}
}

func (v jv) text(r *rand.Rand, shuffle, ws bool) []byte {
	var b strings.Builder
	v.emit(&b, r, shuffle, ws)
	return []byte(b.String())
}

// objectPaths lista las rutas FHIRPath de los objetos de un recurso (sin bajar
// por las claves _x, cuya ruta es la del elemento).
func (v jv) objectPaths(path string, out *[]string) {
	switch v.kind {
	case 'o':
		*out = append(*out, path)
		for i, k := range v.keys {
			if strings.HasPrefix(k, "_") {
				continue
			}
			p := k
			if path != "" {
				p = path + "." + k
			}
			v.vals[i].objectPaths(p, out)
		}
	case 'a':
		for i, c := range v.vals {
			c.objectPaths(fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

// withExtra inserta una propiedad desconocida en el objeto de la ruta dada.
func (v jv) withExtra(path, cur, key string) jv {
	out := jv{kind: v.kind, keys: slices.Clone(v.keys), vals: slices.Clone(v.vals), raw: v.raw}
	switch v.kind {
	case 'o':
		if cur == path {
			out.keys, out.vals = append(out.keys, key), append(out.vals, jv{kind: 's', raw: "1"})
		}
		for i, k := range v.keys {
			if strings.HasPrefix(k, "_") {
				continue
			}
			p := k
			if cur != "" {
				p = cur + "." + k
			}
			out.vals[i] = v.vals[i].withExtra(path, p, key)
		}
	case 'a':
		for i, c := range v.vals {
			out.vals[i] = c.withExtra(path, fmt.Sprintf("%s[%d]", cur, i), key)
		}
	}
	return out
}

// metaInputs son los textos de los casos que se normalizan sin error y los que
// tienen errores (para comparar sus errores).
func metaInputs(tg normTarget) (valid, invalid []string) {
	for _, c := range normCases {
		if !c.applies(tg) {
			continue
		}
		if c.errs == nil {
			valid = append(valid, c.in)
		} else if !strings.Contains(c.in, `"zz"`) && c.name != "duplicate-property" && c.name != "duplicate-property-with-escapes" &&
			c.name != "duplicate-underscore-property" && c.name != "duplicate-resource-type" {
			invalid = append(invalid, c.in)
		}
	}
	return
}

// rootName es el nombre del recurso raíz de una entrada.
func rootName(v jv) string {
	for i, k := range v.keys {
		if k == propResourceType {
			return strings.Trim(v.vals[i].raw, `"`)
		}
	}
	return ""
}

func TestNormalizeMetamorphic(t *testing.T) {
	for _, tg := range normTargets(t) {
		t.Run(tg.name, func(t *testing.T) {
			valid, invalid := metaInputs(tg)
			r := rand.New(rand.NewSource(42))
			for _, in := range valid {
				base, err := tg.ctx.Normalize([]byte(in))
				if err != nil {
					t.Fatal(err)
				}
				v := parseJV(t, []byte(in))
				for round := 0; round < 20; round++ {
					// Las claves en otro orden y con espacios: los mismos bytes.
					got, err := tg.ctx.Normalize(v.text(r, true, true))
					if err != nil || !bytes.Equal(got, base) {
						t.Fatalf("shuffled input of %s: %s, %v\nwant %s", in, got, err, base)
					}
				}
				// Una propiedad desconocida en un objeto al azar: un error con su ruta.
				var paths []string
				v.objectPaths("", &paths)
				p := paths[r.Intn(len(paths))]
				key := fmt.Sprintf("zz%d", r.Intn(1000))
				_, err = tg.ctx.Normalize(v.withExtra(p, "", key).text(r, true, false))
				want := rootName(v)
				if p != "" {
					want += "." + p
				}
				want += "." + key
				if got := errSummary(err); !slices.Equal(got, []string{"unknown-property@" + want}) {
					t.Errorf("unknown property %s inserted at %q of %s: %q", key, p, in, got)
				}
			}
			for _, in := range invalid {
				_, err := tg.ctx.Normalize([]byte(in))
				base := errSummary(err)
				v := parseJV(t, []byte(in))
				for round := 0; round < 20; round++ {
					_, err := tg.ctx.Normalize(v.text(r, true, true))
					if got := errSummary(err); !slices.Equal(got, base) {
						t.Fatalf("shuffled input of %s: errors %q, want %q", in, got, base)
					}
				}
			}
		})
	}
}

// leaves son las hojas de un documento JSON, ordenadas: los escalares con su
// texto (los strings decodificados).
func leaves(t testing.TB, in []byte) []string {
	t.Helper()
	var out []string
	var walk func(v jv)
	walk = func(v jv) {
		if v.kind == 's' {
			out = append(out, v.raw)
		}
		for _, c := range v.vals {
			walk(c)
		}
	}
	walk(parseJV(t, in))
	slices.Sort(out)
	return out
}

// checkNormalizeInvariants son los invariantes de FuzzNormalize: sin pánico y,
// si hay salida, UTF-8 y JSON válidos, idempotente y con las mismas hojas.
func checkNormalizeInvariants(t testing.TB, c *Context, in []byte) {
	out, err := c.Normalize(in)
	if err != nil {
		if out != nil {
			t.Fatalf("output alongside an error")
		}
		var list *ErrorList
		if !errorsAs(err, &list) || len(list.Errors) == 0 {
			t.Fatalf("an error that is not an ErrorList: %v", err)
		}
		return
	}
	if !utf8.Valid(out) || !json.Valid(out) {
		t.Fatalf("invalid output %q", out)
	}
	again, err := c.Normalize(out)
	if err != nil || !bytes.Equal(again, out) {
		t.Fatalf("not idempotent: %q -> %q (%v)", out, again, err)
	}
	// El relleno de nulls del arreglo más corto (json.html R5 §2.1.6.4.2) agrega
	// hojas null y nada más: se comparan las hojas con valor.
	notNull := func(l []string) []string {
		return slices.DeleteFunc(slices.Clone(l), func(x string) bool { return x == "null" })
	}
	if !slices.Equal(notNull(leaves(t, in)), notNull(leaves(t, out))) {
		t.Fatalf("different leaves:\n in %q\nout %q", leaves(t, in), leaves(t, out))
	}
}

func FuzzNormalize(f *testing.F) {
	c := normContext(f, modelVersions[2])
	for _, tc := range normCases {
		f.Add([]byte(tc.in))
	}
	f.Add([]byte(`{"resourceType":"Patient","name":[{"given":["a"],"_given":[{"id":"x"}]}],"birthDate":"2000"}`))
	f.Add([]byte("{\"resourceType\":\"Patient\",\"gender\":\"\xff\"}"))
	f.Fuzz(func(t *testing.T, in []byte) {
		checkNormalizeInvariants(t, c, in)
	})
}

// El corpus de semillas de los casos corre como test (make check).
func TestNormalizeInvariantsOnTheCases(t *testing.T) {
	for _, tg := range normTargets(t) {
		for _, tc := range normCases {
			if tc.applies(tg) {
				checkNormalizeInvariants(t, tg.ctx, []byte(tc.in))
			}
		}
	}
}

var _ = utf8.Valid
