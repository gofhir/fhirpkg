package fhirpkg_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/gofhir/fhirpkg"
)

// memRegistry es un registro en memoria: name#version -> .tgz.
type memRegistry map[string][]byte

func (m memRegistry) Versions(_ context.Context, name string) ([]string, error) {
	var vs []string
	for id := range m {
		if n, v, ok := strings.Cut(id, "#"); ok && n == name {
			vs = append(vs, v)
		}
	}
	return vs, nil
}

func (m memRegistry) Fetch(_ context.Context, name, version string) ([]byte, error) {
	b, ok := m[name+"#"+version]
	if !ok {
		return nil, fhirpkg.ErrNotFound
	}
	return b, nil
}

// tgz arma un package mínimo (solo su package.json), como los de un registro.
func tgz(manifest string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	_ = tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(manifest))})
	_, _ = tw.Write([]byte(manifest))
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// Resolve fija en un lock el núcleo y las guías de implementación, con sus
// dependencias. Aquí el registro es de memoria, así que el ejemplo corre sin red;
// con fhirpkg.HTTPRegistry{BaseURL: fhirpkg.DefaultRegistryURL} es packages.fhir.org.
func ExampleResolve() {
	reg := memRegistry{
		"hl7.fhir.r4.core#4.0.1": tgz(`{"name":"hl7.fhir.r4.core","version":"4.0.1","type":"fhir.core","fhir-version-list":["4.0.1"]}`),
		"example.ig#1.0.0":       tgz(`{"name":"example.ig","version":"1.0.0","type":"IG","fhirVersions":["4.0.1"],"dependencies":{"hl7.fhir.r4.core":"4.0.1"}}`),
	}
	lock, err := fhirpkg.Resolve(context.Background(), []string{"hl7.fhir.r4.core#4.0.1", "example.ig#1.0.0"}, reg, fhirpkg.Options{})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("FHIR", lock.FHIRVersion)
	for _, id := range lock.Order {
		fmt.Println(id)
	}
	// Output:
	// FHIR 4.0.1
	// hl7.fhir.r4.core#4.0.1
	// example.ig#1.0.0
}

// Con los packages ya descargados (fhirpkg lock y los .tgz de un registro), Open
// los verifica contra el lock, NewContext arma el modelo y sus tablas, y el
// Context normaliza y proyecta recursos. Necesita los packages reales, así que
// este ejemplo se compila pero no se ejecuta.
func ExampleContext_Project() {
	lock, err := fhirpkg.ReadLockFile("fhirpkg.lock")
	if err != nil {
		log.Fatal(err)
	}
	set, err := fhirpkg.Open(lock, "packages") // carpeta con name-version.tgz
	if err != nil {
		log.Fatal(err)
	}
	ctx, err := fhirpkg.NewContext(set)
	if err != nil {
		log.Fatal(err)
	}

	// Un canónico se resuelve con la precedencia de la ADR-003.
	if _, err := ctx.Resolve("StructureDefinition", "http://hl7.org/fhir/StructureDefinition/Patient"); err != nil {
		log.Fatal(err)
	}

	// _summary=true: solo lo marcado isSummary en el modelo.
	proj, err := ctx.NewProjection("Patient", fhirpkg.SummaryTrue, nil)
	if err != nil {
		log.Fatal(err)
	}
	out, err := ctx.Project([]byte(`{"gender":"female","resourceType":"Patient","id":"1"}`), proj)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(out))
}
