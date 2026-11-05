# 005 — Proyección `_summary` / `_elements`

**Estado:** plan aprobado por el usuario el 03-11-2026, con la ronda adversarial de §8 y las recomendaciones de D1 a D10 (§4). Revisión previa: [`005-revision-previa.md`](005-revision-previa.md).

## 1. Revisión previa (resumen)

El detalle, con las citas y las sondas, está en la revisión previa. Lo que gobierna el plan:

- **Especificación.** search.html R4 §3.1.1.5.8 y §3.1.1.5.9 (R4B, el mismo texto) y R5 §3.2.1.7.5 y §3.2.1.7.6; `ElementDefinition.isSummary`; http.html (read). `true` conserva lo marcado `isSummary`; `text` conserva `text`, `id`, `meta` y los obligatorios de la raíz (R5: «to ensure that the payload is valid»); `data` quita `text`; `_elements` es una lista de nombres de la raíz, y los obligatorios (las tres versiones) y los modificadores (R5) SHOULD volver siempre. SUBSETTED: `http://terminology.hl7.org/CodeSystem/v3-ObservationValue#SUBSETTED`, en `meta.tag`, «mark the resources».
- **Datos medidos.** `isSummary` está en el SD que define cada elemento (el de un tipo de datos, en el SD del tipo). Hay 4, 5 y 4 elementos raíz obligatorios sin `isSummary` (R4/R4B/R5). `Extension.url` (`min=1`) no está en el resumen. `DomainResource.modifierExtension` es `isSummary=false` en R4 y R4B y `true` en R5 (ronda, H3).
- **`gofhir/server`** (congelado, `internal/search/summary/`). Se toma: los datos deciden, y `_elements` suma los obligatorios. Se descarta: el filtro solo en la raíz, la pérdida de todo `[x]` (hallazgo nuevo) y de `_x`, el orden alfabético, el doble camino de `data`, el display escrito a mano y que `_elements` gane en silencio sobre `_summary`.
- **HAPI** (leído del código): `isSummary` en cada nivel, también en los tipos de datos; 400 si llegan `_summary` y `_elements` juntos; con `_elements` no suma obligatorios; tiene una excepción escrita a mano para `CapabilityStatement`. No hay oráculo HAPI ejecutable en local sin agregar las estructuras (D7).

## 2. Alcance

- `func (c *Context) NewProjection(resourceType string, summary SummaryMode, elements []string) (*Projection, error)`, con `SummaryMode` ∈ {`SummaryFalse`, `SummaryTrue`, `SummaryText`, `SummaryData`}. `Projection` no tiene campos exportados: guarda el `Context` que la creó, el tipo y una copia de `elements`. Un nombre inválido es un `*Error` con su `Kind` propio, para que el núcleo decida si lo rechaza o lo ignora (search.html permite las dos cosas; H9).
- `func (c *Context) Project(data []byte, p *Projection, opts ...NormalizeOption) ([]byte, error)`: una sola pasada sobre el escáner y el recorrido del 004. La salida queda normalizada (mismo orden, `_x` junto a `x`, mismos errores con ruta, mismas opciones `WithMaxBytes`/`WithMaxDepth`/`WithMaxErrors`) y proyectada. Es error que `p` venga de otro `Context` o que el `resourceType` de la entrada no sea el de `p` (H8).
- `Model.IsSummary(path) bool` y `Model.IsModifier(path) bool`, leídos del snapshot ganador, por el camino sin sufijo, por cada variante de `[x]` y por los caminos de los tipos de datos (como `Cardinality`).
- El tag SUBSETTED, con el display leído del CodeSystem del Set cuando está.

**Fuera:** `_summary=count` (no proyecta un recurso: es del núcleo), el parseo de la URL (mayúsculas, comas, prefijos de tipo de R5: el núcleo arma una `Projection` por tipo), las rutas anidadas de `_elements` (D3), `_include`/`_revinclude`, XML.

## 3. Reglas

1. **`true`.** Se conserva un elemento si su `IsSummary` es verdadero, evaluado en cada nivel con el SD que lo define (el del recurso para la raíz y los backbone; el del tipo para los hijos de un tipo de datos) (D1). En cada nivel conservado se conservan además los hijos con `min>0` (D8) y los modificadores con valor (`IsModifier`, D10). Un elemento cuyo tipo es `Extension` se copia entero cuando se conserva (D1).
2. **Primitivos.** El valor de un primitivo es una hoja: se conserva o se quita entero con su elemento. Su `_x` es la parte `Element` del primitivo (`id`, `extension`) y se filtra con la misma regla que cualquier `Element`: con `true` cae (`Element.id` y `Element.extension` no están en el resumen); con los demás modos sigue a su elemento. Un `_x` sin `x` se conserva o se quita según el elemento. En un arreglo, el `_x` conservado queda alineado con `x` (los `null` de alineación de json.html §2.6.2.3), o desaparece si queda solo de `null` (H4).
3. **Contenedores vacíos.** Si al proyectar un objeto o un arreglo queda vacío, se quita, hacia arriba, y cuenta como algo quitado. La salida nunca tiene `{}` ni `[]` (ele-1; `Normalize` los rechaza) (H2).
4. **`text`.** Se conservan `text`, `id`, `meta`, los elementos raíz con `min>0` y los modificadores raíz con valor (D10), enteros. Un obligatorio `[x]` se conserva por la variante presente.
5. **`data`.** Se quita `text` de la raíz y de cada recurso anidado (D2). `Composition.section.text` se conserva: la especificación nombra «the text element» del recurso.
6. **`_elements`.** Se conservan, enteros, `resourceType`, los nombres pedidos, `id` y `meta` si están (decisión, no texto de HL7: D4), los elementos raíz con `min>0` y los modificadores con valor (D4). Un nombre es el de un elemento raíz, sin `[x]` ni variante (`value` sí; `value[x]` y `valueQuantity` no) (D3). Un nombre que el tipo no tiene es un error de `NewProjection`.
7. **`_summary` junto con `_elements`** (distinto de `false`) es un error de `NewProjection` (D5).
8. **Recursos anidados** (los elementos de tipo `Resource`, del 004): con `true` y `data`, la proyección se aplica recursivamente con el modo; con `text` y `_elements`, se copian enteros (los nombres son del tipo de la raíz) (D2).
9. **`contained` referido.** Un recurso de `contained` al que apunta una referencia local (`#id`) conservada se conserva, proyectado con el mismo modo; los demás caen si el modo quita `contained`. Así ninguna referencia conservada queda colgando (ref-1) y ningún contenido queda sin referir (dom-3) (D9, H1).
10. **SUBSETTED.** Se marca **cada recurso que perdió algo**, en la raíz y anidado (D6, H5); la raíz se marca también si solo perdió algo uno de sus anidados. El tag es `{"system":…, "code":"SUBSETTED", "display":…}`, al final de `meta.tag`, sin duplicarlo si ya está, creando `meta` en su lugar del orden si falta. Si el CodeSystem está en el Set, el concepto se verifica y el display sale de ahí; si no está (un lock solo con el núcleo de R5), el tag va sin `display`, que es opcional en `Coding` (D6).
11. **Errores.** `Project` rechaza lo que rechaza `Normalize`, con la misma ruta: proyectar no esconde una entrada inválida.

## 4. Decisiones (para el usuario, con la recomendación)

- **D1 — `isSummary` dentro de los tipos de datos:** aplicarlo en cada nivel, como HAPI, y copiar entera toda `Extension` conservada (si no, una extensión conservada perdería `url` y `value[x]` y quedaría inválida: ext-1 y `Extension.url` `min=1`). Alternativa: copiar enteros los valores de tipos de datos, lo que conserva `Attachment.data`, que la especificación excluye.
- **D2 — Recursos anidados:** recursivo en `true` y `data`; enteros en `text` y `_elements`.
- **D3 — `_elements`:** solo nombres de la raíz en el 005, y `value[x]`/`valueQuantity` rechazados en las tres versiones con un `Kind` propio que el núcleo puede ignorar. Las rutas anidadas (extensión de HAPI) quedan para un incremento posterior si el núcleo las pide.
- **D4 — Lo que `_elements` suma siempre:** `id` y `meta` si están, obligatorios y modificadores con valor, en las tres versiones. Diverge de HAPI.
- **D5 — `_summary` y `_elements` juntos:** error, como HAPI.
- **D6 — SUBSETTED:** cada recurso que perdió algo; display del CodeSystem si está en el Set, sin display si no.
- **D7 — Oráculo HAPI ejecutable:** agregar `hapi-fhir-structures-r4`, `-r4b` y `-r5` 8.10.0 (Maven Central, SHA-256 fijado) y un arnés Java, a lo sumo 2 JVM, en un target aparte no bloqueante (`make hapi-projection`), con las diferencias clasificadas.
- **D8 — Raíz obligatoria sin `isSummary`:** en `true` se suman los hijos `min>0` de cada nivel conservado, así ningún recurso sale inválido por esta causa. Desviación deliberada de los datos, registrada en el ADR-005; la lista (4/5/4) se reporta a HL7 con evidencia si el usuario lo pide.

  **Aclaración del code review, ronda 1 (04-11-2026):** un obligatorio (`min>0`) que el filtro dejaría vacío (p. ej. `Provenance.agent.who` con solo una extensión `data-absent-reason`) se conserva entero, no se quita hacia arriba: "to ensure that the payload is valid" (search.html). Aplica en cada nivel conservado.
  **Ampliación aprobada por el usuario el 04-11-2026:** un primitivo con `min>0` cuyo valor falta pero cuyo `_x` está presente está presente por su extensión (json.html, «Representation of primitive elements»). El elemento es el par `x`/`_x`: si se conserva por `min>0`, se conserva el par entero (valor y `_x` completos, `id` y `extension` incluidos), en todo modo que suma obligatorios (`true`, `text`, `_elements`) y en todo nivel conservado. Los `_x` de primitivos no obligatorios se siguen filtrando por `Element.extension`. Motivo: R5 `QuestionnaireResponse.questionnaire` (1..1) del ejemplo f201 solo existe por `_questionnaire`, y con `true` el recurso salía sin el obligatorio.
- **D9 — `contained` referido** (nueva, H1): conservar los contenidos a los que apunta una referencia conservada. Medido en los ejemplos oficiales: sin esta regla, `true` deja referencias colgando en 98, 99 y 106 recursos (R4/R4B/R5) y `text` en 67, 67 y 72 (sobre todo `medication[x]` → `#med`). Alternativa: quitarlos y aceptar ref-1 roto, como HAPI.
- **D10 — Modificadores en `true` y `text`** (nueva, H3): conservar los modificadores con valor en todos los modos, no solo en `_elements`. En R4 y R4B, `true` borraría en silencio un `modifierExtension` de la raíz (cambia el significado del recurso); con `text`, también `implicitRules` y `modifierExtension` en las tres versiones. Desviación deliberada de los datos de R4/R4B, con ADR.

## 5. Criterio de salida

1. **Oráculo de la especificación, en dos capas** (H6), escritas sin usar el código de `Project` (recorren los SD en crudo, como `model_oracle.py` en el 003):
   - **Literal:** aplica solo `isSummary` (`true`), `text`/`id`/`meta`/`min>0` (`text`), `text` (`data`) y los nombres (`_elements`). Toda diferencia con `Project` se clasifica por la regla que la explica (D1, D8, D9, D10, contenedor vacío, `_x`), con su cuenta por versión en un archivo del repo; una diferencia sin clase falla.
   - **Validez** (punto 3), que juzga la intención: las desviaciones existen para no producir recursos inválidos.
   Corpus: todos los ejemplos oficiales de R4, R4B y R5, Synthea R4 y los generados de `go-searchindex` (el del 004), para `true`, `text`, `data` y una muestra determinista de listas de `_elements` por tipo.
2. **Propiedades** en el mismo corpus, en las tres versiones: `Project(x) == Project(Normalize(x))`; `Normalize(Project(x)) == Project(x)`; `Project(Project(x)) == Project(x)` (el tag no se duplica); `Project(x, false) == Normalize(x)`; la salida, sin el tag, es un subconjunto de `Normalize(x)` con el mismo orden; con `true`, todo `min>0` y todo modificador con valor de un nivel conservado está presente; ninguna referencia local conservada queda sin su `contained`.
3. **Validez con el validador de HL7** (`validator_cli`, fijado en `go-searchindex/scripts/validator.sha256`, a lo sumo 2 JVM; H7): sobre una muestra fija y determinista por versión y modo, se valida la entrada y la salida, y **todo error de la salida que no esté en la entrada** falla (no solo cardinalidad: ref-1, ele-1, dom-3, ext-1, cmp-*). El tamaño de la muestra se fija midiendo el costo por recurso en el primer paso de la implementación, con un tope de 1 hora con 2 JVM desde cero; los veredictos se guardan en `testdata/` y se comprueban sin Java en `make check-packages`. Target aparte (`make hl7-projection`), con caché como `make hl7` de `go-searchindex`.
4. **Casos a mano** con la sección de la especificación citada: `[x]` en el resumen y obligatorio; `_x` con solo extensión, sin `x` y en arreglos con `null`; `contained` referido y no referido; `Bundle` y `Parameters` en la raíz y anidados, con el tag en cada entrada; `Composition.section.text` con `data`; `modifierExtension` en la raíz (R4: `Basic`) y en un backbone; `implicitRules` con `text`; `Appointment`/`AuditEvent` (raíz obligatoria sin `isSummary`); un contenedor que queda vacío; recurso sin `meta`, con `meta` solo con el tag, tag ya presente; lock sin THO (tag sin display); `_elements` con `value[x]`/`valueQuantity`/nombre desconocido; `_summary` con `_elements`; `Projection` de otro `Context` o de otro tipo.
5. **Fuzz** de `Project` con el corpus del 004 y las propiedades de (2); `TestProjectIsConcurrent` con `-race` en `make check`.
6. **Mutaciones** (`scripts/mutations.py`): todas detectadas, con al menos: `isSummary` ignorado en los tipos de datos, `min>0` no sumado, modificador no sumado, `_x` no seguido o no filtrado, contenedor vacío conservado, `contained` referido quitado, tag siempre o nunca, tag duplicado, tag solo en la raíz, recursión desactivada en `data`, `Extension` no copiada entera, `IsModifier` siempre falso, el oráculo literal que comparte el predicado del código.
7. **Paridad HAPI** (si D7): sin diferencias sin clasificar.
8. `make check` y `make check-packages` en verde; costo medido (RSS y duración) de lo que se agrega; evidencia con `make evidence`.

## 6. Regla 5 y ADR

ADR-005 (`docs/adr/005-proyeccion.md`), con search.html R4 §3.1.1.5.8 y R5 §3.2.1.7.5:

- `text`, `id`, `meta` en el modo `text` y `text` en `data` (texto de HL7). `id` y `meta` en `_elements` (decisión D4: `meta` hace falta para el tag; `id`, para identificar el recurso). Test: en las tres versiones `text` es de tipo `Narrative` con base `DomainResource.text`, e `id` y `meta` vienen de `Resource`.
- La URL `http://terminology.hl7.org/CodeSystem/v3-ObservationValue` y el código `SUBSETTED`; `tag` (`Meta.tag`) y `system`/`code`/`display` (`Coding`). Test: el concepto existe en el CodeSystem del Set en los tres locks.
- `Extension` (D1). Test: es el tipo de `Element.extension` y `DomainResource.modifierExtension` en las tres versiones.
- La referencia local `#id` (D9; references.html, «contained resources»). Test: ref-1 en las tres versiones usa `#` y `%rootResource.contained.id`.
- D8 y D10 como desviaciones deliberadas de los datos, con las listas medidas por versión.

Todos los literales van a `fhirnames.go`; `isSummary` e `isModifier` se suman a `jsonFieldSources` con su fuente (elementdefinition.html).

## 7. Dependencias

- Ninguna en el módulo principal.
- D7 (si se aprueba): las estructuras de HAPI 8.10.0 como artefactos de test descargados con SHA-256 fijado, fuera de `go.mod`, en un target aparte.

## 8. Ronda adversarial

Revisor: Opus (excepción vigente mientras Codex no tiene cuota, hasta el 04-11-2026 03:52), sin el razonamiento del plan. Sondas del revisor en `/Volumes/dev/tmp/adv-fhirpkg005/scripts/` (fuera del repo); H3 verificado por Opus en los tres núcleos.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| H1 | alta | Quitar `contained` deja referencias `#id` colgando (ref-1): 98/99/106 recursos con `true`, 67/67/72 con `text` | Aceptado: regla 9, D9, propiedad y casos |
| H2 | alta | Un elemento conservado puede quedar `{}`/`[]` (2/3/14 casos): rompe ele-1 y `Normalize(Project(x)) == Project(x)` | Aceptado: regla 3 |
| H3 | alta | `DomainResource.modifierExtension` es `isSummary=false` en R4/R4B: `true` y `text` borran modificadores en silencio | Aceptado: D10 en reglas 1 y 4, ADR |
| H4 | media | `_x` sin definir: filtrado frente a `Element.extension`, `_x` sin `x`, primitivos como hojas | Aceptado: regla 2 y casos |
| H5 | media | SUBSETTED solo en la raíz no marca las entradas recortadas de un `Bundle`; falta definir el caso sin THO | Aceptado: regla 10 y D6 |
| H6 | media | El oráculo codifica las mismas reglas que el código | Aceptado: dos capas (§5.1) |
| H7 | media | La validez solo mira cardinalidad y ext-1, sin muestra ni costo | Aceptado: todo error nuevo contra la entrada, muestra fija, tope de costo medido (§5.3) |
| H8 | media | `Projection` exportada permite saltar la validación; sin control de tipo ni de `Context` | Aceptado: §2 |
| H9 | baja | Rechazar `valueQuantity` en R4 va más allá del texto; la especificación permite ignorar | Aceptado en parte: se rechaza en las tres versiones, con un `Kind` propio que el núcleo puede ignorar (D3) |
| H10 | baja | `id` y `meta` en `_elements` no salen de la especificación | Aceptado: el ADR los registra como decisión (§6) |

## 9. Implementación

Sin commit. Estado: **cerrado el 05-11-2026** (code review terminado en el tope de rondas, §17). Registro del cierre: mutaciones [`2026-11-05-1238-mutations.md`](../evidence/2026-11-05-1238-mutations.md), paridad [`2026-11-05-projection-parity.md`](../evidence/2026-11-05-projection-parity.md), evidencia [`2026-11-05-1431.md`](../evidence/2026-11-05-1431.md); rondas 1 a 3 (§10 a §12), rediseño del oráculo (§13 a §14) y rondas o1 a o3 (§15 a §17).

**Qué existe.** `Model.IsSummary` e `IsModifier` (`model.go`, `modelbuild.go`); `NewProjection` y `Context.Project` (`projection.go`) sobre el recorrido del 004: `Normalize` es ahora `run(..., nil)` y la proyección es un filtro del mismo recorrido (los elementos quitados se recorren igual, así que `Project` rechaza lo que rechaza `Normalize`); `SummaryMode` y cuatro `ErrorKind` nuevos (`unknown-element`, `choice-element`, `conflicting-modes`, `projection-mismatch`); el tag SUBSETTED con el display leído del CodeSystem del Set; [ADR-005](../adr/005-proyeccion.md) y los literales nuevos en `fhirnames.go`.

**Tests.** Casos a mano con las tres versiones y los cores reales (`projection_test.go`, `projection_real_test.go`, `projection_names_test.go`, `projection_model_test.go`), propiedades 1 a 7 (`projection_props_test.go`), `FuzzProject` y `TestProjectIsConcurrent` (en la lista `-race` de `make check`). `scripts/model_oracle.py` confirma `isSummary`/`isModifier` contra los SD en crudo (`TestRealSummaryCounts`).

**Oráculo (§5.1), en `parity/`.** Capa literal que lee los SD en crudo sin usar `Project` (`projection_oracle_test.go`, `projection_test.go`), sobre ejemplos oficiales de las tres versiones, Synthea R4 y los generados; las cuentas por clase están en `parity/testdata/projection.json` (una diferencia sin clase falla). Capa de validez (§5.3): `make hl7-projection` (`scripts/validator_projection.py`, `parity/projection_hl7_test.go`), 100 ejemplos por versión con la entrada y cada modo, veredictos en `testdata/projection-hl7/` que `make check-packages` verifica sin Java; las diferencias con la entrada están clasificadas con su razón en `testdata/projection-hl7/divergences.json`. Costo medido: [`2026-11-03-projection-hl7-cost.md`](../evidence/2026-11-03-projection-hl7-cost.md); desde cero la muestra completa (1500 archivos) cuesta del orden de 10 minutos con 2 JVM, muy bajo el tope de 1 hora.

**HAPI (§5.7).** `make hapi-projection` (no bloqueante): `scripts/hapi.sha256`, `scripts/fetch-hapi.sh`, `scripts/hapi/Projection.java`, `parity/projection_hapi_test.go`; cuentas por clase y versión en `parity/testdata/hapi-projection.json`, ninguna diferencia sin clase.

**Mutaciones.** PR-01 a PR-28 y P-PR-26, P-PR-27 en `scripts/mutations.py` (cubren la lista de §5.6). Corrida completa tras la ampliación de D8 (355, todas detectadas): [`2026-11-04-0948-mutations.md`](../evidence/2026-11-04-0948-mutations.md). Corrida anterior sin caché: [`2026-11-04-0701-mutations.md`](../evidence/2026-11-04-0701-mutations.md) (seis anclas del 003/004 se reajustaron después por el refactor del recorrido y se reverificaron: [`2026-11-04-0702-mutations.md`](../evidence/2026-11-04-0702-mutations.md)). Evidencia general: [`2026-11-04-0703.md`](../evidence/2026-11-04-0703.md); paridad: [`2026-11-04-parity.md`](../evidence/2026-11-04-parity.md).

**Discrepancias con el plan.**
1. Los literales `Reference`, `reference`, `CodeSystem` y `#` se agregaron a `fhirnames.go` (D9 y el tag), con ADR y test; no estaban en la lista de §6.
2. El oráculo literal es Go en `parity/`, no un script; el de validez es Python (`validator_projection.py`) porque el pool de searchindex es `internal`.
3. Con `text`, los `contained` conservados se copian enteros.
4. HAPI usa `validator_cli.jar` en el classpath para las clases base.
5. `NewProjection` devuelve el primer `*Error`.
6. validator_cli 7.0.2 muere con «Ambiguous type Extension» al armar el OperationOutcome de ciertos StructureDefinition recortados; el script parte el lote hasta aislar el archivo y lo registra como `crash` (clasificado en las divergencias).

**Hallazgo resuelto (aprobado por el usuario 04-11-2026).** R5 `QuestionnaireResponse.questionnaire` es 1..1 y el ejemplo f201 lo cumple solo con `_questionnaire`; con `true` el `_x` se filtraba y el obligatorio desaparecía. D8 se amplió (ver §4): el par `x`/`_x` de un primitivo `min>0` se conserva entero. Implementado en `field` (`normwalk.go`); casos a mano en `projection_test.go` (`TestProjectMandatoryPrimitiveKeepsUnderscore`, fixture en las tres versiones) y `projection_real_test.go` (`TestRealMandatoryPrimitiveKeepsUnderscore`: `questionnaire` R5, `SearchParameter.base` repetido, `CapabilityStatement.rest.mode` anidado); mutación PR-28. La clase D8 salió de `divergences.json`.
- En `_elements`, HAPI no suma obligatorios (D4 diverge a propósito); contra HAPI, `isSummary` en `true` difiere en muchos elementos que el SD marca (p. ej. `SearchParameter.description`): HAPI se aparta de la especificación, ver `hapi-projection.json`. No se reportó el issue.
- Los veredictos de terminología del validador (sin servidor, `-tx n/a`) dependen del orden del lote (verificado a mano); clasificados.

**Pendiente.** Code review ronda 1 (Codex); commit si el usuario lo pide.


## 10. Code review, ronda 1

Revisor: Codex. Los cinco hallazgos se confirmaron con reproductores (copia de revisión `/Volumes/dev/tmp/review-fhirpkg005-r1/go-fhirpkg`), que pasaron al repo como tests con nombres en inglés antes de corregir (vistos en rojo los de `projection_real_test.go` y `projection_test.go`).

| # | Sev. | Hallazgo | Corrección y verificación |
| --- | --- | --- | --- |
| R1-1 | alta | Un obligatorio que el filtro vacía se quitaba hacia arriba (`Provenance.agent.who` solo con extensión: `agent` y `who` desaparecían; HL7 da un min=1 nuevo) | `projField` (`projection.go`): si un obligatorio queda vacío y no hubo errores, se repite entero (`copy++`) y se restaura la marca de pérdida. Aclaración de D8 (§4, ADR-005). Test `TestRealMandatoryComplexEmptiedByFilterStaysWhole`; mutación PR-31 |
| R1-2 | alta | D9 solo recogía `Reference.reference`: un canonical local `#id` (`Questionnaire.item.answerValueSet`) perdía su contenido | `noteRef` recoge también los primitivos de tipo `canonical` (`v.prim`, tipo leído del modelo; el literal `canonical` está en `fhirnames.go` con su test `TestRealCanonicalTypeName`) y recorre arreglos. Test `TestRealCanonicalLocalKeepsContained`; mutación PR-32 |
| R1-3 | media | El oráculo literal no miraba el contenido de los subárboles clasificados (accepted pasaba a declined) y `pairOK` aceptaba `["Alice","Bob"]` → `["Bob"]` | `subsetOf` valida cada subárbol clasificado contra la entrada; `pairOK` solo deja quitar posiciones nulas o vacías. Tests `TestOracleRejectsMandatoryCorruption` y `TestOracleRejectsPrimitiveValueDeletion` (`parity/projection_oracle_hardening_test.go`); mutaciones P-PR-29 y P-PR-30 |
| R1-4 | media | El cierre de `contained` era cuadrático (32 000 contenidos: 5,3 s) | Índice por id y lista de trabajo. `BenchmarkProjectContainedChain` (`projection_bench_test.go`): 32 000 contenidos en unos 28 ms, crecimiento lineal (2 000: 1,7 ms; 8 000: 7 ms; 32 000: 28 ms); mutación PR-33 |
| R1-5 | baja | `false` sin `_elements` no era idéntico a Normalize (`_given:[null]` se perdía) | `Projection.filtersUnderscore` (no filtra con `false` sin nombres ni con `data`) y `onlyNulls` (un `_x` solo con null no se toca). Test `TestProjectFalseEqualsNormalizeWithNullUnderscore` y semillas nuevas en la propiedad `Project(false,nil) == Normalize`; mutación PR-35 |

**Los tests solo-real.** `realTargets` devolvía una lista vacía sin los packages y esos casos pasaban sin comprobar nada. Ahora se omiten a la vista (`t.Skip`) sin `FHIRPKG_PACKAGES` y fallan si la variable está y no hay targets.

Verificación tras las correcciones: `make check` y `make check-packages` verdes; los veredictos de HL7 no cambiaron (`make hl7-projection`: 1500 de 1500 en caché); mutaciones completas, 361 de 361 detectadas: (informe de la ronda 1 reemplazado por el de la ronda 2, §11). Lo que cambió en el oráculo: nuevas clases en `parity/testdata/projection.json` («mandatory kept whole (D8)», fuente `hand` con recursos a mano para `_x` desalineados, obligatorio vaciado y valores de un obligatorio) y pins de HAPI actualizados. Dos mutaciones de la ronda anterior (PR-12 y P-PR-30) habían quedado sin detectar porque en los núcleos oficiales ningún `_x` sobrevive al filtro de `Element`; se cubren con una fixture donde `Element.extension` sí es isSummary (`TestProjectUnderscoreSurvivesWhenElementExtensionIsSummary`) y con la fuente `hand`.

## 11. Code review, ronda 2

Revisor: Codex. Cuatro hallazgos confirmados con reproductores (`/Volumes/dev/tmp/review-fhirpkg005-r2/go-fhirpkg`), llevados al repo como `projection_review2_test.go` y `parity/projection_review2_test.go` y vistos en rojo antes de corregir.

| # | Sev. | Hallazgo | Corrección y verificación |
| --- | --- | --- | --- |
| R2-1 | media | `subsetOf` aceptaba borrar un descendiente obligatorio (`Appointment.participant.status`) conservando su `modifierExtension` | `subtree`/`subtreeKey` (`parity/projection_oracle_test.go`): un subárbol clasificado debe salir de la entrada y conservar, a todo nivel, los hijos `min>0` que ésta tiene. P-PR-31 |
| R2-2 | media | Cualquier reemplazo no vacío de una extensión pasaba, y `contained` estaba exento: `valueString` de un `modifierExtension` o `Organization.name` de un contenido cambiados a «CORRUPTED» pasaban | Una Extension conservada debe ser igual a la entrada (D1). `containedOK`: cada contenido conservado existe en la entrada con el mismo id y es su proyección con las mismas reglas (entero fuera de `true`). P-PR-32 (extensión) y P-PR-33 (contenido) |
| R2-3 | media | El oráculo rechazaba una salida válida de D8 (R5, `Provenance` con dos `agent`, uno con `who` solo con extensión: «1 literal elements, 2 in Project») | `cmpArray` conserva vacía en la capa literal la ocurrencia de un obligatorio que el filtro vacía y la compara con la entrada (clase «mandatory kept whole (D8)»); se corrigió además el desfase de índices al comparar con la entrada cuando se quitaban vacíos |
| R2-4 | baja | El respaldo era por elemento: `Provenance.target=[{extension:DAR},{reference}]` perdía la primera ocurrencia | **Aclaración de D8 (decidida por el coordinador):** el respaldo es por ocurrencia, en R4/R4B/R5. `walker.occurrence` (`normwalk.go`) reemplaza el respaldo de `projField`. PR-36 |

Fuente `hand` de la paridad: se agregaron los recursos de los reproductores.

Verificación tras las correcciones: `make check` y `make check-packages` verdes (paridad con 4 fuentes y los pins regenerados, 0 sin clasificar); `make hl7-projection`: 1500 de 1500 veredictos en caché (se indexan por contenido, no cambió ninguno); mutaciones, 365 de 365 detectadas: [`2026-11-04-1543-mutations.md`](../evidence/2026-11-04-1543-mutations.md) (corrida completa de 2621 s; PR-36, P-PR-29, P-PR-31, P-PR-32, P-PR-33 nuevas; se reajustaron los anclajes de M235 y PR-31, que apuntaban al código movido a `occurrence`).

## 12. Code review, ronda 3 (04-11-2026, Codex): no converge

Codex dejó cinco hallazgos medios, todos del oráculo literal (`parity/projection_oracle_test.go`) y ninguno de producción; los cinco se confirmaron en R4, R4B y R5 con `TestReviewRound3Mutations` (copia de revisión `/Volumes/dev/tmp/review-fhirpkg005-r3`, `parity/review_round3_test.go`): cada caso base pasa y la salida corrompida también.

| # | Severidad | Hueco del oráculo | Caso |
| --- | --- | --- | --- |
| R3-1 | media | `pairOK` deja borrar una ocurrencia obligatoria que el literal deja vacía (D8 por ocurrencia) | `delete-mandatory-occurrence` |
| R3-2 | media | Un primitivo obligatorio presente solo por `_x`: borrar el `_x` o el `valueCode` de su extensión pasa | `delete-mandatory-underscore`, `corrupt-mandatory-underscore-extension` |
| R3-3 | media | Un subárbol clasificado puede perder hijos `isSummary`: la completitud mira `min`, no `isSummary` | `delete-summary-descendant` |
| R3-4 | media | Los arreglos `x` y `_x` se comparan por separado: un `_given` de otro largo pasa | `misaligned-underscore` |
| R3-5 | media | `containedOK` no exige el cierre: borrar un contenido aún referido pasa | `delete-referenced-contained` |

Es el tope de 3 rondas (`~/.claude/CLAUDE.md`): se detiene el incremento y decide el usuario. Lectura: las tres rondas encontraron huecos del mismo tipo en el clasificador de diferencias, que se emparcha caso a caso; es señal de diseño del oráculo (capa literal más clases), no de producción.

## 13. Rediseño del oráculo de la especificación (propuesto el 04-11-2026, tras el tope de rondas)

**Estado:** aprobado por el usuario el 04-11-2026, con la ronda adversarial de §13.5.

**Decisión del usuario (04-11-2026):** rediseñar el oráculo antes de cerrar el 005. Producción no cambia salvo que el oráculo nuevo encuentre un defecto.

### 13.1 Por qué

La capa literal de §5.1 aplica solo `isSummary`/`text`/`min>0`/nombres, y toda diferencia con `Project` se explica con una clase (13 clases hoy, `parity/testdata/projection.json`). Cada clase es un permiso: «esta diferencia está bien si se parece a D1, D8, D9…». Las tres rondas de code review encontraron el mismo defecto de forma: una clase que acepta más de lo que su regla permite (R1-3, R2-1, R2-2, R3-1 a R3-5). Emparcharlas no converge, porque el clasificador tiene que reconocer salidas válidas sin saber cuál es la salida correcta.

### 13.2 Qué se cambia

1. **Oráculo exacto e independiente.** `scripts/projection_oracle.py` implementa las reglas 1 a 10 de §3 y las decisiones D1 a D10 con sus aclaraciones (§4), leyendo los SD en crudo del `package.tgz` (como `model_oracle.py`: snapshot ganador, tipos de datos, `[x]`, `contentReference`, `isSummary`, `isModifier`, `min`, el tipo de cada elemento). Recibe un recurso, el modo y la lista de `_elements`, y devuelve **la salida esperada completa**, en el orden del modelo y con el tag SUBSETTED. No llama a Go ni lee nada de `fhirpkg`.
2. **Comparación exacta.** `TestProjectionParity` compara `Project` con el oráculo **igualdad estructural con orden de claves**, y los números por su lexema (el oráculo lee con `parse_float`/`parse_int` que conservan el texto). Cualquier diferencia falla, con la ruta del primer nodo distinto. No hay clases ni cuentas por clase: `parity/testdata/projection.json` pasa a fijar solo cuántos recursos se compararon por fuente, versión y modo (control de que el corpus no se vacía) y los saltados con motivo (más de 4 MB).
3. **Se eliminan** la capa literal, el clasificador (`classifyProjection`, `pairOK`, `subsetOf`, `containedOK`, `cmpArray` y afines de `parity/projection_oracle_test.go`, `projection_oracle_hardening_test.go`, `projection_review2_test.go`) y sus mutaciones P-PR-*, reemplazadas por las de 13.4.
4. **Independencia de autor.** El oráculo lo escribe un subagente nuevo que recibe solo este plan (§3, §4, §13), la especificación y `model_oracle.py`; no lee `projection.go`, `normwalk.go` ni los tests de Go. Los desacuerdos con `Project` se resuelven contra la especificación y el plan, uno por uno, y cada resolución queda registrada (bug de producción, bug del oráculo o regla ambigua del plan, que vuelve al usuario).
5. **Lo que se conserva:** la capa de validez con HL7 (§5.3, `divergences.json`), las propiedades (§5.2), los casos a mano (§5.4), el fuzz, la paridad HAPI (informativa) y la fuente `hand` de la paridad, que suma los reproductores de las tres rondas como entradas.

### 13.3 Riesgo y control

- **Modo común:** las dos implementaciones salen del mismo plan; un error de lectura del plan queda en ambas. Lo controlan la capa de validez de HL7, que no conoce el plan, y los casos a mano con su cita. Además, cada regla del plan tiene al menos un caso a mano en el oráculo Python (`--self-test`, valores escritos desde la especificación) que corre antes de la comparación.
- **Costo:** unos 100.000 pares recurso-modo (ejemplos oficiales, Synthea, generados y `hand`). El primer paso mide el oráculo por versión; tope: que `make check-packages` no pase de 15 minutos en total. Si no entra, se reduce la muestra de `_elements`, no la de `true`/`text`/`data`.

### 13.4 Criterio de salida (reemplaza §5.1 y la parte del oráculo de §5.6)

1. `TestProjectionParity`: 0 diferencias en todo el corpus, en R4, R4B y R5, para `true`, `text`, `data` y la muestra de `_elements`.
2. Controles positivos: las 6 corrupciones de la ronda 3, las 3 de la ronda 2 y las 2 de la ronda 1 (`accepted→declined`, `["Alice","Bob"]→["Bob"]`), aplicadas a la salida de `Project`, hacen fallar la comparación.
3. Mutaciones: todas las de producción (PR-*) detectadas por la comparación exacta o por otro test; mutaciones del oráculo Python (una por regla de §3 y por decisión D1, D8, D9, D10) detectadas por la comparación o por `--self-test`.
4. `make check` y `make check-packages` en verde, con la duración medida; `make mutations` completo.
5. Code review de este cambio: nuevas rondas, hasta 3, sobre el diff del oráculo.

### 13.5 Ronda adversarial del §13 (04-11-2026, Codex)

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| P13-1 | alta | El autor recibe reglas incompletas y en conflicto: D8 por ocurrencia está solo en §11; la regla 2 quita el `_x` solo de `null`, pero desde R1-5 `false` y `data` no lo tocan; la regla 8 copia enteros los anidados en `text`, la 9 proyecta los `contained` con el mismo modo | **Aceptado:** §13.6 consolida las reglas con su precedencia; es lo único que recibe el autor, junto con la especificación |
| P13-2 | alta | D9 no define canonicals, referencias dentro de extensiones o de copias enteras de D8, cierre transitivo, ciclos, alcance ni orden | **Aceptado:** §13.6, regla D9 completa, con casos a mano de cadena, ciclo, canonical y anidado |
| P13-3 | alta | Un solo `package.tgz` no da lo que depende del Set: el display de SUBSETTED sale de otro package, y `model_oracle.py` no resuelve canonicals duplicados | **Aceptado:** el oráculo carga los packages del lock (lista y SHA-256 del lock, leídos por el script, no de `fhirpkg`) con una regla de ganador escrita (§13.6) y lee también el CodeSystem. Las constantes de prosa (URL y código de SUBSETTED, `#`, `text`/`id`/`meta`) son las de ADR-005 y se citan ahí |
| P13-4 | media | La igualdad estructural esconde escapes y claves duplicadas, y puede mezclar números con strings | **Aceptado:** la comparación es sobre una secuencia de tokens tipados y miembros en orden (string decodificado, número por lexema, booleano, null); duplicados, constantes no JSON y basura al final son error en ambos lados. Los escapes los cubre ya la propiedad `Normalize(Project(x)) == Project(x)` en bytes. Mutaciones del comparador: claves reordenadas, lexema `1.0`→`1`, número→string, display del tag |
| P13-5 | media | Ocultar dos archivos no aísla al autor | **Aceptado:** subagente con contexto nuevo y un paquete de entrada cerrado (§3, §4 y §13.6 de este plan, ADR-003, ADR-005, la especificación, `model_oracle.py`, el lock y los packages). Fuera: código y tests de Go, dorados, evidencia, historial y mis explicaciones. El oráculo y su `--self-test` se congelan (un commit en la copia de trabajo, hash registrado) antes de ver la primera diferencia con `Project` |
| P13-6 | media | El tope de 15 min no está medido: son 9 ms por par | **Aceptado:** primer paso, medir en frío un lote representativo por versión con un proceso y un modelo por versión, cada recurso parseado una vez; RSS pico medido. Orden: `--self-test`, corrupciones históricas, casos borde, corpus completo con `-failfast` |
| P13-7 | media | Borrar la capa literal pierde la vista de cuánto se desvía la política de los datos | **Aceptado en parte:** el oráculo informa, sin bloquear, cuántas veces actuó cada regla (D1, D8, D9, D10, contenedor vacío, `_x`) por versión y modo, en `docs/evidence/<fecha>-parity.md`; cada corrupción histórica pasa a test de rechazo antes de borrar el clasificador; el benchmark de la cadena de `contained` queda |
| P13-8 | alta | El criterio no exige rechazo de entradas inválidas ni cobertura | **Aceptado, salvo la revisión con dos modelos:** §13.4 suma la paridad de errores (`Project` rechaza lo mismo que `Normalize`, ya en las propiedades, ahora también en el corpus `hand`), `false` en la comparación, cobertura mínima por regla y versión (cada regla de §13.6 actúa al menos una vez por versión, o se justifica), identidad del corpus fijada (hash de la lista de entradas), saltos acotados y con motivo, y la resolución escrita de cada diferencia. **Rechazado:** la revisión con dos modelos; el usuario decidió el 30-10-2026 que la revisión adversarial es solo con Codex (`~/.claude/CLAUDE.md`) |

### 13.6 Reglas consolidadas (lo que recibe el autor del oráculo)

Precedencia: las reglas de un nivel se aplican en este orden; una regla posterior no deshace una anterior salvo donde se dice.

1. **Modo por nivel.** Un nivel *filtra* si el modo es `true` (en todo nivel conservado), o `text`/`_elements` (solo la raíz). `false` sin `_elements` y `data` no filtran ningún nivel: la salida es `Normalize(x)`, con `data` quitando el `text` de la raíz y de cada recurso anidado proyectado (no `Composition.section.text`).
2. **Qué se conserva en un nivel que filtra.** `true`: los hijos `isSummary`; `text`: `text`, `id`, `meta`; `_elements`: `resourceType`, los nombres pedidos, `id` y `meta`. En los tres: los hijos `min>0` (D8) y los modificadores con valor (D10; D4 en `_elements`). Un `[x]` se juzga por su elemento y se conserva por la variante presente. Lo conservado en `text` y `_elements` va entero.
3. **Extension.** Un elemento de tipo `Extension` conservado va entero (D1).
4. **Primitivos.** El valor es una hoja. El `_x` es su parte `Element`: en un nivel que filtra se filtra como `Element` (con `true`, `id` y `extension` caen) salvo que el primitivo sea obligatorio (D8: el par `x`/`_x` va entero). En un arreglo, el `_x` que queda se alinea con las ocurrencias de `x` que quedan, con `null` donde no hay parte `Element`, y desaparece si queda solo de `null`. En un nivel que no filtra, el `_x` no se toca (también uno solo de `null`).
5. **Vacíos y D8 por ocurrencia.** Un objeto o arreglo que queda vacío se quita, hacia arriba. Excepción: cada ocurrencia de un elemento obligatorio que el filtro dejaría vacía se conserva entera, como en la entrada.
6. **Recursos anidados** (elementos de tipo `Resource`, incluidos `contained`, `Bundle.entry.resource` y `Parameters.parameter.resource`). Con `true` y `data` se proyectan con el mismo modo; con `text` y `_elements`, si se conservan, van enteros.
7. **D9.** Alcance: cada recurso de dominio con `contained`. Las referencias locales son los `Reference.reference` y los primitivos de tipo `canonical` cuyo valor empieza con `#`, en la salida ya proyectada de ese recurso, incluidas las que están dentro de extensiones enteras, de copias enteras de D8 y de los contenidos conservados (cierre transitivo; un ciclo termina). `#` solo apunta al propio recurso. Un contenido apuntado se conserva, con su `id`, en su posición original y proyectado según la regla 6; los no apuntados caen si el nivel quita `contained`. Una referencia de un recurso anidado (por ejemplo, un `Bundle.entry.resource`) se resuelve en ese recurso, no en la raíz.
8. **SUBSETTED.** Cada recurso, raíz o anidado, que perdió algo o tiene un anidado que perdió algo lleva el tag al final de `meta.tag`, sin duplicarlo (mismo `system` y `code`), creando `meta` en su lugar si falta. El display es el del concepto en el CodeSystem ganador del Set; sin CodeSystem, sin display.
9. **Errores.** Una entrada que `Normalize` rechaza no tiene salida esperada: el oráculo no la juzga y la paridad exige que `Project` la rechace con la misma ruta que `Normalize`.
10. **Ganador de una definición.** Entre varios recursos de conformidad con la misma `(resourceType, url)` en el Set, gana el que da la cascada de [ADR-003](../adr/003-precedencia-canonicos.md), reimplementada desde el texto del ADR (el autor recibe el ADR, no `precedence.go`); sin ganador, el oráculo falla. Solo se necesitan los `StructureDefinition` que definen tipos y el CodeSystem de SUBSETTED.

## 14. Registro del paso 1-4 de §13 (04-11-2026)

- El oráculo `scripts/projection_oracle.py` (sha256 `120484f3...`, congelado en d75dcb1; reemplazado en el §15 por el congelado en 013ef66) y sus decisiones `docs/plans/005-oraculo-decisiones.md` (sha256 `9a0b9ed64d37aab2807444a3b940c2e81c4f8768afd33a11260e7ea53fad4d8d`) se copiaron byte a byte del commit congelado d75dcb1 (`/Volumes/dev/tmp/oracle005-bundle`); no se editan. `locks/<v>.lock` son enlaces a `testdata/<v>.lock` (el `--self-test` los busca ahí). `make check-packages` corre el `--self-test`.
- `TestProjectionParity` compara token a token con el oráculo (un proceso por versión, `python3 -I -u`, porque el oráculo no vacía su salida hasta el final). Los controles positivos (`TestComparisonRejectsHistoricalCorruptions`) cubren las corrupciones de las rondas 1 a 3 y cuatro del comparador. Se eliminaron la capa literal, el clasificador y las mutaciones P-PR-*.
- Primera corrida: 117.566 pares, 1 desacuerdo (3 versiones): `_given:[null]` con `true`. Detalle en `/Volumes/dev/tmp/oracle005-diffs.md`. Duración 270 s, RSS pico del test 835 MB.

### 14.1 Veredicto de D-1 y mutaciones (04-11-2026)

- **D-1 (veredicto del coordinador): defecto de producción.** Regla 4 del §13.6: en un nivel que filtra, un `_x` que queda solo de `null` desaparece; solo un nivel que no filtra (`false` sin `_elements`, `data`) lo deja. Sin tag SUBSETTED: los `null` no llevan contenido (coincide en las dos implementaciones). Corrección: se quitó `walker.onlyNulls` de `normwalk.go` (era la parte equivocada de R1-5). Test a mano en R4/R4B/R5 escrito desde la regla, visto en rojo: `TestProjectNullOnlyUnderscore`; `TestProjectProperties` (propiedad 7, «hay tag si y solo si la salida difiere de Normalize») descuenta ese `_x`. Fuente `hand` suma `hand-null-only-underscore.json` y `hand-tag-already-present.json`. Tras la corrección la paridad completa da 0 desacuerdos en 117.605 pares (244 s, RSS pico del test 920 MB).
- **Mutaciones de producción vistas por la comparación exacta (§13.4.3).** `scripts/mutations.py` corre los `PR-*` primero en el módulo raíz (el detector más barato) y, si no los detecta, en `parity/` con `PARITY_CORPUS=fast` (ejemplos oficiales y `hand`, sin Synthea ni generados, parando en el primer desacuerdo). Con `MUTATION_PARITY_ALSO=1` corre las dos etapas. Medido con las 31 `PR-*`: la comparación ve 29; no ve PR-12 (`_x` de un primitivo no obligatorio con una parte que sobrevive: en los núcleos oficiales ningún hijo de `Element` es `isSummary`, así que no hay dato real que lo ejerza; lo cubre `TestProjectUnderscoreSurvivesWhenElementExtensionIsSummary`) y, antes de agregar `hand-tag-already-present.json`, PR-21 (ahora la ve). Costo de la etapa de paridad por mutación: de 3,8 s a 52,5 s, media 9,2 s (52 s cuando ninguna diferencia aparece y se recorre todo el corpus rápido).
- **Mutaciones del oráculo (`O-*`).** 30 mutaciones de una copia de `scripts/projection_oracle.py`: una por regla 1 a 9 y por D1, D8 (par y ocurrencia), D9 (cierre, canonical, todos, sin proyectar, posición, extensión) y D10; las ve el `--self-test` o la comparación. Quedaron fuera por no ser observables con los datos oficiales: la alineación de un `_x` con `null` y su desaparición (la regla 4 solo actúa sobre partes `Element` que sobreviven; ninguna lo hace en los núcleos), la regla 10 (los dos CodeSystem candidatos de R4 tienen el mismo display) y las mutaciones que solo cambian un contador. Los comparadores (orden de claves, lexema, número por string, display del tag) son los casos `cmp-*` de `TestComparisonRejectsHistoricalCorruptions`.
- **Verificación.** `make check` verde. `make check-packages` verde en 8 min 59 s en total (tope del §13.3: 15 min); la paridad de la proyección son 244 s de los 465 s del módulo. Mutaciones completas, 388 de 388 detectadas en 2149 s: [`2026-11-04-2319-mutations.md`](../evidence/2026-11-04-2319-mutations.md).

## 15. Code review del oráculo, ronda o1 (05-11-2026, Codex)

Informe: `/Volumes/dev/tmp/codex-o1.md`; reproductores en `/Volumes/dev/tmp/review-fhirpkg005-o1/go-fhirpkg`. Los cuatro hallazgos se confirmaron.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| 1 | alta | La paridad mandaba `Normalize(raw)` al oráculo: una corrupción del recorrido compartido contaminaba lo real y lo esperado | **Oráculo corregido por un autor aislado** (acepta cualquier orden de miembros y emite el orden del modelo, plan 004 regla 2; decisiones 25 a 27). Integración: `compareResource` envía los bytes originales (`raw`); `Normalize` queda solo para la regla 9 y para elegir nombres de `_elements`. `PR-37` (el recorrido reescribe el lexema `1.50`) lo ve `TestProjectionParity` sobre `hand-number-lexemes.json`; fuente `hand` suma `hand-shuffled-members.json` |
| 2 | media | `Normalize` rechazaba `x`/`_x` de distinto largo (`KindLengthMismatch`), válido en R5 | **Decisión del dueño (05-11-2026):** se infiere el sufijo de nulls en R4, R4B y R5 por igual (R5 json.html §2.1.6.4.2; R4/R4B callan). `normwalk.go` rellena el más corto; los `null` sobre el relleno siguen siendo `KindNull` en la ruta del valor; se eliminó `KindLengthMismatch`. Tests desde la especificación vistos en rojo: `TestNormalizeCases` (6 casos) y `TestProjectPaddedUnderscoreArrays`. Mutaciones M227 (reemplazada), M227b y M227c. Texto en plan 004 (enmienda). Sonda `underscore-array-shorter` de la paridad del normalizador pasa de `keeps-invalid` a `transforms` |
| 3 | media | El oráculo no aplicaba la regla 10 a los `StructureDefinition` (último gana) | **Oráculo corregido por un autor aislado** (lee los SD de todos los packages sin `excluded` y aplica la cascada del ADR-003). Integración: el oráculo recibe el mismo lock y la misma carpeta de packages; `O-R10-structuredefinition-last-wins` y `-first-wins` los ve el `--self-test` |
| 4 | media | `run_oracle_mutation` contaba como detección cualquier salida no cero sin `Traceback` (un `SyntaxError` era «detectado») | `py_compile` de la copia y detección solo con `FAILED n` en la salida del `--self-test` con código 1 y líneas `FAIL`; el resto es `error`. El arnés comprueba al arrancar (`harness_self_check`, también `--self-check`) que un oráculo roto de sintaxis da `error` |

**Oráculo vigente:** `scripts/projection_oracle.py` sha256 `fd4c26111ff4e8f2eddfe0dce6e8def02e917b518ab0d1cea7f43dee55ccdedb` y `docs/plans/005-oraculo-decisiones.md` sha256 `6b000b81c871329dbf73ccd0e063a5722a1b3106eeceaad33b1ccd8561ad8883`, copiados byte a byte del commit **013ef66** de `/Volumes/dev/tmp/oracle005-bundle` (congelado; reemplazan a los de d75dcb1). Ningún recurso del corpus llega a un miembro desconocido con `false` (Normalize los rechaza antes: regla 9).

**Medido.** Paridad completa: 0 desacuerdos en 117.626 pares, 376 s, RSS pico del test 696 MB. `make check` verde. `make check-packages` verde en 11 min 4 s (tope 15 min). Mutaciones completas, 393 de 393 detectadas (su reporte lo reemplaza el del §16) (la corrida completa de 12.042 s se hizo en una sola pasada; `O-R1-false-filters` se reanclajó tras el cambio del oráculo y el informe sale de la caché).

## 16. Code review del oráculo, ronda o2 (05-11-2026, Codex)

Informe `/Volumes/dev/tmp/codex-o2.md`; reproductores en `/Volumes/dev/tmp/review-fhirpkg005-o2/go-fhirpkg`. Tres hallazgos confirmados.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| 1 | media | El oráculo lee los SD solo de archivos `StructureDefinition-*` | Oráculo corregido por su autor aislado (decisión 28): selecciona los SD y el CodeSystem SUBSETTED por `resourceType` en todo JSON de la raíz de cada package, con un prefiltro por regex. Integrado byte a byte del commit **971bf5c**; mutación `O-R10-structuredefinition-filename-filter` (vuelve el filtro por nombre `StructureDefinition-*`) detectada por `--self-test` |
| 2 | media | `mutations.py` contaba como detección cualquier test de paridad con nombre que fallara: un oráculo que compila, pasa el `--self-test` y se cae en modo interactivo (`a.lock`→`a.lcok`) salía «detectado por TestComparisonRejectsHistoricalCorruptions» con la causa real `oracle stopped: EOF` | Todo fallo del protocolo con el oráculo (no arranca, se cae, línea ilegible, escritura, salida con error) lleva el marcador `ORACLE-PROTOCOL-FAILURE` (`parity/projection_oracle_test.go`); `parity_fast` lo busca en la salida y devuelve `error` (etapa de paridad de los `O-*` y de los `PR-*`). `--self-check` suma el caso «solo se cae en modo interactivo ⇒ error» |
| 3 | baja | Con `given:["A"],_given:[42,null]` y `WithMaxErrors(1)`, `Normalize` informaba `given[1]` y `Project` (filtrado) `given[0]` | El null sin valor de un sufijo inferido se informa ahora dentro del recorrido de los `_x`, en el mismo orden que `primitiveFiltered`. `TestErrorOrderOfAnInferredSuffixIsTheSameInNormalizeAndProject` (R4/R4B/R5, visto en rojo) y la mutación M227d |

**Oráculo vigente (congelado en 971bf5c):** `scripts/projection_oracle.py` sha256 `cd7fd673519f49a5e91a999ab43e6751dc9a39c20c8db3a8aa4fbadf60caec66` y `docs/plans/005-oraculo-decisiones.md` sha256 `c73da0ba0a531fcb978933aa750dfb6890a33bb83b65ae02d6ed6da32e2c6cf4` (el oráculo del §15, 013ef66, queda reemplazado).

**Medido con 971bf5c.** Paridad completa: 0 desacuerdos, 346 s, RSS pico 712 MB. `make check` verde. `make check-packages` verde en 10 min 30 s (tope 15 min). Mutaciones completas, 395 de 395 detectadas, sin estados `error` (2752 s con 3 workers; las dos ancladas de nuevo tras el cambio del orden de errores, M227 y M233, se volvieron a correr): [`2026-11-05-1116-mutations.md`](../evidence/2026-11-05-1116-mutations.md).

## 17. Code review del oráculo, ronda o3 (05-11-2026, Codex): cierre

Informe `/Volumes/dev/tmp/codex-o3.md`; reproductor `/Volumes/dev/tmp/review-fhirpkg005-o3/go-fhirpkg/scripts/review_o3_harness.py`. Un hallazgo confirmado, de severidad media.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| 1 | media | `ask` aceptaba como respuesta cualquier línea JSON (`{}`), y `oracleOne` fallaba ante un `error` del oráculo sin el marcador `ORACLE-PROTOCOL-FAILURE`: una mutación O-* cuyo modo interactivo escribe `{}` o llama `run_line(None, line)` (un `AttributeError` capturado se vuelve respuesta `error`) salía «detectada por `TestComparisonRejectsHistoricalCorruptions`» | Solo infraestructura de test (el oráculo no se toca). `parseEnvelope` (`parity/projection_oracle_test.go`) valida cada respuesta: el `id` es el de la petición, en orden; exactamente uno de `output` y `error`; sin campos extra; `output` es un único valor JSON. Todo lo demás es fallo del protocolo con el marcador. Un `error` del oráculo sobre una entrada que `Normalize` acepta (`compareResource`, `oracleOne`, los controles positivos) también es fallo del protocolo con el marcador: la respuesta `error` esperada solo existe para las entradas que `Normalize` rechaza (regla 9), que no pasan por el oráculo. `mutations.py --self-check` suma los dos casos de Codex (`{}` y `run_line(None, line)`): cada uno debe dar `error` |

**Cierre.** La revisión terminó en el tope de 3 rondas con su último hallazgo corregido por decisión del dueño el 05-11-2026, sin una cuarta ronda.

**Verificación.** `make check` verde; `make mutations` completa, 395 de 395 detectadas, sin estados `error`, sin reutilizar la caché: [`2026-11-05-1238-mutations.md`](../evidence/2026-11-05-1238-mutations.md); `make check-packages` verde en 12 min 4 s (tope 15 min; con la máquina cargada por otros procesos, por eso el `go test` de `parity/` lleva `-timeout 20m`: dos corridas con el tope por defecto de 10 min lo agotaron); reporte de `make evidence`: [`2026-11-05-1431.md`](../evidence/2026-11-05-1431.md).

