package fhirpkg

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestResolveChain(t *testing.T) {
	reg := newMem(t,
		coreR4(),
		ig("ig.mid", "1.0.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}),
		ig("ig.top", "2.0.0", []string{"4.0.1"}, map[string]string{"ig.mid": "1.0.0"}),
	)
	l := mustResolve(t, []string{"ig.top#2.0.0", "core.r4#4.0.1"}, reg, Options{})
	if l.FHIRVersion != "4.0.1" || l.Core != "core.r4#4.0.1" {
		t.Fatalf("core/version: %s %s", l.Core, l.FHIRVersion)
	}
	if want := []string{"core.r4#4.0.1", "ig.mid#1.0.0", "ig.top#2.0.0"}; !slices.Equal(l.Order, want) {
		t.Fatalf("order %v, want %v", l.Order, want)
	}
	mid, _ := l.Package("ig.mid#1.0.0")
	if !slices.Equal(mid.Via, []string{"ig.top#2.0.0"}) {
		t.Fatalf("via of ig.mid: %v", mid.Via)
	}
	core, _ := l.Package("core.r4#4.0.1")
	if !core.LegacyVersionList || !slices.Equal(core.FHIRVersions, []string{"4.0.1"}) {
		t.Fatalf("legacy version list not recorded: %+v", core)
	}
}

func TestResolveRootIsCoreHasEmptyVia(t *testing.T) {
	reg := newMem(t, coreR4(), ig("ig.a", "1.0.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}))
	l := mustResolve(t, []string{"core.r4#4.0.1", "ig.a#1.0.0"}, reg, Options{})
	core, _ := l.Package("core.r4#4.0.1")
	if len(core.Via) != 0 {
		t.Fatalf("a root has no via, got %v", core.Via)
	}
}

func TestResolveWildcard(t *testing.T) {
	var pkgs []pkg
	for _, v := range []string{"4.0.0", "4.0.1", "4.0.10", "4.0.9", "4.1.0", "4.0.11-snapshot", "4.0"} {
		pkgs = append(pkgs, ig("ig.w", v, []string{"4.0.1"}, nil))
	}
	pkgs = append(pkgs, coreR4(), ig("ig.dep", "1.0.0", []string{"4.0.1"}, map[string]string{"ig.w": "4.0.x"}))
	reg := newMem(t, pkgs...)
	l := mustResolve(t, []string{"core.r4#4.0.1", "ig.dep#1.0.0"}, reg, Options{})
	if _, ok := l.Package("ig.w#4.0.10"); !ok || len(l.Packages) != 3 {
		t.Fatalf("wildcard should pick 4.0.10: %v", l.Order)
	}
	dep, _ := l.Package("ig.dep#1.0.0")
	if !slices.Equal(dep.Dependencies, []string{"ig.w#4.0.10"}) {
		t.Fatalf("dependencies must be exact: %v", dep.Dependencies)
	}
	// Una raíz con comodín, y el comodín con * .
	l = mustResolve(t, []string{"core.r4#4.0.1", "ig.w#4.0.*"}, reg, Options{})
	if _, ok := l.Package("ig.w#4.0.10"); !ok {
		t.Fatalf("root wildcard: %v", l.Order)
	}
	// Sin versiones que cumplan.
	_, err := Resolve(context.Background(), []string{"core.r4#4.0.1", "ig.w#9.9.x"}, reg, Options{})
	wantErr(t, err, "no published version", "9.9.x")
}

func TestResolveWildcardAndExactRequestSameVersion(t *testing.T) {
	// 4.0.x se resuelve a 4.0.1 y otro paquete pide 4.0.1 exacto: no es versión doble.
	reg := newMem(t, coreR4(),
		ig("ig.w", "4.0.1", []string{"4.0.1"}, nil),
		ig("ig.a", "1.0.0", []string{"4.0.1"}, map[string]string{"ig.w": "4.0.x"}),
		ig("ig.b", "1.0.0", []string{"4.0.1"}, map[string]string{"ig.w": "4.0.1"}))
	mustResolve(t, []string{"core.r4#4.0.1", "ig.a#1.0.0", "ig.b#1.0.0"}, reg, Options{})
}

func TestResolveMissingDependency(t *testing.T) {
	reg := newMem(t, coreR4(), ig("ig.a", "1.0.0", []string{"4.0.1"}, map[string]string{"nowhere": "1.0.0"}))
	_, err := Resolve(context.Background(), []string{"core.r4#4.0.1", "ig.a#1.0.0"}, reg, Options{})
	wantErr(t, err, "nowhere#1.0.0", "ig.a#1.0.0")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound: %v", err)
	}
	_, err = Resolve(context.Background(), []string{"core.r4#4.0.1", "missing#1.0.0"}, reg, Options{})
	wantErr(t, err, "missing#1.0.0", "(root)")
}

func TestResolveCycle(t *testing.T) {
	reg := newMem(t, coreR4(),
		ig("ig.a", "1.0.0", []string{"4.0.1"}, map[string]string{"ig.b": "1.0.0"}),
		ig("ig.b", "1.0.0", []string{"4.0.1"}, map[string]string{"ig.c": "1.0.0"}),
		ig("ig.c", "1.0.0", []string{"4.0.1"}, map[string]string{"ig.a": "1.0.0"}))
	_, err := Resolve(context.Background(), []string{"core.r4#4.0.1", "ig.a#1.0.0"}, reg, Options{})
	wantErr(t, err, "dependency cycle", "ig.a#1.0.0 -> ig.b#1.0.0 -> ig.c#1.0.0 -> ig.a#1.0.0")
}

// r4bGraph reproduce la forma del grafo real de R4B: extensions.r4b trae
// terminology.r4, que trae un segundo núcleo (de la serie 4.0).
func r4bGraph(t testing.TB) memRegistry {
	return newMem(t, coreR4(), coreR4B(),
		ig("ext.r4", "5.2.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}),
		ig("term.r4", "7.1.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1", "ext.r4": "5.2.0"}),
		ig("ext.r4b", "5.3.0", []string{"4.3.0"}, map[string]string{"core.r4b": "4.3.0", "term.r4": "7.1.0"}),
	)
}

func TestResolveForeignCoreExcluded(t *testing.T) {
	reg := r4bGraph(t)
	roots := []string{"core.r4b#4.3.0", "ext.r4b#5.3.0"}
	// Sin excepciones: term.r4 y ext.r4 son de otra serie.
	_, err := Resolve(context.Background(), roots, reg, Options{})
	wantErr(t, err, "term.r4#7.1.0", "ext.r4#5.2.0", "another FHIR series", "ext.r4b#5.3.0 -> term.r4#7.1.0")
	if strings.Contains(err.Error(), "core.r4#4.0.1 declares") {
		t.Fatalf("the foreign core is excluded, not an error: %v", err)
	}
	exc := []Exception{{"term.r4#7.1.0", "official dependency of ext.r4b"}, {"ext.r4#5.2.0", "official dependency of term.r4"}}
	l := mustResolve(t, roots, reg, Options{Exceptions: exc})
	if l.Core != "core.r4b#4.3.0" || l.FHIRVersion != "4.3.0" {
		t.Fatalf("core: %s %s", l.Core, l.FHIRVersion)
	}
	foreign, ok := l.Package("core.r4#4.0.1")
	if !ok || !foreign.Excluded || foreign.ExcludedReason == "" {
		t.Fatalf("the core of another series must stay in the lock, excluded: %+v", foreign)
	}
	if !slices.Equal(foreign.Via, []string{"ext.r4b#5.3.0", "term.r4#7.1.0"}) {
		t.Fatalf("via of the excluded core: %v", foreign.Via)
	}
	if slices.Contains(l.Order, "core.r4#4.0.1") || len(l.Order) != 4 {
		t.Fatalf("an excluded core is not read: %v", l.Order)
	}
	if !slices.Equal(l.Exceptions, exc) && len(l.Exceptions) != 2 {
		t.Fatalf("exceptions: %v", l.Exceptions)
	}
	// El núcleo excluido no se lee, pero su .tgz se verifica: sin él no abre.
	dir := reg.writeDir(t)
	_ = removeFile(dir, PackageFile("core.r4", "4.0.1"))
	if _, err := Open(l, dir); err == nil {
		t.Fatal("Open accepted a lock without the excluded core's file")
	}
	dir = reg.writeDir(t)
	s, err := Open(l, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(s.Packages()); got != 4 || s.FHIRVersion() != "4.3.0" {
		t.Fatalf("packages %d, version %s", got, s.FHIRVersion())
	}
	if len(s.Resources("core.r4#4.0.1")) != 0 {
		t.Fatalf("excluded packages have no resources")
	}
}

func TestResolveExceptionsAreStrict(t *testing.T) {
	reg := r4bGraph(t)
	roots := []string{"core.r4b#4.3.0", "ext.r4b#5.3.0"}
	two := []Exception{{"term.r4#7.1.0", "r"}, {"ext.r4#5.2.0", "r"}}
	_, err := Resolve(context.Background(), roots, reg, Options{Exceptions: append(slices.Clone(two), Exception{"ext.r4b#5.3.0", "not needed"})})
	wantErr(t, err, "no package needs", "ext.r4b#5.3.0")
	_, err = Resolve(context.Background(), roots, reg, Options{Exceptions: []Exception{{"term.r4#7.1.0", ""}, two[1]}})
	wantErr(t, err, "no reason")
	_, err = Resolve(context.Background(), roots, reg, Options{Exceptions: append(slices.Clone(two), two[0])})
	wantErr(t, err, "two exceptions")
	_, err = Resolve(context.Background(), roots, reg, Options{Exceptions: []Exception{{"bad", "r"}}})
	wantErr(t, err, "name#version")
	// Una excepción para el núcleo excluido tampoco sirve: no se lee.
	_, err = Resolve(context.Background(), roots, reg, Options{Exceptions: append(slices.Clone(two), Exception{"core.r4#4.0.1", "r"})})
	wantErr(t, err, "no package needs", "core.r4#4.0.1")
}

func TestResolveDoubleVersionIsAnError(t *testing.T) {
	reg := newMem(t, coreR4B(), coreR4(),
		ig("ext.r4", "5.2.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}),
		ig("term.r4", "7.0.1", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1", "ext.r4": "5.2.0"}),
		ig("term.r4", "7.1.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1", "ext.r4": "5.2.0"}),
		ig("ext.r4b", "5.3.0", []string{"4.3.0"}, map[string]string{"core.r4b": "4.3.0", "term.r4": "7.1.0"}))
	_, err := Resolve(context.Background(), []string{"core.r4b#4.3.0", "term.r4#7.0.1", "ext.r4b#5.3.0"}, reg, Options{})
	wantErr(t, err, "more than one version", "term.r4:", "7.0.1 requested by (root)", "7.1.0 requested by ext.r4b#5.3.0")
	// Dos raíces del mismo package en versiones distintas.
	_, err = Resolve(context.Background(), []string{"core.r4b#4.3.0", "term.r4#7.0.1", "term.r4#7.1.0"}, reg, Options{})
	wantErr(t, err, "term.r4:", "7.0.1", "7.1.0")
	// La misma raíz repetida no es versión doble.
	_, err = Resolve(context.Background(), []string{"core.r4b#4.3.0", "ext.r4b#5.3.0", "ext.r4b#5.3.0"}, reg, Options{})
	wantErr(t, err, "another FHIR series") // llega a la regla siguiente: no es D2
}

func TestResolveCoreRules(t *testing.T) {
	reg := newMem(t, coreR4(), coreR4B(),
		ig("ig.a", "1.0.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}),
		pkg{name: "core.multi", version: "1.0.0", typ: "Core", fhirVersions: []string{"4.0.1", "4.3.0"}},
		pkg{name: "core.none", version: "1.0.0", typ: "Core"},
		pkg{name: "core.odd", version: "1.0.0", typ: "Core", fhirVersions: []string{"current"}},
		pkg{name: "core.r4.other", version: "1.0.0", typ: "Core", fhirVersions: []string{"4.0.0"}},
		ig("ig.core.other", "1.0.0", []string{"4.0.1"}, map[string]string{"core.r4.other": "1.0.0"}),
	)
	ctx := context.Background()
	_, err := Resolve(ctx, []string{"ig.a#1.0.0"}, reg, Options{})
	wantErr(t, err, "no root is a core")
	_, err = Resolve(ctx, []string{"core.r4#4.0.1", "core.r4b#4.3.0"}, reg, Options{})
	wantErr(t, err, "several roots are core")
	_, err = Resolve(ctx, []string{"core.multi#1.0.0"}, reg, Options{})
	wantErr(t, err, "exactly one")
	_, err = Resolve(ctx, []string{"core.none#1.0.0"}, reg, Options{})
	wantErr(t, err, "exactly one")
	_, err = Resolve(ctx, []string{"core.odd#1.0.0"}, reg, Options{})
	wantErr(t, err, "not M.m.p")
	// Otro núcleo de la misma serie (otro nombre) es error, no se excluye.
	_, err = Resolve(ctx, []string{"core.r4#4.0.1", "ig.core.other#1.0.0"}, reg, Options{})
	wantErr(t, err, "same FHIR series", "core.r4.other#1.0.0")
}

func TestResolveFHIRVersionsMultiple(t *testing.T) {
	// Un package que declara varias versiones sirve a los locks de ambas series.
	multi := ig("ig.multi", "1.0.0", []string{"4.0.1", "4.3.0"}, nil)
	reg := newMem(t, coreR4(), coreR4B(), multi)
	for _, core := range []string{"core.r4#4.0.1", "core.r4b#4.3.0"} {
		l := mustResolve(t, []string{core, "ig.multi#1.0.0"}, reg, Options{})
		if len(l.Order) != 2 {
			t.Fatalf("%s: %v", core, l.Order)
		}
	}
}

func TestResolveUndeclaredFHIRVersionIsRead(t *testing.T) {
	reg := newMem(t, coreR4(), pkg{name: "ig.nover", version: "1.0.0", typ: "IG", deps: map[string]string{"core.r4": "4.0.1"}})
	l := mustResolve(t, []string{"core.r4#4.0.1", "ig.nover#1.0.0"}, reg, Options{})
	p, _ := l.Package("ig.nover#1.0.0")
	if len(p.FHIRVersions) != 0 || len(l.Order) != 2 {
		t.Fatalf("%+v", p)
	}
}

func TestResolveRegistryServesAnotherPackage(t *testing.T) {
	reg := newMem(t, coreR4())
	reg["core.r4#9.9.9"] = reg["core.r4#4.0.1"] // el registro sirve otra versión bajo el nombre pedido
	_, err := Resolve(context.Background(), []string{"core.r4#9.9.9"}, reg, Options{})
	wantErr(t, err, "declares itself as core.r4#4.0.1")
}

func TestResolveRejectsBadRoots(t *testing.T) {
	reg := newMem(t, coreR4())
	for _, r := range []string{"core.r4", "#4.0.1", "core.r4#", "core.r4#current", "core.r4#^4.0.0", "core.r4#4.x", "a/b#1.0.0"} {
		if _, err := Resolve(context.Background(), []string{r}, reg, Options{}); err == nil {
			t.Errorf("root %q accepted", r)
		}
	}
	if _, err := Resolve(context.Background(), nil, reg, Options{}); err == nil {
		t.Error("no roots accepted")
	}
}

func TestResolveRootOrderDoesNotChangeTheLock(t *testing.T) {
	reg := r4bGraph(t)
	exc := Options{Exceptions: []Exception{{"term.r4#7.1.0", "r"}, {"ext.r4#5.2.0", "r"}}}
	roots := []string{"core.r4b#4.3.0", "ext.r4b#5.3.0", "term.r4#7.1.0", "ext.r4#5.2.0"}
	want, _ := mustResolve(t, roots, reg, exc).Marshal()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		rs := slices.Clone(roots)
		rng.Shuffle(len(rs), func(a, b int) { rs[a], rs[b] = rs[b], rs[a] })
		got, _ := mustResolve(t, rs, reg, exc).Marshal()
		if string(got) != string(want) {
			t.Fatalf("roots %v change the lock", rs)
		}
	}
	// La misma excepción en otro orden tampoco.
	exc2 := Options{Exceptions: []Exception{exc.Exceptions[1], exc.Exceptions[0]}}
	got, _ := mustResolve(t, roots, reg, exc2).Marshal()
	if string(got) != string(want) {
		t.Fatal("exception order changes the lock")
	}
}

// randomGraph arma un grafo aleatorio: un núcleo, n IG que dependen de IG de
// índice mayor (sin ciclos) o del núcleo, y devuelve el registro, las raíces y
// los packages alcanzables.
func randomGraph(t testing.TB, rng *rand.Rand) (memRegistry, []string, map[string]bool) {
	n := 2 + rng.Intn(14)
	core := coreR4()
	pkgs := []pkg{core}
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("ig.%02d", i)
	}
	deps := make([][]int, n)
	for i := 0; i < n; i++ {
		d := map[string]string{}
		if rng.Intn(3) > 0 {
			d["core.r4"] = "4.0.1"
		}
		for j := i + 1; j < n; j++ {
			if rng.Intn(4) == 0 {
				d[names[j]] = "1.0.0"
				deps[i] = append(deps[i], j)
			}
		}
		fhir := []string{"4.0.1"}
		switch rng.Intn(3) {
		case 0:
			fhir = nil
		case 1:
			fhir = []string{"4.0.1", "4.3.0"}
		}
		pkgs = append(pkgs, ig(names[i], "1.0.0", fhir, d))
	}
	roots := []string{core.id()}
	for i := 0; i < n; i++ {
		if rng.Intn(3) == 0 {
			roots = append(roots, names[i]+"#1.0.0")
		}
	}
	reach := map[string]bool{core.id(): true}
	var walk func(i int)
	walk = func(i int) {
		id := names[i] + "#1.0.0"
		if reach[id] {
			return
		}
		reach[id] = true
		for _, j := range deps[i] {
			walk(j)
		}
	}
	for _, r := range roots[1:] {
		for i := range names {
			if names[i]+"#1.0.0" == r {
				walk(i)
			}
		}
	}
	return newMem(t, pkgs...), roots, reach
}

func TestResolveRandomGraphs(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 300; i++ {
		reg, roots, reach := randomGraph(t, rng)
		l := mustResolve(t, roots, reg, Options{})
		if err := l.Validate(); err != nil {
			t.Fatalf("graph %d: %v", i, err)
		}
		got := map[string]bool{}
		for _, p := range l.Packages {
			got[p.ID()] = true
		}
		if !reflect.DeepEqual(got, reach) {
			t.Fatalf("graph %d: packages %v, want the reachable set %v", i, got, reach)
		}
		// Metamórfico: otro orden de raíces da el mismo lock, byte a byte.
		want, _ := l.Marshal()
		rs := slices.Clone(roots)
		rng.Shuffle(len(rs), func(a, b int) { rs[a], rs[b] = rs[b], rs[a] })
		again, _ := mustResolve(t, rs, reg, Options{}).Marshal()
		if string(again) != string(want) {
			t.Fatalf("graph %d: roots order changes the lock", i)
		}
		// Round trip del JSON.
		back, err := ReadLock(want)
		if err != nil || !reflect.DeepEqual(back, l) {
			t.Fatalf("graph %d: lock round trip: %v", i, err)
		}
	}
}

func TestResolveRandomGraphsWithADefect(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 200; i++ {
		reg, roots, reach := randomGraph(t, rng)
		// Un defecto al azar sobre un package alcanzable (no el núcleo).
		var victims []string
		for id := range reach {
			if id != "core.r4#4.0.1" {
				victims = append(victims, id)
			}
		}
		if len(victims) == 0 {
			continue
		}
		slices.Sort(victims)
		v := victims[rng.Intn(len(victims))]
		name, _, _ := strings.Cut(v, "#")
		switch rng.Intn(3) {
		case 0: // dependencia faltante
			reg[v] = ig(name, "1.0.0", []string{"4.0.1"}, map[string]string{"absent": "1.0.0"}).tgz(t)
			_, err := Resolve(context.Background(), roots, reg, Options{})
			wantErr(t, err, "absent#1.0.0")
		case 1: // el mismo package en otra versión, pedido por una raíz
			reg[name+"#2.0.0"] = ig(name, "2.0.0", []string{"4.0.1"}, nil).tgz(t)
			_, err := Resolve(context.Background(), append(slices.Clone(roots), name+"#2.0.0"), reg, Options{})
			wantErr(t, err, "more than one version", name+":")
		case 2: // un ciclo con él mismo
			reg[v] = ig(name, "1.0.0", []string{"4.0.1"}, map[string]string{name: "1.0.0"}).tgz(t)
			_, err := Resolve(context.Background(), roots, reg, Options{})
			wantErr(t, err, "dependency cycle")
		}
	}
}

// H3: Undeclared son los packages sin versión FHIR declarada que no son núcleo.
func TestAnalyzeUndeclared(t *testing.T) {
	reg := newMem(t, coreR4(),
		pkg{name: "ig.nover", version: "1.0.0", typ: "IG", deps: map[string]string{"core.r4": "4.0.1"}},
		ig("ig.ok", "1.0.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1"}))
	g, err := Explore(context.Background(), []string{"core.r4#4.0.1", "ig.nover#1.0.0", "ig.ok#1.0.0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Analyze().Undeclared; !slices.Equal(got, []string{"ig.nover#1.0.0"}) {
		t.Fatalf("undeclared %v", got)
	}
	// Un núcleo sin versión es un problema propio, no un package «sin versión declarada».
	reg = newMem(t, coreR4(), pkg{name: "core.nover", version: "1.0.0", typ: "Core"},
		ig("ig.dep", "1.0.0", []string{"4.0.1"}, map[string]string{"core.r4": "4.0.1", "core.nover": "1.0.0"}))
	g, err = Explore(context.Background(), []string{"core.r4#4.0.1", "ig.dep#1.0.0"}, reg, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := g.Analyze()
	if len(a.Undeclared) != 0 || len(a.Problems) != 1 || !strings.Contains(a.Problems[0], "core.nover#1.0.0 declares no FHIR version") {
		t.Fatalf("undeclared %v, problems %v", a.Undeclared, a.Problems)
	}
}
