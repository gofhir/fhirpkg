# 004 — Normalizador de orden

**Estado:** plan aprobado por el usuario el 02-11-2026, con la ronda adversarial de §8 y las decisiones de §3.2: D1 separar la proyección en el 005; D3 Synthea R4 y, en R4B y R5, ejemplos oficiales más generados (cambio del criterio de la carta §6, decidido por el usuario); D2, D4, D5 y D6 según la propuesta.

Cuarto incremento de `gofhir/fhirpkg` (tajada 1a). La carta (`NUCLEO.md` §6, tajada 1, y §5.5) le asigna a `fhirpkg` el **normalizador de orden**: se aplica una vez al escribir y se guardan y se sirven los bytes normalizados. Criterio de la carta: «produce el mismo orden que `models` en Synthea R4, R4B y R5, rechaza elementos desconocidos con su ruta (donde `models` los borra) y se mide contra `models` en tiempo y asignaciones». Usa el modelo del 003 (`Context.Model()`).

## 1. Revisión previa

### 1.1 `server` (congelado)

- **Reordena al leer, no al escribir** (`internal/storage/sql/adapter_read.go`, `reorderResource`): JSONB guarda las claves en orden alfabético y cada lectura hace `UnmarshalResource` + `MarshalResource` de `models` para devolver el orden FHIR, con una caché por hash (PERF-12). Si falla, devuelve el JSON alfabético y solo lo registra. **Descartar:** la carta decide normalizar una vez al escribir y guardar esos bytes (`LINEAMIENTOS.md` §7: resolver → bloquear → enriquecer → normalizar → extraer → persistir); el costo por lectura y la caché desaparecen.
- **Borra en silencio lo desconocido:** el round trip por los structs de `models` descarta toda propiedad que no tenga campo. **Descartar** (la carta pide rechazarlo con su ruta).
- `_summary`/`_elements` (`internal/search/summary/processor.go`): solo nivel raíz, reordena por `map` (sale alfabético), pierde las propiedades `_x` de los primitivos, `_summary=data` no quita `text` por el camino de `Apply`. Se revisa en el incremento de la proyección (§2), no aquí.

### 1.2 `gofhir/models` v2.10.0 (referencia del orden)

- Un struct por tipo con los campos en el orden del `StructureDefinition`: `resourceType` primero, luego los elementos en orden del snapshot; la propiedad `_x` de un primitivo va **inmediatamente después** de `x`; cada variante de un `[x]` es un campo propio en el lugar del `[x]`, en el orden de sus tipos.
- `Marshal` usa `json.Encoder` sin escapar HTML; los decimales conservan su texto (`decimal.go`).
- Lo desconocido se borra al deserializar; un `resourceType` que la versión no define se guarda tal cual (`UnknownResource`); hay un límite de profundidad (`MaxResourceDepth`).
- **Tomar:** el orden como referencia de paridad (carta). **Mejorar:** lo desconocido es error con ruta; el orden sale del modelo dinámico, no de código generado. **Descartar:** el paso por structs (el normalizador trabaja sobre los tokens JSON).

### 1.3 Especificación

- json.html (R4 §2.6.2, igual en R4B y R5): «Properties can appear in any order»; «order within an array SHALL be maintained»; los parsers no pueden suponer que `resourceType` va primero; «implementers may choose to fix the property order if they are able». El orden fijo es una decisión de implementación, permitida.
- json.html §2.6.2.3: primitivos con `id` o extensiones van en `_x`; si no hay valor, solo `_x`; en repeticiones, los dos arreglos se alinean con `null`.
- json.html §2.6.2.6: la canonicalización JSON de FHIR (`http://hl7.org/fhir/canonicalization/json`) ordena **alfabéticamente**; no es lo que pide la carta (orden de `models`), y se usa para firmas, no para almacenar.
- json.html: «JSON whitespace is not part of the contents of a resource».

## 2. Alcance

**Dentro:**

- `Context.Normalize(data []byte) ([]byte, error)`: recibe un recurso JSON y devuelve los mismos datos con las propiedades en el orden del modelo, sin espacios, con los arreglos en su orden.
- Recursos anidados por su propio `resourceType`: `contained`, `Bundle.entry.resource`, `Parameters.parameter.resource` y todo elemento de tipo `Resource` (sale del modelo, sin nombres).
- Errores con ruta FHIRPath-like (`Patient.name[0].givenx`): propiedad desconocida, `resourceType` desconocido o ausente donde se espera un recurso, `_x` sobre un elemento que no es primitivo, dos variantes del mismo `[x]`, arreglos `x`/`_x` de distinto largo, un valor JSON de la forma equivocada (objeto donde va arreglo y viceversa), clave duplicada.
- Paridad de orden con `models` (módulo `parity/`) y benchmark de tiempo y asignaciones contra `models` (`UnmarshalResource` + `Marshal`).

**Fuera:** la proyección `_summary`/`_elements` y `isSummary` (propuesta: incremento 005, D1), la validación de tipos de valores primitivos, de cardinalidad y de invariantes (es del validador), XML, perfiles.

## 3. Diseño

### 3.1 Reglas

1. **Lectura propia de los tokens JSON** (sin `encoding/json` para leer): `encoding/json` cambia el UTF-8 inválido y los surrogates sin par por U+FFFD sin error, `json.Valid` los acepta, `Token` sin `UseNumber` pasa los números por float64 y no detecta claves duplicadas (H2, verificado con una sonda). El lector es un escáner propio, sin dependencias, que rechaza con ruta el UTF-8 inválido y los surrogates sin par (json.html: «The character encoding is always UTF-8»), copia los números con su texto (`1.50E+2`, `-0.0`, enteros de 19 dígitos) y compara las claves duplicadas ya decodificadas (`"name"` choca con `"n\u0061me"`).
2. **Orden de un objeto de tipo T:** `resourceType` primero si T es un recurso; después los hijos de T en el orden de `ChildElements(T)` (snapshot, sin rebanadas, 003). Para un `[x]`, la variante presente va en el lugar del `[x]`. Para un primitivo, `_x` va inmediatamente después de `x` (o sola si no hay valor).
3. **Tipo de cada hijo:** `TypeOf`/`ResolvePath` del modelo (incluye `contentReference`); un elemento de tipo `Resource` se resuelve por el `resourceType` del objeto (también `Bundle.entry.response.outcome` y, en R5, `Bundle.issues`).
4. **`_x` solo sobre un FHIR primitive-type** (H1): el modelo del 003 suma `IsPrimitive(type)` (sale de `kind: primitive-type` del SD; hoy `kindPrimitive` es interno de `modelbuild.go`). Elementos con tipo de sistema (`Resource.id`, `Element.id`, `Extension.url`: `System.String`) no admiten `_x`, aunque su `FHIRTypeOf` sea `id`/`uri`. Los hijos admitidos en `_x` son los del SD del primitivo menos `value` (`{"value":…}` dentro de `_active` es error; `models` lo vuelve `{}` sin avisar). `xhtml`: su SD admite `id` y no `extension`; `models` no tiene `_div` → clase de paridad.
5. **Primitivos:** los números se copian con su texto. Los strings se reescriben con el escape de `models.Marshal` (D2): mismo contenido → mismos bytes.
6. **Forma JSON que se rechaza** (json.html R4 §2.6.2 y §2.6.2.1): objetos y arreglos vacíos, `null` fuera de la alineación de los arreglos `x`/`_x` (R5 trae `null` en `_x` en 31 ejemplos: son válidos), strings vacíos («String property values can never be empty», D5), claves duplicadas («Property names SHALL be unique»), propiedades desconocidas (incluido `fhir_comments`: «comments SHALL not be in JSON instances»), `_x` donde no corresponde, dos variantes del mismo `[x]`, arreglos `x`/`_x` de distinto largo, objeto donde va arreglo y viceversa, `resourceType` ausente o desconocido para la versión (D4).
7. **Límites** (opciones con valor por defecto documentado, no nombres FHIR): profundidad máxima (`models` usa `MaxResourceDepth=32`; el defecto sale de medir la profundidad máxima de los ejemplos oficiales) y tamaño máximo de entrada.
8. **Contrato de errores** (H7): `Normalize` devuelve todos los errores, ordenados por su lugar en la salida (orden del modelo), con tope configurable; así el resultado no depende del orden de las claves de entrada. Tipo `*Error` con `Kind` (enumerado) y `Path`, accesible con `errors.As`, para que el núcleo arme el OperationOutcome. La ruta es FHIRPath con índices (`Patient.name[0].given[1]`); un error en `_given[1]` se informa en la ruta del elemento (`…given[1]`) con su `Kind`; en recursos anidados la ruta sigue por la propiedad (`Bundle.entry[0].resource.name[0]`). Los errores de sintaxis del escáner usan la misma forma (ronda 1 del code review): tipo raíz leído del `resourceType` si ya se alcanzó (si no, `Resource`), claves decodificadas y nunca vacía. Un nombre de propiedad que no es un identificador (vacío, con punto) va delimitado con acentos graves (`Patient.\`\``). Un guion bajo inicial se quita siempre de la clave (`_given` se informa como `given`; una clave desconocida `_foo`, como `foo`): el escáner no conoce el modelo, así que el recorrido usa la misma regla (`appendPropertyPath`) y los dos coinciden. Colisiones aceptadas (ronda 3): una `_foo` desconocida y una `foo` desconocida informan ambas `X.foo` y solo se distinguen por `Offset`; la clave `_` y la clave vacía informan ambas ``X.` ` `` (propiedad vacía). Son claves inválidas de todos modos (ningún elemento de FHIR empieza con guion bajo ni es vacío), así que se informan como error y no hace falta distinguirlas en la ruta. El tipo raíz de un error de sintaxis solo se toma del `resourceType` si es un identificador (un nombre de tipo siempre lo es); si no, la raíz es `Resource`.
9. **Idempotencia:** `Normalize(Normalize(x)) == Normalize(x)` byte a byte.

### 3.2 Decisiones para el usuario

- **D1 — Dividir la proyección** (confirmada en la ronda, H8): 004 = normalizador; 005 = `_summary`/`_elements`, con su revisión previa. Medido: `Extension.url`, `Narrative.status` y `Narrative.div` son `isSummary=false` en las tres versiones, contra el comentario de `isSummary`; 6 modificadores sin `isSummary` en R4/R4B y 5 en R5 (sin contar `modifierExtension`).
- **D2 — Bytes de los strings:** reescribir con el escape de `models.Marshal` (propuesta) o copiar el token tal cual.
- **D3 — Synthea R4B y R5 (H5):** la carta (§6) exige «Synthea R4, R4B y R5»; medido el 02-11-2026: `synthea.properties` (rama master) solo tiene exportadores FHIR R4 (`exporter.fhir.export`), STU3 y DSTU2; no hay R4B ni R5. Cambiar el criterio de la carta es decisión del usuario; propuesta: Synthea R4 fijado con SHA-256, y en R4B y R5 los ejemplos oficiales más los generados desde `StructureDefinition`.
- **D4 — `resourceType` que la versión no define:** error (propuesta); `models` lo guarda como `UnknownResource` → clase de paridad (p. ej. un recurso solo de R5 dentro de un Bundle R4).
- **D5 — Forma JSON (regla 6), incluidos los strings vacíos:** error con ruta (propuesta).
- **D6 — `IsPrimitive` en la API del modelo** (cambio de la API del 003, regla 4).

### 3.3 Paridad con `models` (H3)

`models` no solo borra: también transforma (`[null,{…}]` → `{}`, `"value":"1.5"` → `1.5`, `"name":[]` se omite), conserva lo inválido (`"meta":{}`, dos variantes de un `[x]`, la última de dos claves duplicadas) y falla en otros (`1E400`, `multipleBirthInteger: 2.0`). La paridad compara el árbol JSON de la entrada con el de la salida de `models` y clasifica cada recurso: **sin cambios** (exige la misma secuencia de bytes que `Normalize`, posible por D2), **borra** (`Normalize` da error con la ruta de lo borrado), **transforma** o **`models` falla** (cada caso clasificado con su regla de §3.1), y las clases `_div` (regla 4) y `UnknownResource` (D4).

## 4. Tests y fuentes de datos

- Casos a mano por regla (§3.1) en R4, R4B y R5, cada uno citando json.html; los esperados salen de la especificación, no de correr el código.
- Escáner (regla 1): UTF-8 inválido, surrogates sin par y bien pareados, `\u2028`, controles, `\/`, exponentes (`1.00000000000000000E-24`, `-1E+245`), `-0.0`, enteros de 19 dígitos, claves duplicadas con escapes distintos.
- Ejemplos oficiales (`hl7.fhir.r4.examples` 4.0.1, `r4b` 4.3.0, `r5` 5.0.0, en `scripts/examples.sha256`): paridad (§3.3), idempotencia. Incluye los 87 de R4 con `resourceType` fuera del primer lugar, los `null` en `_x` de R5 y los Bundle-resources de 34, 41 y 40 MB.
- Synthea según D3, fijado con SHA-256.
- Metamórficos: reordenar las claves con semilla → mismos bytes (y mismos errores, regla 8); agregar espacios → mismos bytes; insertar una propiedad desconocida al azar → error con esa ruta.
- Fuzz: `FuzzNormalize` (sin pánico; si no hay error, la salida es idempotente, UTF-8 válido y tiene las mismas hojas que la entrada).
- Benchmark contra `models` (tiempo, asignaciones, bytes y RSS) por versión y por tamaño, con los Bundle grandes; temprano, para decidir el diseño del escáner.
- Mutaciones en `scripts/mutations.py` para cada regla y cada oráculo.

## 5. Criterio de salida

- Paridad (§3.3) en todos los ejemplos oficiales y en Synthea según D3, sin recursos sin clasificar.
- Todo lo que `models` borra es error con ruta en `Normalize`.
- Idempotencia, metamórficos y fuzz en verde en las tres versiones.
- Benchmark contra `models` (tiempo, asignaciones, RSS) en la evidencia.
- Mutaciones todas detectadas; `make check` y `make check-packages` en verde.

## 6. Dependencias nuevas

Ninguna en la librería (escáner propio de JSON; `encoding/json` solo para escribir strings con el escape de D2). `parity/` ya depende de `models`.

## 7. Excepciones a la regla 5

- `resourceType` (clave de json.html) y el prefijo `_` de los primitivos (json.html §2.6.2.3): vocabulario del formato JSON, en `fhirnames.go` con su fuente. El resto (qué es primitivo, qué es recurso, el orden, los hijos de `_x`) sale del modelo.

## 8. Ronda adversarial

Revisor: Opus (excepción temporal hasta que Codex tenga cuota, 04-11-2026). Sondas en `/Volumes/dev/tmp/adv004probe/`. H1 y H2 verificados además por Opus (`Model` no tiene `IsPrimitive`; sonda de `encoding/json` en `/Volumes/dev/tmp/h2v/`).

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| H1 | alta | `_x` sobre primitivo indefinido; el modelo no expone el kind | Aceptado: regla 4, D6 (`IsPrimitive`), clase `_div` |
| H2 | alta | `encoding/json` pierde UTF-8 inválido, números y duplicados | Aceptado: regla 1 (escáner propio) |
| H3 | alta | Paridad sesgada: `models` también transforma y acepta lo inválido | Aceptado: §3.3 |
| H4 | media | D5 incompleto: strings vacíos, comentarios | Aceptado: regla 6 |
| H5 | media | D3 contradice la carta | Aceptado: D3 pasa a decisión del usuario |
| H6 | media | Casos borde, límites y RSS | Aceptado: §4, regla 7 |
| H7 | media | Contrato de errores vago | Aceptado: regla 8 |
| H8 | baja | Conteo de modificadores en D1 | Aceptado: corregido (6/6/5) |
| H9 | baja | `UnknownResource` como clase | Aceptado: D4 y §3.3 |
| S2 | sosp. | Costo de `Decoder.Token` | Resuelto por la regla 1; el benchmark temprano lo confirma |

## 9. Implementación

Sin commit. Estado: **cerrado** (code review en tres rondas, §10), pendiente del commit del usuario.

**Qué existe.** `Model.IsPrimitive` (D6); `Context.Normalize(data, opts...)` con `*Error` (`Kind`, `Path`, `Offset`, `Detail`), `*ErrorList` (`Unwrap() []error`, `Truncated`) y las opciones `WithMaxDepth`, `WithMaxBytes` y `WithMaxErrors`; lector JSON propio (`normscan.go`) que valida UTF-8, surrogates, gramática de números, caracteres de control, profundidad y tamaño, y guarda los números tal cual; spec por ámbito construida perezosamente desde el `Model` (`normspec.go`); escritor (`normwalk.go`) en el orden de `ChildElements`, sin espacios, con el escape de `encoding/json` sin HTML (verificado contra él en todo Unicode). Los nombres de FHIR nuevos viven en `fhirnames.go`.

**Profundidad por defecto.** Medida sobre los ejemplos oficiales (raíz = 1): R4 22, R4B 21, R5 24; `DefaultMaxDepth` = 64 (`models` limita el anidamiento de recursos a 32). Valores por defecto: 256 MiB y 100 errores.

**Paridad (§3.3).** Fuentes: ejemplos oficiales, Synthea R4 (557 archivos), generados de `go-searchindex` (R4, R4B y R5) y entradas a mano (`probeCases`, 27 por versión). Las cuentas por clase están en `parity/testdata/normalizer.json` y en el reporte `docs/evidence/<fecha>-parity.md`; ningún recurso queda sin clasificar. En los ejemplos oficiales, Synthea y los generados el resultado es idéntico al de `models` en todos los recursos; las demás clases (`deletes`, `keeps-invalid`, `transforms`, `models-fails`, `repairs-text`, `unknown-resource`) las ejercen las entradas a mano.

**Benchmark.** Contra `models` en `parity/` (tiempo, asignaciones, bytes y RSS de un proceso hijo): cifras en el reporte de paridad. Medido a mano: el Bundle de 35 MB tarda unas 3 veces menos y retiene unas 5 veces menos RSS; con archivos pequeños y medianos, de 2 a 4 veces menos tiempo y de 20 a 60 veces menos asignaciones. No hizo falta rediseñar el lector.

**Decisiones fuera del plan.**

1. **El plan se equivocaba sobre `_div`:** `models` v2.10.0 sí tiene `Narrative.DivExt` (`_div`), y `_div:{id}` queda igual. Solo `_div` con `extension` difiere: `models` lo conserva y `Normalize` lo rechaza como `unknown-property`, porque el SD de xhtml tiene `extension` con max 0. Se quitó la clase `_div`; las entradas `underscore-div` (igual) y `underscore-div-extension` (`keeps-invalid`) la reemplazan.
2. Clases añadidas del texto de §3.3: `keeps-invalid` y `repairs-text` (`models` cambia UTF-8 inválido y surrogates sueltos por U+FFFD), más `unknown-resource`.
3. `Normalize` es variádica (`Normalize(data, opts...)`) y el tipo de lista de errores es `*ErrorList`.
4. `slotTypes` = ancestros de los recursos concretos menos los de los primitivos; resuelve la raíz común de R5 (`Base`) y los abstractos sin descendientes concretos (`CanonicalResource`, `MetadataResource`).
5. Los hijos de `_x` son `ChildElements(P)` menos `ChildElements(ParentType(P))`, lo que quita `value` sin nombrarlo.
6. Los errores salen en el orden de la salida; las propiedades desconocidas, ordenadas por nombre decodificado.

**Defectos de otras librerías.** Publicado como [gofhir/models#89](https://github.com/gofhir/models/issues/89) (03-11-2026): [`docs/issues/models-json-roundtrip-losses.md`](../issues/models-json-roundtrip-losses.md) (decimal fuera del rango de `float64`, texto inválido cambiado por U+FFFD sin error, pérdidas del round trip).

**Tests y mutaciones.** Casos por regla y versión con la cita de json.html (`normalize_test.go`), lector (`normalize_scan_test.go`, contra `encoding/json`), metamórficos y `FuzzNormalize` (`normalize_meta_test.go`), concurrencia con `-race`. Mutaciones M198 a M259 y P-07 a P-17 en `scripts/mutations.py`. Evidencia: [`2026-11-02-2335.md`](../evidence/2026-11-02-2335.md), paridad [`2026-11-02-parity.md`](../evidence/2026-11-02-parity.md) y mutaciones sin caché, todas detectadas (corrida repetida tras reajustar las anclas M123 y M124): [`2026-11-02-2334-mutations.md`](../evidence/2026-11-02-2334-mutations.md).

**Pendiente:** commit si el usuario lo pide; publicar el issue si lo pide.

## 10. Code review, ronda 1

Revisor: Opus. Hallazgos corregidos el 03-11-2026 (Sonnet), cada uno con su test escrito desde la especificación y comprobado en rojo antes de corregir. Evidencia: [`2026-11-03-0045.md`](../evidence/2026-11-03-0045.md) (`make check`, `make check-packages` y `make parity`); paridad y memoria retenida: [`2026-11-03-parity.md`](../evidence/2026-11-03-parity.md); mutaciones completas sin caché, todas detectadas: [`2026-11-03-0045-mutations.md`](../evidence/2026-11-03-0045-mutations.md) (corrida repetida entera tras reajustar las anclas M215 y M243 y el texto de P-10).

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| H1 | alta | `{"gender":"male","_gender":{"value":"female"}}` pasaba: los hijos de `_x` se calculaban contra `ParentType(prim)`, y para un primitivo que deriva de otro (`code`, `id`, `markdown`: `string`; `url`, `canonical`, `oid`, `uuid`: `uri`; `positiveInt`, `unsignedInt`: `integer`) esa base también trae `value` | `extSpec` sube por las bases mientras `IsPrimitive` y resta los hijos del primer ancestro que no es primitivo (`Element`, o `PrimitiveType` en R5), sin nombrar `value`. La fixture ahora deriva `code`, `id`, `canonical` y `positiveInt` como los SD oficiales y `Extension.value[x]` admite `code`, `canonical` y `positiveInt`. Casos `value-inside-the-underscore-of-a-code`, `-of-an-extension-code`, `-of-an-id`, `-of-a-canonical`, `-of-a-positive-int` y su contraparte válida (`underscore-of-a-code-keeps-id-and-extension`), en las tres versiones y en los núcleos oficiales; cinco entradas a mano nuevas en la paridad. M260, M243 (reajustada) y P-18 |
| M1 | media | `elementPath`/`covered()` quitaba el `_` solo del último paso: el error de `Patient.active.value` no cubría la diferencia `Patient._active.value` | Quita el `_` de todos los pasos (ningún elemento de FHIR empieza con guion bajo). Entrada a mano `value-inside-underscore` (`_active:{"value":false}`), que antes salía `unclassified`. P-20 |
| M2 | media | Ningún oráculo protegía los U+2028 y U+2029 crudos (quitar la marca `dirt` del byte 0xE2 sobrevivía) | Caso `raw-line-and-paragraph-separators-are-escaped` (esperado ` ` y ` `, como `models.Marshal`, D2) y entrada a mano `raw-separators` en la paridad (`unchanged`). M261 y P-19 |
| B1 | baja | Las rutas de los errores del escáner eran claves crudas: sin tipo raíz, sin decodificar, vacías en la raíz | Misma forma que las del recorrido (regla 8): `rawPath` antepone el `resourceType` de la raíz si ya se leyó antes del error y, si no, `Resource` (`rootFallback`, en `fhirnames.go`); decodifica las claves; nunca queda vacía. `TestScannerErrorLocation` con un caso por forma de ruta. §3.1 regla 8 y el comentario de `Error.Path` lo documentan. M262, M263 y M264 |
| B2 | baja | Clave vacía: el error señalaba `Patient` a secas | Forma definida: un nombre que no es un identificador de FHIRPath (el vacío, uno con punto o espacio) va delimitado con acentos graves y con `` ` `` y `\` escapados, como los identificadores delimitados de FHIRPath: `Patient.` + dos acentos graves es la propiedad vacía. Casos `empty-property-name` y `unusual-property-name`. M265 y M268 |
| B3 | baja | Las propiedades desconocidas se ordenaban por la clave decodificada, pero ningún test lo protegía (la mutación «clave cruda» sobrevivía) | Caso `unknown-properties-are-sorted-by-the-decoded-name` (`az` y `ab`: por bytes crudos irían al revés). M266 |
| B4 | baja | Código muerto | Eliminados `walkSource` (con su ruta fija), `_, _ = cs, ce`, `_ = ks` y `objSpec.resource` |
| B5 | baja | `w.out` se reservaba con `len(data)` y se devolvía con esa capacidad | Medido con `TestNormalizeRetainsOnlyItsOutput` (en `parity/`, tabla «Memory retained by the output» del reporte de paridad): con una entrada con mucha sangría la capacidad era la de la entrada y en el Bundle más grande de R4 y R4B retenía la diferencia entre la entrada y la salida (cifras en la tabla). Ahora, si sobra más de un octavo, la salida se copia a su tamaño (`slices.Clone`): el costo es una copia transitoria de la salida, y lo que se guarda no retiene más de un octavo de espacio. El Bundle de R5 queda por debajo del octavo y conserva su capacidad. Test de la raíz `TestNormalizeOutputDoesNotHoldTheInputsSpace`. M267 |

Nueva en `scripts/mutations.py`: M260 a M268 y P-18 a P-20. **Próximo paso:** code review ronda 2.

### Code review, ronda 2

Revisor: Opus. Hallazgos corregidos el 03-11-2026 (Sonnet), con las entradas a mano comprobadas en rojo antes de corregir. Evidencia: [`2026-11-03-0156.md`](../evidence/2026-11-03-0156.md) (`make check`, `make check-packages` y paridad); paridad: [`2026-11-03-parity.md`](../evidence/2026-11-03-parity.md); mutaciones sin caché, todas detectadas: [`2026-11-03-0156-mutations.md`](../evidence/2026-11-03-0156-mutations.md) (corrida repetida entera tras reajustar M226, P-10 y P-13).

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| M-a | media | `diffTrees` armaba rutas sin delimitar y `elementPath` quitaba el `_` de todo paso; `e.Path` ya usaba los acentos graves (B2) y una `_foo` desconocida salía `Patient._foo`: `a.b`, la clave vacía, `1a` y `_foo:{"id":"x"}` quedaban `unclassified` | `diffTrees` arma dos rutas con la regla de la biblioteca (`pathName`, espejo documentado de `appendPathName`, que no se exporta): la tal cual y la sin `_` en cada paso; `covered()` acepta cualquiera. `TestPathNameAgreesWithTheLibrary` comprueba que las dos reglas coinciden. Cuatro entradas a mano (`property-name-with-a-dot`, `empty-property-name`, `property-name-starting-with-a-digit`, `unknown-underscore-property`), en rojo antes. P-20 (reajustada) y P-21 |
| B-a | baja | `rawPath` del escáner conservaba el `_` de `_x`; el recorrido lo quita | Decisión: el guion bajo inicial se quita siempre, incluso en una clave desconocida (`_foo` se informa como `foo`), porque el escáner no conoce el modelo. `appendPropertyPath` la aplica en el escáner y en el recorrido (propiedad desconocida); documentado en la regla 8 y en `Error.Path`. Casos de `TestScannerErrorLocation` y `unknown-underscore-property-is-reported-without-the-underscore`. M269, M271 y M272 |
| B-b | baja | `rootType` usaba el `resourceType` crudo sin pasar por la regla de nombres | Un nombre de tipo siempre es un identificador: si no lo es, la raíz es `Resource` (`rootFallback`). Casos con `a.b`, salto de línea y acento grave. M270 |

Nueva en `scripts/mutations.py`: M269 a M272 y P-21. **Próximo paso:** code review ronda 3.

### Code review, ronda 3

Revisor: Opus. **Convergió** (sin hallazgos medios ni altos); tres hallazgos bajos, corregidos el 03-11-2026 (Sonnet). Evidencia: [`2026-11-03-0321.md`](../evidence/2026-11-03-0321.md) (`make check`, `make check-packages` y paridad); mutaciones sin caché, todas detectadas: [`2026-11-03-0256-mutations.md`](../evidence/2026-11-03-0256-mutations.md).

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| L-1 | baja | En `covered()` la rama `d.Path` era código muerto (la biblioteca siempre quita el `_`), y la mutación `{d.Elem, d.Elem}` sobrevivía | `covered()` compara solo con `d.Elem` y lo explica en un comentario. P-22 |
| L-2 | baja | `TestPathNameAgreesWithTheLibrary` no cubría `\r`, `\f`, controles, una clave no ASCII ni U+2028: borrar esos casos de `appendPathName` sobrevivía | Claves `a\rb`, `a\fb`, `a\x1fb`, U+2028, U+00A0 y `é`. P-23, P-24 y P-25 (quitan el caso de `\r`, el de `\f` y el de `\u00XX`); el test se añadió a la selección de los oráculos `P-` |
| L-3 | baja | `appendPropertyPath` escribía `"_"` en vez de la constante | Usa `primitiveExtPrefix` (`fhirnames.go`) |

Documentación: la regla 8 (§3.1) registra las colisiones aceptadas (`_foo` y `foo` desconocidas informan `X.foo`; la clave `_` y la vacía informan la propiedad vacía), porque son claves inválidas.

Nueva en `scripts/mutations.py`: P-22 a P-25.

## 11. Cierre

Criterio de salida (§5) cumplido: paridad con `models` sin recursos sin clasificar, y lo que `models` borra es error con ruta ([paridad](../evidence/2026-11-03-parity.md)); idempotencia, metamórficos y fuzz en verde en las tres versiones y benchmark contra `models` (mismo reporte); mutaciones todas detectadas ([`2026-11-03-0256-mutations.md`](../evidence/2026-11-03-0256-mutations.md)); `make check` y `make check-packages` en verde ([`2026-11-03-0321.md`](../evidence/2026-11-03-0321.md)). **004 cerrado, sin commit** (a la espera del usuario). Siguiente: plan 005 (proyección `_summary`/`_elements`, D1).
