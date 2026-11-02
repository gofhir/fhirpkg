package fhirpkg

import (
	"fmt"
	"strconv"
	"strings"
)

// semver es una versión M.m.p con prerelease opcional. Los packages FHIR no
// siempre usan semver (hay versiones como "2.9" o fechas), por eso parseSemver
// informa si la cadena lo es y el resto del código no lo exige.
type semver struct {
	major, minor, patch int
	pre                 string
}

func parseSemver(s string) (semver, bool) {
	core, pre, hasPre := strings.Cut(s, "-")
	if hasPre && pre == "" {
		return semver{}, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return semver{}, false
			}
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		n[i] = v
	}
	return semver{n[0], n[1], n[2], pre}, true
}

// compareSemver devuelve -1, 0 o 1. Una versión sin prerelease es mayor que la
// misma con prerelease; los prerelease se comparan por identificador, los
// numéricos como números.
func compareSemver(a, b semver) int {
	for _, d := range [3][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.pre == b.pre:
		return 0
	case a.pre == "":
		return 1
	case b.pre == "":
		return -1
	}
	ai, bi := strings.Split(a.pre, "."), strings.Split(b.pre, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		if c := compareIdentifier(ai[i], bi[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(ai) < len(bi):
		return -1
	case len(ai) > len(bi):
		return 1
	}
	return 0
}

func compareIdentifier(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
		return 0
	case aerr == nil:
		return -1 // los numéricos van antes que los alfanuméricos
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// series devuelve "mayor.menor" de una versión FHIR ("4.0.1" y "4.0.1-cibuild"
// dan "4.0"). Una versión FHIR sin tres componentes numéricos no tiene serie.
func series(v string) (string, bool) {
	sv, ok := parseSemver(v)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%d.%d", sv.major, sv.minor), true
}

// versionSpec es lo que una dependencia pide: una versión exacta o un comodín
// de patch ("4.0.x" o "4.0.*").
type versionSpec struct {
	exact string
	// prefix es "M.m" cuando la petición es un comodín de patch.
	prefix   string
	wildcard bool
}

func parseSpec(s string) (versionSpec, error) {
	if s == "" {
		return versionSpec{}, fmt.Errorf("empty version")
	}
	if strings.ContainsAny(s, " \t\n^~<>=|#/") {
		return versionSpec{}, fmt.Errorf("unsupported version range %q", s)
	}
	switch s {
	case "current", "dev", "latest", "*", "x":
		return versionSpec{}, fmt.Errorf("unsupported version %q (only exact versions and patch wildcards)", s)
	}
	if strings.HasSuffix(s, ".x") || strings.HasSuffix(s, ".*") {
		prefix := s[:len(s)-2]
		if _, ok := series(prefix + ".0"); !ok {
			return versionSpec{}, fmt.Errorf("unsupported wildcard %q (expected M.m.x)", s)
		}
		return versionSpec{prefix: prefix, wildcard: true}, nil
	}
	if strings.Contains(s, "*") {
		return versionSpec{}, fmt.Errorf("unsupported version %q", s)
	}
	return versionSpec{exact: s}, nil
}

// pick elige, entre las versiones disponibles, la mayor semver que cumple el
// comodín. Las versiones que no son semver y los prerelease no participan.
func (v versionSpec) pick(available []string) (string, bool) {
	if !v.wildcard {
		return v.exact, true
	}
	var best semver
	var bestStr string
	for _, a := range available {
		sv, ok := parseSemver(a)
		if !ok || sv.pre != "" || fmt.Sprintf("%d.%d", sv.major, sv.minor) != v.prefix {
			continue
		}
		if bestStr == "" || compareSemver(sv, best) > 0 {
			best, bestStr = sv, a
		}
	}
	return bestStr, bestStr != ""
}

// matches dice si una versión resuelta cumple lo que pedía la dependencia.
func (v versionSpec) matches(version string) bool {
	if !v.wildcard {
		return v.exact == version
	}
	sv, ok := parseSemver(version)
	return ok && sv.pre == "" && fmt.Sprintf("%d.%d", sv.major, sv.minor) == v.prefix
}
