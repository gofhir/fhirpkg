package fhirpkg

import (
	"crypto/sha256"
	"sort"
)

// URLRecord es lo que el reporte del grafo necesita de un recurso: no guarda
// el contenido, solo su hash.
type URLRecord struct {
	ResourceType string
	URL          string
	Version      string
	Hash         [sha256.Size]byte
}

// PackageStats resume los recursos de conformidad de un package.
type PackageStats struct {
	Resources int
	// ByType cuenta los recursos por resourceType.
	ByType map[string]int
	// NonSemver cuenta los recursos cuya versión no es semver (incluye los sin
	// versión).
	NonSemver int
	// Skipped cuenta los JSON de primer nivel que no son recursos de conformidad.
	Skipped int
}

// ReadRecords lee un .tgz y devuelve el registro de cada recurso de
// conformidad y el resumen del package. Valida el .tgz como Open.
func ReadRecords(tgz []byte) ([]URLRecord, PackageStats, error) {
	c, err := scanTgz(tgz, DefaultLimits, true)
	if err != nil {
		return nil, PackageStats{}, err
	}
	st := PackageStats{ByType: map[string]int{}, Skipped: c.skipped}
	recs := make([]URLRecord, 0, len(c.resources))
	for _, r := range c.resources {
		recs = append(recs, URLRecord{ResourceType: r.ResourceType, URL: r.URL, Version: r.Version, Hash: sha256.Sum256(r.Raw)})
		st.Resources++
		st.ByType[r.ResourceType]++
		if _, ok := parseSemver(r.Version); !ok {
			st.NonSemver++
		}
	}
	return recs, st, nil
}

// PairOverlap cuenta las URL de conformidad que dos packages publican a la vez.
type PairOverlap struct {
	A, B string
	// Shared es la cantidad de URL en las dos.
	Shared int
	// SameVersion son las compartidas con el mismo campo version, e Identical
	// las que además tienen el mismo contenido (byte a byte).
	SameVersion, Identical int
	// DiffSemver son las de versión distinta, las dos semver; DiffNonSemver, las
	// de versión distinta donde alguna no es semver (no se pueden ordenar).
	DiffSemver, DiffNonSemver int
	// ByType cuenta las compartidas por resourceType.
	ByType map[string]int
}

// Overlaps calcula los solapes de URL entre cada par de packages (claves de
// recs, name#version). Devuelve solo los pares con alguna URL compartida,
// ordenados por (A, B) con A < B.
func Overlaps(recs map[string][]URLRecord) []PairOverlap {
	ids := make([]string, 0, len(recs))
	byURL := make(map[string]map[string]URLRecord, len(recs))
	for id, rs := range recs {
		ids = append(ids, id)
		m := make(map[string]URLRecord, len(rs))
		for _, r := range rs {
			if _, dup := m[r.URL]; !dup {
				m[r.URL] = r
			}
		}
		byURL[id] = m
	}
	sort.Strings(ids)
	var out []PairOverlap
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			p := PairOverlap{A: a, B: b, ByType: map[string]int{}}
			for url, ra := range byURL[a] {
				rb, ok := byURL[b][url]
				if !ok {
					continue
				}
				p.Shared++
				p.ByType[ra.ResourceType]++
				if ra.Version == rb.Version {
					p.SameVersion++
					if ra.Hash == rb.Hash {
						p.Identical++
					}
					continue
				}
				_, sa := parseSemver(ra.Version)
				_, sb := parseSemver(rb.Version)
				if sa && sb {
					p.DiffSemver++
				} else {
					p.DiffNonSemver++
				}
			}
			if p.Shared > 0 {
				out = append(out, p)
			}
		}
	}
	return out
}
