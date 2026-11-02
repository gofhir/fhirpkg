# ADR-002 — Conjunto base de packages por versión FHIR

**Estado: Propuesta; decidida por el usuario el 02-11-2026 (opción C, sin expansiones). Pasa a Aceptada cuando CI corra su verificación** (`make check-packages` y `make validator-graph`; plan 001 §5; `nucleo/LINEAMIENTOS.md` D-1.4). El resto del documento conserva las opciones y los datos que se le presentaron. **Plan:** [001](../plans/001-lock-y-lectura.md). **Reporte:** [`docs/evidence/2026-11-01-graph.md`](../evidence/2026-11-01-graph.md); costo de `Open`: `make evidence`; contraste con el validador: [`2026-11-02-validator-graph.md`](../evidence/2026-11-02-validator-graph.md).

## Decisión (02-11-2026)

El usuario eligió la **opción C, sin expansiones**:

| Versión | Raíces del lock |
|---|---|
| R4 | `hl7.fhir.r4.core#4.0.1` + `hl7.fhir.uv.extensions.r4#5.2.0` + `hl7.terminology.r4#7.1.0` |
| R4B | `hl7.fhir.r4b.core#4.3.0` + `hl7.fhir.uv.extensions.r4b#5.3.0` + `hl7.terminology.r4#7.1.0` (excluye `r4.core` por D1; dos excepciones) |
| R5 | `hl7.fhir.r5.core#5.0.0` + `hl7.fhir.uv.extensions.r5#5.2.0` + `hl7.terminology.r5#7.0.1` |

Motivo: un solo conjunto para el núcleo, el validador y `searchindex`; el validador necesita la terminología; `terminology.r4#7.1.0` también en R4, para que R4 y R4B usen la misma terminología (el mismo `.tgz`); sin expansiones porque ninguna librería de producción las usa. Respuestas a las preguntas de abajo: (1) sí, extensiones y terminología; (2) sí, un lock con dos excepciones y `r4.core` excluido; (3) `7.1.0`; (4) no; (5) sigue en el plan 002. `testdata/r4.lock` fija desde hoy `terminology.r4#7.1.0` (antes `7.0.1`; la variante `7.0.1` de R4 sigue como candidata del reporte del grafo, no como lock).

## Qué se decide

Qué raíces (`name#version`) fija el lock de cada versión FHIR, y por tanto qué packages ven `searchindex`, el modelo y el validador. Hoy cada librería carga un conjunto distinto (plan 001 §1.2): `searchindex`, solo el núcleo; `validator`, núcleo, terminología y extensiones recortados y sin SHA; `server`, núcleo y extensiones sin terminología.

## Datos del reporte

| Versión | Raíces candidatas | Packages leídos / excluidos | Hallazgos |
|---|---|---|---|
| R4 | `r4.core#4.0.1` + `uv.extensions.r4#5.2.0` + `terminology.r4#7.0.1` o `#7.1.0` | 3 / 0 | Sin núcleos ajenos, sin versiones dobles. Solapes: `r4.core` con `terminology.r4` 1.349 canónicos (CodeSystem 746, ValueSet 603; 207 con versión semver distinta, 1.142 con alguna versión que no es semver), con `extensions.r4` 451 (389 StructureDefinition, 13 SearchParameter), todos de versión distinta |
| R4B, con `terminology.r4#7.1.0` | `r4b.core#4.3.0` + `uv.extensions.r4b#5.3.0` + `terminology.r4#7.1.0` | 4 / 1 | `extensions.r4b` obliga a `terminology.r4#7.1.0`, y este a `r4.core` (núcleo de la serie 4.0, **excluido**, D1) y `extensions.r4#5.2.0`. Son de otra serie y hacen falta **dos excepciones** (`terminology.r4#7.1.0`, `extensions.r4#5.2.0`). Solape `r4.core` y `r4b.core`: 3.113 canónicos (1.333 SearchParameter), 18 de la misma versión y ninguno idéntico |
| R4B, variante con `terminology.r4b#6.0.2` | lo anterior + `terminology.r4b#6.0.2` | 5 / 1 | Las mismas dos excepciones (`extensions.r4b` sigue pidiendo `terminology.r4`) y **dos terminologías a la vez**: solapan 3.303 canónicos (2.165 de la misma versión, ninguno idéntico byte a byte) |
| R4B, `terminology.r4#7.0.1` como raíz | las de la primera fila con `#7.0.1` | error D2 | `7.0.1` (raíz) contra `7.1.0` (la pide `extensions.r4b`): hay que usar `#7.1.0` |
| R5 | `r5.core#5.0.0` + `uv.extensions.r5#5.2.0` + `terminology.r5#7.0.1` | 3 / 0 | Sin hallazgos; solapes de 1 o 2 canónicos |

Costo de `Open` sin red (una corrida, `fhirpkg verify`; `make evidence`): `r4.core` solo 177 ms y 31 MB retenidos; R4 completo 364 ms y 73 MB; R4B con `terminology.r4` 440 ms y 104 MB; R4B con las dos terminologías 593 ms y 130 MB; R5 445 ms y 105 MB.

El validador de HL7 (7.0.2, `-ig` de las raíces) carga la terminología R4 en dos versiones (6.2.0 por defecto y la 7.x) y agrega por su cuenta `xver-extensions`, `hl7.terminology#7.4.0` y `uv.extensions#5.3.0`; en R4B no carga `r4.core`.

## Opciones

**A. Solo el núcleo** (como `searchindex` hoy): `r4.core`, `r4b.core`, `r5.core`. Mínimo costo (31 MB retenidos en R4); no ve SearchParameter de extensiones ni CodeSystem de terminología. Sin excepciones ni D1.

**B. Núcleo + extensiones** (como `server`): R4 `extensions.r4#5.2.0`; R5 `extensions.r5#5.2.0`. **En R4B no es posible sin terminología**: `extensions.r4b` depende de `terminology.r4`, así que B en R4B es A o C.

**C. Núcleo + extensiones + terminología** (como el validador): las filas de la tabla. R4 y R5 son limpios; R4B exige D1 y dos excepciones.

**D. Variante R4B con `terminology.r4b#6.0.2`**: la última de su línea; suma una segunda terminología que solapa con la que `extensions.r4b` ya trae.

## Preguntas que la decisión debe cerrar

1. ¿`searchindex` y el modelo necesitan las extensiones y la terminología, o solo el núcleo (A)? Las extensiones traen 19 o 20 SearchParameter (13 repetidos con el núcleo).
2. En R4B, ¿se acepta un lock con dos excepciones y un núcleo R4 excluido (C), o se queda en A?
3. ¿`terminology.r4` 7.0.1 o 7.1.0 para R4? En R4B la 7.1.0 la impone `extensions.r4b`.
4. ¿Se suman las expansiones (`*.expansions`)? No se midieron en este incremento.
5. Los solapes sin orden posible (1.142 canónicos de `terminology.r4` contra el núcleo R4, y 1.231 de 4.577 del núcleo R4, con versión que no es semver) los resuelve el plan 002; cualquier opción con terminología depende de esa regla.
