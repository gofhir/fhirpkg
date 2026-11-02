package fhirpkg

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestParseSemver(t *testing.T) {
	ok := []string{"0.0.0", "4.0.1", "10.20.30", "1.0.0-rc.1", "5.2.0-snapshot1"}
	bad := []string{"", "4.0", "4", "4.0.1.2", "04.0.1", "4.0.x", "a.b.c", "1.0.0-", "-1.0.0", "4.0.1 ", "2020-01-01", "1.0.-1", "1..0"}
	for _, s := range ok {
		if _, o := parseSemver(s); !o {
			t.Errorf("%q should be semver", s)
		}
	}
	for _, s := range bad {
		if _, o := parseSemver(s); o {
			t.Errorf("%q should not be semver", s)
		}
	}
}

func TestCompareSemverOrder(t *testing.T) {
	// Orden de la especificación de semver, de menor a mayor.
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0"}
	for i, a := range order {
		for j, b := range order {
			sa, _ := parseSemver(a)
			sb, _ := parseSemver(b)
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := compareSemver(sa, sb); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestCompareSemverProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	gen := func() semver {
		s := semver{rng.Intn(4), rng.Intn(4), rng.Intn(4), ""}
		if rng.Intn(3) == 0 {
			s.pre = []string{"a", "b", "1", "2", "a.1", "rc.10"}[rng.Intn(6)]
		}
		return s
	}
	for i := 0; i < 2000; i++ {
		a, b, c := gen(), gen(), gen()
		if compareSemver(a, b) != -compareSemver(b, a) {
			t.Fatalf("not antisymmetric: %v %v", a, b)
		}
		if compareSemver(a, b) <= 0 && compareSemver(b, c) <= 0 && compareSemver(a, c) > 0 {
			t.Fatalf("not transitive: %v %v %v", a, b, c)
		}
	}
}

func TestSeries(t *testing.T) {
	for in, want := range map[string]string{"4.0.1": "4.0", "4.3.0": "4.3", "5.0.0": "5.0", "4.0.1-cibuild": "4.0", "6.0.0-ballot": "6.0"} {
		if got, ok := series(in); !ok || got != want {
			t.Errorf("series(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "4.0", "current", "4.0.x"} {
		if _, ok := series(in); ok {
			t.Errorf("series(%q) should fail", in)
		}
	}
}

func TestParseSpec(t *testing.T) {
	good := map[string]versionSpec{
		"5.2.0": {exact: "5.2.0"}, "2.9": {exact: "2.9"}, "20200101": {exact: "20200101"},
		"4.0.x": {prefix: "4.0", wildcard: true}, "4.3.*": {prefix: "4.3", wildcard: true},
	}
	for in, want := range good {
		got, err := parseSpec(in)
		if err != nil || got != want {
			t.Errorf("parseSpec(%q) = %+v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "current", "dev", "latest", "*", "x", "^4.0.0", "~4.0.0", ">=4.0.0", "4.x", "4.x.x", "4.0.*.1", "1.0.0 - 2.0.0", "a#b", "4.*.0"} {
		if _, err := parseSpec(in); err == nil {
			t.Errorf("parseSpec(%q) should fail", in)
		}
	}
}

func TestSpecPickAndMatches(t *testing.T) {
	avail := []string{"4.0.0", "4.0.1", "4.0.9", "4.0.10", "4.1.0", "4.0.11-rc", "4.0", "3.0.0", "bogus"}
	vs, _ := parseSpec("4.0.x")
	if got, ok := vs.pick(avail); !ok || got != "4.0.10" {
		t.Fatalf("pick = %q, %v", got, ok)
	}
	for _, v := range avail {
		want := v == "4.0.0" || v == "4.0.1" || v == "4.0.9" || v == "4.0.10"
		if vs.matches(v) != want {
			t.Errorf("matches(%q) = %v", v, !want)
		}
	}
	none, _ := parseSpec("9.9.x")
	if _, ok := none.pick(avail); ok {
		t.Fatal("pick should fail")
	}
	// La elegida siempre cumple el comodín, y es la mayor, para muchos conjuntos.
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 300; i++ {
		var vers []string
		for j := 0; j < 1+rng.Intn(8); j++ {
			vers = append(vers, fmt.Sprintf("%d.%d.%d", 3+rng.Intn(2), rng.Intn(2), rng.Intn(30)))
		}
		got, ok := vs.pick(vers)
		var matching []string
		for _, v := range vers {
			if vs.matches(v) {
				matching = append(matching, v)
			}
		}
		if ok != (len(matching) > 0) {
			t.Fatalf("pick(%v) ok=%v", vers, ok)
		}
		if ok {
			sort.Slice(matching, func(a, b int) bool {
				x, _ := parseSemver(matching[a])
				y, _ := parseSemver(matching[b])
				return compareSemver(x, y) < 0
			})
			if got != matching[len(matching)-1] {
				t.Fatalf("pick(%v) = %s, want %s", vers, got, matching[len(matching)-1])
			}
		}
	}
}
