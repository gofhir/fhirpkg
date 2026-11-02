package fhirpkg

import (
	"math/rand"
	"testing"
)

func TestReadRecordsAndOverlaps(t *testing.T) {
	a := pkg{name: "a", version: "1.0.0", fhirVersions: []string{"4.0.1"}, files: map[string]string{
		"1.json": resource("ValueSet", "http://x/same", "1.0.0"),
		"2.json": resource("ValueSet", "http://x/semver", "1.0.0"),
		"3.json": resource("CodeSystem", "http://x/nonsemver", "2.9"),
		"4.json": resource("CodeSystem", "http://x/missing", ""),
		"5.json": resource("ValueSet", "http://x/onlya", "1.0.0"),
		"6.json": resource("SearchParameter", "http://x/changed", "1.0.0"),
	}}
	b := pkg{name: "b", version: "1.0.0", fhirVersions: []string{"4.0.1"}, files: map[string]string{
		"1.json": resource("ValueSet", "http://x/same", "1.0.0"),
		"2.json": resource("ValueSet", "http://x/semver", "1.2.0"),
		"3.json": resource("CodeSystem", "http://x/nonsemver", "2.10"),
		"4.json": resource("CodeSystem", "http://x/missing", ""),
		"6.json": resource("SearchParameter", "http://x/changed", "1.0.0") + " ", // misma versión, otro contenido
	}}
	ra, sa, err := ReadRecords(a.tgz(t))
	if err != nil {
		t.Fatal(err)
	}
	rb, _, _ := ReadRecords(b.tgz(t))
	if sa.Resources != 6 || sa.NonSemver != 2 || sa.ByType["ValueSet"] != 3 {
		t.Fatalf("stats %+v", sa)
	}
	ov := Overlaps(map[string][]URLRecord{"b#1.0.0": rb, "a#1.0.0": ra})
	if len(ov) != 1 {
		t.Fatalf("%+v", ov)
	}
	p := ov[0]
	if p.A != "a#1.0.0" || p.B != "b#1.0.0" || p.Shared != 5 || p.SameVersion != 3 || p.Identical != 2 ||
		p.DiffSemver != 1 || p.DiffNonSemver != 1 || p.ByType["ValueSet"] != 2 {
		t.Fatalf("%+v", p)
	}
}

// Propiedades sobre solapes aleatorios: simétricos respecto del orden de los
// packages, y la suma de las clases es el total de compartidas.
func TestOverlapsProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	for i := 0; i < 300; i++ {
		recs := map[string][]URLRecord{}
		for _, id := range []string{"a#1", "b#1", "c#1"} {
			var rs []URLRecord
			for j := 0; j < rng.Intn(20); j++ {
				rs = append(rs, URLRecord{ResourceType: "T", URL: "u" + string(rune('a'+rng.Intn(15))),
					Version: []string{"", "1.0.0", "1.1.0", "2.9"}[rng.Intn(4)], Hash: [32]byte{byte(rng.Intn(3))}})
			}
			recs[id] = rs
		}
		for _, p := range Overlaps(recs) {
			if p.A >= p.B {
				t.Fatalf("pair out of order: %s %s", p.A, p.B)
			}
			if p.SameVersion+p.DiffSemver+p.DiffNonSemver != p.Shared || p.Identical > p.SameVersion {
				t.Fatalf("classes do not add up: %+v", p)
			}
			// Intercambiar A y B da las mismas cuentas.
			swap := Overlaps(map[string][]URLRecord{"x": recs[p.B], "y": recs[p.A]})
			if len(swap) != 1 || swap[0].Shared != p.Shared || swap[0].SameVersion != p.SameVersion || swap[0].Identical != p.Identical {
				t.Fatalf("not symmetric: %+v vs %+v", p, swap)
			}
		}
	}
}
