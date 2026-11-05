package fhirpkg

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// projFuzzTypes son los tipos con proyección en FuzzProject.
var projFuzzTypes = []string{"Patient", "Observation", "Bundle", "Parameters", "Composition"}

// projFuzzProjections arma una Projection por tipo y modo, y una con _elements.
func projFuzzProjections(t testing.TB, c *Context) []*Projection {
	t.Helper()
	var ps []*Projection
	for _, rt := range projFuzzTypes {
		for _, mode := range []SummaryMode{SummaryFalse, SummaryTrue, SummaryText, SummaryData} {
			p, err := c.NewProjection(rt, mode, nil)
			if err != nil {
				t.Fatal(err)
			}
			ps = append(ps, p)
		}
		first := map[string]string{"Patient": "gender", "Observation": "subject", "Bundle": "entry", "Parameters": "parameter", "Composition": "section"}[rt]
		p, err := c.NewProjection(rt, SummaryFalse, []string{first})
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	return ps
}

// checkProjectInvariants son los invariantes de FuzzProject: sin pánico; la misma
// entrada que Normalize rechaza se rechaza igual; la salida es JSON válido y UTF-8,
// igual al proyectar la normalizada, y fija bajo Normalize y bajo Project; y es un
// subconjunto ordenado de la normalizada sin el tag.
func checkProjectInvariants(t testing.TB, c *Context, ps []*Projection, in []byte) {
	norm, nerr := c.Normalize(in)
	var head struct {
		ResourceType string `json:"resourceType"`
	}
	_ = json.Unmarshal(in, &head)
	for _, p := range ps {
		out, err := c.Project(in, p)
		if nerr != nil {
			if err == nil || out != nil {
				t.Fatalf("Normalize rejects %q and Project does not (%v)", in, err)
			}
			if p.rt == head.ResourceType && err.Error() != nerr.Error() {
				t.Fatalf("different errors:\n Project   %v\n Normalize %v", err, nerr)
			}
			continue
		}
		if p.rt != head.ResourceType {
			var e *Error
			var list *ErrorList
			if err == nil || !errorsAs(err, &list) || !errorsAs(err, &e) || e.Kind != KindProjectionMismatch {
				t.Fatalf("a projection of %s accepted %s (%v)", p.rt, head.ResourceType, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Normalize accepts %q and Project rejects it: %v", in, err)
		}
		if !utf8.Valid(out) || !json.Valid(out) {
			t.Fatalf("invalid output %q", out)
		}
		if fromNorm, err := c.Project(norm, p); err != nil || !bytes.Equal(fromNorm, out) {
			t.Fatalf("Project(x) != Project(Normalize(x)): %q %q (%v)", out, fromNorm, err)
		}
		if again, err := c.Normalize(out); err != nil || !bytes.Equal(again, out) {
			t.Fatalf("Normalize(Project(x)) != Project(x): %q %q (%v)", out, again, err)
		}
		if twice, err := c.Project(out, p); err != nil || !bytes.Equal(twice, out) {
			t.Fatalf("Project is not idempotent: %q %q (%v)", out, twice, err)
		}
		if p.mode == SummaryFalse && p.names == nil && !bytes.Equal(out, norm) {
			t.Fatalf("Project(x, false) != Normalize(x): %q %q", out, norm)
		}
		o, _ := parseOrdered(out)
		n, _ := parseOrdered(norm)
		if !isSubset(stripTag(o), stripTag(n)) {
			t.Fatalf("not an ordered subset:\n out %q\norm %q", out, norm)
		}
	}
}

func projSeeds() [][]byte {
	var out [][]byte
	for _, pi := range projInputs() {
		out = append(out, []byte(strings.ReplaceAll(pi.in, "NARR", narr)))
	}
	for _, tc := range normCases {
		out = append(out, []byte(tc.in))
	}
	return out
}

func FuzzProject(f *testing.F) {
	c := projContext(f, modelVersions[2], true)
	ps := projFuzzProjections(f, c)
	for _, s := range projSeeds() {
		f.Add(s)
	}
	f.Add([]byte(`{"resourceType":"Patient","meta":{"tag":[{"system":"` + systemSubsetted + `","code":"SUBSETTED"}]},"contained":[{"resourceType":"Patient","id":"a"}],"managingOrganization":{"reference":"#a"},"text":` + narr + `}`))
	f.Add([]byte(`{"resourceType":"Patient","name":[{"given":[null],"_given":[{"id":"x"}]}]}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		checkProjectInvariants(t, c, ps, in)
	})
}

// Las semillas corren como test (make check) en las tres versiones.
func TestProjectInvariantsOnTheSeeds(t *testing.T) {
	for _, v := range modelVersions {
		c := projContext(t, v, true)
		ps := projFuzzProjections(t, c)
		for _, s := range projSeeds() {
			checkProjectInvariants(t, c, ps, s)
		}
	}
}

// TestProjectIsConcurrent: Project es seguro para uso concurrente y da lo mismo
// que en serie (se corre con -race en make check).
func TestProjectIsConcurrent(t *testing.T) {
	c := projContext(t, modelVersions[2], true)
	ps := projFuzzProjections(t, c)
	seeds := projSeeds()
	type job struct {
		p  *Projection
		in []byte
	}
	var jobs []job
	var want [][]byte
	for _, p := range ps {
		for _, s := range seeds {
			jobs = append(jobs, job{p, s})
			out, _ := c.Project(s, p)
			want = append(want, out)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range jobs {
				k := (i + g*37) % len(jobs)
				out, _ := c.Project(jobs[k].in, jobs[k].p)
				if !bytes.Equal(out, want[k]) {
					t.Errorf("concurrent Project differs on job %d", k)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
