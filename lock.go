package fhirpkg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// LockSchema es la versión del formato de fhirpkg.lock.
const LockSchema = 1

// Lock es el resultado de resolver un conjunto de packages: qué packages, con
// qué SHA-256 y en qué orden. Se escribe una vez (fhirpkg lock, con red) y se
// lee siempre sin red (Open).
type Lock struct {
	Schema int `json:"schema"`
	// FHIRVersion es la versión FHIR del núcleo, p. ej. "4.0.1".
	FHIRVersion string `json:"fhirVersion"`
	// Core es el núcleo del lock (name#version): uno solo por lock.
	Core string `json:"core"`
	// Roots son las raíces pedidas, con su versión exacta, ordenadas.
	Roots []string `json:"roots"`
	// Packages son todos los packages del grafo, ordenados por name#version,
	// incluidos los excluidos.
	Packages []LockedPackage `json:"packages"`
	// Order es el orden topológico de los packages que se leen (dependencias
	// primero; empates por name#version).
	Order []string `json:"order"`
	// Exceptions es la lista, escrita a mano y con motivo, de los packages cuya
	// serie FHIR difiere de la del núcleo y aun así se leen (D1).
	Exceptions []Exception `json:"exceptions,omitempty"`
}

// LockedPackage es un package del lock.
type LockedPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	// FHIRVersions son las versiones FHIR declaradas (vacías si no declara).
	FHIRVersions []string `json:"fhirVersions,omitempty"`
	// LegacyVersionList indica que salieron de "fhir-version-list".
	LegacyVersionList bool `json:"legacyVersionList,omitempty"`
	// Dependencies son sus dependencias con versión exacta (name#version).
	Dependencies []string `json:"dependencies,omitempty"`
	// Via es el camino más corto desde una raíz hasta quien lo pide, sin
	// incluirlo: vacío en una raíz.
	Via []string `json:"via,omitempty"`
	// Excluded marca el núcleo de otra serie que no se lee (D1).
	Excluded       bool   `json:"excluded,omitempty"`
	ExcludedReason string `json:"excludedReason,omitempty"`
}

// ID es el identificador name#version.
func (p LockedPackage) ID() string { return p.Name + "#" + p.Version }

// Exception permite a un package de otra serie FHIR leerse en el lock.
type Exception struct {
	Package string `json:"package"`
	Reason  string `json:"reason"`
}

// Marshal serializa el lock de forma determinista.
func (l Lock) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(l); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ReadLock lee y valida un lock. Un campo desconocido es error.
func ReadLock(data []byte) (Lock, error) {
	var l Lock
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		return Lock{}, fmt.Errorf("invalid lock: %w", err)
	}
	if dec.More() {
		return Lock{}, fmt.Errorf("invalid lock: trailing data")
	}
	if err := l.Validate(); err != nil {
		return Lock{}, err
	}
	return l, nil
}

// ReadLockFile lee un lock desde un archivo.
func ReadLockFile(file string) (Lock, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return Lock{}, err
	}
	return ReadLock(data)
}

// WriteFile escribe el lock en un archivo.
func (l Lock) WriteFile(file string) error {
	data, err := l.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

// Package busca un package del lock por name#version.
func (l Lock) Package(id string) (LockedPackage, bool) {
	for _, p := range l.Packages {
		if p.ID() == id {
			return p, true
		}
	}
	return LockedPackage{}, false
}

func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Validate comprueba la coherencia interna del lock, sin tocar disco ni red.
func (l Lock) Validate() error {
	if l.Schema != LockSchema {
		return fmt.Errorf("invalid lock: schema %d, want %d", l.Schema, LockSchema)
	}
	if l.FHIRVersion == "" {
		return fmt.Errorf("invalid lock: empty fhirVersion")
	}
	byID := make(map[string]LockedPackage, len(l.Packages))
	byName := make(map[string]string, len(l.Packages))
	for _, p := range l.Packages {
		id := p.ID()
		if err := validName(p.Name); err != nil {
			return fmt.Errorf("invalid lock: package %q: %w", id, err)
		}
		if err := validVersion(p.Version); err != nil {
			return fmt.Errorf("invalid lock: package %q: %w", id, err)
		}
		if _, dup := byID[id]; dup {
			return fmt.Errorf("invalid lock: duplicate package %s", id)
		}
		if other, dup := byName[p.Name]; dup {
			return fmt.Errorf("invalid lock: package %s in two versions (%s and %s)", p.Name, other, p.Version)
		}
		if !isSHA256(p.SHA256) {
			return fmt.Errorf("invalid lock: package %s: sha256 is not 64 lowercase hex digits", id)
		}
		if p.Excluded != (p.ExcludedReason != "") {
			return fmt.Errorf("invalid lock: package %s: excluded and excludedReason must go together", id)
		}
		depNames := make(map[string]bool, len(p.Dependencies))
		for _, d := range p.Dependencies {
			name, _, _ := splitID(d)
			if depNames[name] {
				return fmt.Errorf("invalid lock: package %s lists a dependency on %s twice", id, name)
			}
			depNames[name] = true
		}
		byID[id] = p
		byName[p.Name] = p.Version
	}
	for i := 1; i < len(l.Packages); i++ {
		if l.Packages[i-1].ID() >= l.Packages[i].ID() {
			return fmt.Errorf("invalid lock: packages are not sorted by name#version")
		}
	}
	for _, p := range l.Packages {
		for _, d := range append(append([]string{}, p.Dependencies...), p.Via...) {
			if _, ok := byID[d]; !ok {
				return fmt.Errorf("invalid lock: package %s refers to %s, which is not in the lock", p.ID(), d)
			}
		}
	}
	core, ok := byID[l.Core]
	if !ok {
		return fmt.Errorf("invalid lock: core %q is not in the lock", l.Core)
	}
	if core.Excluded {
		return fmt.Errorf("invalid lock: core %s is excluded", l.Core)
	}
	if len(core.FHIRVersions) != 1 || core.FHIRVersions[0] != l.FHIRVersion {
		return fmt.Errorf("invalid lock: core %s declares %v, not the lock's FHIR version %s", l.Core, core.FHIRVersions, l.FHIRVersion)
	}
	for _, r := range l.Roots {
		rp, ok := byID[r]
		if !ok {
			return fmt.Errorf("invalid lock: root %s is not in the lock", r)
		}
		if rp.Excluded {
			return fmt.Errorf("invalid lock: root %s is excluded", r)
		}
	}
	if !sort.StringsAreSorted(l.Roots) {
		return fmt.Errorf("invalid lock: roots are not sorted")
	}
	// Order: exactamente los no excluidos, con las dependencias primero.
	pos := make(map[string]int, len(l.Order))
	for i, id := range l.Order {
		p, ok := byID[id]
		if !ok || p.Excluded {
			return fmt.Errorf("invalid lock: order lists %s, which is not a package that is read", id)
		}
		if _, dup := pos[id]; dup {
			return fmt.Errorf("invalid lock: order lists %s twice", id)
		}
		pos[id] = i
	}
	for _, p := range l.Packages {
		if p.Excluded {
			continue
		}
		i, ok := pos[p.ID()]
		if !ok {
			return fmt.Errorf("invalid lock: order does not list %s", p.ID())
		}
		for _, d := range p.Dependencies {
			if j, ok := pos[d]; ok && j >= i {
				return fmt.Errorf("invalid lock: order puts %s before its dependency %s", p.ID(), d)
			}
		}
	}
	seen := map[string]bool{}
	for _, e := range l.Exceptions {
		p, ok := byID[e.Package]
		switch {
		case !ok:
			return fmt.Errorf("invalid lock: exception for %s, which is not in the lock", e.Package)
		case p.Excluded:
			return fmt.Errorf("invalid lock: exception for %s, which is excluded", e.Package)
		case e.Reason == "":
			return fmt.Errorf("invalid lock: exception for %s without a reason", e.Package)
		case seen[e.Package]:
			return fmt.Errorf("invalid lock: two exceptions for %s", e.Package)
		}
		seen[e.Package] = true
	}
	return nil
}
