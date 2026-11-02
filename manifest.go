package fhirpkg

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Manifest es el package.json de un package FHIR (packages.html de la
// especificación), con lo que este módulo necesita.
type Manifest struct {
	Name    string
	Version string
	// Type es el campo "type" tal como viene ("Core", "IG", y "fhir.core" en el
	// núcleo R4).
	Type string
	// Canonical es el campo "canonical" del package.json: la URL base bajo la
	// que el package publica sus recursos (packages.html). Puede estar vacío.
	Canonical string
	// FHIRVersions son las versiones FHIR declaradas: "fhirVersions" o, si no
	// está, el campo legado "fhir-version-list" (los núcleos R4 solo traen ese).
	FHIRVersions []string
	// LegacyVersionList indica que FHIRVersions salió de "fhir-version-list".
	LegacyVersionList bool
	// Dependencies mapea el nombre de cada dependencia a lo que pide: una
	// versión exacta o un comodín de patch.
	Dependencies map[string]string
}

type rawManifest struct {
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	Type            string            `json:"type"`
	Canonical       string            `json:"canonical"`
	FHIRVersions    []string          `json:"fhirVersions"`
	FHIRVersionList []string          `json:"fhir-version-list"`
	Dependencies    map[string]string `json:"dependencies"`
}

// ReadManifest lee y valida un package.json. Es estricto: un nombre o una
// versión vacíos o con caracteres que rompen el identificador name#version, una
// versión FHIR vacía o una dependencia que no se puede interpretar son error.
func ReadManifest(data []byte) (Manifest, error) {
	var raw rawManifest
	if err := json.Unmarshal(data, &raw); err != nil {
		return Manifest{}, fmt.Errorf("invalid package.json: %w", err)
	}
	if err := validName(raw.Name); err != nil {
		return Manifest{}, fmt.Errorf("invalid package.json: name: %w", err)
	}
	if err := validVersion(raw.Version); err != nil {
		return Manifest{}, fmt.Errorf("invalid package.json: version: %w", err)
	}
	m := Manifest{Name: raw.Name, Version: raw.Version, Type: raw.Type, Canonical: raw.Canonical, Dependencies: raw.Dependencies}
	switch {
	case len(raw.FHIRVersions) > 0:
		m.FHIRVersions = raw.FHIRVersions
	case len(raw.FHIRVersionList) > 0:
		m.FHIRVersions = raw.FHIRVersionList
		m.LegacyVersionList = true
	}
	for _, v := range m.FHIRVersions {
		if v == "" || strings.TrimSpace(v) != v {
			return Manifest{}, fmt.Errorf("invalid package.json: empty or padded FHIR version %q", v)
		}
	}
	for dep, spec := range m.Dependencies {
		if err := validName(dep); err != nil {
			return Manifest{}, fmt.Errorf("invalid package.json: dependency name: %w", err)
		}
		if _, err := parseSpec(spec); err != nil {
			return Manifest{}, fmt.Errorf("invalid package.json: dependency %s: %w", dep, err)
		}
	}
	return m, nil
}

// ID es el identificador name#version del package.
func (m Manifest) ID() string { return m.Name + "#" + m.Version }

// IsCore dice si el package es un núcleo de la especificación. El campo "type"
// del package es "Core" en R4B y R5 y "fhir.core" en el núcleo R4 (packages.html;
// carta, regla 5: es un valor de la especificación de packages, no un nombre de
// recurso).
func (m Manifest) IsCore() bool {
	t := strings.ToLower(m.Type)
	return t == "core" || t == "fhir.core"
}

// Series devuelve las series FHIR ("4.0", "4.3", "5.0") de sus versiones
// declaradas, ordenadas y sin repetir. Una versión sin forma M.m.p se ignora.
func (m Manifest) Series() []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range m.FHIRVersions {
		if s, ok := series(v); ok && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func validName(s string) error {
	if s == "" {
		return fmt.Errorf("empty")
	}
	if strings.ContainsAny(s, "#/\\ \t\r\n\x00") || strings.Contains(s, "..") || s[0] == '.' {
		return fmt.Errorf("invalid package name %q", s)
	}
	return nil
}

func validVersion(s string) error {
	if s == "" {
		return fmt.Errorf("empty")
	}
	if strings.ContainsAny(s, "#/\\ \t\r\n\x00") || strings.Contains(s, "..") {
		return fmt.Errorf("invalid version %q", s)
	}
	return nil
}

func splitID(id string) (name, version string, err error) {
	name, version, ok := strings.Cut(id, "#")
	if !ok {
		return "", "", fmt.Errorf("invalid package id %q: expected name#version", id)
	}
	if err := validName(name); err != nil {
		return "", "", fmt.Errorf("invalid package id %q: %w", id, err)
	}
	if err := validVersion(version); err != nil {
		return "", "", fmt.Errorf("invalid package id %q: %w", id, err)
	}
	return name, version, nil
}
