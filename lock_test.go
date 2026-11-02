package fhirpkg

import (
	"slices"
	"strings"
	"testing"
)

func validLock(t testing.TB) Lock {
	reg := r4bGraph(t)
	return mustResolve(t, []string{"core.r4b#4.3.0", "ext.r4b#5.3.0"}, reg,
		Options{Exceptions: []Exception{{"term.r4#7.1.0", "dependency"}, {"ext.r4#5.2.0", "dependency"}}})
}

func TestLockRoundTripAndStrictRead(t *testing.T) {
	l := validLock(t)
	b, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLock(b); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(string) string{
		"unknown field": func(s string) string { return strings.Replace(s, `"schema": 1`, `"schema": 1, "extra": true`, 1) },
		"trailing data": func(s string) string { return s + "{}" },
		"truncated":     func(s string) string { return s[:len(s)/2] },
		"bad schema":    func(s string) string { return strings.Replace(s, `"schema": 1`, `"schema": 2`, 1) },
	} {
		if _, err := ReadLock([]byte(mutate(string(b)))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLockValidate(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Lock)
		want   string
	}{
		"empty fhirVersion": {func(l *Lock) { l.FHIRVersion = "" }, "fhirVersion"},
		"core not in lock":  {func(l *Lock) { l.Core = "nope#1" }, "core"},
		"core is excluded": {func(l *Lock) {
			for i := range l.Packages {
				if l.Packages[i].ID() == l.Core {
					l.Packages[i].Excluded, l.Packages[i].ExcludedReason = true, "x"
				}
			}
		}, "excluded"},
		"core declares another version": {func(l *Lock) { l.FHIRVersion = "4.0.1" }, "declares"},
		"bad sha":                       {func(l *Lock) { l.Packages[0].SHA256 = "ABC" }, "sha256"},
		"excluded without reason": {func(l *Lock) {
			for i := range l.Packages {
				if l.Packages[i].Excluded {
					l.Packages[i].ExcludedReason = ""
				}
			}
		}, "excludedReason"},
		"duplicate package": {func(l *Lock) { l.Packages = append(l.Packages, l.Packages[len(l.Packages)-1]) }, "duplicate"},
		"same package in two versions": {func(l *Lock) {
			p := l.Packages[len(l.Packages)-1]
			p.Version = "99.0.0"
			l.Packages = append(l.Packages, p)
		}, "two versions"},
		"unsorted packages":   {func(l *Lock) { l.Packages[0], l.Packages[1] = l.Packages[1], l.Packages[0] }, "sorted"},
		"order drops one":     {func(l *Lock) { l.Order = l.Order[1:] }, "order"},
		"order lists twice":   {func(l *Lock) { l.Order = append(l.Order, l.Order[0]) }, "twice"},
		"order lists unknown": {func(l *Lock) { l.Order[0] = "nope#1" }, "order"},
		"order is not topological": {func(l *Lock) {
			l.Order[0], l.Order[len(l.Order)-1] = l.Order[len(l.Order)-1], l.Order[0]
		}, "before its dependency"},
		"order lists an excluded package": {func(l *Lock) { l.Order = append(l.Order, "core.r4#4.0.1") }, "not a package that is read"},
		"duplicate dependency": {func(l *Lock) {
			d := l.Packages[0].ID()
			l.Packages[1].Dependencies = []string{d, d}
		}, "twice"},
		"dependency not in lock":          {func(l *Lock) { l.Packages[1].Dependencies = []string{"zzz#1"} }, "not in the lock"},
		"root not in lock":                {func(l *Lock) { l.Roots = []string{"zzz#1"} }, "root"},
		"exception for unknown":           {func(l *Lock) { l.Exceptions = append(l.Exceptions, Exception{"zzz#1", "r"}) }, "exception"},
		"exception without reason":        {func(l *Lock) { l.Exceptions[0].Reason = "" }, "reason"},
		"exception for the excluded core": {func(l *Lock) { l.Exceptions = append(l.Exceptions, Exception{"core.r4#4.0.1", "r"}) }, "excluded"},
		"two exceptions":                  {func(l *Lock) { l.Exceptions = append(l.Exceptions, l.Exceptions[0]) }, "two exceptions"},
	}
	if err := validLock(t).Validate(); err != nil {
		t.Fatal(err)
	}
	for name, c := range cases {
		l := validLock(t) // cada caso, su copia
		c.mutate(&l)
		err := l.Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want %q", name, err, c.want)
		}
	}
}

// H1: una raíz no puede estar excluida (solo se excluye el núcleo de otra
// serie, que nadie pidió).
func TestLockValidateRejectsAnExcludedRoot(t *testing.T) {
	l := mustResolve(t, []string{"core.r4b#4.3.0", "ext.r4b#5.3.0"}, r4bGraph(t),
		Options{Exceptions: []Exception{{"term.r4#7.1.0", "d"}, {"ext.r4#5.2.0", "d"}}})
	for i := range l.Packages {
		if l.Packages[i].Name == "ext.r4b" {
			l.Packages[i].Excluded, l.Packages[i].ExcludedReason = true, "edited by hand"
		}
	}
	l.Order = slices.DeleteFunc(l.Order, func(id string) bool { return id == "ext.r4b#5.3.0" })
	wantErr(t, l.Validate(), "root ext.r4b#5.3.0 is excluded")
}
