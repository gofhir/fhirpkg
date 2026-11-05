package fhirpkg

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Package es un package leído de un lock.
type Package struct {
	// ID es name#version.
	ID       string
	Manifest Manifest
	SHA256   string
}

// Set es el conjunto de packages verificados de un lock. Guarda solo los
// recursos de conformidad (archivos JSON de primer nivel de package/ con
// resourceType y url), no los .tgz.
type Set struct {
	lock      Lock
	packages  []Package
	resources map[string][]Resource
}

// Open lee, sin red, los .tgz de un lock desde dir (archivos
// name-version.tgz, como los deja PackageFile) y los verifica. Es error, y no se
// devuelve nada, si falta un archivo, si su SHA-256 no es el del lock, si el
// .tgz es inseguro (nombres que escapan, enlaces, archivos o totales por
// encima de DefaultLimits), si su package.json no coincide con el lock o si la
// serie FHIR de un package difiere de la del núcleo sin estar en las
// excepciones del lock. Los packages excluidos no se leen, pero se verifican: su
// .tgz debe estar, con el SHA-256 del lock, y ser el núcleo de otra serie.
func Open(lock Lock, dir string) (*Set, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	byID := make(map[string]LockedPackage, len(lock.Packages))
	for _, p := range lock.Packages {
		byID[p.ID()] = p
	}
	exc := map[string]bool{}
	for _, e := range lock.Exceptions {
		exc[e.Package] = true
	}
	coreSeries, _ := series(lock.FHIRVersion)

	// Los excluidos no se leen, pero un lock editado no puede excluir un
	// package que no sea el núcleo de otra serie: se verifica su SHA-256 y su
	// package.json, sin guardar recursos ni leer más allá del package.json (D1). Se hace antes de
	// leer los demás: es lo más barato y falla temprano.
	for _, lp := range lock.Packages {
		if !lp.Excluded {
			continue
		}
		c, err := readLocked(dir, lp, true)
		if err != nil {
			return nil, err
		}
		m := c.manifest
		if !m.IsCore() {
			return nil, fmt.Errorf("%s: excluded, but it is not a core package (type %q): only the core of another FHIR series is excluded", lp.ID(), m.Type)
		}
		// Un núcleo sin versión FHIR no declara serie distinta de la del lock.
		if len(m.Series()) == 0 || slices.Contains(m.Series(), coreSeries) {
			return nil, fmt.Errorf("%s: excluded, but it does not declare a FHIR series other than the lock's series %s (declares %v)", lp.ID(), coreSeries, m.FHIRVersions)
		}
	}

	s := &Set{lock: lock, resources: make(map[string][]Resource, len(lock.Order))}
	for _, id := range lock.Order {
		lp := byID[id]
		c, err := readLocked(dir, lp, false)
		if err != nil {
			return nil, err
		}
		m := c.manifest
		if id == lock.Core && !m.IsCore() {
			return nil, fmt.Errorf("%s: the lock's core is not a core package (type %q)", id, m.Type)
		}
		if m.IsCore() && id != lock.Core {
			return nil, fmt.Errorf("%s: a second core in the lock (the core is %s)", id, lock.Core)
		}
		if id != lock.Core && len(m.FHIRVersions) > 0 && !slices.Contains(m.Series(), coreSeries) && !exc[id] {
			return nil, fmt.Errorf("%s: declares FHIR %v, not the core's series %s, and is not in the lock's exceptions", id, m.FHIRVersions, coreSeries)
		}
		s.packages = append(s.packages, Package{ID: id, Manifest: m, SHA256: lp.SHA256})
		s.resources[id] = c.resources
	}
	return s, nil
}

// readLocked lee el .tgz de un package del lock, verifica su SHA-256 y su
// package.json contra el lock. Con excluded recorre el .tgz entero con las mismas
// validaciones, pero conserva solo el manifiesto: no guarda recursos.
func readLocked(dir string, lp LockedPackage, excluded bool) (*contents, error) {
	id := lp.ID()
	data, err := os.ReadFile(filepath.Join(dir, PackageFile(lp.Name, lp.Version))) //nolint:gosec // G703: dir lo elige quien llama; el nombre del archivo sale de un lock ya validado (Lock.Validate)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: %s is missing from %s (restore the file)", id, PackageFile(lp.Name, lp.Version), dir)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != lp.SHA256 {
		return nil, fmt.Errorf("%s: SHA-256 is %s, the lock pins %s", id, got, lp.SHA256)
	}
	c, err := scanTgz(data, DefaultLimits, !excluded)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	if err := matchLocked(c.manifest, lp); err != nil {
		return nil, fmt.Errorf("%s: %w", id, err)
	}
	return c, nil
}

// matchLocked comprueba que el package.json leído es el que el lock describe.
func matchLocked(m Manifest, lp LockedPackage) error {
	if m.ID() != lp.ID() {
		return fmt.Errorf("the package declares itself as %s", m.ID())
	}
	if !slices.Equal(m.FHIRVersions, lp.FHIRVersions) || m.LegacyVersionList != lp.LegacyVersionList {
		return fmt.Errorf("FHIR versions %v differ from the lock's %v", m.FHIRVersions, lp.FHIRVersions)
	}
	if len(m.Dependencies) != len(lp.Dependencies) {
		return fmt.Errorf("its %d dependencies differ from the lock's %d", len(m.Dependencies), len(lp.Dependencies))
	}
	// Biyección por nombre: cada dependencia del lock está en el manifiesto y
	// cada una del manifiesto aparece en el lock (Validate ya rechaza repetidas).
	locked := make(map[string]bool, len(lp.Dependencies))
	for _, d := range lp.Dependencies {
		name, version, _ := strings.Cut(d, "#")
		locked[name] = true
		spec, ok := m.Dependencies[name]
		if !ok {
			return fmt.Errorf("the lock lists %s, which the package does not depend on", d)
		}
		vs, err := parseSpec(spec)
		if err != nil || !vs.matches(version) {
			return fmt.Errorf("the lock resolves %s to %s, which does not satisfy %q", name, version, spec)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Dependencies)) {
		if !locked[name] {
			return fmt.Errorf("the package depends on %s, which the lock does not list", name)
		}
	}
	return nil
}

// FHIRVersion es la versión FHIR del núcleo del lock.
func (s *Set) FHIRVersion() string { return s.lock.FHIRVersion }

// Lock devuelve el lock con el que se abrió.
func (s *Set) Lock() Lock { return s.lock }

// Packages devuelve los packages leídos, dependencias primero (el orden del
// lock).
func (s *Set) Packages() []Package { return slices.Clone(s.packages) }

// Resources devuelve los recursos de conformidad de un package (name#version),
// ordenados por archivo. El slice y sus bytes son del Set: no se modifican.
// Un package que no se leyó (desconocido o excluido) no tiene recursos.
func (s *Set) Resources(pkg string) []Resource { return s.resources[pkg] }
