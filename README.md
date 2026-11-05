# fhirpkg

[![CI](https://github.com/gofhir/fhirpkg/actions/workflows/ci.yml/badge.svg)](https://github.com/gofhir/fhirpkg/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gofhir/fhirpkg.svg)](https://pkg.go.dev/github.com/gofhir/fhirpkg)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Verified loading of FHIR packages (`package.tgz` from [packages.fhir.org](https://packages.fhir.org)) for Go, and the FHIR knowledge that can be derived from them.

## What it does

- **Lock and verification.** `Resolve` walks the dependency graph of the packages you ask for and writes a lock (`fhirpkg.lock`) with the SHA-256 of every `.tgz`. `Open` reads them back with no network access and fails closed: wrong checksum, missing file, unsafe archive, missing dependency, cycle, or a package from another FHIR series without a declared exception are all errors.
- **Canonical resolution.** `Context.Resolve` finds a resource by canonical URL (`url|version#fragment`) across all the packages of the lock, applying a documented precedence when several packages publish the same URL ([ADR-003](docs/adr/003-precedencia-canonicos.md)). `Conflicts` and `Explain` show what was decided and why.
- **SearchParameters.** `Context.SearchParameters` returns the parameters of each resource type with inheritance resolved from `baseDefinition`.
- **Type model.** `Context.Model` is a model of the FHIR types (hierarchy, choice types, reference targets, cardinality, `isSummary`) built from the `StructureDefinition` snapshots of the lock, not hard-coded ([ADR-004](docs/adr/004-tipos-del-modelo.md)).
- **Order normalizer.** `Context.Normalize` rewrites a JSON resource with its properties in the order of the model and without whitespace, reporting every defect it finds.
- **`_summary` and `_elements` projection.** `Context.NewProjection` and `Context.Project` apply the search result projection (`_summary=true|text|data`, `_elements`) in the same pass as the normalizer ([ADR-005](docs/adr/005-proyeccion.md)).

R4, R4B and R5 are supported; the FHIR version is read from the core package of the lock, not from the code. The library uses only the Go standard library.

## Install

```sh
go get github.com/gofhir/fhirpkg
```

Requires Go 1.26 or later.

## Usage

Resolve a set of packages into a lock (`Example`s are compiled and, when they have an output, run by `go test`; this one runs offline with an in-memory registry):

```go
reg := memRegistry{ /* name#version -> .tgz */ }
lock, err := fhirpkg.Resolve(ctx, []string{"hl7.fhir.r4.core#4.0.1", "example.ig#1.0.0"}, reg, fhirpkg.Options{})
if err != nil {
	log.Fatal(err)
}
fmt.Println("FHIR", lock.FHIRVersion) // FHIR 4.0.1
```

See [`ExampleResolve`](example_test.go). With the real registry use `&fhirpkg.HTTPRegistry{BaseURL: fhirpkg.DefaultRegistryURL}`.

Once the `.tgz` files are on disk, open them against the lock and build a `Context`:

```go
lock, _ := fhirpkg.ReadLockFile("fhirpkg.lock")
set, err := fhirpkg.Open(lock, "packages") // directory of name-version.tgz
if err != nil {
	log.Fatal(err)
}
fhirCtx, err := fhirpkg.NewContext(set)
if err != nil {
	log.Fatal(err)
}

proj, _ := fhirCtx.NewProjection("Patient", fhirpkg.SummaryTrue, nil)
out, err := fhirCtx.Project(patientJSON, proj) // normalized and projected
```

See [`ExampleContext_Project`](example_test.go), which needs the real packages, so it is compiled but not run.

### Command line

```sh
go run github.com/gofhir/fhirpkg/cmd/fhirpkg@latest lock -o fhirpkg.lock hl7.fhir.r4.core#4.0.1
go run github.com/gofhir/fhirpkg/cmd/fhirpkg@latest verify -lock fhirpkg.lock -dir packages
```

`fhirpkg lock` is the only step that needs the network (with a remote registry); `verify`, `context` and `graph` (with a directory registry) do not. `-registry` accepts a URL or a directory of `name-version.tgz`.

## Status

Version 0: the API may change between minor versions until 1.0. Releases are produced by [release-please](https://github.com/googleapis/release-please) from [conventional commits](https://www.conventionalcommits.org/); see the [changelog](CHANGELOG.md) once the first one is published.

## Development

```sh
make check            # gofmt, go vet, tests (offline)
make lint             # golangci-lint
make vuln             # govulncheck
make check-packages   # tests against the official packages (network)
```

`make check-packages` also compares against `gofhir/searchindex`, `gofhir/models` and `gofhir/fhirpath` through the separate module in `parity/`, so it expects `gofhir/searchindex` cloned next to this repository.

## Documentation

Design decisions and plans are written in Spanish:

- [`docs/adr`](docs/adr): architecture decision records (lockfile, base set, canonical precedence, type model, projection).
- [`docs/plans`](docs/plans): one plan per increment, with its record and the [resume point](docs/plans/README.md).

## License

[MIT](LICENSE)
