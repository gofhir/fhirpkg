package fhirpkg

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// Los tests con los packages oficiales del plan 002 (make check-packages). Los
// valores esperados salen de las cuentas y los casos medidos en el plan (§8.2 y
// §8.3), escritos a mano.

var update = flag.Bool("update", false, "rewrite testdata/golden (make golden); review the diff")

// realCases son los tres conjuntos base (ADR-002).
var realCases = []struct {
	lock, golden string
	// Cuentas de §8.2 del plan: claves, repetidas y cuántas decidió cada paso.
	keys, repeated                                         int
	series, status, publisher, version, lastUpdated, unres int
}{
	{"r4.lock", "r4-conflicts.tsv", 6940, 1802, 0, 645, 1151, 6, 0, 0},
	{"r4b.lock", "r4b-conflicts.tsv", 7156, 937, 240, 584, 11, 102, 0, 0},
	{"r5.lock", "r5-conflicts.tsv", 7789, 4, 0, 1, 2, 1, 0, 0},
}

func realContext(t testing.TB, lock string) (*Context, *Set) {
	t.Helper()
	dir := os.Getenv("FHIRPKG_PACKAGES")
	if dir == "" {
		t.Skip("FHIRPKG_PACKAGES is not set (make check-packages)")
	}
	l, err := ReadLockFile(filepath.Join("testdata", lock))
	if err != nil {
		t.Fatal(err)
	}
	set, err := Open(l, dir)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewContext(set)
	if err != nil {
		t.Fatal(err)
	}
	return c, set
}

func countBy(c *Context, kind Kind) map[Step]int {
	out := map[Step]int{}
	for _, cf := range c.Conflicts() {
		if cf.Kind == kind {
			out[cf.DecidedBy]++
		}
	}
	return out
}

// §8.2: las cuentas de la cascada por versión.
func TestRealCascadeCounts(t *testing.T) {
	for _, rc := range realCases {
		c, _ := realContext(t, rc.lock)
		by := countBy(c, KindCanonical)
		repeated := 0
		for _, n := range by {
			repeated += n
		}
		t.Logf("%s: %d keys, %d repeated; series %d, status %d, publisher %d, version %d, last-updated %d, unresolved %d",
			rc.lock, len(c.groups), repeated, by[StepSeries], by[StepStatus], by[StepPublisher], by[StepVersion], by[StepLastUpdated], by[StepUnresolved])
		got := []int{len(c.groups), repeated, by[StepSeries], by[StepStatus], by[StepPublisher], by[StepVersion], by[StepLastUpdated], by[StepUnresolved]}
		want := []int{rc.keys, rc.repeated, rc.series, rc.status, rc.publisher, rc.version, rc.lastUpdated, rc.unres}
		if !slices.Equal(got, want) {
			t.Errorf("%s: keys, repeated, series, status, publisher, version, lastUpdated, unresolved = %v, plan 002 §8.2 says %v", rc.lock, got, want)
		}
	}
}

// formatGolden escribe Conflicts() en un formato estable y revisable: una línea
// por conflicto y, antes, las cuentas por paso.
func formatGolden(c *Context) string {
	var b strings.Builder
	for _, kind := range []Kind{KindCanonical, KindSearchParameter} {
		by := countBy(c, kind)
		fmt.Fprintf(&b, "# %s:", kind)
		for _, s := range []Step{StepSeries, StepStatus, StepPublisher, StepVersion, StepLastUpdated, StepNotExperimental, StepCore, StepUnresolved} {
			if by[s] > 0 {
				fmt.Fprintf(&b, " %s=%d", s, by[s])
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("# kind\tresourceType\tkey\tdecidedBy\twinner\tcandidates (package version status [url])\n")
	for _, cf := range c.Conflicts() {
		winner := "-"
		if cf.Winner >= 0 {
			winner = cf.Candidates[cf.Winner].Package
		}
		var cands []string
		for _, cd := range cf.Candidates {
			c := fmt.Sprintf("%s %s %s", cd.Package, cd.Version, cd.Status)
			if cf.Kind == KindSearchParameter {
				c += " " + cd.URL // dos definiciones del mismo package se distinguen por la URL
			}
			cands = append(cands, c)
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n", cf.Kind, cf.ResourceType, cf.Key, cf.DecidedBy, winner, strings.Join(cands, " | "))
	}
	return b.String()
}

// Dorado de Conflicts(): cada canónico repetido y cada base:code, con su
// ganador y el paso. make golden lo regenera; hay que revisar el diff.
func TestRealConflictsGolden(t *testing.T) {
	for _, rc := range realCases {
		c, _ := realContext(t, rc.lock)
		got := formatGolden(c)
		path := filepath.Join("testdata", "golden", rc.golden)
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (make golden)", err)
		}
		if got != string(want) {
			gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
			for i := 0; i < len(gl) && i < len(wl); i++ {
				if gl[i] != wl[i] {
					t.Errorf("%s differs from %s at line %d:\n got %s\nwant %s\n(make golden, review the diff)", rc.lock, rc.golden, i+1, gl[i], wl[i])
					break
				}
			}
			if len(gl) != len(wl) {
				t.Errorf("%s: %d lines, golden has %d", rc.lock, len(gl), len(wl))
			}
		}
	}
}

func spFor(c *Context, rt, code string) (SearchParameter, bool) {
	for _, sp := range c.SearchParameters(rt) {
		if sp.Code == code {
			return sp, true
		}
	}
	return SearchParameter{}, false
}

func hasConflict(c *Context, kind Kind, key string) (Conflict, bool) {
	for _, cf := range c.Conflicts() {
		if cf.Kind == kind && cf.Key == key {
			return cf, true
		}
	}
	return Conflict{}, false
}

// §8.3: los casos medidos que van a los tests a mano.
func TestRealMeasuredCases(t *testing.T) {
	const sp = "http://hl7.org/fhir/SearchParameter/"
	for _, rc := range realCases {
		c, _ := realContext(t, rc.lock)
		// A-H1: el normativo, no el ejemplo de la raíz del núcleo.
		if got, ok := spFor(c, "Condition", "_id"); !ok || got.URL != sp+"Resource-id" {
			t.Errorf("%s: Condition:_id = %+v, %v, want Resource-id", rc.lock, got, ok)
		}
		if got, ok := spFor(c, "Condition", "subject"); !ok || got.URL != sp+"Condition-subject" {
			t.Errorf("%s: Condition:subject = %+v, %v, want Condition-subject", rc.lock, got, ok)
		}
		// A-M5: Resource:_filter queda sin resolver en R4B y R5, y se excluye.
		cf, unresolved := hasConflict(c, KindSearchParameter, "Resource:_filter")
		wantUnresolved := rc.lock != "r4.lock"
		if unresolved != wantUnresolved {
			t.Errorf("%s: Resource:_filter conflict = %v, want %v", rc.lock, unresolved, wantUnresolved)
		}
		if unresolved {
			if cf.Winner != -1 || cf.DecidedBy != StepUnresolved {
				t.Errorf("%s: Resource:_filter %+v", rc.lock, cf)
			}
			if _, ok := spFor(c, "Patient", "_filter"); ok {
				t.Errorf("%s: _filter is not excluded", rc.lock)
			}
		}
	}

	// A-I1: R5 CodeSystem:effective y ValueSet:effective.
	c5, _ := realContext(t, "r5.lock")
	for _, rt := range []string{"CodeSystem", "ValueSet"} {
		if got, ok := spFor(c5, rt, "effective"); !ok || got.URL != sp+"MetadataResource-effective" {
			t.Errorf("r5 %s:effective = %+v, %v, want MetadataResource-effective", rt, got, ok)
		}
	}

	// R4: los 13 SearchParameter de uv.extensions.r4 (active) sobre los draft del núcleo.
	c4, _ := realContext(t, "r4.lock")
	const ext4, core4 = "hl7.fhir.uv.extensions.r4#5.2.0", "hl7.fhir.r4.core#4.0.1"
	n := 0
	for _, cf := range c4.Conflicts() {
		if cf.Kind != KindCanonical || cf.ResourceType != "SearchParameter" {
			continue
		}
		n++
		if winnerOf(cf) != ext4 || cf.DecidedBy != StepStatus || len(cf.Candidates) != 2 || cf.Candidates[0].Package != core4 || cf.Candidates[0].Status != "draft" || cf.Candidates[1].Status != "active" {
			t.Errorf("r4 SearchParameter %s: %+v", cf.Key, cf)
		}
	}
	if n != 13 {
		t.Errorf("r4: %d SearchParameter repeated between the core and the extensions, want 13", n)
	}

	// R4B: el de ValueSet-workflow lo gana uv.extensions.r4 (active) sobre
	// uv.extensions.r4b (retired) y r4b.core (draft); un SearchParameter no pasa por la serie.
	c4b, _ := realContext(t, "r4b.lock")
	cf, err := c4b.Explain("SearchParameter", sp+"valueset-extensions-ValueSet-workflow")
	if err != nil {
		t.Fatal(err)
	}
	if winnerOf(cf) != "hl7.fhir.uv.extensions.r4#5.2.0" || cf.DecidedBy != StepStatus || len(cf.Candidates) != 3 {
		t.Errorf("r4b valueset-extensions-ValueSet-workflow: %+v", cf)
	}
	for _, st := range cf.Trace {
		if st.Step == StepSeries && st.In != st.Out {
			t.Errorf("the series step separated a SearchParameter: %+v", st)
		}
	}
	// R4B: valueset-trusted-expansion (una extensión, StructureDefinition) queda en r4b.core.
	cf, err = c4b.Explain("StructureDefinition", "http://hl7.org/fhir/StructureDefinition/valueset-trusted-expansion")
	if err != nil || winnerOf(cf) != "hl7.fhir.r4b.core#4.3.0" {
		t.Errorf("r4b valueset-trusted-expansion: %+v, %v", cf, err)
	}
	// ResolveFrom: el núcleo de R4B solo se ve a sí mismo.
	r, err := c4b.ResolveFrom("hl7.fhir.r4b.core#4.3.0", "SearchParameter", sp+"valueset-extensions-ValueSet-workflow")
	if err != nil || r.Package != "hl7.fhir.r4b.core#4.3.0" {
		t.Errorf("ResolveFrom(core): %+v, %v", r, err)
	}
}

// A-H2: en R4B la serie decide 240 canónicos: 239 a favor de uv.extensions.r4b
// sobre uv.extensions.r4, y 1 a favor de r4b.core.
func TestRealR4BSeriesStep(t *testing.T) {
	c, _ := realContext(t, "r4b.lock")
	wins := map[string]int{}
	for _, cf := range c.Conflicts() {
		if cf.Kind == KindCanonical && cf.DecidedBy == StepSeries {
			wins[winnerOf(cf)]++
		}
	}
	if wins["hl7.fhir.uv.extensions.r4b#5.3.0"] != 239 || wins["hl7.fhir.r4b.core#4.3.0"] != 1 || len(wins) != 2 {
		t.Errorf("series winners: %v, plan 002 §8.2 says 239 for uv.extensions.r4b and 1 for r4b.core", wins)
	}
}

// Herencia (plan 002 §4): todo SearchParameter con base Resource aparece en
// todos los tipos, y los de DomainResource, en sus descendientes.
func TestRealInheritance(t *testing.T) {
	for _, rc := range realCases {
		c, _ := realContext(t, rc.lock)
		if len(c.ResourceTypes()) < 100 {
			t.Errorf("%s: only %d resource types", rc.lock, len(c.ResourceTypes()))
		}
		// Los base:code sin resolver bloquean su code (ADR-003): se descuentan.
		blocked := map[string]map[string]bool{}
		for _, cf := range c.Conflicts() {
			if cf.Kind == KindSearchParameter && cf.Winner < 0 {
				base, code, _ := strings.Cut(cf.Key, ":")
				if blocked[base] == nil {
					blocked[base] = map[string]bool{}
				}
				blocked[base][code] = true
			}
		}
		for _, level := range []string{c.rootType(), c.domainType()} {
			want := c.SearchParameters(level)
			for _, rt := range c.ResourceTypes() {
				if !slices.Contains(c.chains[rt], level) {
					continue
				}
				have := map[string]SearchParameter{}
				for _, sp := range c.SearchParameters(rt) {
					have[sp.Code] = sp
				}
				for _, sp := range want {
					if _, ok := have[sp.Code]; !ok && !blockedFor(blocked, c.chains[rt], sp.Code) {
						t.Errorf("%s: %s lacks %s from %s", rc.lock, rt, sp.Code, level)
					}
				}
			}
		}
	}
}

func blockedFor(blocked map[string]map[string]bool, chain []string, code string) bool {
	for _, b := range chain {
		if blocked[b][code] {
			return true
		}
	}
	return false
}

// rootType y domainType son la raíz de la herencia y su hijo, tal como salen de
// las cadenas de baseDefinition (sin nombrarlos): el tipo que está en todas las
// cadenas, y el que está en todas salvo en la suya.
func (c *Context) rootType() string {
	for _, t := range c.types {
		if len(c.chains[t]) == 1 {
			return t
		}
	}
	return ""
}

func (c *Context) domainType() string {
	root := c.rootType()
	count := map[string]int{}
	for _, t := range c.types {
		for _, x := range c.chains[t] {
			count[x]++
		}
	}
	// El nivel con más descendientes después de la raíz.
	best, bestN := "", 0
	for x, n := range count {
		if x != root && n > bestN {
			best, bestN = x, n
		}
	}
	return best
}

// searchIndexData es el formato de los datos embebidos de searchindex que
// importa aquí. Se lee por ruta: este módulo no depende de searchindex.
type searchIndexData struct {
	SearchParameters []struct {
		URL  string   `json:"url"`
		Code string   `json:"code"`
		Base []string `json:"base"`
	} `json:"searchParameters"`
	BaseTypes map[string]string `json:"baseTypes"`
}

// Comparación de la herencia con searchindex, para el núcleo solo: la misma
// lista de code por tipo de recurso.
func TestRealInheritanceMatchesSearchIndex(t *testing.T) {
	dir := packagesDir(t)
	for _, tc := range []struct{ data, core string }{
		{"r4.json.gz", "hl7.fhir.r4.core#4.0.1"},
		{"r4b.json.gz", "hl7.fhir.r4b.core#4.3.0"},
		{"r5.json.gz", "hl7.fhir.r5.core#5.0.0"},
	} {
		path := filepath.Join("..", "go-searchindex", "definitions", "data", tc.data)
		f, err := os.Open(path)
		if err != nil {
			t.Skipf("SKIPPED: the searchindex data %s is not available (%v): this comparison needs the sibling repo ../go-searchindex; make check-packages fails without it", path, err)
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		var si searchIndexData
		if err := json.NewDecoder(zr).Decode(&si); err != nil {
			t.Fatal(err)
		}
		f.Close()

		// Un Context solo del núcleo: se resuelve esa raíz sola.
		name, version, _ := splitID(tc.core)
		lock, err := Resolve(context.Background(), []string{tc.core}, DirRegistry{Dir: dir}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		set, err := Open(lock, dir)
		if err != nil {
			t.Fatal(err)
		}
		c, err := NewContext(set)
		if err != nil {
			t.Fatal(err)
		}
		_ = name
		_ = version

		// La lista de searchindex: los del tipo primero y luego los de cada tipo
		// base; el primero con un code gana.
		byBase := map[string][]string{}
		urlOf := map[string]string{} // base + ":" + code -> URL en searchindex
		for _, sp := range si.SearchParameters {
			for _, b := range sp.Base {
				byBase[b] = append(byBase[b], sp.Code)
				if _, dup := urlOf[b+":"+sp.Code]; !dup {
					urlOf[b+":"+sp.Code] = sp.URL
				}
			}
		}
		types := map[string]bool{}
		for k, v := range si.BaseTypes {
			types[k], types[v] = true, true
		}
		if len(types) < 100 {
			t.Fatalf("%s: %d types in searchindex data", tc.data, len(types))
		}
		compared := 0
		for rt := range types {
			var want []string
			seen := map[string]bool{}
			for cur, hops := rt, 0; cur != "" && hops < 10; hops++ {
				for _, code := range byBase[cur] {
					if !seen[code] {
						seen[code] = true
						want = append(want, code)
					}
				}
				cur = si.BaseTypes[cur]
			}
			got := spCodes(c.SearchParameters(rt))
			// Diferencia deliberada (A-M5, ADR-003): Resource:_filter queda sin
			// resolver (dos definiciones del núcleo, ninguna experimental) y se
			// excluye; searchindex conserva una de las dos.
			if _, unresolved := hasConflict(c, KindSearchParameter, "Resource:_filter"); unresolved {
				want = slices.DeleteFunc(want, func(code string) bool { return code == "_filter" })
			}
			// La URL que gana por base:code, donde ambos tienen una. Los
			// SearchParameter de este lado traen su Base; el nivel es el primero de
			// la cadena que declara el code.
			for _, mine := range c.SearchParameters(rt) {
				for cur, hops := rt, 0; cur != "" && hops < 10; hops++ {
					theirs, ok := urlOf[cur+":"+mine.Code]
					if ok {
						if theirs != mine.URL {
							t.Errorf("%s %s:%s: winning URL %s, searchindex %s", tc.core, rt, mine.Code, mine.URL, theirs)
						}
						compared++
						break
					}
					cur = si.BaseTypes[cur]
				}
			}
			slices.Sort(want)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s %s: codes differ from searchindex\n  only here: %v\n  only searchindex: %v", tc.core, rt, diff(got, want), diff(want, got))
			}
		}
		for _, rt := range c.ResourceTypes() {
			if !types[rt] && len(c.SearchParameters(rt)) > 0 && rt != c.rootType() {
				t.Errorf("%s: type %s is not in searchindex", tc.core, rt)
			}
		}
		if compared < 3000 {
			t.Errorf("%s: only %d winning URLs compared", tc.core, compared)
		}
		t.Logf("%s: %d resource types and %d winning URLs by base:code compared with searchindex/%s; expected differences: _filter only (excluded here, A-M5)", tc.core, len(types), compared, tc.data)
	}
}

func diff(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !slices.Contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

// Metamórfico sobre los datos reales: el orden de los packages no cambia
// Conflicts() ni las listas de SearchParameter.
func TestRealPackageOrderDoesNotChangeTheResult(t *testing.T) {
	for _, rc := range realCases {
		base, set := realContext(t, rc.lock)
		want := formatGolden(base)
		wantSP := fmt.Sprint(base.SearchParameters("Observation"))
		ids := make([]string, 0, len(set.packages))
		byID := map[string]Package{}
		for _, p := range set.packages {
			ids = append(ids, p.ID)
			byID[p.ID] = p
		}
		permute(ids, func(order []string) {
			perm := &Set{lock: set.lock, resources: set.resources}
			for _, id := range order {
				perm.packages = append(perm.packages, byID[id])
			}
			c, err := NewContext(perm)
			if err != nil {
				t.Fatal(err)
			}
			if formatGolden(c) != want || fmt.Sprint(c.SearchParameters("Observation")) != wantSP {
				t.Errorf("%s: order %v changes the result", rc.lock, order)
			}
		})
	}
}

// El tiempo de NewContext sobre un Set ya abierto (make evidence suma el RSS
// con `fhirpkg context`).
func TestRealNewContextTime(t *testing.T) {
	for _, rc := range realCases {
		dir := packagesDir(t)
		l := readRealLock(t, rc.lock)
		set, err := Open(l, dir)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		c, err := NewContext(set)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: NewContext in %s, %d types, %d conflicts", rc.lock, time.Since(start).Round(time.Millisecond), len(c.ResourceTypes()), len(c.Conflicts()))
	}
}

var _ = bufio.NewReader

// TestRealResourceTypesFile: testdata/resource-types.txt (la lista del guardián
// sin packages) es la unión de los tipos, abstractos incluidos, de R4, R4B y R5.
// make golden la reescribe.
func TestRealResourceTypesFile(t *testing.T) {
	set := map[string]bool{}
	for _, rc := range realCases {
		c, _ := realContext(t, rc.lock)
		for _, rt := range c.types {
			set[rt] = true
		}
	}
	all := make([]string, 0, len(set))
	for rt := range set {
		all = append(all, rt)
	}
	slices.Sort(all)
	got := strings.Join(all, "\n") + "\n"
	path := filepath.Join("testdata", "resource-types.txt")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (make golden)", err)
	}
	if got != string(want) {
		t.Errorf("%s is out of date: %d types from the packages, %d in the file (make golden)", path, len(all), len(strings.Fields(string(want))))
	}
}
