# ADR-003 — Precedencia de canónicos repetidos y de `base:code` repetido

**Estado:** Propuesta. Las reglas las decidió el usuario (02-11-2026, plan 002 §3 y §8); pasa a Aceptada cuando CI corra su verificación (`NUCLEO.md` §3: el repo aún no tiene CI) y termine el code review del plan 002. **Plan:** [002](../plans/002-canonicos-y-searchparameter.md). **Dorado:** [`testdata/golden/`](../../testdata/golden/).

## Contexto

El conjunto base de cada versión (ADR-002) trae el mismo canónico en más de un package: el núcleo R4 solapa `terminology.r4` en 1.349 canónicos y `uv.extensions.r4` en 451; en R4B hay además un segundo núcleo de otra serie (excluido por D1) y su IG de extensiones. 1.231 de 4.577 versiones del núcleo R4 no son semver (`2.9`: 840, fechas: 288, vacías: 12). Hace falta una regla única y explicable para elegir, y otra para dos SearchParameter con URL distinta y el mismo `base:code` (`LINEAMIENTOS.md` §4.2: «un IG no pisa un `base:code` del núcleo»).

## Decisión

### 1. Canónico sin versión: la cascada

La clave del índice es `(resourceType, url)`: dos tipos de recurso con la misma URL no compiten (A-M3). Entre los candidatos visibles para quien pregunta, cada paso conserva a los mejores; si deja uno solo, ese gana y el paso queda registrado.

| # | Paso | Regla |
| --- | --- | --- |
| 1 | `StepSeries` | Solo para recursos que declaran su propio `fhirVersion` (cadena o lista). Pierde quien declara series (major.minor) y ninguna es la del núcleo del lock. Sin `fhirVersion`, o con un valor que no es una versión FHIR, no se descarta. Si todos perderían, no se descarta a nadie |
| 2 | `StepStatus` | `active` 3 > `draft` 2 > desconocido 1 (`unknown`, vacío, cualquier otro valor) > `retired` 0. Decide antes que la versión: un `retired` más nuevo no le gana a un `draft` (A-M2) |
| 3 | `StepPublisher` | Es copia el candidato cuyo package declara un `canonical` (`package.json`) con un host distinto del de la URL del recurso (sin distinguir mayúsculas). Las copias pierden ante quien no lo es. Si todos o ninguno son copias, no separa. Una URL sin host (`urn:`) o un package sin `canonical` no son copias |
| 4 | `StepVersion` | Un comparador para todo el conjunto (abajo). Si ninguno aplica, no separa |
| 5 | `StepLastUpdated` | `meta.lastUpdated` como instante RFC 3339. Si alguno falta o no se lee, no separa |
| 6 | `StepUnresolved` | Quedan varios: es un `Conflict` sin ganador |

**Comparador del paso 4**, en este orden, el primero que vale para todas las versiones del conjunto:

1. Si algún candidato declara `versionAlgorithmString` (una expresión FHIRPath, que aquí no se evalúa): ninguno, el paso no separa.
2. El algoritmo declarado en `versionAlgorithmCoding.code` (R5, con `system` `http://hl7.org/fhir/version-algorithm`), si todos declaran el mismo, es `semver`, `integer`, `alpha`, `date` o `natural` y todas las versiones lo cumplen.
3. SemVer 2.0.0 estricto (los metadatos de build no cuentan), si todas lo son.
4. Fecha FHIR (`YYYY`, `YYYY-MM`, `YYYY-MM-DD`), si todas lo son; se comparan por su inicio.

Lo que declara un algoritmo que sus versiones no cumplen no se descarta: se infiere. Los empates en un paso (varias versiones iguales en el máximo) pasan juntos al siguiente.

**Resolución.** `Resolve(tipo, canónico)` sin versión devuelve el ganador decidido al construir el `Context`; sin ganador, `ErrUnresolved` con los candidatos y los pasos. `url|version` devuelve la definición con esa versión si es única entre los visibles; si hay varias con esa versión, `ErrUnresolved`. `#fragmento` devuelve el recurso contenido con ese `id`. `ResolveFrom(pkg, ...)` aplica lo mismo sobre los candidatos del cierre de dependencias de `pkg` (él mismo y sus dependencias en el lock, transitivas): una IG no ve las definiciones de otra de la que no depende, y un empate global puede resolverse desde un package que ve menos candidatos.

### 2. `base:code` repetido (SearchParameter)

Parte de un SearchParameter por URL (el ganador de la cascada anterior: la misma URL en dos packages es un canónico). Dos con URL distinta y el mismo `base:code` se deciden así:

1. `StepNotExperimental`: el no `experimental` le gana al `experimental` (A-H1). Si todos lo son, no separa.
2. `StepCore`: el del núcleo del lock le gana al de una IG. Si ninguno o todos lo son, no separa.
3. `StepUnresolved`: es un `Conflict` y `SearchParameters` excluye ese `code` en ese tipo. Un `base:code` sin resolver también bloquea el mismo `code` en los niveles siguientes de la herencia (no se cae al de un ancestro sin saberlo).

**Un SearchParameter cuyo canónico queda sin resolver** (misma URL en dos packages, sin ganador) no desaparece: cada candidato aporta sus propios pares `base:code` (sus `base` por su `code`; sin producto cruzado entre candidatos) y todos quedan sin resolver (si los pares de los candidatos difieren, se registra en el `Trace`). La regla es la de un SearchParameter resuelto: un candidato sin `base` no aplica a ningún tipo y no aporta (el Conflict del canónico es el rastro), y uno con `base` y sin `code` es un error (ronda 2, puntos 4 y 5), y ese `base:code` bloquea el `code` también en los ancestros (code review ronda 1, M1).

`SearchParameters(tipo)` devuelve los del tipo y los heredados por `baseDefinition`, deduplicados por `code` (el del tipo gana), por nivel y por `code` dentro del nivel. Un SearchParameter sin `base` no se indexa (abajo).

### 3. Tipos de recurso y herencia

Un tipo de recurso es un `StructureDefinition` de `kind` `resource` que no es un perfil (`derivation` distinto de `constraint`). `ResourceTypes()` lista solo los concretos (`abstract` distinto de true); los abstractos cuentan para la herencia y se pueden pedir a `SearchParameters` (ronda 1, B2). Un `Coding` de `versionAlgorithmCoding` solo declara el algoritmo si su `system` es el CodeSystem `version-algorithm` (B4). Una URL usada por varios tipos visibles, sin tipo en la llamada, da `ErrAmbiguousType` (B3). La herencia sigue `baseDefinition`; la cadena termina cuando este no resuelve a un tipo de recurso. Si un `baseDefinition` no resuelve, hay un ciclo, o dos definiciones reclaman el mismo `type`, `NewContext` falla.

## Excepciones a la regla 5 (plan 002 §7)

- El orden de los pasos y de `status` no está en la especificación: es esta decisión, con sus tests.
- `SearchParameter` y `StructureDefinition` se nombran en `fhirnames.go`: son la materia del módulo y no hay otro modo de encontrarlos.
- `kind` `resource`, `derivation` `constraint`, los códigos de `PublicationStatus` y de `version-algorithm`, y los nombres de campo JSON (`jsonFieldSources`, con su fuente) también viven allí. `TestFHIRNamesOnlyInFHIRNames` (AST) falla si otro archivo nuevo trae un literal de una palabra que no esté en su lista de formato, o una etiqueta `json` sin fuente.

## Lo que gana en cada caso medido (plan 002 §8.3; dorado en `testdata/golden/`)

| Caso | Gana | Paso | Test |
| --- | --- | --- | --- |
| `_id` y `Condition:subject`, R4, R4B y R5 | `Resource-id` y `Condition-subject` (normativos) sobre `example` y `example-reference` (experimentales) | `not-experimental` | `TestRealMeasuredCases` |
| `Resource:_filter`, R4B y R5 | Nadie; se excluye (`Resource-filter` y `filter`, ambos del núcleo, ninguno experimental). No ocurre en R4 | `unresolved` | `TestRealMeasuredCases` |
| R5 `CodeSystem:effective` y `ValueSet:effective` | `MetadataResource-effective` (núcleo) sobre la IG (A-I1) | `core-first` | `TestRealMeasuredCases` |
| R4, los 13 SearchParameter repetidos con `uv.extensions.r4` | La IG (`active`) sobre el núcleo (`draft`) | `publication-status` | `TestRealMeasuredCases` |
| R4B `valueset-extensions-ValueSet-workflow` | `uv.extensions.r4` (`active`) sobre `uv.extensions.r4b` (`retired`) y `r4b.core` (`draft`) | `publication-status` | `TestRealMeasuredCases` |
| R4B `valueset-trusted-expansion` (una extensión) | `r4b.core` | `publication-status` | `TestRealMeasuredCases` |
| R4B, 240 canónicos | 239 a `uv.extensions.r4b` sobre `uv.extensions.r4` y 1 a `r4b.core` | `series` | `TestRealR4BSeriesStep` |
| Terminología copiada en el núcleo | Su publicador (`terminology.hl7.org` contra `hl7.org`) | `publisher` | `TestCascadePublisher` |
| IG de extensiones contra el núcleo (mismo host `hl7.org`) | El de mayor versión; el publicador no separa (A-M1) | `version-order` | `TestCascadePublisher` |
| `2.9` contra `3.0` | Nadie por versión: no son comparables | `unresolved` o `last-updated` | `TestCascadeVersion` |
| Mismo `urn:uuid` en `CapabilityStatement` y `TerminologyCapabilities` | No compiten (A-M3) | — | `TestKeyIsTypeAndURL` |

Las cuentas por paso de los tres conjuntos (R4: status 645, publicador 1.151, versión 6; R4B: serie 240, status 584, publicador 11, versión 102; R5: status 1, publicador 2, versión 1; cero sin resolver) las fija `TestRealCascadeCounts` y el dorado.

## Hallazgos de la implementación

- **La raíz de la herencia no declara `derivation` en R4 y R4B**, y en R5 hereda de `Base`, un tipo complejo. El plan decía «`kind` resource y `derivation` specialization»; la regla es «`kind` resource y no `constraint`», y la cadena termina en un `baseDefinition` que no es un tipo de recurso.
- **`hl7.fhir.r4.core` y `hl7.fhir.r4b.core` traen 10 SearchParameter sin `base` cada uno** (`codesystem-extensions-*`, `draft`, `experimental`; medido en los `.tgz`): la especificación pide `base` 1..*. No se indexan y no son error. (Los 13 SearchParameter repetidos entre el núcleo R4 y `uv.extensions.r4` son otro conjunto.) `R5` no tiene ninguno.
- `valueset-trusted-expansion` (plan §8.3) es un `StructureDefinition` (una extensión), no un SearchParameter.
- **Diferencia deliberada con `searchindex`:** para el núcleo solo, la lista de `code` por tipo es idéntica en R4, R4B y R5, salvo `_filter` en R4B y R5, que aquí se excluye por quedar sin resolver y que `searchindex` conserva con una de las dos definiciones (`TestRealInheritanceMatchesSearchIndex`).
- `Resolve` y `ResolveFrom` llevan el tipo de recurso (vacío si la URL no la usan dos tipos): el plan decía `Resolve(canónico)`, pero la clave `(resourceType, url)` (A-M3) hace ambigua una URL sin tipo.

## Consecuencias

- Un canónico sin resolver no es error de `Open` ni de `NewContext`: queda en `Conflicts()` y falla `Resolve`.
- Cambiar una regla cambia el dorado: `make golden` y revisar el diff.
- `searchindex` adopta esta cascada en la tajada 3; hoy aplica solo `status` → semver con `Overrides()`.

## Alternativas descartadas

- **Orden de instalación como último recurso** (`server`): esconde una elección; un empate es un `Conflict`.
- **Publicador por prefijo más largo:** daba ganador al núcleo en las extensiones, que publican bajo `http://hl7.org/fhir/` aunque su `canonical` sea `http://hl7.org/fhir/extensions` (A-M1).
- **Serie aplicada a todo recurso:** le quita 178 canónicos de terminología a `r4b.core`, porque `terminology.r4` declara 4.0.1 (A-H2b).
- **Parser de semver permisivo** (Masterminds): lee `2013-11` como `2013.0.0-11`.
