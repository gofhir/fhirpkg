# gofhir/fhirpkg

Librería que fija, verifica y lee los packages FHIR (`package.tgz` de packages.fhir.org) de un conjunto base: resuelve el grafo de dependencias, lo escribe en `fhirpkg.lock` con SHA-256 y lo lee después sin red. Sobre ese `Set`, un `Context` inmutable resuelve canónicos (cascada de ADR-003), da los `SearchParameter` de cada tipo de recurso con su herencia y (plan 003) el `Model` de tipos que consumen `fhirpath` y `searchindex`. Tajada 1a del núcleo FHIR nuevo (`nucleo/LINEAMIENTOS.md` §4.2, §5, §6).

**Al reanudar o después de una compactación, leer primero [`docs/plans/README.md`](docs/plans/README.md)** (punto de reanudación) y actualizarlo al cerrar cada paso.

## Reglas

Este repo se rige por `~/projects/personal/health-platform/workspace/NUCLEO.md`. Leerla antes de trabajar aquí. Lo más importante para esta librería:

- **Todo sale de la especificación FHIR y de los packages.** Ningún nombre de package, recurso o parámetro en el código de producción: los conjuntos base van en los locks (`testdata/*.lock`), que son datos. Lo único de la especificación de packages que el código nombra es `type` (`Core`, `fhir.core`), `fhirVersions`, `fhir-version-list` y `canonical`. Los nombres de FHIR del plan 002 y del 003 (campos de los recursos canónicos, `SearchParameter` y `StructureDefinition`, snapshot y `ElementDefinition`, códigos de `PublicationStatus`, `kind`, `derivation` y `version-algorithm`, la extensión `fhir-type`) viven solo en `fhirnames.go`, cada uno con su fuente; `TestFHIRNamesOnlyInFHIRNames` (AST) lo hace cumplir.
- **Solo la biblioteca estándar.** Sin dependencias. El `Model` implementa por tipado estructural las interfaces de `fhirpath` sin importarlo. `parity/` es otro módulo (su `go.mod`: `gofhir/models`, `gofhir/fhirpath`, `../go-searchindex`) y es el único que las importa.
- **Solo packages oficiales** de `packages.fhir.org`, con SHA-256 fijado en `scripts/packages.sha256`.
- **Sin red salvo `lock` y `graph` con un registro remoto.** `Open` nunca usa la red.
- **Fallar cerrado:** SHA distinto, archivo que falta, `.tgz` inseguro, dependencia faltante, ciclo, un package en dos versiones o de otra serie sin excepción son error y no construyen nada.
- **Revisar `gofhir/server` antes de implementar** (`internal/packages`, `igmanager`): tomar, mejorar o descartar (plan 001 §1.1).
- **Cada incremento sigue el proceso de la carta §10** (skill `fhir-incremento`).
- Código, identificadores, tests y errores en inglés; comentarios y docs en español.

## Comandos

| Comando | Qué hace |
| --- | --- |
| `make check` | `gofmt`, `go vet`, tests y `TestContextIsConcurrent` y `TestModelIsConcurrent` con `-race`. Sin red ni packages (los tests con packages oficiales se omiten). Correr antes de dar algo por terminado |
| `make check-packages` | Descarga los packages oficiales con SHA-256 fijado a `.cache/packages` y corre los `TestReal*`: los locks de `testdata/` reproducibles, el grafo real de R4B (D1), el núcleo R4 con `fhir-version-list`, D2 con `terminology.r4` 7.0.1 y 7.1.0, los solapes medidos en el plan y (plan 002) las cuentas de la cascada, el dorado de `Conflicts()`, los casos medidos, la herencia y su comparación con `../go-searchindex` (se omite si no está) y (plan 003) el modelo de tipos: dorado de tipos, casos de la especificación, invariantes, costo y el oráculo independiente `TestRealModelOracle` (`scripts/model_oracle.py`, necesita `python3`: destinos de referencia, cardinalidad y tipo FHIR leídos del package sin pasar por `modelbuild.go`); corre `make parity` al final |
| `make golden` | Reescribe `testdata/golden/*-conflicts.tsv` (`Conflicts()` por versión), `resource-types.txt` y `*-types.txt` (los tipos del modelo por versión, H9) con los packages oficiales. Revisar siempre el diff |
| `make parity` | Módulo `parity/` (plan 003): las nueve tablas de `gofhir/models` contra `Context.Model()` en R4, R4B y R5, con cada diferencia clasificada en `parity/testdata/differences.json` (falla ante una sin clasificar o una clase cuya cuenta cambia); `searchindex.Extract` sobre los ejemplos oficiales con cada modelo; el diferencial de `fhirpath` (`is`, `as`, `type()`, `children()`, `Analyze` sobre los invariantes y SearchParameter de los núcleos, con la pérdida conocida de H1); y el benchmark con los dos modelos. Escribe `docs/evidence/<fecha>-parity.md` y el bloque de cifras de `docs/issues/models-fhirpath-model-gaps.md`. Necesita `scripts/examples.sha256` (se descargan solos) |
| `make validator-types` | Evidencia de la ADR-004 (D1 a D3): qué nombres de tipo acepta `validator_cli` en `as(T)`. Necesita Java y el jar de `scripts/validator.sha256`; un solo JVM. Escribe `docs/evidence/<fecha>-validator-types.md` |
| `make graph` | Reporte del grafo de las raíces candidatas de `scripts/graph-candidates.txt` en `docs/evidence/<fecha>-graph.md` (D-1.4) |
| `make validator-graph` | Compara cada lock con los packages que `validator_cli` dice cargar (`Package Summary`, `-verbose`). Necesita Java y el jar de `scripts/validator.sha256`; un solo JVM a la vez. Escribe `docs/evidence/<fecha>-validator-graph.md` |
| `make evidence` | Reporte de evidencia fechado: commit, comandos, duración y RSS de `make check`, `TestReal*` (con el tiempo y el heap que retiene el modelo por versión), `fhirpkg verify` de cada lock, `fhirpkg context` (Open + `NewContext`, que ahora incluye el modelo) de R4, R4B y R5 y `make parity` |
| `make mutations` | Inyecta defectos (`scripts/mutations.py`) en copias del repo y exige que algún test los detecte; los `P-*` corren solo los oráculos de `parity/`. `MUTATION_FRESH=1` ignora la caché; `MUTATION_WORKERS` (3) |
| `make fuzz` | `FuzzReadManifest`, `FuzzReadTgz`, `FuzzParseCanonical`, `FuzzCompareVersions` y `FuzzModelFromStructureDefinition`, `FUZZTIME` (30s) cada uno |

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
| `model.go`, `modelbuild.go` | `Context.Model()` (plan 003, [ADR-004](docs/adr/004-tipos-del-modelo.md)): tipos, jerarquía, `[x]`, destinos de referencia, `contentReference`, hijos en orden, tipo FHIR y `Cardinality`, construidos en `NewContext` desde los snapshot ganadores; fallar cerrado ante snapshots incoherentes (ids y rebanadas, colisiones de `[x]`, `contentReference` con hijos, tipos del sistema fuera de un elemento de un solo tipo, primitivos del núcleo sobrescritos) |
| `fhirnames.go` | Único lugar de los nombres de FHIR de los planes 002 y 003, con su fuente; lo cuida `fhirnames_test.go` |
| `parity/` | Módulo aparte con `gofhir/models`, `gofhir/fhirpath` y `searchindex`: paridad de las nueve tablas, diferenciales y benchmark; `testdata/differences.json` clasifica cada diferencia |
| `testdata/*.lock`, `testdata/golden/` | Locks reales (R4, R4B, R5): dorado reproducible con `make check-packages`; dorado de `Conflicts()` por versión (`make golden`) |
| `scripts/` | `fetch-packages.sh`, `packages.sha256`, `hl7-packages.sha256`, `validator.sha256`, `mutations.py`, `model_oracle.py` (oráculo independiente del modelo, ronda 1 del 003), `validator_graph.py`, `evidence.sh` |
| `docs/adr/`, `docs/plans/`, `docs/evidence/`, `docs/issues/` | ADR, un plan por incremento con su registro, reportes generados e issues redactados para otras librerías (sin publicar) |
