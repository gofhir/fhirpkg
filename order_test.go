package fhirpkg

import (
	"slices"
	"testing"
)

// SemVer 2.0.0 §11: la cadena de precedencia de sus ejemplos.
func TestStrictSemverPrecedence(t *testing.T) {
	chain := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for i, a := range chain {
		for j, b := range chain {
			got, ok := compareSemverStrict(a, b)
			if !ok {
				t.Fatalf("%q or %q is not semver", a, b)
			}
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("compare(%q, %q) = %d, want %d", a, b, got, want)
			}
		}
	}
	// SemVer 2.0.0 §11: 1.0.0 < 2.0.0 < 2.1.0 < 2.1.1.
	for _, p := range [][2]string{{"1.0.0", "2.0.0"}, {"2.0.0", "2.1.0"}, {"2.1.0", "2.1.1"}, {"2.9.0", "2.10.0"}} {
		if got, ok := compareSemverStrict(p[0], p[1]); !ok || got != -1 {
			t.Errorf("compare(%q, %q) = %d, %v", p[0], p[1], got, ok)
		}
	}
	// SemVer 2.0.0 §10: los metadatos de build no cuentan para la precedencia.
	if got, ok := compareSemverStrict("1.0.0+a", "1.0.0+b"); !ok || got != 0 {
		t.Errorf("build metadata: %d, %v", got, ok)
	}
}

// SemVer 2.0.0 §2, §9 y §10: lo que no es una versión.
func TestStrictSemverRejects(t *testing.T) {
	for _, v := range []string{
		"", "2.9", "3.0", "1", "1.0", "1.0.0.0", "01.0.0", "1.00.0", "1.0.00", "-1.0.0", "1.0.0-", "1.0.0-01", "1.0.0-a..b",
		"1.0.0-a b", "1.0.0+", "1.0.0+a..b", "v1.0.0", "1.0.x", "2013-11", "20190905", "May 2021 Edition", "1.0.0-é", "1.0.0 ", " 1.0.0",
	} {
		if _, ok := compareSemverStrict(v, "1.0.0"); ok {
			t.Errorf("%q accepted as semver", v)
		}
	}
	// Un identificador de prerelease alfanumérico puede empezar con cero.
	if _, ok := compareSemverStrict("1.0.0-0a", "1.0.0"); !ok {
		t.Error("1.0.0-0a rejected")
	}
}

// FHIR date (datatypes.html): YYYY, YYYY-MM, YYYY-MM-DD.
func TestFHIRDateKey(t *testing.T) {
	good := map[string]string{
		"2013": "2013-01-01", "2013-11": "2013-11-01", "2013-11-15": "2013-11-15",
		"2024-02-29": "2024-02-29", "0001": "0001-01-01",
	}
	for in, want := range good {
		if got, ok := fhirDateKey(in); !ok || got != want {
			t.Errorf("fhirDateKey(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "13", "2013-1", "2013-13", "2013-00", "2013-02-30", "2023-02-29", "2013-11-00", "2013-11-32", "20130101", "2013-11-15T10:00:00Z", "2013/11", " 2013", "2013-11-15 ", "2.9", "0000", "２０１３"} {
		if got, ok := fhirDateKey(in); ok {
			t.Errorf("fhirDateKey(%q) = %q, want no date", in, got)
		}
	}
}

// version-algorithm (R5): natural compara los números de una cadena como números.
func TestNaturalCompare(t *testing.T) {
	asc := []string{"1", "2", "2.9", "2.10", "2.10a", "10", "a2", "a10", "a10b"}
	for i, a := range asc {
		for j, b := range asc {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := compareNatural(a, b); got != want {
				t.Errorf("compareNatural(%q, %q) = %d, want %d", a, b, got, want)
			}
		}
	}
	if compareNatural("007", "7") != 0 {
		t.Error("leading zeros must not matter")
	}
}

func TestOrderChoice(t *testing.T) {
	semver := func(vs ...string) []versionDecl {
		var out []versionDecl
		for _, v := range vs {
			out = append(out, versionDecl{Version: v})
		}
		return out
	}
	alg := func(code string, vs ...string) []versionDecl {
		var out []versionDecl
		for _, v := range vs {
			out = append(out, versionDecl{Version: v, Algorithm: code})
		}
		return out
	}
	cases := []struct {
		name string
		in   []versionDecl
		want string // "" si el paso no separa
	}{
		{"semver inferred", semver("1.0.0", "1.1.0"), "semver"},
		{"semver and non-semver", semver("1.0.0", "2.9"), ""},
		{"2.9 against 3.0 (plan 002 §1.3)", semver("2.9", "3.0"), ""},
		{"dates", semver("2013-11", "2014"), "date"},
		{"dates and semver", semver("2013-11", "1.0.0"), ""},
		{"prose", semver("May 2021 Edition", "Nov 2021 Edition"), ""},
		{"empty version", semver("", "1.0.0"), ""},
		{"declared integer", alg("integer", "9", "10"), "integer"},
		{"declared integer with a non-integer", alg("integer", "9", "x"), ""},
		{"declared natural", alg("natural", "v2", "v10"), "natural"},
		{"declared alpha", alg("alpha", "b", "a"), "alpha"},
		{"declared date", alg("date", "2013-11", "2014"), "date"},
		{"declared semver", alg("semver", "1.0.0", "1.1.0"), "semver"},
		{"declared semver over dates", alg("semver", "2013-11", "2014"), "date"}, // no se cumple lo declarado: se infiere
		{"different algorithms", append(alg("integer", "9"), alg("alpha", "10")...), ""},
		{"only one declares", append(alg("integer", "9"), semver("10")...), ""}, // 9 y 10 no son semver ni fecha
		{"unknown algorithm falls back to inference", alg("other", "1.0.0", "1.1.0"), "semver"},
		{"expression is not comparable", []versionDecl{{Version: "1.0.0", Expression: true}, {Version: "1.1.0"}}, ""},
		{"one candidate", semver("1.0.0"), "semver"},
	}
	for _, c := range cases {
		got, _ := chooseOrder(c.in)
		if got.name != c.want {
			t.Errorf("%s: order %q, want %q", c.name, got.name, c.want)
		}
	}
}

// El orden elegido es un orden: reflexivo, antisimétrico y transitivo.
func TestOrdersAreTotalPreorders(t *testing.T) {
	samples := map[string][]string{
		"semver":  {"1.0.0", "1.0.0-rc.1", "1.0.1", "2.0.0", "10.0.0", "1.0.0+b"},
		"integer": {"1", "2", "10", "-3", "007"},
		"alpha":   {"a", "b", "B", "ab", ""},
		"date":    {"2013", "2013-11", "2013-11-15", "2014", "2013-01-01"},
		"natural": {"1", "2", "10", "a1", "a10", "a2", "2.9", "2.10"},
	}
	for name, vs := range samples {
		o, ok := orderByName(name)
		if !ok {
			t.Fatalf("no order %s", name)
		}
		for _, a := range vs {
			if o.cmp(a, a) != 0 {
				t.Errorf("%s: cmp(%q, %q) != 0", name, a, a)
			}
			for _, b := range vs {
				if o.cmp(a, b) != -o.cmp(b, a) {
					t.Errorf("%s: cmp(%q, %q) is not antisymmetric", name, a, b)
				}
				for _, c := range vs {
					if o.cmp(a, b) <= 0 && o.cmp(b, c) <= 0 && o.cmp(a, c) > 0 {
						t.Errorf("%s: %q <= %q <= %q but not %q <= %q", name, a, b, c, a, c)
					}
				}
			}
		}
		sorted := slices.Clone(vs)
		slices.SortFunc(sorted, o.cmp)
		if len(sorted) != len(vs) {
			t.Fatal("sort lost values")
		}
	}
}

func FuzzCompareVersions(f *testing.F) {
	for _, s := range []string{"1.0.0", "1.0.0-rc.1", "2.9", "2013-11", "10", "a10", "", "May 2021 Edition", "1.0.0+x"} {
		f.Add(s, "1.0.0")
		f.Add("2.10.0", s)
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		for _, name := range orderNames {
			o, _ := orderByName(name)
			if !o.valid(a) {
				continue
			}
			if !o.valid(b) {
				continue
			}
			if o.cmp(a, a) != 0 || o.cmp(b, b) != 0 {
				t.Fatalf("%s: not reflexive on %q, %q", name, a, b)
			}
			if o.cmp(a, b) != -o.cmp(b, a) {
				t.Fatalf("%s: cmp(%q, %q) = %d, cmp(%q, %q) = %d", name, a, b, o.cmp(a, b), b, a, o.cmp(b, a))
			}
		}
	})
}

// Code review ronda 1, M3: el orden integer con negativos (un número negativo
// con más dígitos es menor).
func TestIntegerOrderWithNegatives(t *testing.T) {
	o, _ := orderByName(algInteger)
	asc := []string{"-100", "-10", "-9", "-1", "0", "1", "9", "10", "100"}
	for i, a := range asc {
		for j, b := range asc {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := o.cmp(a, b); got != want {
				t.Errorf("cmp(%q, %q) = %d, want %d", a, b, got, want)
			}
		}
	}
	if o.cmp("-0", "0") != 0 || o.cmp("-007", "-7") != 0 {
		t.Error("-0 and leading zeros must not matter")
	}
	if o.valid("--1") || o.valid("+1") || o.valid("1.5") || o.valid("-") || o.valid("") {
		t.Error("invalid integer accepted")
	}
}
