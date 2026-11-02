# 002 — Canónicos y SearchParameter

**Estado:** cerrado (02-11-2026). Code review convergido en la ronda 3. Sin commit.

Segundo incremento de `gofhir/fhirpkg` (tajada 1a). Sobre el `Set` verificado del 001, construye un `Context` inmutable que resuelve canónicos y da los `SearchParameter` de cada tipo de recurso. Arrastra P-3 y P-4 de la ronda adversarial del 001.

## 1. Revisión previa

### 1.1 `server` (`internal/igmanager/postgres_store.go`, congelado)

`latestOf` decide entre las definiciones de un canónico pedido sin versión:

1. `status`: `active` 3, `draft` 2, desconocido 1, `retired` 0;
2. versión de negocio, con un comparador **elegido para todo el conjunto** (semver si todos lo son; si no, orden natural), para que el orden sea transitivo;
3. `meta.lastUpdated` como instante (no como texto);
4. el orden de instalación, como último recurso.

`indexOfBest` da el ganador y `ExplainCanonical` corre el mismo código para explicar cada paso (`Criterion`).

- **Tomar:** el comparador por conjunto, la explicación generada por el mismo código que decide, `lastUpdated` como instante y el rango de `status`.
- **Mejorar:**
  - semver estricto (SemVer 2.0.0), en vez del parser permisivo de Masterminds, que lee `2013-11` como `2013.0.0-11`;
  - el paso del publicador, que `server` no tiene (P-3);
  - `versionAlgorithm[x]` en R5;
  - la restricción al grafo de dependencias de quien pregunta.
- **Descartar:**
  - el orden de instalación: un empate es un `Conflict` sin resolver, no una elección (`LINEAMIENTOS.md` §4.2);
  - el almacén en PostgreSQL;
  - la resolución en cada consulta: aquí se resuelve al construir el `Context`.

`searchindex` (plan 005) ya aplica la cascada `status` → semver a los SearchParameter repetidos, con `Overrides()`. Este incremento la generaliza; `searchindex` adopta `fhirpkg` en la tajada 3.

### 1.2 Especificación

- **Canónicos:** R4 `references.html` §2.3.0.5 y §2.3.0.6 (`url|version`, `#fragmento`; «should pick the latest version»). R5 `resource.html` (`versionAlgorithm[x]`: `semver`, `integer`, `alpha`, `date`, `natural`, o una expresión FHIRPath).
- **Estado:** `PublicationStatus` (`draft`, `active`, `retired`, `unknown`).
- **SearchParameter:** R4 `searchparameter.html`: `base`, `code`, `derivedFrom`; la herencia de tipos de recurso por `StructureDefinition.baseDefinition` (`Resource` → `DomainResource` → recurso).

### 1.3 Datos medidos (001, P-3 y P-4)

- 1.231 de 4.577 versiones del núcleo R4 no son semver (`2.9`: 840; fechas: 288; vacías: 12). En `hl7.terminology`, 22 en prosa (`May 2021 Edition`).
- El conjunto base R4 (ADR-002) solapa `r4.core` con `terminology.r4` en 1.349 canónicos (1.142 con alguna versión que no es semver) y con `extensions.r4` en 451.
- El campo `canonical` de `package.json` identifica al publicador de las copias de terminología (`http://terminology.hl7.org`). `http://hl7.org/fhir` es prefijo de `http://hl7.org/fhir/extensions`: el publicador es el prefijo más largo.

## 2. Alcance

| # | Pieza | Qué entrega |
| --- | --- | --- |
| A | Índice de canónicos | `NewContext(set)`: cada recurso canónico del `Set` (los que tienen `url`), con todas sus versiones y su procedencia. Sin copias: referencias a los bytes del `Set` |
| B | Resolución | `Resolve(canonical)` y `ResolveFrom(pkg, canonical)` (restringida al cierre de dependencias de `pkg`). `url|version` resuelve esa versión exacta; `#fragmento`, un recurso contenido. Sin versión: la cascada de §3.1, decidida al construir el `Context` |
| C | Explicación | `Conflicts()`: cada canónico con más de un candidato, con el ganador, el paso que decidió o «sin resolver», ordenado de forma determinista. `Explain(canonical)`: los pasos de la cascada para ese canónico |
| D | SearchParameter | `SearchParameters(resourceType)`: los del tipo y los heredados por `baseDefinition`, deduplicados por `code`. `base:code` repetido con URL distinta es un `Conflict` (§3.2) |

**Fuera:** el modelo de tipos (003), el normalizador (004), `versionAlgorithm` como expresión FHIRPath (sin evaluador aquí: se trata como no comparable), terminología pesada.

## 3. Diseño

### 3.1 Cascada (sin versión pedida)

**Clave del índice:** `(resourceType, url)`. Dos recursos de tipo distinto con la misma URL no compiten (A-M3).

Entre los candidatos visibles para quien pregunta:

1. **Serie FHIR** (A-H2, decisión del usuario): solo para los recursos que declaran su propio `fhirVersion` (`StructureDefinition`, `CapabilityStatement`, `ImplementationGuide`...). Pierde el que declara una serie (major.minor) distinta de la del núcleo del lock. Se lee del recurso, sin lista de tipos. Los recursos sin `fhirVersion` (terminología, SearchParameter) no pasan por este paso.
2. **`status`**: `active` > `draft` > desconocido > `retired`. Decide antes que la versión: un `retired` más nuevo no le gana a un `draft` (A-M2, decisión del usuario).
3. **Publicador antes que copia** (P-3, A-M1): es copia el candidato cuyo package declara un `canonical` con un host distinto del de la URL del recurso. El núcleo y la IG de extensiones publican bajo `http://hl7.org/fhir/...` (mismo host), así que este paso no los separa y pasan a la versión; la terminología copiada en el núcleo sí se separa (`terminology.hl7.org` contra `hl7.org`). Reemplaza al «prefijo más largo», que daba ganador al núcleo en las extensiones (A-M1).
4. **Versión** (P-4), con un comparador elegido para todo el conjunto: `versionAlgorithm` si todos declaran el mismo y es `semver`, `integer`, `alpha`, `date` o `natural`; si no, semver estricto si todas lo son; si no, fecha FHIR si todas lo son. Si ninguno aplica, el paso no separa.
5. **`meta.lastUpdated`** como instante.
6. **Sin resolver:** un `Conflict` sin ganador. `Resolve` sin versión de ese canónico devuelve error, con la explicación; con versión, resuelve si la versión es única.

`Open` y `NewContext` no fallan por un canónico sin resolver: el reporte los cuenta, y el dorado de cada versión los fija.

### 3.2 SearchParameter

- La herencia sigue `baseDefinition` de los `StructureDefinition` de tipo recurso del `Set`; ningún nombre de recurso en el código.
- **Solo recursos de conformidad:** los ejemplos que un package trae en su raíz (p. ej. `SearchParameter-example`, `experimental=true`, con `code` `_id`) no deben excluir un parámetro normativo (A-H1). La regla de abajo los deja perder; el dorado fija `_id` y `Condition:subject`.
- Dos SearchParameter con URL distinta y el mismo `base:code` (decisión del usuario):
  1. el no `experimental` le gana al `experimental`;
  2. si no separa, el del núcleo le gana al de una IG («un IG no pisa un `base:code` del núcleo», `LINEAMIENTOS.md` §4.2);
  3. si no separa, es un `Conflict` sin resolver y `SearchParameters` excluye ese `code`.
- La misma URL en dos packages es un canónico: se decide con §3.1. En R4, los 13 SearchParameter `draft` del núcleo ceden ante los `active` de `uv.extensions.r4`, como en `searchindex` (plan 005).

## 4. Tests y fuentes de datos

- **Casos a mano** desde la especificación y desde los pares medidos: cada paso de la cascada, el empate, el publicador por host, la serie solo con `fhirVersion` propio, la clave `(resourceType, url)`, el núcleo contra `expansions`, `2.9` contra `3.0`, fechas, prosa, `versionAlgorithm` de R5 (sintético), `|version`, `#fragmento`, la restricción al grafo (una IG que no depende de otra no ve sus definiciones).
- **Dorado por versión**: `Conflicts()` del conjunto base de R4, R4B y R5 (ADR-002), con las cuentas por paso y la lista de los sin resolver.
- **Metamórficos:** el orden de los packages y de los recursos no cambia ni el ganador ni `Conflicts()`.
- **Herencia de SearchParameter:** en las tres versiones, todo SearchParameter con `base` `Resource` aparece en todos los tipos de recurso, y los de `DomainResource`, en sus descendientes. El resultado se compara con `searchindex/definitions` para el núcleo solo (misma lista de `code` por tipo).
- **Fuzzing:** `FuzzParseCanonical`, `FuzzCompareVersions`.
- **Mutaciones:** cada paso de la cascada apagado o invertido; el publicador por prefijo en vez de por host; la serie aplicada a todo o a nada; la clave solo por `url`; el experimental que gana; un empate que elige el primero; `ResolveFrom` sin restricción al grafo; la herencia cortada en un nivel; `base:code` donde gana la IG.

## 5. Criterio de salida

- `make check` en verde; `NewContext` del conjunto base de las tres versiones, con tiempo y RSS en el reporte de evidencia.
- El dorado de `Conflicts()` por versión, con los sin resolver contados y revisados uno por uno (los que queden sin resolver se informan al usuario).
- La lista de `SearchParameters` por tipo coincide con la de `searchindex` en el núcleo solo.
- ADR de precedencia de canónicos y de `base:code` repetido (ADR-003), con las reglas de §3.1 y §3.2 y lo que gana en cada caso medido (§8.3).
- Mutaciones detectadas; code review hasta converger.

## 6. Dependencias nuevas

Ninguna.

## 7. Excepciones a la regla 5

Los códigos de `status` salen del ValueSet `PublicationStatus` del núcleo, pero su orden (`active` > `draft` > desconocido > `retired`) no está en la especificación: es una decisión de la ADR de precedencia, con su test. Lo mismo vale para el orden de los pasos de §3.1 y §3.2 (serie, estado, publicador por host, versión; no experimental, núcleo, sin resolver). Los tipos de recurso salen de los `StructureDefinition`; los nombres de campo (`fhirVersion`, `status`, `experimental`, `url`, `version`, `meta.lastUpdated`, `versionAlgorithm`) son del formato de los recursos canónicos, como `resourceType` en `searchindex`, y van en una lista con su fuente y un guardián de literales.

## 8. Ronda adversarial

Revisor: Opus (Codex sin cuota hasta el 04-11-2026; excepción aprobada por el usuario). Las cifras salen de sondas en el scratchpad sobre los locks de `testdata/`; el dorado de `Conflicts()` (§4) las reproduce en el repo.

### 8.1 Hallazgos

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| A-H1 | alta | Los ejemplos de la raíz del núcleo (`SearchParameter-example`, `experimental`, `code` `_id`; `example-reference` en `Condition:subject`) chocan con los normativos por `base:code` y, con «mismo package = sin resolver», excluirían `_id` | Aceptado: paso «no experimental gana» (§3.2). Medido: `_id` y `Condition:subject` quedan con `Resource-id` y `Condition-subject` en las tres versiones |
| A-H2 | alta | En R4B, el lock trae `uv.extensions.r4` (serie 4.0) junto a `uv.extensions.r4b`; sin un paso de serie, 27 canónicos los gana la de R4 | Aceptado: paso de serie (§3.1, 1) |
| A-H2b | alta | La serie aplicada a todo da a `r4b.core` 178 canónicos de terminología, porque `terminology.r4` declara 4.0.1 | Aceptado: la serie solo se aplica a recursos con `fhirVersion` propio (decisión del usuario). Medido: la terminología vuelve a su publicador |
| A-M1 | media | El `canonical` de la IG de extensiones es `http://hl7.org/fhir/extensions`, pero publica bajo `http://hl7.org/fhir/StructureDefinition/`; con «prefijo más largo», el núcleo gana | Aceptado: publicador por host (§3.1, 3). Medido: la IG gana 4/4 en R4, 4/6 en R4B (2 quedan con el núcleo por estado, A-M2) |
| A-M2 | media | Un `draft` del núcleo le gana a un `retired` más nuevo de la IG (82 en R4) | Aceptado como regla: el estado decide antes que la versión (decisión del usuario) |
| A-M3 | media | La misma URL `urn:uuid` es `CapabilityStatement` y `TerminologyCapabilities` | Aceptado: clave `(resourceType, url)` |
| A-M4 | media | Los tests confirman más de lo que atacan | Aceptado: §4 suma los casos de la tabla 8.3 como esperados escritos a mano y las mutaciones de cada paso nuevo |
| A-M5 | media | `Resource:_filter` queda sin resolver en R4B y R5 (`Resource-filter` y `filter`, ambos del núcleo, ninguno experimental) | Aceptado como regla: se excluye (decisión del usuario); es `special` sin expresión, y `searchindex` no lo indexa |
| A-I1 | info | En R5, `CodeSystem:effective` y `ValueSet:effective` quedan con `MetadataResource-effective` (núcleo sobre IG), al revés de los 13 de R4, que comparten URL y se deciden por estado | Aceptado como consecuencia de las reglas; va al dorado |

### 8.2 Cuentas de la cascada aprobada

| Lock | Claves | Repetidas | serie | status | publicador | versión | lastUpdated | sin resolver |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| R4 | 6940 | 1802 | 0 | 645 | 1151 | 6 | 0 | 0 |
| R4B | 7156 | 937 | 240 | 584 | 11 | 102 | 0 | 0 |
| R5 | 7789 | 4 | 0 | 1 | 2 | 1 | 0 | 0 |

En R4B, la serie decide 239 a favor de `uv.extensions.r4b` sobre `uv.extensions.r4` y 1 a favor de `r4b.core`.

### 8.3 Casos que van al dorado y a los tests a mano

- `_id`, `Condition:subject`: el normativo, en R4, R4B y R5.
- `Resource:_filter`: sin resolver en R4B y R5; excluido.
- R5 `CodeSystem:effective`, `ValueSet:effective`: `MetadataResource-effective`.
- R4: los 13 SearchParameter de `uv.extensions.r4` (`active`) sobre los `draft` del núcleo.
- R4B: `valueset-extensions-ValueSet-workflow` lo gana `uv.extensions.r4` (`active`) sobre `uv.extensions.r4b` (`retired`) y `r4b.core` (`draft`). Es un SearchParameter, sin `fhirVersion`, así que no pasa por la serie; lo decide el estado.
- R4B: `valueset-trusted-expansion` queda en `r4b.core`.

## 9. Implementación

**Estado:** cerrado (02-11-2026). Code review convergido en la ronda 3. Sin commit.

### 9.1 Lo hecho

- `NewContext(set)`, `Resolve`, `ResolveFrom`, `Conflicts`, `Explain`, `ResourceTypes`, `SearchParameters`; índice `(resourceType, url)` que referencia los bytes del `Set`; inmutable y concurrente (`TestContextIsConcurrent` con `-race` en `make check`).
- Cascada serie → estado → publicador por host → versión (comparador por conjunto) → `lastUpdated`, y regla de `base:code` (no experimental, núcleo, sin resolver). Reglas y casos en [ADR-003](../adr/003-precedencia-canonicos.md).
- Archivos: `canonical.go`, `order.go`, `cascade.go`, `context.go`, `searchparameters.go`, `fhirnames.go`; `Manifest.Canonical` en `manifest.go`; `fhirpkg context` en `cmd/fhirpkg`; `make golden`; fuzz `FuzzParseCanonical` y `FuzzCompareVersions`; guardián `fhirnames_test.go`; dorado `testdata/golden/*-conflicts.tsv`.
- Tests: `canonical_test.go`, `order_test.go`, `context_test.go`, `searchparameters_test.go` (a mano, con R4/R4B/R5 sintéticos) y `real_context_test.go` (cuentas §8.2, dorado, casos §8.3, herencia, comparación con `../go-searchindex`, metamórficos).
- Mutaciones M45 a M98 en `scripts/mutations.py` (una por paso, apagado o invertido, más las de §4).

### 9.2 Resultados

Las cuentas de la cascada coinciden con §8.2 en las tres versiones, sin diferencias. La herencia coincide con `searchindex` para el núcleo solo, salvo `_filter` (A-M5, deliberado).

### 9.3 Decisiones fuera del plan

- `Resolve`/`ResolveFrom`/`Explain` reciben el tipo de recurso (vacío si no hay ambigüedad).
- Tipo de recurso: `kind` resource y `derivation` distinto de `constraint` (la raíz no declara `derivation` en R4/R4B; en R5 hereda de `Base`, y la cadena termina ahí).
- Un SearchParameter sin `base` (10 en `hl7.fhir.r4.core` y 10 en `hl7.fhir.r4b.core`, medido; los 13 de R4 son los repetidos con `uv.extensions.r4`, otro conjunto) no se indexa y no es error.
- `SearchParameter` y `StructureDefinition` se nombran en `fhirnames.go` (excepción a la regla 5, ADR-003).
- `versionAlgorithmString` desactiva el paso de versión; con `url|version` repetida en dos packages, `ErrUnresolved`.
- Un `base:code` sin resolver bloquea el `code` en los ancestros.
- `valueset-trusted-expansion` del §8.3 es un StructureDefinition.

### 9.4 Evidencia

`make check` y `make check-packages` en verde. Reporte: [`2026-11-02-1155.md`](../evidence/2026-11-02-1155.md) (incluye tiempo y RSS de Open + `NewContext` por versión). Mutaciones completas, todas detectadas: [`2026-11-02-1155-mutations.md`](../evidence/2026-11-02-1155-mutations.md).

### 9.5 Pendiente

Code review ronda 1 (§10); ADR-003 pasa a Aceptada con CI.

## 10. Code review, ronda 1

Revisor Opus (excepción del usuario). Para cada corrección, primero un test que fallaba y después el arreglo. Los scripts de mutación del revisor (`m1.json`, `m2.json`) quedaron en `scripts/mutations.py` con el prefijo `R1-` (menos X20, que es M99).

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| M1 | media | Un SearchParameter con canónico sin resolver desaparecía en silencio y otro SP de otra URL ganaba su `base:code` sin Conflict | Sus candidatos dejan sin resolver cada `base:code` de la unión de sus `base` y `code` (si no coinciden, se anota en el `Trace`); el `code` se excluye y se bloquea en los ancestros. Tests: `TestUnresolvedSearchParameterCanonicalLeavesTheBaseCodeUnresolved` (incluye `Observation:_id` contra el `_id` de `Resource`). Mutación M99 |
| M2 | media | El guardián dejaba pasar literales con espacio o `%` (`strings.Fields("Patient Observation")`, `" SearchParameter"`) y no miraba `cmd/` ni los archivos del 001 | Cada literal se parte en palabras; uno de una sola pieza va a la lista de formato por su valor exacto; uno de varias palabras no puede contener un tipo de recurso ni un identificador camelCase o con guion de FHIR. Los tipos salen del dorado de `testdata` (siempre) y de `Context.ResourceTypes` de las tres versiones (`TestRealFHIRNamesOnlyInFHIRNames`, `make check-packages`): así no hay una lista escrita a mano. Se quitó `legacyFiles`; se escanea `cmd/`; los literales legítimos del 001 y de la CLI están en la allowlist con su fuente. Límite: una palabra corriente de FHIR (`status`, `code`) dentro de un mensaje de varias palabras es inglés y se admite. Mutaciones M100 a M104 |
| M3 | media | Caminos sin test | Tests y mutaciones R1-X3, R1-X4, R1-X6, R1-X7, R1-X10 y R1-Y6/Y2: SD de tipo sin resolver da error; `typeFor` respeta la visibilidad; `Explain` sin tipo informa la ambigüedad; `base` `[X, X]`; orden `integer` con negativos |
| B1 | baja | `Explain` de un candidato único sin lectura daba `Status: ""` | `Explain` lee solo `status` de esos candidatos. `NewContext` no cambia (la lectura es solo en `Explain`). M106 |
| B2 | baja | Comentarios de `ResourceTypes` | Documentado: lista los tipos concretos; los abstractos (`abstract` true) cuentan para la herencia y se piden a `SearchParameters`. Se agregó `abstract` a `jsonFieldSources`. M107 y M108 |
| B3 | baja | El error de tipo ambiguo no tenía sentinel | `ErrAmbiguousType`. M109 |
| B4 | baja | `versionAlgorithmCoding` no miraba el `system` | Exige `http://hl7.org/fhir/version-algorithm` (constante en `fhirnames.go`, con su fuente; verificado en `CodeSystem-version-algorithm` de R5). M105 |
| B5 | baja | La comparación con searchindex se omitía en silencio y no miraba URL | Mensaje de `Skip` explícito; `make check-packages` falla si falta `../go-searchindex` (`ALLOW_NO_SEARCHINDEX=1` lo permite); se compara también la URL ganadora por `base:code`: sin diferencias salvo `_filter` (excluido aquí) |
| — | baja | «El núcleo R4 trae 13 sin `base`» | Son 10 en `hl7.fhir.r4.core` y 10 en `hl7.fhir.r4b.core` (medido en los `.tgz`); los 13 son los SearchParameter repetidos entre el núcleo R4 y `uv.extensions.r4`. Corregido en código, ADR-003 y §9.3 |

Evidencia de la ronda 1: [`2026-11-02-1155.md`](../evidence/2026-11-02-1155.md); mutaciones completas, todas detectadas: [`2026-11-02-1155-mutations.md`](../evidence/2026-11-02-1155-mutations.md).

## 11. Code review, ronda 2

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| 1 | media | `TestRealFHIRNamesOnlyInFHIRNames` usaba `ResourceTypes()` (sin abstractos) | Usa todos los tipos, abstractos incluidos (`c.types`). Prueba sintética con `"see DomainResource and Resource"`. Mutación M113 |
| 2 | media | Rodeos del guardián | Las palabras se cuentan sin los verbos de formato (`"status %s"` ya no es prosa; `"exception: %w"` va a la allowlist con su motivo, junto con otros 4 literales de formato). En la prosa se prohíbe toda palabra camelCase y todo literal cuyas palabras son todas campos de `jsonFieldSources`. `lock.go:161` va a `proseAllowed`. Mutaciones M110 a M112. **Límite documentado:** una lista con al menos una palabra corriente («url version and status») pasa; el guardián no es un analizador de lenguaje |
| 3 | baja | Sin packages el guardián conocía 7 tipos | `testdata/resource-types.txt` (unión de R4, R4B y R5, abstractos incluidos), que `make golden` regenera y `TestRealResourceTypesFile` compara con `c.types`. `make check` lo usa (`TestResourceTypesListIsComplete`) |
| 4 y 5 | baja | Un SP sin resolver con base y sin code no daba error; el producto cruzado de `collectUnresolved` | Una sola regla, la del SP resuelto: sin `base` no aporta; con `base` y sin `code` es error. Cada candidato aporta sus propios pares, sin producto cruzado; el comentario ya dice lo que hace el código. ADR-003 actualizado. Mutación M114 |
| 6 | baja | Mutaciones sobrevivientes | El filtro `d.Code != ""` es ahora el error de M114. El desempate por archivo se prueba con 40 archivos en un package (más de 12: `SortFunc` deja de ser una inserción). M115 |
| 7 | nit | Comentario de `SearchParameters` | Reajustado |

Evidencia de la ronda 2: [`2026-11-02-1301.md`](../evidence/2026-11-02-1301.md); mutaciones completas, todas detectadas: [`2026-11-02-1301-mutations.md`](../evidence/2026-11-02-1301-mutations.md).

## 12. Code review, ronda 3

Revisor: Opus (Codex sin cuota hasta el 04-11-2026). Sobre las correcciones de la ronda 2:

- Verificó las nueve mutaciones nuevas o reescritas (M110 a M115, M98, M99, R1-Y10) con una corrida sin caché: todas detectadas.
- `make check-packages` en verde, con `../go-searchindex` presente: la comparación de herencia no se omitió.
- La regla «con base y sin code es error» no rompe `NewContext` con los locks de R4, R4B y R5: ningún canónico de SearchParameter queda empatado.
- `testdata/resource-types.txt` es reproducible: `make golden` deja `testdata/` idéntico, y un script independiente da los mismos 182 tipos.
- No hay falsos positivos del guardián sobre el código actual.

**Converge:** sin hallazgos confirmados de ninguna severidad. Dos observaciones, sin hallazgo:

- un candidato con base y otro sin base no se marcan como discrepantes, pero el Conflict del canónico deja el rastro;
- una palabra prohibida con guion final (`"Patient- x"`) pasa el guardián, dentro del límite documentado en §11.

## 13. Cierre

- `make check`, `make check-packages` y `make evidence` en verde: [`2026-11-02-1309.md`](../evidence/2026-11-02-1309.md).
- Mutaciones: [`2026-11-02-1301-mutations.md`](../evidence/2026-11-02-1301-mutations.md), todas detectadas, después de una corrida completa sin caché (§11).
- El dorado de `Conflicts()` reproduce las cuentas de §8.2. La herencia y el ganador de cada `base:code` coinciden con `searchindex` en el núcleo, salvo `_filter`, excluido por A-M5.
- ADR-003 en Propuesta hasta que corra CI, como ADR-001 y ADR-002.
- Pendiente fuera del 002: el defecto latente de `searchindex` (`base:code` deduplicado por orden de URL) se resuelve cuando `searchindex` adopte `fhirpkg` (tajada 3) o con un arreglo propio, a decidir.
