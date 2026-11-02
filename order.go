package fhirpkg

import (
	"strings"
)

// Orden de versiones (plan 002 §3.1, paso 4). Las versiones de los recursos
// canónicos no siempre son semver (plan 001: 1.231 de 4.577 en el núcleo R4),
// así que el comparador se elige para todo el conjunto de candidatos y, si no
// hay uno que valga para todos, el paso no separa.

// versionDecl es lo que un candidato dice de su versión.
type versionDecl struct {
	Version string
	// Algorithm es el código de versionAlgorithm[x] cuando es un Coding (R5);
	// vacío si no lo declara.
	Algorithm string
	// Expression indica que declara el algoritmo como expresión FHIRPath, que
	// aquí no se evalúa: el orden queda como no comparable.
	Expression bool
}

// versionOrder es un orden total preliminar (un preorden: versiones distintas
// pueden empatar) sobre las cadenas que valid acepta. cmp solo se llama con
// cadenas válidas.
type versionOrder struct {
	name  string
	valid func(string) bool
	cmp   func(a, b string) int
}

// orderNames son los órdenes que se pueden declarar o inferir.
var orderNames = []string{algSemver, algInteger, algAlpha, algDate, algNatural}

func orderByName(name string) (versionOrder, bool) {
	switch name {
	case algSemver:
		return versionOrder{name, func(s string) bool { _, ok := parseStrictSemver(s); return ok }, func(a, b string) int {
			x, _ := parseStrictSemver(a)
			y, _ := parseStrictSemver(b)
			return cmpStrictSemver(x, y)
		}}, true
	case algInteger:
		return versionOrder{name, func(s string) bool { _, ok := parseInteger(s); return ok }, func(a, b string) int {
			x, _ := parseInteger(a)
			y, _ := parseInteger(b)
			return cmpInteger(x, y)
		}}, true
	case algAlpha:
		return versionOrder{name, func(s string) bool { return s != "" }, strings.Compare}, true
	case algDate:
		return versionOrder{name, func(s string) bool { _, ok := fhirDateKey(s); return ok }, func(a, b string) int {
			x, _ := fhirDateKey(a)
			y, _ := fhirDateKey(b)
			return strings.Compare(x, y)
		}}, true
	case algNatural:
		return versionOrder{name, func(s string) bool { return s != "" }, compareNatural}, true
	}
	return versionOrder{}, false
}

// chooseOrder elige el orden de un conjunto de candidatos:
//
//  1. si alguno declara su algoritmo como expresión, ninguno (no se evalúa);
//  2. el algoritmo declarado, si todos declaran el mismo, es uno conocido y
//     todas las versiones lo cumplen;
//  3. semver estricto, si todas las versiones lo son;
//  4. fecha FHIR, si todas lo son.
//
// Si ninguno aplica devuelve false: el paso no separa.
func chooseOrder(ds []versionDecl) (versionOrder, bool) {
	if len(ds) == 0 {
		return versionOrder{}, false
	}
	same := ds[0].Algorithm != ""
	for _, d := range ds {
		if d.Expression {
			return versionOrder{}, false
		}
		if d.Algorithm != ds[0].Algorithm {
			same = false
		}
	}
	allValid := func(o versionOrder) bool {
		for _, d := range ds {
			if !o.valid(d.Version) {
				return false
			}
		}
		return true
	}
	if same {
		if o, ok := orderByName(ds[0].Algorithm); ok && allValid(o) {
			return o, true
		}
	}
	for _, name := range []string{algSemver, algDate} {
		if o, _ := orderByName(name); allValid(o) {
			return o, true
		}
	}
	return versionOrder{}, false
}

// strictSemver es una versión de SemVer 2.0.0. Los números se guardan como
// cadenas para no desbordar.
type strictSemver struct {
	core [3]string
	pre  []string
}

// parseStrictSemver acepta exactamente la gramática de SemVer 2.0.0 (§2, §9 y
// §10): MAYOR.MENOR.PARCHE sin ceros a la izquierda, prerelease opcional tras
// "-" y metadatos de build tras "+", que no cuentan en la precedencia.
func parseStrictSemver(s string) (strictSemver, bool) {
	var v strictSemver
	rest, build, hasBuild := strings.Cut(s, "+")
	if hasBuild && !identifiers(build, false) {
		return v, false
	}
	core, pre, hasPre := strings.Cut(rest, "-")
	if hasPre {
		if !identifiers(pre, true) {
			return v, false
		}
		v.pre = strings.Split(pre, ".")
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		if !numeric(p) || (len(p) > 1 && p[0] == '0') {
			return v, false
		}
		v.core[i] = p
	}
	return v, true
}

// identifiers valida una lista de identificadores separados por puntos
// ([0-9A-Za-z-]+). En un prerelease, uno numérico no lleva ceros a la izquierda.
func identifiers(s string, prerelease bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		for i := 0; i < len(id); i++ {
			c := id[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
				return false
			}
		}
		if prerelease && numeric(id) && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// cmpDigits compara dos cadenas de dígitos como números.
func cmpDigits(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// cmpStrictSemver aplica la precedencia de SemVer 2.0.0 §11.
func cmpStrictSemver(a, b strictSemver) int {
	for i := range a.core {
		if c := cmpDigits(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1 // una versión sin prerelease es mayor
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, yn := numeric(x), numeric(y)
		switch {
		case xn && yn:
			if c := cmpDigits(x, y); c != 0 {
				return c
			}
		case xn:
			return -1 // los identificadores numéricos van antes que los alfanuméricos
		case yn:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// compareSemverStrict compara dos versiones SemVer 2.0.0. El segundo valor es
// falso si alguna no lo es.
func compareSemverStrict(a, b string) (int, bool) {
	x, ok := parseStrictSemver(a)
	if !ok {
		return 0, false
	}
	y, ok := parseStrictSemver(b)
	if !ok {
		return 0, false
	}
	return cmpStrictSemver(x, y), true
}

// integerValue es un entero con signo en forma normal (sin ceros a la izquierda;
// el cero no es negativo).
type integerValue struct {
	neg    bool
	digits string
}

func parseInteger(s string) (integerValue, bool) {
	neg := strings.HasPrefix(s, "-")
	d := strings.TrimPrefix(s, "-")
	if !numeric(d) {
		return integerValue{}, false
	}
	d = strings.TrimLeft(d, "0")
	if d == "" {
		return integerValue{digits: "0"}, true
	}
	return integerValue{neg, d}, true
}

func cmpInteger(a, b integerValue) int {
	switch {
	case a.neg && !b.neg:
		return -1
	case !a.neg && b.neg:
		return 1
	}
	c := cmpDigits(a.digits, b.digits)
	if a.neg {
		return -c
	}
	return c
}

// fhirDateKey valida una fecha FHIR (datatypes.html, date: YYYY, YYYY-MM o
// YYYY-MM-DD) y devuelve su inicio como YYYY-MM-DD, que se ordena como texto.
func fhirDateKey(s string) (string, bool) {
	var y, m, d string
	switch len(s) {
	case 4:
		y, m, d = s, "01", "01"
	case 7:
		if s[4] != '-' {
			return "", false
		}
		y, m, d = s[:4], s[5:], "01"
	case 10:
		if s[4] != '-' || s[7] != '-' {
			return "", false
		}
		y, m, d = s[:4], s[5:7], s[8:]
	default:
		return "", false
	}
	if !numeric(y) || !numeric(m) || !numeric(d) || len(m) != 2 || len(d) != 2 || y == "0000" {
		return "", false
	}
	yi := int(y[0]-'0')*1000 + int(y[1]-'0')*100 + int(y[2]-'0')*10 + int(y[3]-'0')
	mi := int(m[0]-'0')*10 + int(m[1]-'0')
	di := int(d[0]-'0')*10 + int(d[1]-'0')
	if mi < 1 || mi > 12 || di < 1 || di > daysIn(yi, mi) {
		return "", false
	}
	return y + "-" + m + "-" + d, true
}

func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// compareNatural ordena cadenas comparando las secuencias de dígitos como
// números y el resto byte a byte (version-algorithm natural). Un número va
// antes que un texto; si una cadena es prefijo de la otra, es la menor.
func compareNatural(a, b string) int {
	for a != "" && b != "" {
		ad, bd := a[0] >= '0' && a[0] <= '9', b[0] >= '0' && b[0] <= '9'
		var ta, tb string
		ta, a = naturalToken(a, ad)
		tb, b = naturalToken(b, bd)
		switch {
		case ad && bd:
			if c := cmpDigits(ta, tb); c != 0 {
				return c
			}
		case ad:
			return -1
		case bd:
			return 1
		default:
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
		}
	}
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	}
	return 1
}

// naturalToken separa la secuencia inicial de dígitos (o de no dígitos).
func naturalToken(s string, digits bool) (token, rest string) {
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') == digits {
		i++
	}
	return s[:i], s[i:]
}
