package fhirpkg

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// Las propiedades de la proyección (plan 005 §5.2) sobre las entradas de los
// casos a mano y sobre los recursos que las otras pruebas arman.

type projInput struct {
	rt, in   string
	elements [][]string
}

func projInputs() []projInput {
	ext := `[{"url":"u","valueString":"v"}]`
	return []projInput{
		{"Patient", patientFull, [][]string{{"gender"}, {"managingOrganization"}, {"birthDate", "name"}, {"contained"}, {"text", "extension"}}},
		{"Patient", `{"resourceType":"Patient","name":[{"given":["a",null],"_given":[null,{"id":"i"}],"family":"F","_family":{"extension":` + ext + `}}],"_birthDate":{"extension":` + ext + `}}`, [][]string{{"name"}, {"birthDate"}}},
		{"Patient", `{"resourceType":"Patient","name":[{"given":["A"],"_given":[null]},{"given":["B","C"],"_given":[null,null]}]}`, [][]string{{"name"}}},
		{"Patient", `{"resourceType":"Patient","name":[{"extension":` + ext + `},{"family":"A"}],"managingOrganization":{"extension":` + ext + `}}`, [][]string{{"name"}}},
		{"Patient", `{"resourceType":"Patient","meta":{"versionId":"1","profile":["http://p"],"tag":[{"system":"s","code":"c"}]},"text":NARR,"modifierExtension":` + ext + `,"gender":"male"}`, [][]string{{"gender"}}},
		{"Patient", `{"resourceType":"Patient","id":"p","contained":[{"resourceType":"Patient","id":"a","managingOrganization":{"reference":"#b"}},{"resourceType":"Patient","id":"b","gender":"male","text":NARR},{"resourceType":"Patient","id":"c"}],"managingOrganization":{"reference":"#a"}}`,
			[][]string{{"managingOrganization"}}},
		{"Patient", `{"resourceType":"Patient","implicitRules":"http://x","language":"es","text":NARR,"active":true,"deceasedBoolean":false,"multipleBirthInteger":2}`, [][]string{{"language"}}},
		{"Observation", `{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"subject":{"reference":"Patient/1"},"valueString":"s","extension":` + ext + `}`, [][]string{{"subject"}, {"value"}}},
		{"Bundle", bundleIn, [][]string{{"entry"}, {"total"}}},
		{"Bundle", `{"resourceType":"Bundle","type":"collection","entry":[{"modifierExtension":[{"url":"u","extension":[{"url":"i","valueString":"n"}]}],"extension":` + ext + `,"fullUrl":"f"}]}`, [][]string{{"entry"}}},
		{"Parameters", `{"resourceType":"Parameters","parameter":[{"name":"p","resource":{"resourceType":"Patient","id":"a","text":NARR,"gender":"male"}},{"name":"q","valueString":"s","part":[{"name":"r","valueBoolean":true}]}]}`, [][]string{{"parameter"}}},
		{"Composition", `{"resourceType":"Composition","status":"final","title":"t","text":NARR,"section":[{"title":"s","text":NARR}]}`, [][]string{{"section"}, {"title"}}},
		{"Patient", `{"resourceType":"Patient","id":"p","gender":"male"}`, [][]string{{"gender"}}},
	}
}

// localRefs reúne los «#id» de los Reference.reference del recurso, sin bajar a los
// recursos anidados ni al contained.
func localRefs(v any, out map[string]bool, root bool) {
	switch x := v.(type) {
	case jobj:
		if _, isRes := x.get(propResourceType); isRes && !root {
			return
		}
		for _, e := range x {
			if e.k == elemContained {
				continue
			}
			if s, ok := e.v.(string); ok && e.k == elemReference && strings.HasPrefix(s, localRefPrefix) && len(s) > 1 {
				out[s[1:]] = true
			}
			localRefs(e.v, out, false)
		}
	case []any:
		for _, e := range x {
			localRefs(e, out, false)
		}
	}
}

func containedIDs(o jobj) map[string]bool {
	ids := map[string]bool{}
	arr, _ := o.getArr(elemContained)
	for _, c := range arr {
		if id, ok := c.(jobj).get(elemID); ok {
			ids[id.(string)] = true
		}
	}
	return ids
}

// noDanglingRefs: toda referencia local conservada de un recurso cuyo contenido
// estaba en la entrada tiene su contained en la salida (ref-1), y todo contenido
// conservado está referido por algo conservado (dom-3), en la raíz y en los anidados.
func noDanglingRefs(out, in any) []string {
	var bad []string
	var rec func(o, i any, path string)
	rec = func(o, i any, path string) {
		switch ox := o.(type) {
		case jobj:
			ix, ok := i.(jobj)
			if !ok {
				return
			}
			if _, isRes := ox.get(propResourceType); isRes {
				refs := map[string]bool{}
				localRefs(ox, refs, true)
				// Las referencias de los contenidos conservados también cuentan.
				carr, _ := ox.getArr(elemContained)
				for _, c := range carr {
					localRefs(c, refs, true)
				}
				have, had := containedIDs(ox), containedIDs(ix)
				for r := range refs {
					if had[r] && !have[r] {
						bad = append(bad, path+": the reference #"+r+" is kept without its contained resource")
					}
				}
				for id := range have {
					if !refs[id] {
						bad = append(bad, path+": the contained resource "+id+" is kept and nobody references it")
					}
				}
			}
			for _, e := range ox {
				if iv, ok := ix.get(e.k); ok {
					rec(e.v, iv, path+"."+e.k)
				}
			}
		case []any:
			ia, ok := i.([]any)
			if !ok || len(ia) != len(ox) {
				return // algún elemento cayó: la correspondencia por posición ya no vale
			}
			for k := range ox {
				rec(ox[k], ia[k], path)
			}
		}
	}
	rec(out, in, "")
	return bad
}

func TestProjectProperties(t *testing.T) {
	modes := []struct {
		mode SummaryMode
		name string
	}{{SummaryFalse, "false"}, {SummaryTrue, "true"}, {SummaryText, "text"}, {SummaryData, "data"}}
	for _, pt := range projTargets(t) {
		t.Run(pt.name, func(t *testing.T) {
			n := 0
			for _, pi := range projInputs() {
				in := strings.ReplaceAll(pi.in, "NARR", narr)
				// Un contenido no referido no es un recurso válido por dom-3, pero la
				// entrada solo debe pasar Normalize.
				norm, err := pt.ctx.Normalize([]byte(in))
				if err != nil {
					t.Fatalf("Normalize(%s): %v", in, err)
				}
				normTree, _ := parseOrdered(norm)
				type run struct {
					name     string
					mode     SummaryMode
					elements []string
				}
				runs := []run{}
				for _, m := range modes {
					runs = append(runs, run{m.name, m.mode, nil})
				}
				for _, els := range pi.elements {
					runs = append(runs, run{"elements=" + strings.Join(els, ","), SummaryFalse, els})
				}
				for _, r := range runs {
					p, err := pt.ctx.NewProjection(pi.rt, r.mode, r.elements)
					if err != nil {
						t.Fatalf("%s %s: %v", pi.rt, r.name, err)
					}
					out, err := pt.ctx.Project([]byte(in), p)
					if err != nil {
						t.Fatalf("%s %s: %v\n%s", pi.rt, r.name, err, in)
					}
					n++
					where := pi.rt + " " + r.name + " " + in
					// 1. Project(x) == Project(Normalize(x)).
					if fromNorm, err := pt.ctx.Project(norm, p); err != nil || !bytes.Equal(fromNorm, out) {
						t.Errorf("Project(x) != Project(Normalize(x)): %s\n %s\n %s (%v)", where, out, fromNorm, err)
					}
					// 2. Normalize(Project(x)) == Project(x).
					if again, err := pt.ctx.Normalize(out); err != nil || !bytes.Equal(again, out) {
						t.Errorf("Normalize(Project(x)) != Project(x): %s\n %s\n %s (%v)", where, out, again, err)
					}
					// 3. Project(Project(x)) == Project(x): el tag no se duplica.
					if twice, err := pt.ctx.Project(out, p); err != nil || !bytes.Equal(twice, out) {
						t.Errorf("Project is not idempotent: %s\n %s\n %s (%v)", where, out, twice, err)
					}
					// 4. Project(x, false) == Normalize(x).
					if r.mode == SummaryFalse && r.elements == nil && !bytes.Equal(out, norm) {
						t.Errorf("Project(x, false) != Normalize(x): %s\n %s\n %s", where, out, norm)
					}
					// 5. Sin el tag, la salida es un subconjunto de Normalize(x), en el mismo orden.
					outTree, _ := parseOrdered(out)
					if !isSubset(stripTag(outTree), stripTag(normTree)) {
						t.Errorf("the output is not an ordered subset of Normalize(x): %s\n %s", where, out)
					}
					// 6. Ninguna referencia local conservada queda sin su contained (ref-1) ni un
					// contenido sin referir (dom-3) que la entrada sí referiría.
					if r.mode != SummaryData && r.mode != SummaryFalse || r.elements != nil {
						for _, b := range noDanglingRefs(outTree, normTree) {
							if !strings.Contains(b, "nobody references") || r.mode != SummaryFalse {
								t.Errorf("%s: %s\n %s", where, b, out)
							}
						}
					}
					// 7. Hay tag si y solo si la salida difiere de Normalize(x), en cada recurso: la
					// raíz perdió algo si su sin-tag no es la entrada normalizada.
					hasTag := strings.Contains(string(out), codeSubsetted)
					// Un `_x` solo de null no cuenta como contenido (veredicto D-1): se quita sin tag.
					differs := !bytes.Equal(out, norm) && !bytes.Equal(out, dropNullOnlyUnderscore(norm))
					if hasTag != differs && !strings.Contains(string(norm), codeSubsetted) {
						t.Errorf("tag %v but the output differs from Normalize(x) %v: %s\n %s", hasTag, differs, where, out)
					}
				}
			}
			t.Logf("%d projections checked", n)
		})
	}
}

var (
	nullOnlyAfter  = regexp.MustCompile(`,"_[A-Za-z0-9]+":\[null(,null)*\]`)
	nullOnlyBefore = regexp.MustCompile(`"_[A-Za-z0-9]+":\[null(,null)*\],`)
)

// dropNullOnlyUnderscore quita de un JSON compacto los arreglos `_x` formados solo
// por null.
func dropNullOnlyUnderscore(b []byte) []byte {
	return nullOnlyBefore.ReplaceAll(nullOnlyAfter.ReplaceAll(b, nil), nil)
}
