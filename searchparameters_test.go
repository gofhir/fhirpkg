package fhirpkg

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	sdBase = "http://hl7.org/fhir/StructureDefinition/"
	spBase = "http://hl7.org/fhir/SearchParameter/"
)

func sd(name, parent string, extra ...any) string {
	kv := []any{"kind", "resource", "type", name, "status", "active", "version", "4.3.0"}
	if parent != "" {
		// La raíz de la herencia en R4 y R4B no declara derivation.
		kv = append(kv, "derivation", "specialization", "baseDefinition", sdBase+parent)
	}
	return j("StructureDefinition", sdBase+name, append(kv, extra...)...)
}

func sp(id, code string, bases []string, extra ...any) string {
	kv := []any{"code", code, "base", bases, "type", "token", "status", "active", "version", "4.3.0"}
	return j("SearchParameter", spBase+id, append(kv, extra...)...)
}

// sdFixtureCore es un núcleo mínimo: Resource → DomainResource → Observation y
// Patient, con las "trampas" de un núcleo real: un perfil de Observation (kind
// resource, derivation constraint) y un modelo lógico.
func sdFixtureCore(extra ...string) pkg {
	rs := []string{
		sd("Resource", ""),
		sd("DomainResource", "Resource"),
		sd("Observation", "DomainResource"),
		sd("Patient", "DomainResource"),
		// Un perfil comparte `type` con el recurso, pero no define un tipo.
		j("StructureDefinition", sdBase+"vitalsigns", "kind", "resource", "derivation", "constraint", "type", "Observation", "baseDefinition", sdBase+"Observation", "status", "active", "version", "4.3.0"),
		// Un modelo lógico no es un tipo de recurso.
		j("StructureDefinition", sdBase+"Foo", "kind", "logical", "derivation", "specialization", "type", "Foo", "status", "active", "version", "4.3.0"),
		sp("Resource-id", "_id", []string{"Resource"}),
		sp("Resource-lastUpdated", "_lastUpdated", []string{"Resource"}),
		sp("DomainResource-text", "_text", []string{"DomainResource"}),
		sp("DomainResource-identifier", "identifier", []string{"DomainResource"}),
		sp("Observation-code", "code", []string{"Observation"}),
		sp("Observation-identifier", "identifier", []string{"Observation"}),
		sp("clinical-date", "date", []string{"Observation", "Patient"}),
		sp("Patient-name", "name", []string{"Patient"}),
	}
	return coreP(append(rs, extra...)...)
}

// replaceIn cambia el archivo de p que contiene url por body.
func replaceIn(p pkg, url, body string) pkg {
	for k, v := range p.files {
		if strings.Contains(v, `"`+url+`"`) {
			p.files[k] = body
			return p
		}
	}
	panic("no file with " + url)
}

func spCodes(sps []SearchParameter) []string {
	var out []string
	for _, s := range sps {
		out = append(out, s.Code)
	}
	return out
}

// §3.2: la herencia sigue baseDefinition; el del tipo le gana al heredado.
func TestSearchParametersInheritance(t *testing.T) {
	c, _ := buildCtx(t, sdFixtureCore())
	want := map[string][]string{
		// Por nivel, del tipo hacia Resource; dentro del nivel, por code.
		"Observation":    {"code", "date", "identifier", "_text", "_id", "_lastUpdated"},
		"Patient":        {"date", "name", "_text", "identifier", "_id", "_lastUpdated"},
		"DomainResource": {"_text", "identifier", "_id", "_lastUpdated"},
		"Resource":       {"_id", "_lastUpdated"},
	}
	for rt, codes := range want {
		if got := spCodes(c.SearchParameters(rt)); !slices.Equal(got, codes) {
			t.Errorf("%s: %v, want %v", rt, got, codes)
		}
	}
	// El identifier de Observation es el del tipo, no el de DomainResource.
	for _, s := range c.SearchParameters("Observation") {
		if s.Code == "identifier" && s.URL != spBase+"Observation-identifier" {
			t.Errorf("identifier comes from %s", s.URL)
		}
		if s.Package != coreID || s.Version != "4.3.0" || len(s.Base) == 0 || len(s.Raw) == 0 {
			t.Errorf("%+v", s)
		}
	}
	// Un SearchParameter con varias bases está en cada una.
	for _, rt := range []string{"Observation", "Patient"} {
		if !slices.Contains(spCodes(c.SearchParameters(rt)), "date") {
			t.Errorf("%s lacks the SearchParameter of two bases", rt)
		}
	}
	if got := c.ResourceTypes(); !slices.Equal(got, []string{"DomainResource", "Observation", "Patient", "Resource"}) {
		t.Errorf("ResourceTypes = %v: profiles and logical models are not resource types", got)
	}
	if got := c.SearchParameters("Foo"); got != nil {
		t.Errorf("a logical model has search parameters: %v", got)
	}
	if got := c.SearchParameters("Nope"); got != nil {
		t.Errorf("unknown type: %v", got)
	}
	// El resultado es una copia: modificarlo no afecta al Context.
	got := c.SearchParameters("Patient")
	got[0].Code = "changed"
	if c.SearchParameters("Patient")[0].Code != "date" {
		t.Error("SearchParameters returns the Context's own slice")
	}
}

func TestSearchParametersBaseCodeRules(t *testing.T) {
	obsCode := func(id string, extra ...any) string {
		return sp(id, "code", []string{"Observation"}, extra...)
	}
	// pick devuelve el SearchParameter con ese code para Observation.
	pick := func(c *Context, code string) (SearchParameter, bool) {
		for _, s := range c.SearchParameters("Observation") {
			if s.Code == code {
				return s, true
			}
		}
		return SearchParameter{}, false
	}
	t.Run("a non-experimental one beats an experimental one (A-H1)", func(t *testing.T) {
		// El ejemplo de la raíz del núcleo (experimental) contra el normativo.
		c, _ := buildCtx(t, sdFixtureCore(obsCode("example", "experimental", true)))
		s, ok := pick(c, "code")
		if !ok || s.URL != spBase+"Observation-code" {
			t.Errorf("%+v, %v", s, ok)
		}
		cf := conflictFor(t, c, KindSearchParameter, "", "Observation:code")
		if winnerOf(cf) != coreID || cf.DecidedBy != StepNotExperimental || len(cf.Candidates) != 2 {
			t.Errorf("%+v", cf)
		}
	})
	t.Run("the non-experimental one wins even from an IG over the core", func(t *testing.T) {
		core := replaceIn(sdFixtureCore(), spBase+"Observation-code", sp("Observation-code", "code", []string{"Observation"}, "experimental", true))
		c, _ := buildCtx(t, core, igP("a", obsCode("ig-code")))
		s, ok := pick(c, "code")
		if !ok || s.URL != spBase+"ig-code" || s.Package != aID {
			t.Errorf("%+v, %v", s, ok)
		}
		if cf := conflictFor(t, c, KindSearchParameter, "", "Observation:code"); cf.DecidedBy != StepNotExperimental || winnerOf(cf) != aID {
			t.Errorf("%+v", cf)
		}
	})
	t.Run("the core beats an IG (LINEAMIENTOS 4.2)", func(t *testing.T) {
		c, _ := buildCtx(t, sdFixtureCore(), igP("a", obsCode("ig-code")))
		s, ok := pick(c, "code")
		if !ok || s.URL != spBase+"Observation-code" || s.Package != coreID {
			t.Errorf("%+v, %v", s, ok)
		}
		if cf := conflictFor(t, c, KindSearchParameter, "", "Observation:code"); cf.DecidedBy != StepCore || winnerOf(cf) != coreID {
			t.Errorf("%+v", cf)
		}
	})
	t.Run("two IGs with the same base:code are unresolved and the code is excluded", func(t *testing.T) {
		c, _ := buildCtx(t, sdFixtureCore(), igP("a", sp("ig-a", "foo", []string{"Observation"})), igP("b", sp("ig-b", "foo", []string{"Observation", "Patient"})))
		if _, ok := pick(c, "foo"); ok {
			t.Error("an unresolved code is still listed")
		}
		cf := conflictFor(t, c, KindSearchParameter, "", "Observation:foo")
		if cf.Winner != -1 || cf.DecidedBy != StepUnresolved || len(cf.Candidates) != 2 {
			t.Errorf("%+v", cf)
		}
		// En Patient solo hay uno con ese base:code: no es un conflicto.
		var found bool
		for _, s := range c.SearchParameters("Patient") {
			found = found || s.Code == "foo" && s.Package == bID
		}
		if !found {
			t.Error("Patient lost the SearchParameter that does not conflict there")
		}
		for _, x := range c.Conflicts() {
			if x.Kind == KindSearchParameter && x.Key == "Patient:foo" {
				t.Error("Patient:foo reported as a conflict")
			}
		}
	})
	t.Run("both experimental: the core still beats the IG", func(t *testing.T) {
		core := replaceIn(sdFixtureCore(), spBase+"Observation-code", sp("Observation-code", "code", []string{"Observation"}, "experimental", true))
		c, _ := buildCtx(t, core, igP("a", obsCode("ig-code", "experimental", true)))
		cf := conflictFor(t, c, KindSearchParameter, "", "Observation:code")
		if cf.DecidedBy != StepCore || winnerOf(cf) != coreID {
			t.Errorf("%+v", cf)
		}
	})
	t.Run("an unresolved base:code blocks the same code in the ancestors", func(t *testing.T) {
		// Observation:_id queda sin resolver (dos IG): Observation no recibe el
		// _id de Resource; Patient sí.
		c, _ := buildCtx(t, sdFixtureCore(),
			igP("a", sp("ig-a", "_id", []string{"Observation"})),
			igP("b", sp("ig-b", "_id", []string{"Observation"})))
		if _, ok := pick(c, "_id"); ok {
			t.Error("Observation still has _id")
		}
		if !slices.Contains(spCodes(c.SearchParameters("Patient")), "_id") {
			t.Error("Patient lost _id")
		}
	})
	t.Run("the same URL in two packages is a canonical decided by the cascade (R4: 13 SearchParameter)", func(t *testing.T) {
		// draft en el núcleo, active en la IG, misma URL.
		core := replaceIn(sdFixtureCore(), spBase+"Observation-code", sp("Observation-code", "code", []string{"Observation"}, "status", "draft"))
		c, _ := buildCtx(t, core, igP("a", sp("Observation-code", "code", []string{"Observation"}, "version", "5.0.0")))
		s, ok := pick(c, "code")
		if !ok || s.Package != aID {
			t.Errorf("%+v, %v", s, ok)
		}
		cf := conflictFor(t, c, KindCanonical, "SearchParameter", spBase+"Observation-code")
		if winnerOf(cf) != aID || cf.DecidedBy != StepStatus {
			t.Errorf("%+v", cf)
		}
		// Una sola definición por URL: no hay conflicto de base:code.
		for _, x := range c.Conflicts() {
			if x.Kind == KindSearchParameter {
				t.Errorf("unexpected %+v", x)
			}
		}
	})
}

func TestNewContextRejectsBrokenInheritance(t *testing.T) {
	try := func(pkgs ...pkg) error {
		reg := newMem(t, pkgs...)
		var roots []string
		for _, p := range pkgs {
			roots = append(roots, p.id())
		}
		l := mustResolve(t, roots, reg, Options{})
		set, err := Open(l, reg.writeDir(t))
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewContext(set)
		return err
	}
	t.Run("a baseDefinition that no package defines", func(t *testing.T) {
		err := try(coreP(sd("Observation", "DomainResource")))
		wantErr(t, err, "Observation", "base definition", sdBase+"DomainResource")
	})
	t.Run("two definitions claim the same type", func(t *testing.T) {
		other := j("StructureDefinition", "http://example.org/StructureDefinition/Observation2", "kind", "resource", "derivation", "specialization", "type", "Observation", "status", "active")
		err := try(coreP(sd("Observation", ""), other))
		wantErr(t, err, "Observation", "more than one")
	})
	t.Run("an inheritance cycle", func(t *testing.T) {
		err := try(coreP(sd("A", "B"), sd("B", "A")))
		wantErr(t, err, "cycle")
	})
	t.Run("a SearchParameter with a base but no code", func(t *testing.T) {
		err := try(coreP(j("SearchParameter", spBase+"x", "base", []string{"Patient"}, "status", "active")))
		wantErr(t, err, "search parameter", "code")
	})
}

// R5: Resource hereda de Base, que no es un tipo de recurso; la cadena termina
// en Resource sin error.
func TestInheritanceEndsAtANonResourceBase(t *testing.T) {
	base := j("StructureDefinition", sdBase+"Base", "kind", "complex-type", "derivation", "specialization", "type", "Base", "status", "active", "version", "4.3.0")
	root := j("StructureDefinition", sdBase+"Resource", "kind", "resource", "derivation", "specialization", "type", "Resource", "baseDefinition", sdBase+"Base", "status", "active", "version", "4.3.0")
	c, _ := buildCtx(t, coreP(base, root,
		sd("Patient", "Resource"),
		sp("Base-only", "zzz", []string{"Base"}), // Base no es un tipo de recurso: nadie lo hereda
		sp("Resource-id", "_id", []string{"Resource"}),
		sp("Patient-name", "name", []string{"Patient"})))
	if got := c.ResourceTypes(); !slices.Equal(got, []string{"Patient", "Resource"}) {
		t.Errorf("ResourceTypes = %v", got)
	}
	if got := spCodes(c.SearchParameters("Patient")); !slices.Equal(got, []string{"name", "_id"}) {
		t.Errorf("Patient: %v", got)
	}
}

// hl7.fhir.r4.core y hl7.fhir.r4b.core traen 10 SearchParameter sin base (codesystem-extensions-*, draft;
// plan 002, hallazgo de la implementación): no aplican a ningún tipo y no son
// error.
func TestSearchParameterWithoutBaseIsIgnored(t *testing.T) {
	c, _ := buildCtx(t, sdFixtureCore(j("SearchParameter", spBase+"codesystem-extensions-CodeSystem-author", "code", "author", "type", "string", "status", "draft", "experimental", true)))
	for _, rt := range c.ResourceTypes() {
		if slices.Contains(spCodes(c.SearchParameters(rt)), "author") {
			t.Errorf("%s has a SearchParameter without base", rt)
		}
	}
}

// Code review ronda 1, M1: un SearchParameter cuyo canónico queda sin resolver
// no desaparece en silencio: deja su base:code sin resolver (Conflict), excluye
// el code y lo bloquea en los ancestros.
func TestUnresolvedSearchParameterCanonicalLeavesTheBaseCodeUnresolved(t *testing.T) {
	const u = "http://example.org/SearchParameter/dup"
	tie := func(code string, bases []string) []pkg {
		// Misma URL, mismo estado y versión en dos IG: el canónico no se resuelve.
		return []pkg{
			igP("a", sp("dup", code, bases, "url", u)),
			igP("b", sp("dup", code, bases, "url", u)),
		}
	}
	t.Run("another URL does not win the base:code", func(t *testing.T) {
		c, _ := buildCtx(t, append([]pkg{sdFixtureCore()}, tie("code", []string{"Observation"})...)...)
		if cf := conflictFor(t, c, KindCanonical, "SearchParameter", u); cf.Winner != -1 {
			t.Fatalf("the canonical is resolved: %+v", cf)
		}
		cf := conflictFor(t, c, KindSearchParameter, "", "Observation:code")
		if cf.Winner != -1 || cf.DecidedBy != StepUnresolved || len(cf.Candidates) < 3 {
			t.Errorf("%+v", cf)
		}
		if slices.Contains(spCodes(c.SearchParameters("Observation")), "code") {
			t.Error("Observation still lists code")
		}
		if !slices.Contains(spCodes(c.SearchParameters("Patient")), "name") {
			t.Error("unrelated codes were lost")
		}
	})
	t.Run("it blocks the inherited code", func(t *testing.T) {
		c, _ := buildCtx(t, append([]pkg{sdFixtureCore()}, tie("_id", []string{"Observation"})...)...)
		if slices.Contains(spCodes(c.SearchParameters("Observation")), "_id") {
			t.Error("Observation inherits _id from Resource although Observation:_id is unresolved")
		}
		if !slices.Contains(spCodes(c.SearchParameters("Patient")), "_id") {
			t.Error("Patient lost _id")
		}
	})
	t.Run("candidates that disagree: every pair of any candidate is unresolved, no cross product", func(t *testing.T) {
		c, _ := buildCtx(t, sdFixtureCore(),
			igP("a", sp("dup", "x", []string{"Observation"}, "url", u)),
			igP("b", sp("dup", "y", []string{"Patient"}, "url", u)))
		for _, key := range []string{"Observation:x", "Patient:y"} {
			cf := conflictFor(t, c, KindSearchParameter, "", key)
			if cf.Winner != -1 || !strings.Contains(explainTrace(cf.Trace), "disagree") {
				t.Errorf("%s: %+v", key, cf)
			}
		}
		for _, cf := range c.Conflicts() {
			if cf.Kind == KindSearchParameter && (cf.Key == "Observation:y" || cf.Key == "Patient:x") {
				t.Errorf("cross product pair %s", cf.Key)
			}
		}
	})
	t.Run("a candidate with base and no code is an error, as for a resolved one (round 2, 4 and 5)", func(t *testing.T) {
		// A trae base sin code y B code sin base: A es un error; B no aporta.
		_, err := tryCtx(t, sdFixtureCore(), igP("a", j("SearchParameter", u, "base", []string{"Observation"}, "status", "active", "version", "1")),
			igP("b", j("SearchParameter", u, "code", "zz", "status", "active", "version", "1")))
		wantErr(t, err, "has no code", u)
		// Resuelto: el mismo error.
		_, err = tryCtx(t, sdFixtureCore(j("SearchParameter", u, "base", []string{"Observation"}, "status", "active", "version", "1")))
		wantErr(t, err, "has no code", u)
	})
	t.Run("code without base in every candidate: nothing is indexed and the canonical conflict is the trace", func(t *testing.T) {
		c, _ := buildCtx(t, sdFixtureCore(sp("other", "zz", []string{"Observation"})),
			igP("a", j("SearchParameter", u, "code", "zz", "status", "active", "version", "1")),
			igP("b", j("SearchParameter", u, "code", "zz", "status", "active", "version", "1")))
		if cf := conflictFor(t, c, KindCanonical, "SearchParameter", u); cf.Winner != -1 {
			t.Errorf("%+v", cf)
		}
		// Sin base no aplica a ningún tipo: el otro sigue ganando Observation:zz.
		if !slices.Contains(spCodes(c.SearchParameters("Observation")), "zz") {
			t.Error("Observation:zz lost")
		}
	})
	t.Run("candidates are listed by package, URL and file (round 2, 6)", func(t *testing.T) {
		// Dos archivos de la misma URL en el mismo package: solo el archivo los ordena.
		one := sp("dup", "code", []string{"Observation"}, "url", u)
		p := igP("a")
		// Más de 12 elementos: slices.SortFunc deja de ser una inserción y sin el
		// desempate por archivo el orden cambiaría.
		p.files = map[string]string{}
		var want []string
		for i := 0; i < 40; i++ {
			f := fmt.Sprintf("f%02d.json", i)
			p.files[f] = one
			want = append(want, f)
		}
		c, _ := buildCtx(t, sdFixtureCore(), p)
		cf := conflictFor(t, c, KindSearchParameter, "", "Observation:code")
		var files []string
		for _, cd := range cf.Candidates {
			files = append(files, cd.File)
		}
		want = append(want, "r10.json") // el del núcleo, de un package posterior
		if !slices.Equal(files, want) {
			t.Errorf("candidates in %v", files)
		}
	})
	t.Run("a candidate without base contributes nothing", func(t *testing.T) {
		c, _ := buildCtx(t, sdFixtureCore(),
			igP("a", j("SearchParameter", u, "code", "x", "status", "active", "version", "1")),
			igP("b", j("SearchParameter", u, "code", "x", "status", "active", "version", "1")))
		for _, cf := range c.Conflicts() {
			if cf.Kind == KindSearchParameter {
				t.Errorf("unexpected %+v", cf)
			}
		}
	})
}

// Code review ronda 1, M3.
func TestUnresolvedResourceTypeDefinitionIsAnError(t *testing.T) {
	// El mismo StructureDefinition de un tipo de recurso en dos IG, sin nada que
	// los separe: no se puede tomar el tipo de ninguno.
	dup := sd("Widget", "DomainResource")
	reg := newMem(t, sdFixtureCore(), igP("a", dup), igP("b", dup))
	l := mustResolve(t, []string{coreID, aID, bID}, reg, Options{})
	set, err := Open(l, reg.writeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewContext(set)
	wantErr(t, err, "Widget", "unresolved")
}

func TestSearchParameterWithRepeatedBaseDoesNotConflictWithItself(t *testing.T) {
	c, _ := buildCtx(t, sdFixtureCore(sp("dupbase", "dupbase", []string{"Observation", "Observation"})))
	for _, cf := range c.Conflicts() {
		if cf.Kind == KindSearchParameter {
			t.Errorf("unexpected %+v", cf)
		}
	}
	n := 0
	for _, s := range c.SearchParameters("Observation") {
		if s.Code == "dupbase" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d entries for dupbase, want 1", n)
	}
}

// Code review ronda 1, B2: ResourceTypes devuelve los tipos concretos; los
// abstractos (abstract true) cuentan para la herencia y para SearchParameters.
func TestAbstractTypesAreInheritedButNotListed(t *testing.T) {
	c, _ := buildCtx(t, coreP(
		sd("Resource", "", "abstract", true),
		sd("DomainResource", "Resource", "abstract", true),
		sd("Observation", "DomainResource"),
		sp("Resource-id", "_id", []string{"Resource"}),
		sp("DomainResource-text", "_text", []string{"DomainResource"}),
		sp("Observation-code", "code", []string{"Observation"})))
	if got := c.ResourceTypes(); !slices.Equal(got, []string{"Observation"}) {
		t.Errorf("ResourceTypes = %v, want only the concrete types", got)
	}
	if got := spCodes(c.SearchParameters("Observation")); !slices.Equal(got, []string{"code", "_text", "_id"}) {
		t.Errorf("Observation: %v", got)
	}
	// Los de un tipo abstracto se piden igual.
	if got := spCodes(c.SearchParameters("DomainResource")); !slices.Equal(got, []string{"_text", "_id"}) {
		t.Errorf("DomainResource: %v", got)
	}
}

// tryCtx es buildCtx sin fallar si NewContext da error.
func tryCtx(t testing.TB, pkgs ...pkg) (*Context, error) {
	t.Helper()
	reg := newMem(t, pkgs...)
	var ids []string
	var exc []Exception
	for _, p := range pkgs {
		ids = append(ids, p.id())
	}
	l := mustResolve(t, ids, reg, Options{Exceptions: exc})
	set, err := Open(l, reg.writeDir(t))
	if err != nil {
		t.Fatal(err)
	}
	return NewContext(set)
}
