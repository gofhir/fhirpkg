# gofhir/fhirpkg

Librería que fija, verifica y lee los packages FHIR (`package.tgz` de packages.fhir.org) de un conjunto base: resuelve el grafo de dependencias, lo escribe en `fhirpkg.lock` con SHA-256 y lo lee después sin red. Sobre ese `Set`, un `Context` inmutable resuelve canónicos (cascada de ADR-003) y da los `SearchParameter` de cada tipo de recurso con su herencia. Tajada 1a del núcleo FHIR nuevo (`nucleo/LINEAMIENTOS.md` §4.2, §5, §6).

**Al reanudar o después de una compactación, leer primero [`docs/plans/README.md`](docs/plans/README.md)** (punto de reanudación) y actualizarlo al cerrar cada paso.

## Reglas

Este repo se rige por `~/projects/personal/health-platform/workspace/NUCLEO.md`. Leerla antes de trabajar aquí. Lo más importante para esta librería:

- **Todo sale de la especificación FHIR y de los packages.** Ningún nombre de package, recurso o parámetro en el código de producción: los conjuntos base van en los locks (`testdata/*.lock`), que son datos. Lo único de la especificación de packages que el código nombra es `type` (`Core`, `fhir.core`), `fhirVersions`, `fhir-version-list` y `canonical`. Los nombres de FHIR del plan 002 (campos de los recursos canónicos, `SearchParameter` y `StructureDefinition`, códigos de `PublicationStatus` y de `version-algorithm`) viven solo en `fhirnames.go`, cada uno con su fuente; `TestFHIRNamesOnlyInFHIRNames` (AST) lo hace cumplir.
- **Solo la biblioteca estándar.** Sin dependencias.
- **Solo packages oficiales** de `packages.fhir.org`, con SHA-256 fijado en `scripts/packages.sha256`.
- **Sin red salvo `lock` y `graph` con un registro remoto.** `Open` nunca usa la red.
- **Fallar cerrado:** SHA distinto, archivo que falta, `.tgz` inseguro, dependencia faltante, ciclo, un package en dos versiones o de otra serie sin excepción son error y no construyen nada.
- **Revisar `gofhir/server` antes de implementar** (`internal/packages`, `igmanager`): tomar, mejorar o descartar (plan 001 §1.1).
- **Cada incremento sigue el proceso de la carta §10** (skill `fhir-incremento`).
- Código, identificadores, tests y errores en inglés; comentarios y docs en español.

## Comandos

| Comando | Qué hace |
| --- | --- |
| `make check` | `gofmt`, `go vet`, tests y `TestContextIsConcurrent` con `-race`. Sin red ni packages (los tests con packages oficiales se omiten). Correr antes de dar algo por terminado |
| `make check-packages` | Descarga los packages oficiales con SHA-256 fijado a `.cache/packages` y corre los `TestReal*`: los locks de `testdata/` reproducibles, el grafo real de R4B (D1), el núcleo R4 con `fhir-version-list`, D2 con `terminology.r4` 7.0.1 y 7.1.0, los solapes medidos en el plan y (plan 002) las cuentas de la cascada, el dorado de `Conflicts()`, los casos medidos, la herencia y su comparación con `../go-searchindex` (se omite si no está) |
| `make golden` | Reescribe `testdata/golden/*-conflicts.tsv` (`Conflicts()` por versión) con los packages oficiales. Revisar siempre el diff |
| `make graph` | Reporte del grafo de las raíces candidatas de `scripts/graph-candidates.txt` en `docs/evidence/<fecha>-graph.md` (D-1.4) |
| `make validator-graph` | Compara cada lock con los packages que `validator_cli` dice cargar (`Package Summary`, `-verbose`). Necesita Java y el jar de `scripts/validator.sha256`; un solo JVM a la vez. Escribe `docs/evidence/<fecha>-validator-graph.md` |
| `make evidence` | Reporte de evidencia fechado: commit, comandos, duración y RSS de `make check`, `TestReal*`, `fhirpkg verify` de cada lock y `fhirpkg context` (Open + `NewContext`) de R4, R4B y R5 |
| `make mutations` | Inyecta defectos (`scripts/mutations.py`) en copias del repo y exige que algún test los detecte. `MUTATION_FRESH=1` ignora la caché; `MUTATION_WORKERS` (3) |
| `make fuzz` | `FuzzReadManifest`, `FuzzReadTgz`, `FuzzParseCanonical` y `FuzzCompareVersions`, `FUZZTIME` (30s) cada uno |

CLI: `fhirpkg lock -o fhirpkg.lock [-registry URL|DIR] [-except name#version=motivo] raíz...`, `fhirpkg verify -lock L -dir D`, `fhirpkg graph -candidates F -o R`, `fhirpkg context -lock L -dir D` (tiempo, heap y cuentas de la cascada).

## Estructura

| Ruta | Contenido |
| --- | --- |
| `manifest.go`, `version.go` | `package.json` estricto (`fhirVersions` o `fhir-version-list`), semver, comodín de patch y serie FHIR |
| `tgz.go` | Lectura de un `.tgz`: nombres seguros, sin enlaces, sin duplicados, límites de tamaño; solo los JSON de primer nivel de `package/` con `resourceType` y `url` |
| `registry.go` | `Registry`: `HTTPRegistry` (packages.fhir.org) y `DirRegistry` (carpeta `name-version.tgz`) |
| `graph.go`, `resolve.go`, `lock.go` | `Explore` (grafo sin política), `Analyze` y `Resolve` (D1, D2, ciclos), `Lock` |
| `open.go` | `Open(lock, dir)`: verificación sin red, `Set` con `FHIRVersion`, `Packages`, `Resources` |
| `overlap.go`, `cmd/fhirpkg` | Solapes de URL entre packages; `lock`, `verify`, `graph` y `context` |
| `context.go`, `cascade.go` | `NewContext(set)`, `Resolve`, `ResolveFrom`, `Conflicts`, `Explain` (plan 002): índice `(resourceType, url)` sin copiar bytes y la cascada serie → estado → publicador → versión → `lastUpdated` ([ADR-003](docs/adr/003-precedencia-canonicos.md)) |
| `searchparameters.go` | Tipos de recurso y herencia por `baseDefinition`; regla de `base:code` repetido; `SearchParameters(tipo)` |
| `canonical.go`, `order.go` | `ParseCanonical` (`url\|version#fragmento`) y los órdenes de versión (SemVer estricto, entero, alfa, fecha FHIR, natural) |
| `fhirnames.go` | Único lugar de los nombres de FHIR del plan 002, con su fuente; lo cuida `fhirnames_test.go` |
| `testdata/*.lock`, `testdata/golden/` | Locks reales (R4, R4B, R5): dorado reproducible con `make check-packages`; dorado de `Conflicts()` por versión (`make golden`) |
| `scripts/` | `fetch-packages.sh`, `packages.sha256`, `hl7-packages.sha256`, `validator.sha256`, `mutations.py`, `validator_graph.py`, `evidence.sh` |
| `docs/adr/`, `docs/plans/`, `docs/evidence/` | ADR, un plan por incremento con su registro, reportes generados |
