package fhirpkg

import (
	"strings"
	"testing"
)

// R4 references.html §2.3.0.5 y §2.3.0.6: un canónico es url, con |version
// opcional, y puede terminar en #fragmento (un recurso contenido).
func TestParseCanonical(t *testing.T) {
	cases := []struct {
		in   string
		want Canonical
	}{
		{"http://x.org/fhir/ValueSet/a", Canonical{URL: "http://x.org/fhir/ValueSet/a"}},
		{"http://x.org/fhir/ValueSet/a|1.0.0", Canonical{URL: "http://x.org/fhir/ValueSet/a", Version: "1.0.0"}},
		{"http://x.org/fhir/ValueSet/a|1.0.0#frag", Canonical{URL: "http://x.org/fhir/ValueSet/a", Version: "1.0.0", Fragment: "frag"}},
		{"http://x.org/fhir/ValueSet/a#frag", Canonical{URL: "http://x.org/fhir/ValueSet/a", Fragment: "frag"}},
		{"#frag", Canonical{Fragment: "frag"}},
		// Versiones que no son semver ni fecha (plan 002 §1.3).
		{"http://x.org/CodeSystem/b|May 2021 Edition", Canonical{URL: "http://x.org/CodeSystem/b", Version: "May 2021 Edition"}},
		{"http://x.org/CodeSystem/b|2.9", Canonical{URL: "http://x.org/CodeSystem/b", Version: "2.9"}},
		{"urn:uuid:53fefa32-fcbb-4ff8-8a92-55ee120877b7", Canonical{URL: "urn:uuid:53fefa32-fcbb-4ff8-8a92-55ee120877b7"}},
	}
	for _, c := range cases {
		got, err := ParseCanonical(c.in)
		if err != nil {
			t.Errorf("ParseCanonical(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCanonical(%q) = %+v, want %+v", c.in, got, c.want)
		}
		if got.String() != c.in {
			t.Errorf("String() = %q, want %q", got.String(), c.in)
		}
	}
}

func TestParseCanonicalRejects(t *testing.T) {
	for _, in := range []string{
		"",                   // vacío
		"|1.0.0",             // versión sin url
		"http://x.org/a|",    // versión vacía
		"http://x.org/a#",    // fragmento vacío
		"http://x.org/a|1|2", // dos versiones
		" http://x.org/a",    // espacio al inicio
		"http://x.org/a ",    // espacio al final
		"http://x.org/a\n",   // salto de línea
		"http://x.org/a\x00", // NUL
		"|",
		"#",
		"http://x.org/a#b#c", // dos fragmentos
	} {
		if got, err := ParseCanonical(in); err == nil {
			t.Errorf("ParseCanonical(%q) = %+v, want an error", in, got)
		}
	}
}

func FuzzParseCanonical(f *testing.F) {
	for _, s := range []string{"http://x/a", "http://x/a|1.0", "http://x/a|1.0#f", "#f", "", "|", "a|b|c", "a#b#c", "urn:oid:1.2.3"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		c, err := ParseCanonical(in)
		if err != nil {
			return
		}
		if strings.ContainsAny(c.URL, "|#") || strings.Contains(c.Version, "|") || strings.Contains(c.Version, "#") || strings.Contains(c.Fragment, "#") {
			t.Fatalf("ParseCanonical(%q) = %+v keeps a separator", in, c)
		}
		if c.URL == "" && c.Fragment == "" {
			t.Fatalf("ParseCanonical(%q) = %+v is empty", in, c)
		}
		if c.URL == "" && c.Version != "" {
			t.Fatalf("ParseCanonical(%q) = %+v has a version without a url", in, c)
		}
		// Ida y vuelta.
		if c.String() != in {
			t.Fatalf("String() = %q, input %q", c.String(), in)
		}
		again, err := ParseCanonical(c.String())
		if err != nil || again != c {
			t.Fatalf("reparse of %q = %+v, %v", in, again, err)
		}
	})
}
