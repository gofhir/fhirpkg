package parity

import (
	"bytes"
	"testing"
)

// Controles positivos de la comparación exacta (plan 005 §13.4, criterio 2): cada
// corrupción que un code review encontró aceptada por el clasificador antiguo,
// aplicada a la salida de Project, debe hacer fallar la comparación con el
// oráculo. Antes de corromper se comprueba que la salida sin corromper coincide
// (si no, el caso no prueba nada). Los cuatro últimos son del comparador mismo
// (P13-4): orden de claves, lexema de un número, número por string y display del
// tag.
const dar = `{"extension":[{"url":"http://hl7.org/fhir/StructureDefinition/data-absent-reason","valueCode":"unknown"}]}`

func TestComparisonRejectsHistoricalCorruptions(t *testing.T) {
	appt := `{"resourceType":"Appointment","status":"proposed","description":"drop","participant":[{"status":"accepted","actor":{"reference":"Patient/p"}}]}`
	apptMod := `{"resourceType":"Appointment","status":"proposed","description":"removed","participant":[{"status":"accepted","modifierExtension":[{"url":"https://example.org/mod","valueString":"original"}]}]}`
	apptUnder := `{"resourceType":"Appointment","status":"proposed","description":"drop","participant":[{"_status":` + dar + `,"actor":{"reference":"Patient/p"}}]}`
	cases := []struct {
		name, mode, raw string
		mutate          func(r *onode)
	}{
		// Ronda 1
		{"r1-accepted-to-declined", "true", appt, func(r *onode) {
			r.get("participant").vals[0].get("status").s = "declined"
		}},
		{"r1-alice-bob-to-bob", "true", `{"resourceType":"Patient","name":[{"given":["Alice","Bob"],"family":"F"}]}`, func(r *onode) {
			g := r.get("name").vals[0].get("given")
			g.vals = g.vals[1:]
		}},
		// Ronda 2
		{"r2-delete-mandatory-descendant", "true", apptMod, func(r *onode) {
			r.get("participant").vals[0].del("status")
		}},
		{"r2-corrupt-extension-value", "true", `{"resourceType":"Observation","status":"final","code":{"text":"test"},"note":[{"text":"removed"}],"component":[{"code":{"text":"test"},"modifierExtension":[{"url":"https://example.org/mod","valueString":"original"}]}]}`, func(r *onode) {
			r.get("component").vals[0].get("modifierExtension").vals[0].get("valueString").s = "CORRUPTED"
		}},
		{"r2-corrupt-contained-value", "true", `{"resourceType":"Patient","managingOrganization":{"reference":"#o"},"contained":[{"resourceType":"Organization","id":"o","name":"original","alias":["removed"]}]}`, func(r *onode) {
			r.get("contained").vals[0].get("name").s = "CORRUPTED"
		}},
		// Ronda 3
		{"r3-delete-mandatory-occurrence", "true", `{"resourceType":"Provenance","extension":[{"url":"https://example.org/drop","valueString":"drop"}],"target":[` + dar + `,{"reference":"Patient/p"}],"recorded":"2020-01-01T00:00:00Z","agent":[{"who":{"reference":"Practitioner/p"}}]}`, func(r *onode) {
			tg := r.get("target")
			tg.vals = tg.vals[1:]
		}},
		{"r3-delete-mandatory-underscore", "true", apptUnder, func(r *onode) {
			r.get("participant").vals[0].del("_status")
		}},
		{"r3-corrupt-mandatory-underscore-extension", "true", apptUnder, func(r *onode) {
			r.get("participant").vals[0].get("_status").get("extension").vals[0].del("valueCode")
		}},
		{"r3-delete-summary-descendant", "true", appt, func(r *onode) {
			r.get("participant").vals[0].del("actor")
		}},
		{"r3-misaligned-underscore", "true", `{"resourceType":"Patient","name":[{"given":["A",null,"B"],"_given":[null,{"extension":[{"url":"https://example.org/drop","valueString":"drop"}]},null]}]}`, func(r *onode) {
			// Con true el `_given` cae (Element no es resumen): la corrupción lo agrega.
			n := r.get("name").vals[0]
			n.keys = append(n.keys, "_given")
			n.vals = append(n.vals, &onode{kind: 'a', vals: []*onode{{kind: 'z'}}})
		}},
		{"r3-delete-referenced-contained", "true", `{"resourceType":"Patient","extension":[{"url":"https://example.org/drop","valueString":"drop"}],"managingOrganization":{"reference":"#o"},"contained":[{"resourceType":"Organization","id":"o","name":"O","partOf":{"reference":"#p"}},{"resourceType":"Organization","id":"p","name":"P"}]}`, func(r *onode) {
			c := r.get("contained")
			c.vals = c.vals[:1]
		}},
		// El comparador (P13-4)
		{"cmp-reordered-members", "false", `{"resourceType":"Patient","id":"a","active":true}`, func(r *onode) {
			r.keys[1], r.keys[2] = r.keys[2], r.keys[1]
			r.vals[1], r.vals[2] = r.vals[2], r.vals[1]
		}},
		{"cmp-number-lexeme", "true", `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":1.50,"unit":"mg"}}`, func(r *onode) {
			r.get("valueQuantity").get("value").s = "1.5"
		}},
		{"cmp-number-to-string", "true", `{"resourceType":"Observation","status":"final","code":{"text":"t"},"valueQuantity":{"value":2,"unit":"mg"}}`, func(r *onode) {
			q := r.get("valueQuantity").get("value")
			q.kind = 's'
		}},
		{"cmp-tag-display", "true", `{"resourceType":"Patient","id":"a","active":true,"gender":"male","extension":[{"url":"https://example.org/drop","valueString":"drop"}]}`, func(r *onode) {
			tag := r.get("meta").get("tag").vals[0]
			tag.get("display").s = "other"
		}},
	}
	for _, v := range loadVersions(t) {
		for _, tc := range cases {
			t.Run(v.name+"/"+tc.name, func(t *testing.T) {
				rt := "Patient"
				if i := bytes.Index([]byte(tc.raw), []byte(`"resourceType":"`)); i >= 0 {
					rest := tc.raw[i+len(`"resourceType":"`):]
					rt = rest[:bytes.IndexByte([]byte(rest), '"')]
				}
				p, err := mkProjection(v, rt, tc.mode, nil)
				if err != nil {
					t.Fatal(err)
				}
				out, err := v.ctx.Project([]byte(tc.raw), p)
				if err != nil {
					t.Fatal(err)
				}
				want := oracleOne(t, v, oracleReq{ID: "1", Mode: tc.mode, Resource: tc.raw})
				if d := compareOutputs(out, want); d != "" {
					t.Fatalf("the uncorrupted output already differs from the oracle: %s", d)
				}
				tree, err := parseOrdered(out)
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(tree)
				if bytes.Equal(tree.bytes(), out) {
					t.Fatal("the corruption did not change the output")
				}
				if d := compareOutputs(tree.bytes(), want); d == "" {
					t.Errorf("the comparison accepted the corruption: %s", tree.bytes())
				}
			})
		}
	}
}
