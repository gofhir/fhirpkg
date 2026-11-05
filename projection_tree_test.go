package fhirpkg

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Un árbol JSON que conserva el orden de las claves, para las propiedades de la
// proyección (un subconjunto con el mismo orden). No usa nada de la librería.

type jkv struct {
	k string
	v any
}

type jobj []jkv

func (o jobj) get(k string) (any, bool) {
	for _, e := range o {
		if e.k == k {
			return e.v, true
		}
	}
	return nil, false
}

func parseOrdered(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := parseValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err == nil {
		return nil, fmt.Errorf("trailing data")
	}
	return v, nil
}

func parseValue(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := jobj{}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				v, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				o = append(o, jkv{kt.(string), v})
			}
			_, err := d.Token()
			return o, err
		case '[':
			a := []any{}
			for d.More() {
				v, err := parseValue(d)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err := d.Token()
			return a, err
		}
	}
	return tok, nil
}

// isSubset dice si out se obtiene de in quitando elementos, con el mismo orden:
// las claves de un objeto son un subconjunto ordenado, y un arreglo es una
// subsecuencia.
func isSubset(out, in any) bool {
	switch o := out.(type) {
	case jobj:
		i, ok := in.(jobj)
		if !ok {
			return false
		}
		j := 0
		for _, e := range o {
			for j < len(i) && i[j].k != e.k {
				j++
			}
			if j == len(i) || !isSubset(e.v, i[j].v) {
				return false
			}
			j++
		}
		return true
	case []any:
		i, ok := in.([]any)
		if !ok {
			return false
		}
		j := 0
		for _, e := range o {
			for j < len(i) && !isSubset(e, i[j]) {
				j++
			}
			if j == len(i) {
				return false
			}
			j++
		}
		return true
	}
	return fmt.Sprint(out) == fmt.Sprint(in)
}

// stripTag quita el tag SUBSETTED de un recurso y de los anidados, y el meta o el
// tag que queda vacío por ello.
func stripTag(v any) any {
	switch x := v.(type) {
	case jobj:
		var out jobj
		for _, e := range x {
			nv := stripTag(e.v)
			if e.k == elemMeta {
				if m, ok := nv.(jobj); ok {
					var mm jobj
					for _, me := range m {
						if me.k == elemTag {
							var keep []any
							for _, t := range me.v.([]any) {
								if o, ok := t.(jobj); ok {
									if c, _ := o.get(elemCode); c == codeSubsetted {
										continue
									}
								}
								keep = append(keep, t)
							}
							if len(keep) == 0 {
								continue
							}
							me.v = keep
						}
						mm = append(mm, me)
					}
					if len(mm) == 0 {
						continue
					}
					nv = mm
				}
			}
			out = append(out, jkv{e.k, nv})
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = stripTag(e)
		}
		return out
	}
	return v
}
