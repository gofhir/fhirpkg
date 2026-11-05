package fhirpkg

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Canonical es un canónico FHIR descompuesto (R4 references.html §2.3.0.5 y
// §2.3.0.6): url, con |version opcional y #fragmento opcional (un recurso
// contenido).
type Canonical struct {
	URL      string
	Version  string
	Fragment string
}

// String vuelve a armar el canónico.
func (c Canonical) String() string {
	s := c.URL
	if c.Version != "" {
		s += "|" + c.Version
	}
	if c.Fragment != "" {
		s += "#" + c.Fragment
	}
	return s
}

// checkCanonicalText rechaza el canónico vacío, con UTF-8 inválido o con
// caracteres de control.
func checkCanonicalText(s string) error {
	if s == "" {
		return errors.New("empty canonical")
	}
	if !utf8.ValidString(s) {
		return errors.New("canonical is not valid UTF-8")
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("canonical %q has a control character", s)
		}
	}
	return nil
}

// ParseCanonical descompone url[|version][#fragmento]. Es estricto: un canónico
// vacío, una versión o un fragmento vacíos, más de una versión o un fragmento,
// espacios dentro de la url o del fragmento, espacios en los bordes de la
// versión y caracteres de control son error. Un fragmento sin url ("#id") es
// válido: Resolve decide qué hacer con él.
func ParseCanonical(s string) (Canonical, error) {
	if err := checkCanonicalText(s); err != nil {
		return Canonical{}, err
	}
	rest, frag, hasFrag := strings.Cut(s, "#")
	if hasFrag && (frag == "" || strings.Contains(frag, "#")) {
		return Canonical{}, fmt.Errorf("canonical %q: the fragment is empty or repeated", s)
	}
	url, version, hasVersion := strings.Cut(rest, "|")
	if hasVersion && (version == "" || strings.Contains(version, "|")) {
		return Canonical{}, fmt.Errorf("canonical %q: the version is empty or repeated", s)
	}
	if url == "" && !hasFrag {
		return Canonical{}, fmt.Errorf("canonical %q has no url", s)
	}
	if url == "" && hasVersion {
		return Canonical{}, fmt.Errorf("canonical %q has a version but no url", s)
	}
	if strings.IndexFunc(url, unicode.IsSpace) >= 0 || strings.IndexFunc(frag, unicode.IsSpace) >= 0 {
		return Canonical{}, fmt.Errorf("canonical %q has whitespace in the url or the fragment", s)
	}
	if strings.TrimSpace(version) != version {
		return Canonical{}, fmt.Errorf("canonical %q: the version has leading or trailing whitespace", s)
	}
	return Canonical{URL: url, Version: version, Fragment: frag}, nil
}
