package fhirpkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// Los escenarios de este archivo son sintéticos y salen de las reglas del plan
// 002 (§3) y de los casos medidos de §8; los valores esperados están escritos a
// mano, no producidos por el código.

// j arma un recurso de conformidad: resourceType, url y pares clave-valor.
func j(rt, url string, kv ...any) string {
	m := map[string]any{"resourceType": rt, "url": url}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func fileSet(rs ...string) map[string]string {
	out := map[string]string{}
	for i, r := range rs {
		out[fmt.Sprintf("r%02d.json", i)] = r
	}
	return out
}

// hl7 es el canónico del package de los escenarios: todos publican bajo el
// mismo host, así que el paso del publicador no los separa salvo que un caso
// lo cambie.
const hl7 = "http://hl7.org/fhir"

func coreP(rs ...string) pkg {
	return pkg{name: "core", version: "4.3.0", typ: "Core", fhirVersions: []string{"4.3.0"}, canonical: hl7, files: fileSet(rs...)}
}

// igP es una IG de la serie del núcleo que depende de él.
func igP(name string, rs ...string) pkg {
	return pkg{name: name, version: "1.0.0", typ: "IG", fhirVersions: []string{"4.3.0"}, canonical: hl7,
		deps: map[string]string{"core": "4.3.0"}, files: fileSet(rs...)}
}

// buildCtx resuelve, abre y construye el Context de los packages, todos como
// raíces. Un package de otra serie que no es el núcleo se declara como excepción.
func buildCtx(t testing.TB, pkgs ...pkg) (*Context, *Set) {
	t.Helper()
	var roots []string
	var exc []Exception
	for _, p := range pkgs {
		roots = append(roots, p.id())
		if p.typ == "Core" || len(p.fhirVersions) == 0 {
			continue
		}
		if s, _ := series(p.fhirVersions[0]); s != "4.3" {
			exc = append(exc, Exception{Package: p.id(), Reason: "test"})
		}
	}
	reg := newMem(t, pkgs...)
	l := mustResolve(t, roots, reg, Options{Exceptions: exc})
	set, err := Open(l, reg.writeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewContext(set)
	if err != nil {
		t.Fatal(err)
	}
	return c, set
}

func conflictFor(t testing.TB, c *Context, kind Kind, rt, key string) Conflict {
	t.Helper()
	for _, cf := range c.Conflicts() {
		if cf.Kind == kind && cf.ResourceType == rt && cf.Key == key {
			return cf
		}
	}
	t.Fatalf("no conflict for %v %s %s in %v", kind, rt, key, c.Conflicts())
	return Conflict{}
}

func winnerOf(cf Conflict) string {
	if cf.Winner < 0 {
		return ""
	}
	return cf.Candidates[cf.Winner].Package
}

// wantWinner comprueba el ganador, el paso que decidió y que Resolve devuelve
// el mismo package.
func wantWinner(t *testing.T, c *Context, rt, url, winner string, by Step) {
	t.Helper()
	cf := conflictFor(t, c, KindCanonical, rt, url)
	if winnerOf(cf) != winner || cf.DecidedBy != by {
		t.Errorf("%s: winner %q by %q, want %q by %q (trace %+v)", url, winnerOf(cf), cf.DecidedBy, winner, by, cf.Trace)
	}
	r, err := c.Resolve(rt, url)
	if winner == "" {
		if !errors.Is(err, ErrUnresolved) {
			t.Errorf("Resolve(%s) = %+v, %v, want ErrUnresolved", url, r, err)
		}
		return
	}
	if err != nil || r.Package != winner {
		t.Errorf("Resolve(%s) = %q, %v, want %q", url, r.Package, err, winner)
	}
}

const (
	coreID = "core#4.3.0"
	aID    = "a#1.0.0"
	bID    = "b#1.0.0"
	cID    = "c#1.0.0"
)

// §3.1 paso 2 (A-M2): active > draft > desconocido > retired; el estado decide
// antes que la versión, así que el perdedor lleva la versión más alta.
func TestCascadeStatus(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/s"
	cases := []struct {
		first, second string
		winner        string
		by            Step
	}{
		{"active", "draft", coreID, StepStatus},
		{"draft", "active", aID, StepStatus},
		{"draft", "unknown", coreID, StepStatus},
		{"unknown", "draft", aID, StepStatus},
		{"unknown", "retired", coreID, StepStatus},
		{"retired", "unknown", aID, StepStatus},
		{"draft", "retired", coreID, StepStatus}, // A-M2: un retired más nuevo no le gana a un draft
		{"", "retired", coreID, StepStatus},      // sin status es desconocido
		{"made-up", "retired", coreID, StepStatus},
		{"active", "retired", coreID, StepStatus},
		{"active", "active", "", StepUnresolved},
		{"retired", "retired", "", StepUnresolved},
		{"", "unknown", "", StepUnresolved}, // los dos desconocidos
	}
	for _, tc := range cases {
		t.Run(tc.first+"/"+tc.second, func(t *testing.T) {
			// El perdedor lleva la versión mayor, así solo el estado puede
			// decidir; en un empate esperado, las dos versiones son iguales.
			second := "9.0.0"
			if tc.winner == "" {
				second = "1.0.0"
			}
			c, _ := buildCtx(t,
				coreP(j("ValueSet", url, "status", tc.first, "version", "1.0.0")),
				igP("a", j("ValueSet", url, "status", tc.second, "version", second)))
			wantWinner(t, c, "ValueSet", url, tc.winner, tc.by)
		})
	}
}

// §3.1 paso 3 (P-3, A-M1): publicador por host.
func TestCascadePublisher(t *testing.T) {
	const tx = "http://terminology.hl7.org/CodeSystem/v3-x"
	const ext = "http://hl7.org/fhir/StructureDefinition/ext"

	t.Run("terminology copied into the core loses to its publisher", func(t *testing.T) {
		// El núcleo (hl7.org) trae una copia de la terminología con la versión
		// más alta; el publicador (terminology.hl7.org) gana igual.
		term := igP("a", j("CodeSystem", tx, "status", "active", "version", "2.0.0"))
		term.canonical = "http://terminology.hl7.org"
		c, _ := buildCtx(t, coreP(j("CodeSystem", tx, "status", "active", "version", "3.0.0")), term)
		wantWinner(t, c, "CodeSystem", tx, aID, StepPublisher)
	})
	t.Run("same host does not separate, the version decides (A-M1)", func(t *testing.T) {
		// La IG de extensiones publica bajo hl7.org/fhir/StructureDefinition/ aunque su
		// canonical sea hl7.org/fhir/extensions: es el mismo host que el núcleo.
		e := igP("a", j("StructureDefinition", ext, "status", "active", "version", "2.0.0"))
		e.canonical = "http://hl7.org/fhir/extensions"
		c, _ := buildCtx(t, coreP(j("StructureDefinition", ext, "status", "active", "version", "1.0.0")), e)
		wantWinner(t, c, "StructureDefinition", ext, aID, StepVersion)
	})
	t.Run("host comparison ignores case", func(t *testing.T) {
		term := igP("a", j("CodeSystem", tx, "status", "active", "version", "2.0.0"))
		term.canonical = "HTTP://Terminology.HL7.org/"
		c, _ := buildCtx(t, coreP(j("CodeSystem", tx, "status", "active", "version", "3.0.0")), term)
		wantWinner(t, c, "CodeSystem", tx, aID, StepPublisher)
	})
	t.Run("two copies do not separate", func(t *testing.T) {
		x := igP("a", j("CodeSystem", tx, "status", "active", "version", "2.0.0"))
		x.canonical = "http://example.org"
		c, _ := buildCtx(t, coreP(j("CodeSystem", tx, "status", "active", "version", "3.0.0")), x)
		wantWinner(t, c, "CodeSystem", tx, coreID, StepVersion)
	})
	t.Run("a package without canonical is not a copy", func(t *testing.T) {
		x := igP("a", j("StructureDefinition", ext, "status", "active", "version", "2.0.0"))
		x.canonical = ""
		c, _ := buildCtx(t, coreP(j("StructureDefinition", ext, "status", "active", "version", "3.0.0")), x)
		wantWinner(t, c, "StructureDefinition", ext, coreID, StepVersion)
	})
	t.Run("urls without a host are not copies", func(t *testing.T) {
		const u = "urn:uuid:53fefa32-fcbb-4ff8-8a92-55ee120877b7"
		c, _ := buildCtx(t, coreP(j("ValueSet", u, "status", "active", "version", "1.0.0")), igP("a", j("ValueSet", u, "status", "active", "version", "2.0.0")))
		wantWinner(t, c, "ValueSet", u, aID, StepVersion)
	})
	t.Run("status decides before the publisher", func(t *testing.T) {
		term := igP("a", j("CodeSystem", tx, "status", "retired", "version", "2.0.0"))
		term.canonical = "http://terminology.hl7.org"
		c, _ := buildCtx(t, coreP(j("CodeSystem", tx, "status", "active", "version", "3.0.0")), term)
		wantWinner(t, c, "CodeSystem", tx, coreID, StepStatus)
	})
}

// §3.1 paso 1 (A-H2, A-H2b): la serie FHIR solo cuenta en los recursos que
// declaran su propio fhirVersion.
func TestCascadeSeries(t *testing.T) {
	const sd = "http://hl7.org/fhir/StructureDefinition/e"
	const vs = "http://hl7.org/fhir/ValueSet/v"
	r4 := func(name string, rs ...string) pkg {
		p := igP(name, rs...)
		p.fhirVersions = []string{"4.0.1"}
		return p
	}
	t.Run("a StructureDefinition of another series loses even with a higher version", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(),
			r4("a", j("StructureDefinition", sd, "status", "active", "version", "9.0.0", "fhirVersion", "4.0.1")),
			igP("b", j("StructureDefinition", sd, "status", "active", "version", "1.0.0", "fhirVersion", "4.3.0")))
		wantWinner(t, c, "StructureDefinition", sd, bID, StepSeries)
	})
	t.Run("series is major.minor of the declared version", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(),
			r4("a", j("StructureDefinition", sd, "status", "active", "version", "9.0.0", "fhirVersion", "4.0.1")),
			igP("b", j("StructureDefinition", sd, "status", "active", "version", "1.0.0", "fhirVersion", "4.3.2-snapshot1")))
		wantWinner(t, c, "StructureDefinition", sd, bID, StepSeries)
	})
	t.Run("a resource without fhirVersion never goes through the series step (A-H2b)", func(t *testing.T) {
		// El ValueSet de la IG de otra serie no declara fhirVersion: la serie
		// del package no lo separa y decide la versión.
		c, _ := buildCtx(t,
			coreP(j("ValueSet", vs, "status", "active", "version", "1.0.0")),
			r4("a", j("ValueSet", vs, "status", "active", "version", "2.0.0")))
		wantWinner(t, c, "ValueSet", vs, aID, StepVersion)
	})
	t.Run("if every candidate declares another series none is discarded", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(),
			r4("a", j("StructureDefinition", sd, "status", "active", "version", "1.0.0", "fhirVersion", "4.0.1")),
			r4("b", j("StructureDefinition", sd, "status", "active", "version", "2.0.0", "fhirVersion", "4.0.1")))
		wantWinner(t, c, "StructureDefinition", sd, bID, StepVersion)
	})
	t.Run("a list of versions that includes the core's series is kept (ImplementationGuide)", func(t *testing.T) {
		const ig = "http://hl7.org/fhir/ImplementationGuide/g"
		c, _ := buildCtx(t,
			coreP(),
			r4("a", j("ImplementationGuide", ig, "status", "active", "version", "9.0.0", "fhirVersion", []string{"4.0.1"})),
			igP("b", j("ImplementationGuide", ig, "status", "active", "version", "1.0.0", "fhirVersion", []string{"4.0.1", "4.3.0"})))
		wantWinner(t, c, "ImplementationGuide", ig, bID, StepSeries)
	})
	t.Run("an undeclared or unreadable series does not lose", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(),
			r4("a", j("StructureDefinition", sd, "status", "active", "version", "9.0.0", "fhirVersion", "4.0.1")),
			igP("b", j("StructureDefinition", sd, "status", "active", "version", "1.0.0", "fhirVersion", "R4B")),
			igP("c", j("StructureDefinition", sd, "status", "active", "version", "2.0.0")))
		cf := conflictFor(t, c, KindCanonical, "StructureDefinition", sd)
		// a pierde por la serie; b (serie ilegible) y c (sin serie) siguen, y la
		// versión decide: gana c (2.0.0).
		if winnerOf(cf) != cID || cf.DecidedBy != StepVersion {
			t.Errorf("winner %q by %q, trace %+v", winnerOf(cf), cf.DecidedBy, cf.Trace)
		}
	})
}

// §3.1 paso 4 (P-4): el comparador se elige para todo el conjunto.
func TestCascadeVersion(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/v"
	two := func(v1, v2 string, extra ...any) (*Context, *Set) {
		return buildCtx(t,
			coreP(j("ValueSet", url, append([]any{"status", "active", "version", v1}, extra...)...)),
			igP("a", j("ValueSet", url, append([]any{"status", "active", "version", v2}, extra...)...)))
	}
	cases := []struct {
		name, v1, v2 string
		winner       string
		by           Step
	}{
		{"semver numeric not textual", "1.2.0", "1.10.0", aID, StepVersion},
		{"release over prerelease", "1.0.0-rc.1", "1.0.0", aID, StepVersion},
		{"semver the other way", "2.0.0", "1.9.9", coreID, StepVersion},
		{"fhir dates", "2013-11", "2014", aID, StepVersion},
		{"fhir dates by day", "2013-11-15", "2013-11-03", coreID, StepVersion},
		{"2.9 against 3.0 is not comparable (plan 002 §1.3)", "2.9", "3.0", "", StepUnresolved},
		{"prose is not comparable", "May 2021 Edition", "Nov 2021 Edition", "", StepUnresolved},
		{"a missing version is not comparable", "", "1.0.0", "", StepUnresolved},
		{"semver against a date is not comparable", "1.0.0", "2014", "", StepUnresolved},
		{"equal versions", "1.0.0", "1.0.0", "", StepUnresolved},
		{"build metadata does not count", "1.0.0+a", "1.0.0+b", "", StepUnresolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := two(tc.v1, tc.v2)
			wantWinner(t, c, "ValueSet", url, tc.winner, tc.by)
		})
	}

	t.Run("the comparator is for the whole set: a non-semver version disables it", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(j("ValueSet", url, "status", "active", "version", "1.0.0")),
			igP("a", j("ValueSet", url, "status", "active", "version", "2.0.0")),
			igP("b", j("ValueSet", url, "status", "active", "version", "2.9")))
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
		if n := len(conflictFor(t, c, KindCanonical, "ValueSet", url).Candidates); n != 3 {
			t.Errorf("%d candidates, want 3", n)
		}
	})
	t.Run("a tie at the top goes on to the next step", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(j("ValueSet", url, "status", "active", "version", "1.0.0", "meta", map[string]any{"lastUpdated": "2020-01-01T00:00:00Z"})),
			igP("a", j("ValueSet", url, "status", "active", "version", "2.0.0", "meta", map[string]any{"lastUpdated": "2020-01-01T00:00:00Z"})),
			igP("b", j("ValueSet", url, "status", "active", "version", "2.0.0", "meta", map[string]any{"lastUpdated": "2021-01-01T00:00:00Z"})))
		wantWinner(t, c, "ValueSet", url, bID, StepLastUpdated)
	})

	// R5 versionAlgorithm[x] (resource.html): el Coding declara el orden.
	coding := func(code string) map[string]any {
		return map[string]any{"system": "http://hl7.org/fhir/version-algorithm", "code": code}
	}
	t.Run("versionAlgorithm natural", func(t *testing.T) {
		c, _ := two("v2", "v10", "versionAlgorithmCoding", coding("natural"))
		wantWinner(t, c, "ValueSet", url, aID, StepVersion)
	})
	t.Run("versionAlgorithm integer", func(t *testing.T) {
		c, _ := two("10", "9", "versionAlgorithmCoding", coding("integer"))
		wantWinner(t, c, "ValueSet", url, coreID, StepVersion)
	})
	t.Run("versionAlgorithm alpha", func(t *testing.T) {
		c, _ := two("a", "b", "versionAlgorithmCoding", coding("alpha"))
		wantWinner(t, c, "ValueSet", url, aID, StepVersion)
	})
	t.Run("versionAlgorithm date", func(t *testing.T) {
		c, _ := two("2013-11", "2014", "versionAlgorithmCoding", coding("date"))
		wantWinner(t, c, "ValueSet", url, aID, StepVersion)
	})
	t.Run("versionAlgorithm semver", func(t *testing.T) {
		c, _ := two("1.9.0", "1.10.0", "versionAlgorithmCoding", coding("semver"))
		wantWinner(t, c, "ValueSet", url, aID, StepVersion)
	})
	t.Run("versionAlgorithm Coding of another system is not the algorithm (code review B4)", func(t *testing.T) {
		// v2 y v10 solo se ordenan si el Coding es del CodeSystem version-algorithm.
		c, _ := two("v2", "v10", "versionAlgorithmCoding", map[string]any{"system": "http://example.org/own-algorithms", "code": "natural"})
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
		c, _ = two("v2", "v10", "versionAlgorithmCoding", map[string]any{"code": "natural"})
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
	})
	t.Run("versionAlgorithm as a FHIRPath expression is not comparable", func(t *testing.T) {
		c, _ := two("1.0.0", "2.0.0", "versionAlgorithmString", "$this.toInteger()")
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
	})
	t.Run("different algorithms fall back to inference", func(t *testing.T) {
		c, _ := buildCtx(t,
			coreP(j("ValueSet", url, "status", "active", "version", "1.0.0", "versionAlgorithmCoding", coding("natural"))),
			igP("a", j("ValueSet", url, "status", "active", "version", "2.0.0", "versionAlgorithmCoding", coding("alpha"))))
		wantWinner(t, c, "ValueSet", url, aID, StepVersion)
	})
	t.Run("a declared algorithm that the versions do not follow falls back to inference", func(t *testing.T) {
		c, _ := two("1.2.0", "1.10.0", "versionAlgorithmCoding", coding("integer"))
		wantWinner(t, c, "ValueSet", url, aID, StepVersion)
	})
}

// §3.1 paso 5: meta.lastUpdated como instante.
func TestCascadeLastUpdated(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/l"
	lu := func(s string) map[string]any { return map[string]any{"lastUpdated": s} }
	build := func(a, b any) *Context {
		c, _ := buildCtx(t,
			coreP(j("ValueSet", url, "status", "active", "version", "1.0", "meta", a)),
			igP("a", j("ValueSet", url, "status", "active", "version", "1.1", "meta", b)))
		return c
	}
	t.Run("instants, not text", func(t *testing.T) {
		// 10:00+01:00 es 09:00Z; el texto lo pondría después de 09:30Z.
		c := build(lu("2020-01-01T10:00:00+01:00"), lu("2020-01-01T09:30:00Z"))
		wantWinner(t, c, "ValueSet", url, aID, StepLastUpdated)
	})
	t.Run("fractions of a second", func(t *testing.T) {
		c := build(lu("2020-01-01T09:30:00.500Z"), lu("2020-01-01T09:30:00.250Z"))
		wantWinner(t, c, "ValueSet", url, coreID, StepLastUpdated)
	})
	t.Run("the same instant in two zones is a tie", func(t *testing.T) {
		c := build(lu("2020-01-01T10:00:00+01:00"), lu("2020-01-01T09:00:00Z"))
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
	})
	t.Run("a candidate without instant: the step does not separate", func(t *testing.T) {
		c := build(lu("2020-01-01T10:00:00Z"), map[string]any{})
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
	})
	t.Run("an unreadable instant: the step does not separate", func(t *testing.T) {
		c := build(lu("yesterday"), lu("2020-01-01T10:00:00Z"))
		wantWinner(t, c, "ValueSet", url, "", StepUnresolved)
	})
}

// §3.1 paso 6 y §2 B: lo que devuelve Resolve cuando no hay ganador.
func TestResolveUnresolvedAndVersioned(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/u"
	c, _ := buildCtx(t,
		coreP(j("ValueSet", url, "status", "active", "version", "2.9")),
		igP("a", j("ValueSet", url, "status", "active", "version", "3.0")),
		igP("b", j("ValueSet", url, "status", "active", "version", "3.0")))
	cf := conflictFor(t, c, KindCanonical, "ValueSet", url)
	if cf.Winner != -1 || cf.DecidedBy != StepUnresolved || len(cf.Candidates) != 3 {
		t.Fatalf("%+v", cf)
	}
	_, err := c.Resolve("ValueSet", url)
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("Resolve without version: %v", err)
	}
	for _, want := range []string{url, coreID, aID, bID, "version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	// Con versión, resuelve si es única.
	r, err := c.Resolve("ValueSet", url+"|2.9")
	if err != nil || r.Package != coreID || r.Resource.Version != "2.9" {
		t.Errorf("url|2.9: %+v, %v", r, err)
	}
	// Dos candidatos con esa versión: no es única.
	if _, err := c.Resolve("ValueSet", url+"|3.0"); !errors.Is(err, ErrUnresolved) || !strings.Contains(err.Error(), aID) {
		t.Errorf("url|3.0: %v", err)
	}
	if _, err := c.Resolve("ValueSet", url+"|7.7"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown version: %v", err)
	}
	if _, err := c.Resolve("ValueSet", url+"-nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown url: %v", err)
	}
	if _, err := c.Resolve("ValueSet", ""); err == nil {
		t.Error("empty canonical accepted")
	}
	// La explicación sale del mismo código que decide.
	ex, err := c.Explain("ValueSet", url)
	if err != nil || ex.DecidedBy != StepUnresolved || len(ex.Trace) == 0 {
		t.Fatalf("Explain: %+v, %v", ex, err)
	}
	if _, err := c.Explain("ValueSet", url+"|2.9"); err == nil {
		t.Error("Explain accepted a version")
	}
	if _, err := c.Explain("ValueSet", url+"-nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Explain of an unknown url: %v", err)
	}
}

// Un canónico sin repetir no es un conflicto, pero se explica y se resuelve.
func TestExplainSingleCandidate(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/one"
	c, _ := buildCtx(t, coreP(j("ValueSet", url, "version", "1")))
	for _, cf := range c.Conflicts() {
		t.Errorf("unexpected conflict %+v", cf)
	}
	ex, err := c.Explain("ValueSet", url)
	if err != nil || ex.DecidedBy != StepSingle || ex.Winner != 0 || len(ex.Candidates) != 1 || len(ex.Trace) != 0 {
		t.Errorf("%+v, %v", ex, err)
	}
	if r, err := c.Resolve("ValueSet", url); err != nil || r.Package != coreID {
		t.Errorf("%+v, %v", r, err)
	}
}

// A-M3: la clave es (resourceType, url).
func TestKeyIsTypeAndURL(t *testing.T) {
	const url = "urn:uuid:53fefa32-fcbb-4ff8-8a92-55ee120877b7"
	c, _ := buildCtx(t,
		coreP(j("CapabilityStatement", url, "status", "active", "version", "1.0.0")),
		igP("a", j("TerminologyCapabilities", url, "status", "active", "version", "2.0.0")))
	if cfs := c.Conflicts(); len(cfs) != 0 {
		t.Fatalf("two types with the same URL do not compete: %+v", cfs)
	}
	if r, err := c.Resolve("CapabilityStatement", url); err != nil || r.Package != coreID {
		t.Errorf("%+v, %v", r, err)
	}
	if r, err := c.Resolve("TerminologyCapabilities", url); err != nil || r.Package != aID {
		t.Errorf("%+v, %v", r, err)
	}
	// Sin tipo es ambiguo, y lo dice.
	_, err := c.Resolve("", url)
	if err == nil || !strings.Contains(err.Error(), "CapabilityStatement") || !strings.Contains(err.Error(), "TerminologyCapabilities") {
		t.Errorf("Resolve without a type: %v", err)
	}
	// Sin tipo y sin ambigüedad, resuelve.
	c2, _ := buildCtx(t, coreP(j("ValueSet", "http://hl7.org/fhir/ValueSet/q", "version", "1")))
	if r, err := c2.Resolve("", "http://hl7.org/fhir/ValueSet/q"); err != nil || r.Resource.ResourceType != "ValueSet" {
		t.Errorf("%+v, %v", r, err)
	}
}

// Referencias a un recurso contenido: url#fragmento (R4 references.html §2.3.0.6).
func TestResolveFragment(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/c"
	c, _ := buildCtx(t, coreP(j("ValueSet", url, "version", "1.0.0", "contained", []any{
		map[string]any{"resourceType": "CodeSystem", "id": "cs1", "url": "http://x/cs", "version": "7"},
		map[string]any{"resourceType": "ValueSet", "id": "vs2"},
	})))
	r, err := c.Resolve("ValueSet", url+"#cs1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Fragment != "cs1" || r.Resource.ResourceType != "CodeSystem" || r.Resource.URL != "http://x/cs" || r.Resource.Version != "7" || r.Package != coreID {
		t.Errorf("%+v", r)
	}
	var m map[string]any
	if err := json.Unmarshal(r.Resource.Raw, &m); err != nil || m["id"] != "cs1" {
		t.Errorf("raw %s: %v", r.Resource.Raw, err)
	}
	// Con versión y fragmento.
	if r, err := c.Resolve("ValueSet", url+"|1.0.0#vs2"); err != nil || r.Resource.ResourceType != "ValueSet" || r.Fragment != "vs2" {
		t.Errorf("%+v, %v", r, err)
	}
	if _, err := c.Resolve("ValueSet", url+"#missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing fragment: %v", err)
	}
	// Un fragmento solo no dice de qué recurso: es error.
	_, err = c.Resolve("ValueSet", "#cs1")
	wantErr(t, err, "fragment of the resource that contains it")
}

// §2 B: ResolveFrom ve solo el cierre de dependencias de quien pregunta.
func TestResolveFromRestrictsToTheDependencyGraph(t *testing.T) {
	const u = "http://hl7.org/fhir/ValueSet/g"
	const onlyA = "http://hl7.org/fhir/ValueSet/only-a"
	mk := func(version string) string { return j("ValueSet", u, "status", "active", "version", version) }
	a := igP("a", mk("2.0.0"), j("ValueSet", onlyA, "version", "1"))
	b := igP("b", mk("3.0.0"))
	cc := igP("c", mk("1.5.0"))
	cc.deps = map[string]string{"a": "1.0.0"} // c depende de a, y a del núcleo
	c, _ := buildCtx(t, coreP(mk("2.5.0")), a, b, cc)

	// Visión global: gana la versión mayor (b).
	wantWinner(t, c, "ValueSet", u, bID, StepVersion)
	for _, tc := range []struct{ from, want string }{
		{coreID, coreID}, // solo se ve a sí mismo
		{aID, coreID},    // a y el núcleo: gana el núcleo (2.5.0)
		{bID, bID},       // b y el núcleo: gana b (3.0.0)
		{cID, coreID},    // c, a y el núcleo (cierre transitivo): gana el núcleo (2.5.0)
	} {
		r, err := c.ResolveFrom(tc.from, "ValueSet", u)
		if err != nil || r.Package != tc.want {
			t.Errorf("ResolveFrom(%s) = %q, %v, want %q", tc.from, r.Package, err, tc.want)
		}
	}
	// Lo que solo define a no lo ve b, ni el núcleo; c sí, por a.
	for _, tc := range []struct {
		from string
		ok   bool
	}{{aID, true}, {cID, true}, {bID, false}, {coreID, false}} {
		_, err := c.ResolveFrom(tc.from, "ValueSet", onlyA)
		if tc.ok && err != nil || !tc.ok && !errors.Is(err, ErrNotFound) {
			t.Errorf("ResolveFrom(%s, only-a): %v", tc.from, err)
		}
	}
	// El global ve todo.
	if r, err := c.Resolve("ValueSet", onlyA); err != nil || r.Package != aID {
		t.Errorf("%+v, %v", r, err)
	}
	if _, err := c.ResolveFrom("nope#1", "ValueSet", u); err == nil {
		t.Error("unknown package accepted")
	}
	// Con versión: se busca solo entre los visibles.
	if _, err := c.ResolveFrom(bID, "ValueSet", u+"|2.0.0"); !errors.Is(err, ErrNotFound) {
		t.Errorf("b does not see a's 2.0.0: %v", err)
	}
	if r, err := c.ResolveFrom(cID, "ValueSet", u+"|2.0.0"); err != nil || r.Package != aID {
		t.Errorf("%+v, %v", r, err)
	}
}

// Un empate global puede no serlo desde un package que ve menos candidatos.
func TestResolveFromCanResolveWhatIsGloballyUnresolved(t *testing.T) {
	const u = "http://hl7.org/fhir/ValueSet/t"
	mk := func() string { return j("ValueSet", u, "status", "active", "version", "1.0.0") }
	c, _ := buildCtx(t, coreP(), igP("a", mk()), igP("b", mk()))
	wantWinner(t, c, "ValueSet", u, "", StepUnresolved)
	for _, id := range []string{aID, bID} {
		if r, err := c.ResolveFrom(id, "ValueSet", u); err != nil || r.Package != id {
			t.Errorf("ResolveFrom(%s) = %+v, %v", id, r, err)
		}
	}
	if _, err := c.ResolveFrom(coreID, "ValueSet", u); !errors.Is(err, ErrNotFound) {
		t.Errorf("core: %v", err)
	}
}

// El índice referencia los bytes del Set: no los copia.
func TestResolveDoesNotCopyBytes(t *testing.T) {
	const u = "http://hl7.org/fhir/ValueSet/n"
	c, set := buildCtx(t, coreP(j("ValueSet", u, "version", "1")))
	r, err := c.Resolve("ValueSet", u)
	if err != nil {
		t.Fatal(err)
	}
	var orig *byte
	for _, x := range set.Resources(coreID) {
		if x.URL == u {
			orig = &x.Raw[0]
		}
	}
	if orig == nil || &r.Resource.Raw[0] != orig {
		t.Error("Resolve returned a copy of the resource bytes")
	}
}

func TestNewContextRejectsWhatItCannotRead(t *testing.T) {
	if _, err := NewContext(nil); err == nil {
		t.Error("nil Set accepted")
	}
	build := func(rs ...string) error {
		reg := newMem(t, coreP(rs...))
		l := mustResolve(t, []string{coreID}, reg, Options{})
		set, err := Open(l, reg.writeDir(t))
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewContext(set)
		return err
	}
	// Se leen los recursos que compiten (misma url en dos packages) y los
	// SearchParameter y StructureDefinition.
	dup := func(field string, v any) error {
		reg := newMem(t, coreP(j("ValueSet", "http://x/a", field, v)), igP("a", j("ValueSet", "http://x/a", "version", "2")))
		l := mustResolve(t, []string{coreID, aID}, reg, Options{})
		set, err := Open(l, reg.writeDir(t))
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewContext(set)
		return err
	}
	err := dup("status", 5)
	if err == nil || !strings.Contains(err.Error(), coreID) || !strings.Contains(err.Error(), "r00.json") {
		t.Errorf("status of the wrong type: %v", err)
	}
	if err := dup("meta", "x"); err == nil {
		t.Error("meta of the wrong type accepted")
	}
	if err := dup("experimental", "yes"); err == nil {
		t.Error("experimental of the wrong type accepted")
	}
	if err := dup("fhirVersion", 4); err == nil {
		t.Error("fhirVersion of the wrong type accepted")
	}
	if err := build(j("SearchParameter", "http://x/sp", "status", 5, "code", "c", "base", []string{"X"})); err == nil {
		t.Error("SearchParameter status of the wrong type accepted")
	}
	if err := build(j("StructureDefinition", "http://x/sd", "kind", 5)); err == nil {
		t.Error("StructureDefinition kind of the wrong type accepted")
	}
}

// El Context es inmutable: se usa desde varias goroutines (go test -race).
func TestContextIsConcurrent(t *testing.T) {
	const u = "http://hl7.org/fhir/ValueSet/k"
	c, _ := buildCtx(t, sdFixtureCore(), igP("a", j("ValueSet", u, "status", "draft", "version", "1.0.0")))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = c.Resolve("ValueSet", u)
				_, _ = c.ResolveFrom(aID, "ValueSet", u+"|1.0.0")
				_, _ = c.Explain("ValueSet", u)
				_ = c.Conflicts()
				_ = c.SearchParameters("Observation")
				_ = c.ResourceTypes()
			}
		}()
	}
	wg.Wait()
}

// Metamórfico: el orden de los packages del Set (y por tanto el de los
// candidatos) no cambia ni el ganador ni Conflicts().
func TestCandidateOrderDoesNotChangeTheResult(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/m"
	const url2 = "http://hl7.org/fhir/ValueSet/m2"
	mk := func(u, v, status string) string { return j("ValueSet", u, "status", status, "version", v) }
	term := igP("d", j("ValueSet", url, "status", "active", "version", "9.0.0"))
	term.canonical = "http://terminology.hl7.org"
	_, set := buildCtx(t,
		coreP(mk(url, "1.0.0", "draft"), mk(url2, "1.0.0", "active")),
		igP("a", mk(url, "2.0.0", "active"), mk(url2, "1.0.0", "active")),
		igP("b", mk(url, "3.0.0", "retired")),
		igP("c", mk(url, "2.0.0", "active")),
		term)
	base, err := NewContext(set)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint(base.Conflicts())
	ids := make([]string, 0, len(set.packages))
	for _, p := range set.packages {
		ids = append(ids, p.ID)
	}
	permute(ids, func(order []string) {
		perm := &Set{lock: set.lock, resources: set.resources}
		byID := map[string]Package{}
		for _, p := range set.packages {
			byID[p.ID] = p
		}
		for _, id := range order {
			perm.packages = append(perm.packages, byID[id])
		}
		c, err := NewContext(perm)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(c.Conflicts()); got != want {
			t.Fatalf("order %v changes Conflicts():\n%s\nwant\n%s", order, got, want)
		}
		for _, u := range []string{url, url2} {
			r1, e1 := base.Resolve("ValueSet", u)
			r2, e2 := c.Resolve("ValueSet", u)
			if r1.Package != r2.Package || (e1 == nil) != (e2 == nil) {
				t.Fatalf("order %v changes the winner of %s", order, u)
			}
		}
	})
}

// permute llama a f con cada permutación de xs.
func permute(xs []string, f func([]string)) {
	var rec func(k int)
	cur := slices.Clone(xs)
	rec = func(k int) {
		if k == len(cur) {
			f(slices.Clone(cur))
			return
		}
		for i := k; i < len(cur); i++ {
			cur[k], cur[i] = cur[i], cur[k]
			rec(k + 1)
			cur[k], cur[i] = cur[i], cur[k]
		}
	}
	rec(0)
}

// Conflicts() devuelve una copia: modificarla no cambia el Context.
func TestConflictsReturnsACopy(t *testing.T) {
	const u = "http://hl7.org/fhir/ValueSet/copy"
	c, _ := buildCtx(t,
		coreP(j("ValueSet", u, "status", "active", "version", "1.0.0")),
		igP("a", j("ValueSet", u, "status", "draft", "version", "2.0.0")))
	first := c.Conflicts()
	first[0].Candidates[0].Package = "changed"
	first[0].Trace[0].Out = 99
	first[0].Winner = 7
	again := c.Conflicts()
	if again[0].Candidates[0].Package != aID || again[0].Trace[0].Out == 99 || again[0].Winner == 7 {
		t.Errorf("Conflicts() shares its data: %+v", again[0])
	}
	ex, err := c.Explain("ValueSet", u)
	if err != nil {
		t.Fatal(err)
	}
	ex.Candidates[0].Package = "changed"
	ex.Trace[0].Out = 99
	if ex2, _ := c.Explain("ValueSet", u); ex2.Candidates[0].Package != aID || ex2.Trace[0].Out == 99 {
		t.Errorf("Explain shares its data: %+v", ex2)
	}
}

// Code review ronda 1, M3 y B3: la visibilidad cuenta al elegir el tipo, y la
// ambigüedad de tipo se informa con su sentinel.
func TestTypeSelectionRespectsVisibilityAndReportsAmbiguity(t *testing.T) {
	const url = "urn:uuid:53fefa32-fcbb-4ff8-8a92-55ee120877b7"
	c, _ := buildCtx(t,
		coreP(j("CapabilityStatement", url, "status", "active", "version", "1.0.0")),
		igP("a", j("TerminologyCapabilities", url, "status", "active", "version", "2.0.0")))
	// Desde el núcleo solo se ve uno de los dos tipos: resuelve sin tipo.
	r, err := c.ResolveFrom(coreID, "", url)
	if err != nil || r.Resource.ResourceType != "CapabilityStatement" {
		t.Errorf("ResolveFrom(core): %+v, %v", r, err)
	}
	// Desde a se ven los dos: ambiguo.
	if _, err := c.ResolveFrom(aID, "", url); !errors.Is(err, ErrAmbiguousType) {
		t.Errorf("ResolveFrom(a): %v", err)
	}
	if _, err := c.Resolve("", url); !errors.Is(err, ErrAmbiguousType) {
		t.Errorf("Resolve: %v", err)
	}
	// Explain sin tipo no toma el primero.
	_, err = c.Explain("", url)
	if !errors.Is(err, ErrAmbiguousType) || !strings.Contains(err.Error(), "TerminologyCapabilities") {
		t.Errorf("Explain: %v", err)
	}
	if ex, err := c.Explain("CapabilityStatement", url); err != nil || ex.ResourceType != "CapabilityStatement" {
		t.Errorf("Explain with a type: %+v, %v", ex, err)
	}
}

// Code review ronda 1, B1: Explain de un canónico con un solo candidato que no
// es SearchParameter ni StructureDefinition da su status.
func TestExplainSingleCandidateHasItsStatus(t *testing.T) {
	const url = "http://hl7.org/fhir/ValueSet/status-only"
	c, _ := buildCtx(t, coreP(j("ValueSet", url, "status", "draft", "version", "1")))
	ex, err := c.Explain("ValueSet", url)
	if err != nil || ex.Candidates[0].Status != "draft" {
		t.Errorf("%+v, %v", ex, err)
	}
	// Los conflictos no cambian.
	if len(c.Conflicts()) != 0 {
		t.Error("a single candidate is a conflict")
	}
}
