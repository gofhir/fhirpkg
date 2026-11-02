package fhirpkg

import (
	"slices"
	"strings"
	"testing"
)

func TestReadManifest(t *testing.T) {
	cases := []struct {
		name, in string
		want     Manifest
	}{
		{"r4 core: legacy list", `{"name":"hl7.fhir.r4.core","version":"4.0.1","type":"fhir.core","fhir-version-list":["4.0.1"]}`,
			Manifest{Name: "hl7.fhir.r4.core", Version: "4.0.1", Type: "fhir.core", FHIRVersions: []string{"4.0.1"}, LegacyVersionList: true}},
		{"fhirVersions", `{"name":"a","version":"1.0.0","fhirVersions":["4.3.0"],"dependencies":{"b":"2.1.x"}}`,
			Manifest{Name: "a", Version: "1.0.0", FHIRVersions: []string{"4.3.0"}, Dependencies: map[string]string{"b": "2.1.x"}}},
		{"fhirVersions wins over the legacy list", `{"name":"a","version":"1","fhirVersions":["4.3.0"],"fhir-version-list":["4.0.1"]}`,
			Manifest{Name: "a", Version: "1", FHIRVersions: []string{"4.3.0"}}},
		{"empty fhirVersions falls back", `{"name":"a","version":"1","fhirVersions":[],"fhir-version-list":["4.0.1"]}`,
			Manifest{Name: "a", Version: "1", FHIRVersions: []string{"4.0.1"}, LegacyVersionList: true}},
		{"no versions", `{"name":"a","version":"1"}`, Manifest{Name: "a", Version: "1"}},
		{"multi", `{"name":"a","version":"1","fhirVersions":["4.0.1","4.3.0","5.0.0"]}`, Manifest{Name: "a", Version: "1", FHIRVersions: []string{"4.0.1", "4.3.0", "5.0.0"}}},
		{"unknown fields are ignored", `{"name":"a","version":"1","jurisdiction":"x","maintainers":[1]}`, Manifest{Name: "a", Version: "1"}},
	}
	for _, c := range cases {
		got, err := ReadManifest([]byte(c.in))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got.Name != c.want.Name || got.Version != c.want.Version || got.Type != c.want.Type ||
			!slices.Equal(got.FHIRVersions, c.want.FHIRVersions) || got.LegacyVersionList != c.want.LegacyVersionList ||
			len(got.Dependencies) != len(c.want.Dependencies) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestReadManifestInvalid(t *testing.T) {
	for name, in := range map[string]string{
		"empty":               ``,
		"not json":            `nope`,
		"array":               `[]`,
		"no name":             `{"version":"1"}`,
		"no version":          `{"name":"a"}`,
		"name with hash":      `{"name":"a#b","version":"1"}`,
		"name with slash":     `{"name":"../a","version":"1"}`,
		"leading dot":         `{"name":".a","version":"1"}`,
		"version with space":  `{"name":"a","version":"1 0"}`,
		"version with hash":   `{"name":"a","version":"1#0"}`,
		"fhirVersions string": `{"name":"a","version":"1","fhirVersions":"4.0.1"}`,
		"empty fhir version":  `{"name":"a","version":"1","fhirVersions":[""]}`,
		"padded fhir version": `{"name":"a","version":"1","fhirVersions":[" 4.0.1"]}`,
		"dep not a string":    `{"name":"a","version":"1","dependencies":{"b":1}}`,
		"dep range":           `{"name":"a","version":"1","dependencies":{"b":"^1.0.0"}}`,
		"dep current":         `{"name":"a","version":"1","dependencies":{"b":"current"}}`,
		"dep empty":           `{"name":"a","version":"1","dependencies":{"b":""}}`,
		"dep bad name":        `{"name":"a","version":"1","dependencies":{"b/c":"1"}}`,
		"name number":         `{"name":1,"version":"1"}`,
	} {
		if _, err := ReadManifest([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIsCoreAndSeries(t *testing.T) {
	for typ, want := range map[string]bool{"Core": true, "core": true, "fhir.core": true, "FHIR.Core": true, "IG": false, "": false, "Examples": false} {
		if got := (Manifest{Type: typ}).IsCore(); got != want {
			t.Errorf("IsCore(%q) = %v", typ, got)
		}
	}
	m := Manifest{FHIRVersions: []string{"4.3.0", "4.0.1", "4.0.0", "current", "5.0.0-snapshot"}}
	if got := m.Series(); !slices.Equal(got, []string{"4.0", "4.3", "5.0"}) {
		t.Errorf("Series() = %v", got)
	}
}

func FuzzReadManifest(f *testing.F) {
	for _, s := range []string{
		`{"name":"a","version":"1.0.0","fhirVersions":["4.0.1"],"dependencies":{"b":"1.0.x"}}`,
		`{"name":"hl7.fhir.r4.core","version":"4.0.1","type":"fhir.core","fhir-version-list":["4.0.1"]}`,
		`{}`, `[]`, `null`, `{"name":"a","version":"1","dependencies":null}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ReadManifest(data)
		if err != nil {
			return
		}
		// Lo que se acepta cumple las invariantes que el resto del código supone.
		if _, _, err := splitID(m.ID()); err != nil {
			t.Fatalf("accepted manifest has an invalid id %q: %v", m.ID(), err)
		}
		for _, v := range m.FHIRVersions {
			if v == "" || strings.TrimSpace(v) != v {
				t.Fatalf("bad FHIR version %q", v)
			}
		}
		for d, spec := range m.Dependencies {
			if validName(d) != nil {
				t.Fatalf("bad dependency name %q", d)
			}
			if _, err := parseSpec(spec); err != nil {
				t.Fatalf("bad dependency spec %q", spec)
			}
		}
	})
}
