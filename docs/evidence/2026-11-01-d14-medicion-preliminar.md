# Informe D-1.4: conjunto base de packages por versión FHIR

Medición de solo lectura, 01-11-2026. Repos: `go-searchindex`, `go-validator`, `go-fhir-mutator`, `go-fhir-server` (congelado). Scripts y salidas crudas en este directorio: `probe.py`/`probe.txt` (recursos por package), `overlap.py`/`overlap.txt` (solapes de URL), `sp.py`/`sp.txt` (qué cambia en los SearchParameter repetidos), `emb.py`/`emb.txt` (packages embebidos de validador y server), `dl.txt` (tres packages oficiales bajados para completar la medición). Todo con `python3 -I`, `TMPDIR=/Volumes/dev/tmp`; el solape tardó 14 s y 0,7 GB de RSS.

## 1. Qué carga cada librería

### 1.1 go-searchindex

| Aspecto | Hallazgo | Evidencia |
|---|---|---|
| Producción (embebido) | Solo el **core** de cada versión: `hl7.fhir.r4.core#4.0.1`, `r4b.core#4.3.0`, `r5.core#5.0.0`, regenerado a `definitions/data/{r4,r4b,r5}.json.gz` (48/50/56 KB) | `scripts/gendefs.sh:9-10` |
| Origen y SHA | packages.fhir.org (`dist.tarball` de la metadata), SHA-256 fijado y verificado dos veces (antes de reusar y al final) | `scripts/packages.sha256`; `scripts/fetch-packages.sh:18,30` |
| Runtime | `definitions.LoadPackages(paths...)`: núcleo primero, luego guías | `definitions/packages.go:56,77` |
| Tipos que lee (`readPackage`) | `SearchParameter` (candidatos), `ValueSet` (para binding de un solo sistema), `StructureDefinition` (rutas de array, BaseTypes, bindings required). No lee CodeSystem, ConceptMap ni OperationDefinition. Solo ficheros de primer nivel de `package/` | `definitions/packages.go:254,266,274`; filtro de nivel en `:236` |
| Tests/oráculos (`internal/gen`) | `StructureDefinition` (solo del core), `ValueSet` y `CodeSystem` del core y de terminología | `internal/gen/gen.go:205-212,296-306` |
| Packages de test | core + `hl7.terminology#7.4.0` para las tres versiones; expansiones como oráculo de `gen`; ejemplos para el dorado; ambiente HL7 con 8 packages fijados | `testdata/gen-packages.json`; `internal/gen/load_test.go:33-35`; `examples_test.go:38-40`; `scripts/hl7-packages.sha256` |
| URL repetida (SearchParameter) | Cascada independiente del orden: idénticos cuentan como uno; gana mayor status (active, draft, resto), luego mayor `version` semver; **error** si empatan dos distintos o la versión no es semver. Cada descartado queda en `Data.Overrides` | `definitions/packages.go:72-76,397-460` |
| URL repetida (ValueSet/CodeSystem en `gen`) | Gana el package posterior; se registra `Override` | `internal/gen/gen.go:328-346` |
| `fhirVersions` distinto | **Error** si un package posterior no es de la misma serie (mayor.menor) que el núcleo; sin `fhirVersions` solo se acepta la familia `hl7.fhir.r4.*` del núcleo. `gen.Load` no lo comprueba | `definitions/packages.go:67-70,347-365` |

### 1.2 go-validator

| Aspecto | Hallazgo | Evidencia |
|---|---|---|
| Embebido | Por versión: core + terminología + extensiones. **R4** `core#4.0.1`, `terminology.r4#7.0.1`, `extensions.r4#5.2.0`. **R4B** `r4b.core#4.3.0` + **los mismos terminología R4 y extensiones R4** (con `fhirVersions` 4.0.1). **R5** `r5.core#5.0.0`, `terminology.r5#7.0.1`, `extensions.r5#5.2.0` | `pkg/specs/specs.go:11-34` |
| Recorte | Copias filtradas a SD, VS, CS y NamingSystem de tipo codesystem. No coinciden byte a byte con las oficiales (SHA de core R4 `d1a9a3a8` frente a `ebd7731d` oficial) | `pkg/specs/specs.go:3-4`; `emb.txt` |
| Sin embebido | `DefaultPackages` desde la caché NPM; R4B usa `uv.extensions.r4#5.2.0` «como fallback» | `pkg/loader/loader.go:92-109,214` |
| Descarga | `Install` desde `config.PackageRegistry` (dependencias) | `pkg/validator/dependencies.go:100-110` |
| SHA-256 | **No verifica** (0 apariciones de sha/integrity en `pkg/loader/install.go`) | grep |
| Tipos | SD al abrir; VS, CS y NamingSystem diferidos. Ejemplos solo SD/VS/CS/NamingSystem | `pkg/loader/open.go:61,181` |
| URL repetida | `Registry.supersedes`: la del publicador gana sobre una copia (`Publishers.IsCopy`: packages `fhir.core`/`examples`/`expansions` publican copias de `terminology.hl7.org`); luego el que declara la versión FHIR validada (`fhirRank` 2/1/0); luego mayor versión (`versionorder`); empate: el cargado primero | `pkg/terminology/terminology.go:556-575`; `pkg/loader/copies.go:10-84` |
| `fhirVersions` distinto | No es error: se **rankea** (`fhirRank`). Un core de otra versión FHIR se rechaza con warning | `pkg/validator/dependencies.go:71-77,133-135` |

### 1.3 go-fhir-mutator

| Aspecto | Hallazgo | Evidencia |
|---|---|---|
| Carga | **Un solo package desplegado**: `hl7.fhir.r4.core#4.0.1` (`fhirpkg.Open(dir)`), buscado en `$MUTATOR_PACKAGES`, caché del oráculo, `~/.fhir`. No hay R4B ni R5 en el código de producción | `internal/fhirpkg/fhirpkg.go:58-59,85-90` |
| Tipos | Lee ficheros uno a uno: indexa **toda** resource por `url`; lee `StructureDefinition` (tipos concretos), `ValueSet`, `CodeSystem` (issue-severity, issue-type, composición de ValueSet) | `fhirpkg.go:119-139`; `internal/verdict/severity.go:39`, `issuetype.go:26`; `internal/profile/codes.go:78,91` |
| Oráculo | Manifiesto `oracle/r4.json` con 8 packages (core R4, xver-extensions 0.1.0, terminology.r4 6.2.0, extensions.r4 5.2.0, terminology 7.4.0, extensions.r5 5.2.0, terminology.r5 7.1.0, extensions 5.3.0), cada uno con `tgz` y `tree` SHA-256, verificados por `VerifyHome` | `oracle/r4.json`; `internal/hl7/hermetic.go:34,297` |
| URL repetida | **Ninguna política**: `p.files[url] = name` sobrescribe en orden de `ReadDir` (un solo package, no ocurre en la práctica) | `fhirpkg.go:133` |
| `fhirVersions` | No lo mira | `fhirpkg.go:48-51` |

### 1.4 go-fhir-server (congelado)

| Aspecto | Hallazgo | Evidencia |
|---|---|---|
| Base declarada | R4: `r4.core#4.0.1`, `terminology.r4#7.0.1`, `uv.extensions.r4#5.2.0`, `r4.expansions#4.0.1`. **R4B**: `r4b.core#4.3.0`, `terminology.r4#7.0.1` («por diseño: no hay `hl7.terminology.r4b` en la línea 7.x»), `uv.extensions.r4b#5.3.0`, `r4b.expansions#4.3.0`. R5: análogo con `.r5` y expansiones 5.0.0. Todos `Required: true` | `internal/packages/defaults.go:16-45` |
| Embebido | Recorte agresivo: core con **SearchParameter, SD de especialización y OperationDefinition**; extensiones solo SearchParameter (19/20/19); terminología solo `package.json` (0 recursos) | `pkg/fhirdefs/embed.go:5-33`; `emb.txt` |
| Origen y SHA | Descarga de `{registry}/{name}/{version}` en runtime, terminología completa en segundo plano; **sin SHA-256** | `internal/packages/downloader.go:210`; grep sin resultados |
| Tipos | SD, SearchParameter, OperationDefinition (índices por tipo, por URL y `tipo$código`) | `internal/fhircontext/registry.go:48-61` |
| URL repetida | Sobrescribe por clave `base:code`; un experimental no pisa a uno no experimental; un IG posterior no pisa lo conocido (`skipKnown`). El índice por URL solo se pisa en el base | `internal/fhircontext/registry.go:1985-2060` |
| `fhirVersions` | No lo compara (su almacén de IG tampoco; ver `nucleo/revision/02` §2.2) | `internal/packages/cache.go:77` |

## 2. Medición de los candidatos (.tgz de `go-searchindex/.cache/packages` + 3 oficiales bajados)

Conteos de recursos de primer nivel de `package/`. Tamaño: del `.tgz` oficial.

### 2.1 Recursos por package

| Package | Tamaño | SD | SP | VS | CS | ConceptMap | OperationDef | NamingSys | `fhirVersions` | Dependencias |
|---|---|---|---|---|---|---|---|---|---|---|
| r4.core 4.0.1 | 12,2 MB | 658 | 1400 | 1316 | 1062 | 80 | 47 | 2 | 4.0.1 | - |
| r4b.core 4.3.0 | 13,7 MB | 651 | 1439 | 721 | 540 | 79 | 47 | 0 | 4.3.0 | - |
| r5.core 5.0.0 | 16,3 MB | 307 | 1244 | 788 | 448 | 94 | 61 | 4 | 5.0.0 | - |
| r4.expansions 4.0.1 | 2,1 MB | 0 | 0 | 553 | 0 | 0 | 0 | 0 | 4.0.1 | - |
| r4b.expansions 4.3.0 | 2,7 MB | 0 | 0 | 657 | 0 | 0 | 0 | 0 | 4.3.0 | - |
| r5.expansions 5.0.0 | 3,3 MB | 0 | 0 | 773 | 0 | 0 | 0 | 0 | 5.0.0 | - |
| uv.extensions.r4 5.2.0 | 1,2 MB | 632 | 19 | 66 | 41 | 0 | 0 | 0 | 4.0.1 | r4.core |
| **uv.extensions.r4b 5.3.0** (oficial, bajado) | 0,77 MB | 701 | 20 | 91 | 65 | 0 | 0 | 0 | **4.3.0** | r4b.core; terminology.r4 7.1.0 |
| uv.extensions 5.3.0 (unificado) | 1,2 MB | 701 | 20 | 82 | 52 | 0 | 0 | 0 | **5.0.0** | terminology.r5 7.1.0 |
| uv.extensions.r5 5.2.0 | 1,2 MB | 632 | 19 | 66 | 41 | 0 | 0 | 0 | 5.0.0 | r5.core |
| terminology.r4 6.2.0 | 5,4 MB | 9 | 0 | 2458 | 1180 | 0 | 0 | 640 | 4.0.1 | r4.core |
| **terminology.r4 7.0.1** (oficial, bajado) | 4,6 MB | 9 | 0 | 2499 | 897 | 0 | 0 | 660 | 4.0.1 | r4.core; extensions.r4 5.2.0 |
| **terminology.r4b 6.0.2** (oficial, bajado; última de su línea) | 5,0 MB | 9 | 0 | 2447 | 1157 | 0 | 0 | 630 | 4.3.0 | r4b.core |
| terminology.r5 6.2.0 | 5,4 MB | 9 | 0 | 2458 | 1180 | 0 | 0 | 640 | 5.0.0 | r5.core |
| terminology.r5 7.1.0 | 4,5 MB | 9 | 0 | 2499 | 897 | 0 | 0 | 666 | 5.0.0 | extensions.r5 5.2.0 |
| terminology (unificado) 7.4.0 | 5,4 MB | 9 | 0 | 2536 | 931 | 0 | 0 | 704 | **5.0.0 solo** | r5.core |

Otros hechos medidos:

- Los SearchParameter de las extensiones: 19 (R4, R5 5.2.0), 20 (5.3.0 y r4b), solo sobre bases de definición de CodeSystem, ValueSet, CarePlan, CommunicationRequest, Goal, Patient... Estado `active` 13 y `draft` 6 (5.2.0); en 5.3.0 hay además 1 `retired`.
- Los SP del core R4 son todos `draft` (1400); R4B tiene 77 `active`; R5 104 `active`. Esto importa a la cascada de `searchindex` (status primero).
- Las expansiones traen `expansion.contains` en el 100 % de sus ValueSet: 55.342 (R4), 70.940 (R4B) y 100.127 (R5) códigos. Los ValueSet del core no están expandidos. El validador y el server no las usan (el server las declara `Required` pero no las embebe).

### 2.2 Solapes de URL (hash del JSON completo; «distinto» significa que difieren en algún campo)

| Par | ValueSet comunes | CodeSystem comunes | SP comunes | SD comunes |
|---|---|---|---|---|
| R4 core / r4.expansions | 552 (todos distintos: la expansión) | - | - | - |
| R4B core / r4b.expansions | 633 | - | - | - |
| R5 core / r5.expansions | 734 | - | - | - |
| R4 core / terminology.r4 6.2.0 | 603 | 752 | - | 0 |
| R4 core / terminology 7.4.0 | 603 | 746 | - | 0 |
| R4B core / terminology.r4 6.2.0 | **0** | 162 | - | 0 |
| R4B core / terminology 7.4.0 | **0** | 177 | - | 0 |
| R5 core / terminology.r5 (6.2.0, 7.1.0) y terminology 7.4.0 | **0** | **0** | - | 0 |
| R4 core / ext.r4 5.2.0 | 40 | 9 | 13 | 389 |
| R4B core / ext.r4 5.2.0 | 30 | 12 | 13 | 393 |
| R4B core / ext 5.3.0 | 30 | 12 | 13 | 392 |
| R5 core / ext.r5 5.2.0 o ext 5.3.0 | 1 | 0 | 0 | 0 |
| terminology.r4 6.2.0 / terminology 7.4.0 | 2457 | 862 | - | 9 |
| ext.r4 5.2.0 / ext 5.3.0 | 65 | 40 | 19 | 631 |

Lectura:

- **Los 13 SearchParameter comunes de R4/R4B con las extensiones difieren en todo**: `version` (core `4.0.1`/`4.3.0`, ext `5.2.0`/`5.3.0`), `status` (`draft` frente a `active`), `expression`, `experimental`, `date`, `publisher`. En R4B además `identifier`, `title`, `processingMode`, `xpathUsage`. Es decir, la cascada de `searchindex` (status, luego semver) elige **siempre el de la extensión**, y en R5 no hay solape. Con `ext.r4` 5.2.0 la cascada es la misma que con `ext.r4b` 5.3.0 en estos 13: la diferencia son 1 SP más (20 frente a 19) y 70 SD más.
- **R5 core no se solapa con la terminología** (0 URL comunes de VS y CS): la terminología de R5 es puramente aditiva. En R4 sí: 603 VS y ~750 CS se repiten, y los CS tienen contenido distinto (ej. `v2-0203`: core R4 versión 2.9, 127 códigos; `terminology.r4` 6.2.0 versión 4.0.0, 138 códigos; `terminology` 7.4.0 versión 5.0.0, 147 códigos; en R4B y R5 el core ni lo trae).
- `hl7.terminology#7.4.0` declara **`fhirVersions: ["5.0.0"]` y depende de `r5.core`**. Para R4 y R4B, `searchindex.accepts` lo rechazaría con error (serie 5.0 frente a 4.0/4.3) y `fhirRank` del validador lo pondría en 0. Hoy solo el `gen` de los tests lo usa para las tres versiones porque `gen.Load` no comprueba `fhirVersions`.
- `hl7.terminology.r4#7.0.1` (R4) y `r5#7.1.0` (R5) tienen los mismos conteos (2499 VS, 897 CS). `terminology.r4b` solo llega a 6.0.2 (2447/1157), confirmando el comentario del server.

## 3. Tabla comparativa por versión

### R4 (4.0.1)

| | searchindex (prod) | searchindex (tests/oráculo) | validator | mutator | server |
|---|---|---|---|---|---|
| core | r4.core 4.0.1 (SHA) | r4.core | r4.core filtrado | r4.core (SHA vía manifiesto) | r4.core filtrado a SP/SD/OpDef |
| terminología | - | `hl7.terminology#7.4.0` (fv 5.0.0) | `terminology.r4#7.0.1` filtrado | manifiesto: `r4#6.2.0` y `#7.4.0` (solo oráculo Java) | `terminology.r4#7.0.1` (vacío embebido; completa por descarga) |
| extensiones | - | - | `extensions.r4#5.2.0` | oráculo: `.r4#5.2.0`, `5.3.0` | `.r4#5.2.0` (solo SP) |
| expansiones | - | `r4.expansions#4.0.1` | - | - | declaradas, no embebidas |

### R4B (4.3.0)

| | searchindex (prod) | searchindex (tests) | validator | server |
|---|---|---|---|---|
| core | r4b.core 4.3.0 | r4b.core | r4b.core | r4b.core |
| terminología | - | `hl7.terminology#7.4.0` | **`terminology.r4#7.0.1`** | `terminology.r4#7.0.1` |
| extensiones | - | - | **`extensions.r4#5.2.0`** (fv 4.0.1) | `extensions.r4b#5.3.0` (oficial; embebido solo 20 SP) |
| expansiones | - | `r4b.expansions#4.3.0` | - | declaradas |

El mutator no soporta R4B hoy.

### R5 (5.0.0)

| | searchindex (prod) | searchindex (tests) | validator | server |
|---|---|---|---|---|
| core | r5.core 5.0.0 | r5.core | r5.core | r5.core |
| terminología | - | `hl7.terminology#7.4.0` (fv 5.0.0, coincide) | `terminology.r5#7.0.1` | `terminology.r5#7.0.1` |
| extensiones | - | - | `extensions.r5#5.2.0` | `extensions.r5#5.2.0` (solo SP) |
| expansiones | - | `r5.expansions#5.0.0` | - | declaradas |

### Diferencias entre librerías

| Eje | Diferencia |
|---|---|
| Qué es «terminología» | validator y server: la línea `hl7.terminology.r4`/`.r5` en 7.0.1; searchindex (tests) y el oráculo del mutator: `hl7.terminology#7.4.0`. Ninguna otra versión coincide. |
| R4B | validator reusa R4 para terminología **y** extensiones; el server reusa `terminology.r4` pero usa `extensions.r4b#5.3.0`; searchindex no tiene extensiones. |
| Extensiones | solo validator (SD, VS, CS) y server (solo SP) las usan en producción; searchindex y mutator no. |
| SHA-256 | solo searchindex y el mutator (manifiesto del oráculo). Validator y server no verifican; sus embebidos son recortes con SHA distinto del oficial. |
| URL repetida | searchindex: cascada determinista con error en empate; validator: ranking (publicador, `fhirVersions`, versión, primero); server: sobrescribe por `base:code` con guardas; mutator: ninguna. |
| `fhirVersions` | searchindex: error por serie; validator: ranking y rechazo de core ajeno; server y mutator: ignorado. |

## 4. Qué consume realmente cada uno (para decidir)

- **searchindex (producción)**: SD (estructura y bindings), SP y ValueSet de **un solo package, el core**. Lo que las extensiones aportan es 19-20 SP (más SD que no usa). La terminología solo la lee el generador de tests.
- **validator**: SD + VS + CS + NamingSystem de core, terminología y extensiones. Es el único que necesita terminología completa.
- **server (congelado)**: SP + SD + OperationDefinition; extensiones solo por sus SP; terminología vía descarga en segundo plano.
- **mutator**: SD, VS, CS del core R4.

## 5. Propuesta de conjunto base por versión

Criterio: un package por función, con `fhirVersions` que contiene la versión servida, sin solape que obligue a una política, con SHA fijado. Capas: **A** (definiciones, común a las cuatro librerías), **B** (terminología, solo validador y quien valide códigos), **C** (expansiones, solo tests).

| Versión | A: definiciones | B: terminología | C: expansiones |
|---|---|---|---|
| R4 | `hl7.fhir.r4.core#4.0.1` + `hl7.fhir.uv.extensions.r4#5.2.0` | `hl7.terminology.r4#7.0.1` | **no** en la base; opcional en tests (`r4.expansions#4.0.1`) |
| R4B | `hl7.fhir.r4b.core#4.3.0` + `hl7.fhir.uv.extensions.r4b#5.3.0` (oficial, `fhirVersions` 4.3.0) | `hl7.terminology.r4#7.0.1` (la línea 7.x de R4B no existe; `r4b#6.0.2` es la última propia) | no |
| R5 | `hl7.fhir.r5.core#5.0.0` + `hl7.fhir.uv.extensions.r5#5.2.0` | `hl7.terminology.r5#7.0.1` | no |

Justificación medida:

1. **Terminología `.r4`/`.r5`, no `hl7.terminology#7.4.0`**: la unificada declara `fhirVersions: ["5.0.0"]` y depende de `r5.core`, así que para R4 y R4B contradice la regla de serie de `searchindex` y cae a rank 0 en el validador; además su `v2-0203` es de la versión 5.0.0 (147 códigos) frente a 127 del core R4. La línea `.r4`/`.r5` coincide con lo que ya embeben validator y server.
2. **Terminología para R4B = `terminology.r4#7.0.1`**: sus 2499 VS y 897 CS son los mismos conteos que `r5#7.1.0`; el core R4B no solapa ningún VS con ella (0) y solo 162 CS, así que no exige política de URL repetida. `terminology.r4b#6.0.2` es una alternativa más antigua (6.x).
3. **Extensiones**: el 100 % de lo que `searchindex` y el server toman de ellas son 19-20 SP; los 13 SP que solapan con el core R4/R4B difieren en versión y status, de modo que la cascada determinista ya los resuelve (gana el de la extensión). Para R4B existe el package oficial `uv.extensions.r4b#5.3.0` (701 SD, 20 SP, `fhirVersions` 4.3.0, 0,77 MB), así que no hace falta el «fallback» a `.r4` del validador (fv 4.0.1) ni el de 5.3.0 unificado (fv 5.0.0).
4. **Expansiones fuera de la base**: ninguna librería de producción las usa; cuestan 2,1 / 2,7 / 3,3 MB y 55k / 71k / 100k códigos. Son oráculo de tests de `gen`.
5. **Core con SHA fijado y recorte documentado**: validator y server embeben recortes cuyo SHA no coincide con el oficial; la base única debería fijar el `.tgz` oficial (como `scripts/packages.sha256`) y derivar el recorte reproduciblemente.

Pendientes y límites de la medición:

- No se midieron los solapes de `terminology.r4#7.0.1`, `.r4b#6.0.2` y `extensions.r4b#5.3.0` (bajados después); sus conteos están en `dl.txt`. Sus SHA-256: `1bda115a…c810471` (ext.r4b 5.3.0), `7e88619a…ce3840e` (terminology.r4 7.0.1), `4435143b…ce7d` (terminology.r4b 6.0.2).
- `hl7.terminology.r4#7.5.0` y `extensions.r4b#5.4.0` existen en el registro; no se midieron.
- El contenido de un package de terminología importa a quien resuelva ValueSet, no a `searchindex`; la decisión de B es independiente de la de A y puede hacerse en otro orden.
