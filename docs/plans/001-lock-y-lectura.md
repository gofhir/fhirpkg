# 001 — Lock y lectura verificada

**Estado:** cerrado (02-11-2026), sin commit. Implementación, code review en tres rondas (§10, §11, §12) y decisión del conjunto base (opción C, ADR-002) hechos; cierre en §13. El usuario aprobó el plan ajustado (§8) y las propuestas de D1 (excluir el núcleo de otra serie) y D2 (error ante un package en dos versiones), §3.2.

Primer incremento de `gofhir/fhirpkg`, la librería de la tajada 1a (`NUCLEO.md` §6; `nucleo/LINEAMIENTOS.md` §4.2, §5 y §6, D-1.1 a D-1.4). Por la ronda adversarial (§8, P-5), el plan original se partió:

| # | Contenido |
| --- | --- |
| **001** | Lock, lectura verificada sin red, versión FHIR y el reporte del grafo con el que se decide el conjunto base (D-1.4) |
| 002 | Canónicos (todas las versiones, cascada restringida al grafo, `Conflicts()`) y `SearchParameters` con herencia |
| 003 | Modelo de tipos dinámico, con paridad contra `gofhir/models` |
| 004 | Normalizador de orden |

## 1. Revisión previa

### 1.1 Lo que ya está revisado

`nucleo/revision/02-packages-y-versiones.md` (21-10-2026) cubre el legacy (`server`: `internal/packages`, `igmanager`, `fhircontext`, `pkg/fhirdefs`), la especificación (§3), otras implementaciones (§4) y la tabla de tomar, mejorar o descartar (§5). Para este incremento:

- **Mejorar** `internal/packages`: la extracción segura de `.tgz` (zip-slip, zip-bomb), más SHA-256 fijado y fallar cerrado.
- **Mejorar** `igmanager/resolver`: DFS con ciclos; una dependencia faltante es error; orden determinista; el resultado va al lock en vez de recalcularse.
- **Descartar** las copias recortadas y los `switch` por versión.

### 1.2 Implementaciones de la plataforma (medido el 01-11-2026)

Medición preliminar en `docs/evidence/2026-11-01-d14-medicion-preliminar.md` (sondas fuera del repo: no es evidencia según la carta; este incremento la reemplaza con un comando). Cada librería carga un conjunto distinto: `searchindex`, el núcleo con SHA fijado; `validator`, núcleo, terminología y extensiones recortados y sin SHA; `mutator`, solo `r4.core`; `server`, SD, SP y OperationDefinition sin SHA. En R4B difieren en terminología (`.r4` 7.0.1, `hl7.terminology` 7.4.0 o ninguna) y extensiones (`.r4`, `.r4b` o ninguna).

### 1.3 El grafo real (medido en los manifiestos de packages.fhir.org)

- `hl7.fhir.uv.extensions.r4b#5.3.0` (`fhirVersions` 4.3.0) depende de `hl7.terminology.r4#7.1.0`; la 5.4.0, de `terminology.r4#7.5.0`.
- `hl7.terminology.r4#7.0.1` y `#7.1.0` (`fhirVersions` 4.0.1) dependen de `hl7.fhir.r4.core#4.0.1` y `hl7.fhir.uv.extensions.r4#5.2.0`.
- **El grafo de R4B trae un segundo núcleo** (`r4.core`): comparte 3.113 canónicos con `r4b.core`, 1.333 de ellos SearchParameter, 18 con la misma versión.
- `hl7.fhir.r4.core#4.0.1` no tiene `fhirVersions`: declara `fhir-version-list: ["4.0.1"]` (campo legado).
- `hl7.terminology#7.4.0` declara solo 5.0.0.

### 1.4 Especificación

- **Packages:** `packages.html` (R5 §2.1.10; R4 en `R4/packages.html`): `package.json` con `name`, `version`, `fhirVersions`, `dependencies` (versión exacta o comodín de *patch*).
- **Versiones:** `versioning.html` §2.8.2.

## 2. Alcance

| # | Pieza | Qué entrega |
| --- | --- | --- |
| A | Resolución y lock | `fhirpkg lock`: desde una lista de raíces (`name#version`), resuelve el grafo transitivo desde un registro (por defecto packages.fhir.org), descarga y escribe `fhirpkg.lock` (JSON: el núcleo, cada `name#version` con SHA-256, sus dependencias exactas, el camino que lo trajo y el orden topológico determinista). Es el único paso con red |
| B | Lectura verificada | `Open(lock, dir)`: lee los `.tgz` del directorio, sin red. Un archivo que falta, un SHA distinto o un `.tgz` inseguro (zip-slip, tamaño) es error y no se construye nada. Extracción en memoria |
| C | Versión FHIR | **Un solo núcleo por lock**, declarado. La versión sale de su `fhirVersions` o, si no está, de `fhir-version-list` (campo legado de los núcleos R4). Un package de otra serie es error salvo la regla de D1. Un package en dos versiones sigue la regla de D2 |
| F | Reporte del grafo (D-1.4) | `fhirpkg graph`: para las raíces candidatas de cada versión, el grafo (packages, `fhirVersions`, quién trae a quién, núcleos ajenos, versiones dobles) y los solapes de URL de conformidad entre packages, con versión semver o no. Reporte generado en `docs/evidence/`. La ADR del conjunto base se decide con él y con la aprobación del usuario, antes de abrir el 002 |

`Open` en este incremento devuelve los packages verificados y sus recursos de conformidad crudos, agrupados por package; no decide entre canónicos (eso es el 002).

**Fuera:** canónicos y SearchParameter (002), el modelo (003), el normalizador (004), la adopción por `searchindex` (tajada 3), terminología pesada, `current`/`dev` y registros privados.

## 3. Diseño

### 3.1 Módulo y API

- `github.com/gofhir/fhirpkg`, sin dependencias fuera de la biblioteca estándar (`net/http`, `archive/tar`, `compress/gzip`, `crypto/sha256`).
- `Lock`, `LockedPackage`, `Resolve(roots, registry) (Lock, error)`, `Open(lock, dir) (*Set, error)`, `Set.FHIRVersion()`, `Set.Packages()`, `Set.Resources(pkg)`. Comandos en `cmd/fhirpkg`: `lock`, `verify`, `graph`.
- **Memoria:** solo se guardan los recursos de conformidad (los de `package/`, sin `Bundle`, `List` ni ejemplos). Se mide el RSS y el heap retenido de `Open` por versión.

### 3.2 Decisiones (aprobadas por el usuario el 01-11-2026: D1 excluir, D2 error)

- **D1, núcleo de otra serie traído por dependencia** (P-1). Propuesta: el lock declara un solo núcleo; un núcleo de otra serie que aparece en el grafo se **excluye** (no se lee), queda en el lock como excluido con el camino que lo trajo, y el reporte lo muestra. Los packages que dependen de él se leen igual, con su `fhirVersions` de otra serie permitido solo si están en la lista de excepciones del lock (escrita a mano, con motivo). Alternativa: verlo solo en el subgrafo de quien lo trae (más complejo; se puede hacer en el 002 si hace falta).
- **D2, el mismo package en dos versiones** (P-2). Propuesta: **error**, con las dos versiones y quién pide cada una; el usuario ajusta las raíces (p. ej. `terminology.r4#7.1.0` en vez de 7.0.1). Alternativas: gana la mayor, o gana la raíz.

Ambas quedan en la ADR del lockfile.

## 4. Tests y fuentes de datos

- Los packages oficiales con SHA-256 fijado en locks de tests (`testdata/*.lock`), descargados a `.cache/packages` por `make fetch`.
- **Casos de la especificación y del grafo real** (P-6): el núcleo R4 con `fhir-version-list`; el grafo de R4B con `r4.core` traído por `terminology.r4` (D1); `terminology.r4` 7.0.1 como raíz más `extensions.r4b#5.3.0` que pide 7.1.0 (D2); comodín de *patch*; dependencia faltante; ciclo; `fhirVersions` multiversión.
- **Packages sintéticos** escritos por los tests (`.tgz` en memoria): SHA distinto, zip-slip, archivo enorme, manifiesto inválido.
- **Metamórficos:** el orden de las raíces y de los archivos dentro del `.tgz` no cambia el lock ni el `Set`.
- **Comparación** (P-6): el grafo resuelto se compara con el del validador de HL7 sobre las mismas raíces, leyendo los packages que su log dice cargar (`-debug`, target aparte, máximo 2 JVM). Antes de prometerla se verifica qué emite el validador; si no da la lista, se compara con el `package-lock` de `npm`/FHIR CLI o se retira, y queda registrado.
- **Fuzzing:** `FuzzReadManifest`, `FuzzReadTgz`.
- **Mutaciones:** SHA ignorado, dependencia faltante tolerada, núcleo ajeno no excluido, versión doble que elige una, campo legado ignorado, `.tgz` inseguro aceptado.

## 5. Criterio de salida

- `make check` en verde.
- `Open` de las raíces candidatas de R4, R4B y R5 sin red, con tiempo y RSS en un reporte generado.
- El reporte de F, y la ADR del conjunto base y la del lockfile (con D1 y D2) escritas; la de conjunto base, aprobada por el usuario.
- Mutaciones detectadas; code review hasta converger.

## 6. Dependencias nuevas

Ninguna.

## 7. Excepciones a la regla 5

Ninguna prevista. Los nombres de packages del conjunto base van en los locks (datos), no en el código.

## 8. Ronda adversarial (Opus, 01-11-2026)

Revisor: un subagente Opus independiente, porque Codex agotó su límite hasta el 04-11-2026 (decisión del usuario). Sondas en `/Volumes/dev/tmp/review-fhirpkg001`; los puntos 1, 4 y 6 los verifiqué en los manifiestos.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| P-1 | Alta | El grafo de R4B trae un segundo núcleo (`extensions.r4b` → `terminology.r4` → `r4.core#4.0.1`): 3.113 canónicos compartidos, 18 empates; «la versión sale del núcleo» era ambiguo | **Aceptado.** Un solo núcleo por lock; D1 |
| P-2 | Alta | Sin política para un package en dos versiones (`terminology.r4` 7.0.1 raíz y 7.1.0 pedida) | **Aceptado.** D2 |
| P-3 | Media | «Publicador antes que copia» solo se determina en parte (prefijos de `canonical`, núcleo y `expansions` con el mismo `canonical`, dos packages de extensiones publicadores) | **Aceptado, al 002**, con estos pares como tests |
| P-4 | Media | 1.231 de 4.577 versiones del núcleo R4 no son semver (`2.9`, fechas, vacías) | **Aceptado, al 002**: el reporte de F ya las cuenta |
| P-5 | Media | Alcance demasiado grande y criterio de salida atado a una aprobación humana y a dorados que P-1 a P-4 cambian | **Aceptado.** El 001 se partió (tabla del inicio) |
| P-6 | Baja | `fhir-version-list` sin leer; la comparación con `validator_cli` sin definir; tests que no atacan P-1 a P-3 | **Aceptado.** §2 C y §4 |

## 9. Registro de implementación (01-11-2026)

Implementado por un subagente Sonnet sobre el plan aprobado; sin commit. Los números salen de los reportes generados, no se copian a mano.

### 9.1 Qué se entregó

| Pieza | Dónde |
| --- | --- |
| A, resolución y lock | `graph.go` (`Explore`, `Analyze`), `resolve.go` (`Resolve`, D1, D2), `lock.go`, `registry.go`, `cmd/fhirpkg lock` |
| B, lectura verificada | `open.go` (`Open`, `Set`), `tgz.go`, `cmd/fhirpkg verify` |
| C, versión FHIR | `manifest.go` (`fhirVersions`, `fhir-version-list`), `version.go` (serie) |
| F, reporte del grafo | `overlap.go`, `cmd/fhirpkg graph`, `scripts/graph-candidates.txt`; reporte en [`docs/evidence/2026-11-01-graph.md`](../evidence/2026-11-01-graph.md) |
| ADR | [001, el lockfile con D1 y D2](../adr/001-lockfile.md) (aceptada); [002, el conjunto base](../adr/002-conjunto-base.md) (**Propuesta**, decidida por el usuario: opción C; Aceptada cuando CI corra su verificación) |
| Herramientas | `Makefile` (`check`, `check-packages`, `graph`, `validator-graph`, `evidence`, `mutations`, `fuzz`), `scripts/` |

### 9.2 Tests

- 94,4 % de las sentencias de la raíz y 83 % de `cmd/fhirpkg`.
- **Reales** (`make check-packages`, `TestReal*`): los cinco locks de `testdata/` se reproducen byte a byte; el núcleo R4 con `fhir-version-list`; el grafo real de R4B con `r4.core` excluido (D1; `Open` verifica su `.tgz`, ronda 1 H1); D2 con `terminology.r4` 7.0.1 y 7.1.0; los SHA de los locks son los de `scripts/packages.sha256`; el solape `r4.core` y `r4b.core` da 3.113 canónicos, 1.333 SearchParameter y 18 de la misma versión, igual que la medición del plan §1.3.
- **Sintéticos** (`.tgz` en memoria): zip-slip, ruta absoluta, enlaces, duplicados, bomba de compresión de 200 MB, límites, manifiestos inválidos, SHA distinto, archivo que falta, `package.json` distinto del lock, dependencia faltante, ciclo, comodín de patch, núcleos (ninguno, varios, varias versiones, de la misma serie), excepciones, `fhirVersions` multiversión.
- **Metamórficos:** el orden de las raíces, de las excepciones y de los archivos dentro del `.tgz` no cambia el lock ni el `Set`; 300 grafos aleatorios (más 200 con un defecto inyectado) con propiedades (conjunto alcanzable, orden topológico, ida y vuelta del JSON).
- **Fuzzing:** `FuzzReadManifest` y `FuzzReadTgz` (15 s cada uno, sin hallazgos).
- **Mutaciones:** 31 en `scripts/mutations.py`, las 6 del plan §4 y 25 más; todas detectadas (`MUTATION_FRESH=1`, 97 s con 3 workers): [`docs/evidence/2026-11-01-2352-mutations.md`](../evidence/2026-11-01-2352-mutations.md). Quedan por correr en el cierre, después del code review.

### 9.3 Lo medido

[`docs/evidence/2026-11-02-0013.md`](../evidence/2026-11-02-0013.md) (`make evidence`): `Open` sin red por lock, con tiempo, RSS y heap retenido (`r4.core` solo 177 ms y 31 MB retenidos; R4 completo 364 ms y 73 MB; R4B 440 ms y 104 MB; R5 445 ms y 105 MB). `make check`: 1 s con la caché de tests y 2 s sin ella, RSS máximo 172 MB (la compilación de los tests). `make check-packages`: 7 s y 264 MB. `make graph`: 5 s y 150 MB. Dos medidas llevaron cambios: la lectura de cada archivo del `.tgz` usaba `io.ReadAll`, que deja capacidad de sobra retenida (corregido: tamaño exacto, con test), y `Explore` no guarda los `.tgz` (solo el SHA y el manifiesto).

### 9.4 Comparación con `validator_cli`

El validador **sí da la lista**: con `-verbose` escribe una línea `Package Summary: [...]` (`-debug` no existe en 7.0.2). Se implementó: `make validator-graph` (`scripts/validator_graph.py`), un JVM a la vez, `-no-http-access`, con el `home` armado desde el lock y los packages por defecto del validador (`scripts/hl7-packages.sha256`). Reporte: [`docs/evidence/2026-11-02-validator-graph.md`](../evidence/2026-11-02-validator-graph.md). Límite (ronda 1, H2): la contención va en un solo sentido; un lock al que le falte una dependencia pasa si el validador la carga por defecto. La completitud la cubren los `TestReal*`, que resuelven otra vez las raíces y comparan byte a byte (el reporte lo dice). Resultado: el validador carga todo lo que el lock lee y **no carga** el `r4.core` que D1 excluye en R4B. Límite: carga además por sus defaults packages que el lock no pide (`xver-extensions`, `hl7.terminology#7.4.0`, `uv.extensions#5.3.0`, una terminología por versión), así que la comparación es de contención y no de igualdad; carga la terminología R4 en dos versiones (6.2.0 y la 7.x), que D2 no admite.

### 9.5 Desviaciones del plan

1. `Resolve` recibe `Options` con las excepciones (el plan decía `Resolve(roots, registry)`): D1 las necesita al resolver, y `fhirpkg lock` conserva las del lock existente.
2. `make check` no usa packages ni red; los tests con packages oficiales corren en `make check-packages` (variable `FHIRPKG_PACKAGES`), como pide `CLAUDE.md` de este repo.
3. Un package sin versión FHIR declarada se lee y queda registrado (el plan no lo cubría); un package con versiones FHIR que no son `M.m.p` cuenta como de otra serie.
4. Un ciclo es error (el plan lo listaba como caso sin decir si era error).
5. El comodín admitido es solo `M.m.x` o `M.m.*` (patch), como dice la especificación de packages; `^`, `~`, `current` y `dev` son error.
6. Se agregó el candidato `R4B con terminology.r4 7.0.1` al reporte para mostrar D2 sobre el grafo real.
7. La URL de descarga que publica `packages.fhir.org` apunta a `packages.simplifier.net` (como en `searchindex`); `HTTPRegistry` sigue `dist.tarball`, sin código propio para ese host.
8. Se descargaron de packages.fhir.org los 16 packages distintos de `scripts/packages.sha256` y `scripts/hl7-packages.sha256`. Los SHA-256 de los tres núcleos y de los ocho de `hl7-packages.sha256` son los fijados en `searchindex`; los otros cinco se fijaron al bajarlos por primera vez (confianza en el primer uso, como lo hace `searchindex` con cada package nuevo).

### 9.6 Pendiente (al momento de la implementación)

Resuelto en §10 a §13.

## 10. Code review

### Ronda 1 (02-11-2026, revisor Opus por excepción del usuario mientras Codex no tiene cupo)

| # | Severidad | Hallazgo | Corrección |
| --- | --- | --- | --- |
| H1 | Media (confirmado con sonda) | `Lock.Validate` y `Open` confiaban en la marca `excluded`: un lock editado podía excluir un package normal, incluso una raíz, o declarar como núcleo un IG | `Open` lee y verifica (SHA-256 y `package.json`) el `.tgz` de cada excluido, sin guardar recursos, y exige que sea núcleo (`IsCore`) de otra serie que la del núcleo del lock; exige `IsCore()` al núcleo del lock; `Validate` rechaza un excluido que sea raíz. Efecto: un núcleo excluido ya no se abre sin su `.tgz` (`TestRealR4BGraphExcludesTheR4Core` y `TestResolveForeignCoreExcluded` cambiaron). Tests: `TestOpenRejectsExcludingANonCorePackage`, `...ACoreOfTheLocksSeries`, `TestOpenRejectsALockCoreThatIsNotACore`, `TestOpenVerifiesExcludedPackages`, `TestOpenDoesNotKeepExcludedResources`, `TestLockValidateRejectsAnExcludedRoot`. Mutaciones M32 a M36, todas detectadas |
| H2 | Baja | `scripts/validator_graph.py` solo comprueba contención en una dirección: un lock sin una dependencia pasa | Documentado en §9.4 y en el reporte que genera: la completitud la cubren los `TestReal*` |
| H3 | Baja | El cálculo de `Undeclared` en `graph.go` no tenía test | `TestAnalyzeUndeclared` (un IG sin versión cuenta; un núcleo sin versión es un problema, no un `Undeclared`) y la mutación M37 |
| H4 | Baja | `matchLocked` repetía el chequeo de `Validate` (`byID[d]`) | Borrado |

### Decisión del conjunto base (02-11-2026)

El usuario eligió la opción C sin expansiones ([ADR-002](../adr/002-conjunto-base.md)): R4 con `terminology.r4#7.1.0` (antes la 7.0.1, para que R4 y R4B compartan terminología); R4B y R5 sin cambios. `testdata/r4.lock` regenerado con `fhirpkg lock` y los reportes de `docs/evidence/` regenerados. Test nuevo: `TestRealR4AndR4BShareTheTerminology`.

## 11. Code review, ronda 2

Revisor Opus (excepción del usuario, Codex sin cupo hasta el 04-11).

| # | Severidad | Hallazgo | Corrección |
| --- | --- | --- | --- |
| H5 | Media (confirmado) | `matchLocked` comparaba la cantidad de dependencias y que las del lock estuvieran en el manifiesto, pero no la inversa, y `Validate` no rechazaba dependencias repetidas: un lock con `[ig.a#1.0.0, ig.a#1.0.0]` ocultaba a `ig.b`, que el manifiesto declara | `Validate` rechaza dependencias repetidas (por nombre) en un `LockedPackage`; `matchLocked` exige que cada dependencia del manifiesto aparezca en el lock (biyección por nombre, junto con la unicidad). Tests: caso «duplicate dependency» en `TestLockValidate` y `TestMatchLockedRequiresEveryManifestDependencyOnce`. Mutaciones M38 y M39 |
| H6 | Baja | `Open` aceptaba excluir un núcleo sin versión FHIR (`Series()` vacía) | El excluido debe declarar una serie y distinta de la del lock. Test: `TestOpenRejectsExcludingACoreWithoutFHIRVersion`. Mutación M40 (M33 sigue cubriendo la serie igual) |
| H7 | Baja (costo) | `Open` verificaba los excluidos después de leer todo y descomprimía el `.tgz` entero del excluido | Los excluidos se verifican antes del bucle principal (falla temprano) y `scan(..., stopAtManifest)` deja de descomprimir al leer `package/package.json` (el SHA-256 del archivo completo se calcula igual; las entradas posteriores no se validan, y el grafo sigue usando `scanTgz`, que recorre todo). Tests: `TestOpenVerifiesExcludedPackagesFirst`, `TestScanStopsAtTheManifest`. Mutaciones M41 y M42 |

### Medición de H7 (3 corridas, `/usr/bin/time -l`, `.cache/packages`)

| Comando | Antes | Después |
| --- | --- | --- |
| `TestRealR4BGraphExcludesTheR4Core` | 1,26 s, 181 MB RSS | 0,93 s, 165 MB RSS |
| `fhirpkg verify -lock testdata/r4b.lock` | 0,52 s, 176 MB RSS | 0,56 s, 163 MB RSS (tiempo sin cambio medible; RSS bajó unos 13 MB) |

### Verificación

`make check` y `make check-packages` verdes. Mutaciones M32 a M34 y M38 a M42 detectadas (corridas por prefijo; las completas quedan para el cierre).

## 12. Code review, ronda 3

Revisor Opus (excepción del usuario, Codex sin cupo hasta el 04-11). Por decisión del usuario: corregir con la opción (a) y cerrar sin otra ronda.

| # | Severidad | Hallazgo | Corrección |
| --- | --- | --- | --- |
| H8 | Media (confirmado) | Desde H7, `Open` leía el `.tgz` de un núcleo excluido solo hasta `package/package.json` (`stopAtManifest`): un excluido con `package.json` repetido, un symlink posterior o un gzip truncado pasaba, aunque `Resolve` y el scan completo lo rechazan | Opción (a): el excluido se recorre entero con las mismas validaciones que `scanTgz` (duplicados, tipos, rutas, límites, gzip y tar completos), sin guardar los cuerpos de los recursos. `stopAtManifest` y `scan` desaparecen; `readLocked` llama a `scanTgz(data, ..., !excluded)`. Al probarlo apareció una brecha del propio scan: `tar` se detiene en los bloques de cierre sin leer el trailer del gzip, así que un gzip truncado ahí pasaba también en `Resolve`; ahora `scanTgz` drena el gzip y verifica su CRC y largo |

Tests: `TestOpenRejectsAnExcludedCoreWithABadTgz` (`package.json` repetido, symlink después del manifiesto, gzip truncado) y `TestScanWithoutResourcesStillValidatesTheWholeTgz` (además, ruta que escapa, tar corrupto con gzip íntegro). Sustituyen a `TestScanStopsAtTheManifest`. Mutaciones: M41 pasa a «el scan sin recursos se detiene en el manifiesto», M42 sigue igual, y se agregan M43 (el scan sin recursos ignora errores de tar) y M44 (no se verifica el trailer del gzip); M08 a M10, M32 a M34 y M41 a M44 detectadas (corridas por prefijo).

### Medición de H8 (3 corridas, `/usr/bin/time -l`, `.cache/packages`)

| Comando | §11 (con H7) | Después de H8 |
| --- | --- | --- |
| `TestRealR4BGraphExcludesTheR4Core` | 0,93 s, 165 MB RSS | 1,18 s (corrida en frío), 0,83 s y 0,82 s; 167, 165 y 165 MB RSS |
| `fhirpkg verify -lock testdata/r4b.lock` | 0,56 s, 163 MB RSS | 0,88 s (en frío), 0,49 s y 0,50 s; 164, 163 y 164 MB RSS |

Sin cambio medible frente a H7 (el costo de descomprimir el excluido entero ya estaba dentro del ruido; el RSS no sube porque no se guardan recursos) y por debajo de los valores previos a H7 (1,26 s y 181 MB; 0,52 s y 176 MB).

### Verificación

`make check` y `make check-packages` verdes.

## 13. Cierre

- **Criterio de salida (§5) cumplido:** `make check` en verde; `Open` de las raíces de R4, R4B y R5 sin red, con tiempo y RSS en el reporte de evidencia; reporte del grafo ([`2026-11-01-graph.md`](../evidence/2026-11-01-graph.md)), contraste con el validador ([`2026-11-02-validator-graph.md`](../evidence/2026-11-02-validator-graph.md)) y evidence más reciente ([`2026-11-02-0604.md`](../evidence/2026-11-02-0604.md)); ADR del lockfile y del conjunto base escritas; mutaciones completas sin caché, todas detectadas ([`2026-11-02-0608-mutations.md`](../evidence/2026-11-02-0608-mutations.md)); code review en tres rondas; la tercera dejó H8 (medio) y, por el tope, el usuario decidió corregirlo y cerrar sin una cuarta (§12).
- **ADR:** [ADR-001](../adr/001-lockfile.md) y [ADR-002](../adr/002-conjunto-base.md) quedan en estado Propuesta (decididas por el usuario); pasan a Aceptadas cuando CI corra su verificación.
- **Revisores:** ronda adversarial (§8) y code review (§10 a §12) con Opus por falta de cupo de Codex (hasta el 04-11), por excepción del usuario.
- **Lo que sigue:** plan 002, canónicos y `SearchParameter` con herencia, con P-3 y P-4 de la ronda adversarial.
