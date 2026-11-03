# 005 — Revisión previa: proyección `_summary` / `_elements`

**Estado:** revisión previa (paso 1 de la carta §10), 03-11-2026. Sin código de producción. Insumo del plan `docs/plans/005-proyeccion.md`.

Las sondas están fuera del repo, en `/Volumes/dev/tmp/probe005/` (`scripts/summ.py`, `summ2.py`, `summ3.py`, `nar.py`, `ex.py`; páginas de la especificación en `spec/`; fuentes de HAPI en `hapi/`; `repo/probe005_test.go`). Leen los packages de `.cache/packages` con los SHA-256 del repo. El plan debe portarlas a tests de Go (§5.4), para que las cifras sean código del repo.

## 1. Especificación (R4 4.0.1, R4B 4.3.0, R5 5.0.0)

Fuentes: `search.html` de las tres versiones (R4 §3.1.1.5.8 «Summary» y §3.1.1.5.9 «Elements»; R4B, las mismas secciones con el texto idéntico, verificado con `diff`; R5 §3.2.1.7.5 «Summary (_summary)» y §3.2.1.7.6 «Elements (_elements)»), `http.html` (read) y `ElementDefinition.isSummary` en el `StructureDefinition-ElementDefinition.json` de cada núcleo.

### 1.1 `_summary` (R4 = R4B)

> «The `_summary` parameter requests the server to return a subset of the resource. It can contain one of the following values:
> - **true** Return a limited subset of elements from the resource. This subset SHOULD consist solely of all supported elements that are marked as "summary" in the base definition of the resource(s) (see ElementDefinition.isSummary)
> - **text** Return only the "text" element, the 'id' element, the 'meta' element, and only top-level mandatory elements
> - **data** Remove the text element
> - **count** Search only: just return a count of the matching resources, without returning the actual matches
> - **false** Return all parts of the resource(s)»

> «Servers are not obliged to return just a summary as requested. […] Servers SHOULD mark the resources with the tag SUBSETTED to ensure that the incomplete resource is not accidentally used to overwrite a complete resource.»
> «Note that the `_include` and `_revinclude` parameters cannot be mixed with `_summary=text`.»
> «Implementation Note: There is some question about the inclusion of extensions in the summary. Additional rules may be made around this in the future.»

**R5 cambia `text`:** «Return only the text, id, meta, and top-level mandatory elements (these mandatory elements are included to ensure that the payload is valid FHIR; servers MAY omit elements within these sub-trees as long as they ensure that the payload is valid). Servers MAY return extensions, but clients SHOULD NOT rely on extensions being present and SHOULD use another search mode if data contained in extensions is required. TU». También suma: «The self link in the search result Bundle is important for the interpretation of the result and should always be returned, regardless of the type of `_summary`.» `true`, `data`, `count` y `false` no cambian.

`ElementDefinition.isSummary`, en el package (igual en R4 y R4B): definición «Whether the element should be included if a client requests a search with the parameter `_summary=true`»; `meaningWhenMissing` «An element is not included in the summary unless it is explicitly specified to be so». El comentario dice:

> R4/R4B: «When a request is made with `_summary=true`, serailisers only include elements marked as 'isSummary = true'. Other than Attachment.data, all data type properties are included in the summary form. In resource and data type definitions, if an element is at the root or has a parent that is 'mustSupport' and the minimum cardinality is 1 or the element is a modifier, it must be marked as isSummary=true.»
> R5: «[…] With a few exceptions (listed below), all datatype properties are included in the summary form. In resource and datatype definitions, if an element has a descendant marked as isSummary=true or if it satisfies both of the following conditions, it must be marked as isSummary=true: is at the root or has a parent that is 'mustSupport'; has a minimum cardinality of 1 or is a modifier element. The following datatype properties are exceptions, and are not marked as isSummary: Attachment.data; Signature.data, Signature.targetFormat, Signature.sigFormat; Narrative.status, Narrative.div; SampledData.data.»

Los datos contradicen los dos comentarios (§2): hay elementos raíz obligatorios sin `isSummary`, y en R5 hay elementos de tipos de datos fuera de la lista de excepciones que tampoco lo tienen.

### 1.2 `_elements`

R4 = R4B:

> «The `_elements` parameter consists of a comma-separated list of base element names such as, elements defined at the root level in the resource. Only elements that are listed are to be returned. Clients SHOULD list all mandatory and modifier elements in a resource as part of the list of elements. The list of elements does not apply to included resources. Servers are not obliged to return just the requested elements. Servers SHOULD always return mandatory elements whether they are requested or not. Servers SHOULD mark the resources with the tag SUBSETTED […]»

R5 cambia y suma:

> «Each element name SHALL be the base element name without specifying [x] or one of the types (e.g., "value" is valid, while "value[x]" and "valueQuantity" are not) TU.» «Servers have the right to return more data than request, however clients can generally expect that returned elements will be: elements explicity requested, mandatory elements, or modifier elements that have values.» «Servers SHOULD always return mandatory **and modifier** elements whether they are requested or not.» «If a client supplies `_elements` values prefixed with resource types, such as `_elements=Patient.gender`, then servers SHOULD apply these element-level restrictions to all resources in the returned bundle, rather than just to direct search matches (i.e., restrictions should be applied to bundle entries with any search.mode).» La frase de R4 «does not apply to included resources» desaparece.

Consecuencias:

- **Solo nivel raíz** en las tres versiones: la especificación no define rutas anidadas (`Patient.name.family`). HAPI las acepta (§4); es una extensión.
- **`resourceType`, `id`, `meta`:** `_elements` no las menciona. `resourceType` no es un elemento: es la sintaxis JSON (json.html §2.6.2) y va siempre. `id` y `meta` no son obligatorios (`min=0`, §2), así que la especificación no exige mantenerlos. Para poner SUBSETTED hace falta `meta`.
- **Obligatorios:** SHOULD en las tres versiones. **Modificadores:** SHOULD solo en R5.
- **`_summary` junto con `_elements`:** la especificación no dice nada. HAPI responde 400 (§4).

### 1.3 SUBSETTED

- El sistema es el mismo en las tres versiones: `http://terminology.hl7.org/CodeSystem/v3-ObservationValue`, código `SUBSETTED` (en R4 el enlace va a `v3/SecurityIntegrityObservationValue/cs.html#SUBSETTED`; en R5, a `terminology.hl7.org/CodeSystem-v3-ObservationValue.html#…-SUBSETTED`). El concepto cuelga de `_SecurityObservationValue/_SECINTOBV/_SECALTINTOBV`, con display «subsetted».
- **Medido: está en los datos.** El `CodeSystem-v3-ObservationValue.json` viene en `hl7.fhir.r4.core` (versión 2018-08-12) y en THO (`hl7.terminology.r4` 7.1.0 → CS 5.0.0; `hl7.terminology.r5` 7.0.1 → CS 4.0.0; `hl7.terminology.r4b` 6.0.2). R4B y R5 core no lo traen. `Context.Resolve("CodeSystem", url)` lo resuelve en los tres locks (`repo/probe005_test.go`, 30 s, 337 MB de RSS por los tres contextos): r4 y r4b dan `hl7.terminology.r4#7.1.0` y r5 da `hl7.terminology.r5#7.0.1`, y todos contienen `SUBSETTED`. Se puede verificar la URL y el código contra el Set y tomar el display del CodeSystem en tiempo de ejecución. La URL y el código siguen siendo literales: la prosa los nombra, ningún dato los selecciona.
- Ningún ejemplo oficial lleva el tag (0 de 5309 en R4, 0 de 2840 en R4B, 0 de 2822 en R5; `ex.py`).
- `http.html` (read, R4 y R5): «Servers SHOULD define a Resource.meta.tag with the SUBSETTED as a Simple Tag to explicitly mark such resources.» R4 agrega: «Note that the impact of `_elements` is not defined for interactions other than search where the response is a bundle that contains more than one type of resource.» R5 deja a criterio del servidor aceptar un create o un update que traiga SUBSETTED.

### 1.4 Bundle y `contained`

- La proyección se aplica **a cada recurso**. En una búsqueda, a cada entrada (R4: no a las incluidas; R5: a todas si el nombre lleva el tipo como prefijo), no al Bundle de la respuesta.
- `DomainResource.contained` es `isSummary=false` en las tres versiones, así que `_summary=true` y `text` lo quitan. Con `data`, la especificación dice «Remove the text element» y no aclara si también se quita el `text` de los contenidos ni el de `Composition.section.text`.
- Elementos de tipo `Resource` (`nar.py`): `Bundle.entry.resource`, `Bundle.entry.response.outcome`, `Parameters.parameter.resource` (los tres con `isSummary=true`), `DomainResource.contained` (false) y, solo en R5, `Bundle.issues` (true). Elementos de tipo `Narrative`: `DomainResource.text` y `Composition.section.text` en R4; R4B y R5 suman `EvidenceReport.section.text`. Todos tienen `isSummary=false`.

## 2. `isSummary` en los packages (medido)

Se miden los SD de especialización del núcleo (sin `logical` ni `constraint`), sobre los elementos del snapshot que no son la raíz (`summ.py`, `summ2.py`, `summ3.py`):

| | R4 | R4B | R5 |
| --- | --- | --- | --- |
| SD | 209 | 206 | 231 |
| elementos del snapshot (sin la raíz) | 7466 | 7579 | 9324 |
| con `isSummary=true` | 3266 | 3211 | 4056 |
| `isSummary` ausente | 0 | 0 | 0 |
| propios (base en el mismo tipo) con `min>0` y sin `isSummary` | 296 | 318 | 362 |
| modificadores propios sin `isSummary` (sin `modifierExtension`) | 6 | 6 | 5 |
| hijo `isSummary` bajo un padre sin `isSummary` | 68 | 76 | 103 |
| recursos concretos | 146 | 141 | 158 |
| raíz obligatoria sin `isSummary` | 4 | 5 | 4 |
| raíz modificadora sin `isSummary` | 0 | 0 | 0 |
| anidado obligatorio o modificador sin `isSummary` con todos sus ancestros en el resumen | 0 | 0 | 0 |

- **Modificadores sin `isSummary`** (se confirman las cifras del plan 004, 6/6/5): en R4 y R4B, `ConceptMap.group.element.target.equivalence`, `Contract.term.action.doNotPerform`, `Questionnaire.item.enableWhen`, `List.entry.deleted`, `CarePlan.activity.detail.status` y `CarePlan.activity.detail.doNotPerform`. En R5, `List.entry.deleted`, `Contract.term.action.doNotPerform`, `ConceptMap.group.element.target.relationship`, `ConceptMap.group.unmapped.relationship` y `Questionnaire.item.enableWhen`. Ninguno está en la raíz. `DomainResource.modifierExtension` es false en R4 y R4B y true en R5. `BackboneElement.modifierExtension` es true en las tres.
- **Raíz obligatoria sin `isSummary`:** en R4, `Appointment.participant`, `Provenance.agent`, `AuditEvent.agent` y `AuditEvent.source`. En R4B, esos cuatro más `Evidence.variableDefinition`. En R5, `Appointment.participant`, `Evidence.variableDefinition`, `NutritionIntake.consumedItem` y `BodyStructure.includedStructure`. Esto va contra el comentario de `isSummary`, y en esos recursos `_summary=true` produce un recurso inválido.
- **Anidado:** si `isSummary` se aplica en cada nivel dentro del SD del recurso (el recurso y sus backbone), nunca se cae un hijo obligatorio o modificador de un padre que se conserva (0/0/0). Los 68/76/103 hijos `true` bajo padres `false` desaparecen con el padre. En R5 esto contradice la regla «if an element has a descendant marked as isSummary=true… must be marked».
- **Elementos base:** `Resource.id`, `Resource.meta` y `Resource.implicitRules` (modificador) son true. **`Resource.language` es false** en las tres. `DomainResource.text`, `contained` y `extension` son false. `Element.id` y `Element.extension` son false en los tres SD y en cada copia heredada (514/539/677 en R4/R4B/R5).
- **Tipos de datos:** el `isSummary` de un hijo está en el **SD del tipo**, no en el elemento que lo usa. El snapshot de `Patient` no tiene `Patient.name.family`: `Patient.name` lleva true y `HumanName.family`, `given`, etc. llevan su propio true en `StructureDefinition-HumanName`. Los elementos propios de tipos complejos sin `isSummary` son 11 en R4 y R4B (`Element.id`, `Element.extension`, `Extension.url`, `Extension.value[x]`, `Narrative.status`, `Narrative.div`, `Attachment.data`, `Signature.targetFormat`, `Signature.sigFormat`, `Signature.data` y `SampledData.data`) y 18 en R5 (además `Attachment.height`, `width`, `frames`, `duration` y `pages`, y `SampledData.codeMap` y `offsets`, que no están en la lista de excepciones del comentario). Los `value` de los primitivos son false (0 propios true).
- **Choques entre los datos y el comentario de `isSummary`:** (a) `Extension.url` tiene `min=1` y es false: si `isSummary` se aplica también dentro de los tipos de datos, una `modifierExtension` conservada (true en todo backbone) pierde su `url` y su `value[x]` y queda inválida. (b) El comentario dice que todas las propiedades de los tipos de datos entran en el resumen, salvo Attachment.data en R4, salvo la lista en R5. Los datos marcan además `Element.id`, `Element.extension` y `Extension.*`, y en R5 siete más.
- **Modo `text`, por recurso concreto:** se conservan `text`, `id`, `meta` y los elementos raíz con `min>0`. Elementos raíz obligatorios por recurso (cantidad de recursos según cuántos tiene): R4 {0: 33, 1: 31, 2: 33, 3: 21, 4: 11, 5: 9, 6: 2, 7: 4, 8: 1, 9: 1}; R4B {0: 25, 1: 33, 2: 35, 3: 21, 4: 11, 5: 8, 6: 2, 7: 4, 8: 1, 9: 1}; R5 {0: 31, 1: 34, 2: 42, 3: 27, 4: 8, 5: 9, 6: 4, 7: 3}. Un elemento raíz obligatorio que es un `[x]` se conserva por la variante que esté presente en la instancia.
- **Ejemplos oficiales** (`ex.py`): `text` en la raíz en 3468/5309 (R4), 2337/2840 (R4B) y 2527/2822 (R5); `contained` en 136, 144 y 309; propiedades `_x` en la raíz en 37, 6 y 12; `modifierExtension` en la raíz en 1, 1 y 1. Alcanzan para probar los modos `text` y `data`, `contained` y `_x`.

## 3. `gofhir/server` (congelado): `internal/search/summary/processor.go`

Se leyó `processor.go` y lo que llama: `fhircontext/registry.go` (`SummaryElements`, `MandatoryElements`, `HasElement`), `transport/http/handlers_read.go` (`applyReadSummary`), `search/parser.go` (`parseSummary`, `parseElements`), `service/search.go` (`applySummaryFiltering`) y `service/bundle.go`.

| Pieza | Qué hace | Veredicto |
| --- | --- | --- |
| Lo que se conserva sale del SD (`SummaryElements`: `isSummary`; `MandatoryElements`: `min>=1`) | lee el snapshot, solo elementos raíz | **Tomar** la idea (los datos deciden). **Mejorar:** que salga del `Model` y no de recorrer el snapshot en cada llamada |
| Solo nivel raíz | `SummaryElements` y `MandatoryElements` descartan las rutas con punto; `filterFields` filtra solo las claves de primer nivel | **Mejorar:** `_summary=true` debe filtrar también los backbone (el anidado no rompe la validez, 0/0/0, §2). Las rutas anidadas de `_elements` quedan como decisión (D3) |
| **`[x]` perdido (hallazgo nuevo)** | `SummaryElements` devuelve `value[x]` (el último segmento del path) y `filterFields` compara con la clave JSON `valueQuantity`: no coinciden nunca, así que `_summary=true` borra todo `[x]` del resumen (por ejemplo `Observation.value[x]`, `effective[x]`). `HasElement("Observation","value")` busca `Observation.value`, que no existe, así que `_elements=value` se ignora sin aviso; `_elements=valueQuantity` también se ignora | **Descartar.** La clave JSON se resuelve a su elemento con la tabla de variantes del normalizador (`objSpec.keys`) |
| `_x` perdido | `allowed["birthDate"]` no cubre `_birthDate`: se borran el id y las extensiones de los primitivos conservados | **Descartar.** Con `keyRef.under`, `_x` sigue a `x` |
| Orden alfabético | `map[string]any` + `json.Marshal` ordena las claves | **Descartar:** la salida conserva el orden del normalizador |
| `_summary=data` por `Apply` | `getAllowedFields` devuelve nil y `filterFields` copia todo sin quitar `text`. **Está latente:** los tres llamadores (`handlers_read.go:220`, `service/search.go:802` y `service/bundle.go:648/675`) desvían `data` a `ApplyDataMode`, que sí borra `text`. Con `data`, esos llamadores ignoran `_elements` | **Descartar** el doble camino: una sola función con el modo |
| `resourceType`, `id` y `meta` siempre, como literales | `allowed["resourceType"/"id"/"meta"]` en todos los modos | **Mejorar:** en `true` salen de `isSummary` (`Resource.id` y `meta` son true). En `text` y `_elements` los nombres van a `fhirnames.go` con su ADR (§5.3) |
| `_elements` suma los obligatorios | cumple el SHOULD de R4 | **Tomar.** **Mejorar:** sumar los modificadores (SHOULD de R5; en R4 y R4B, decisión D4) |
| `_elements` gana sobre `_summary` | si hay `_elements`, `getAllowedFields` vuelve antes de mirar el modo | **Descartar** el comportamiento implícito: es una decisión explícita (D5) |
| SUBSETTED | sistema y código como constantes y display «subsetted» literal; se agrega siempre, aunque no se haya quitado nada; crea `meta` si no existe; no duplica | **Mejorar:** el display sale del CodeSystem del Set y la URL y el código se verifican contra él (§1.3). Si se marca siempre o solo cuando algo se quitó es la decisión D6. **Tomar:** no duplicar |
| Decimales | `UseNumber` conserva el texto del número | ya resuelto en el escáner del 004 (copia el texto) |
| `count` | se resuelve en el servicio; `Apply` no lo atiende | **Tomar:** `count` no es una proyección de un recurso, es del núcleo |
| `parseSummary` en minúsculas | acepta `TRUE` | **Descartar:** los valores son códigos y distinguen mayúsculas. El parseo de la URL es del núcleo |
| `_elements` con un prefijo de cualquier tipo | quita el calificador y lo aplica | la regla de R5 es del núcleo, que arma la lista por tipo. La librería recibe nombres por tipo de recurso |
| `contained` y las entradas de un Bundle | sin recursión | ver D2 |

## 4. Implementaciones de referencia

### 4.1 HAPI FHIR (`hapi-fhir` master, leído del código, sin ejecutar)

- `RestfulServerUtils.configureResponseParser`: combinar `_summary` y `_elements` da **400** (`Msg.code(304)`: «Cannot combine the _summary and _elements parameters»). `text` → `setEncodeElements({"*.text","*.id","*.meta","*.(mandatory)"})` con `setEncodeElementsAppliesToChildResourcesOnly(true)`: no toca el Bundle. `data` → `setSuppressNarratives(true)`. `true` → `setSummaryMode(true)`. `count` → solo `Bundle.total` y `Bundle.type`. `_elements` → cada nombre en minúscula se prefija con el tipo de la petición (o `*.`); en una búsqueda o un historial no se aplica al Bundle salvo que se nombre `Bundle.`.
- `BaseParser.CompositeChildElement.shouldBeEncoded`: en modo resumen se omite todo hijo con `!getDef().isSummary()`, **en cada nivel, también dentro de los tipos de datos**. El `isSummary` sale de las anotaciones `@Child(summary=…)` de las estructuras generadas. Excepción escrita a mano: las `extension` de `CapabilityStatement` se conservan. El `Narrative` (`RuntimeChildNarrativeDefinition`) se omite con `data` o `true` en cualquier nivel y en todo recurso anidado, también `Composition.section.text`. Las extensiones se filtran con el `isSummary` del hijo `extension` (`JsonParser.isEncodeExtension`); la `url` de una extensión conservada se escribe siempre, porque no es un hijo filtrable.
- **Rutas anidadas de `_elements`:** `EncodeContextPath.startsWith(…, true)` compara simétrico, así que `Patient.name.family` conserva `name` (el ancestro) con solo `family`, y `Patient.name` conserva todo lo que hay debajo. Acepta comodines `*.`. El marcador `(mandatory)` (`min>0`) solo lo usa el modo `text`: **con `_elements`, HAPI no agrega los obligatorios ni los modificadores.** `meta` se fuerza para poder poner el tag.
- **SUBSETTED:** `Constants.TAG_SUBSETTED_SYSTEM_R4 = "http://terminology.hl7.org/CodeSystem/v3-ObservationValue"` (DSTU3: `http://hl7.org/fhir/v3/ObservationValue`), código `SUBSETTED`, display **«Resource encoded in summary mode»** (no el del CodeSystem). Se agrega en `true`, `data`, `text` y `_elements`.
- **Tomar:** `isSummary` en cada nivel del SD del recurso; un solo paso de filtrado sobre la serialización. **Mejorar:** el `isSummary` sale de los packages y no de clases generadas; sin excepciones escritas por nombre de recurso (la de `CapabilityStatement` es justo lo que la regla 5 prohíbe). **Descartar:** el display propio del tag.

### 4.2 Oráculo ejecutable local

- **`validator_cli` 7.0.2** (`go-searchindex/.cache/hl7/validator_cli.jar`, SHA-256 `e3e50f06…` = `scripts/validator.sha256`): no tiene ninguna tarea de resumen. Trae `hapi-fhir-base` 8.10.0 (`ca/uhn/fhir/parser/BaseParser`, `FhirContext`, `SummaryEnum`) pero **no las estructuras de HAPI** (`org.hl7.fhir.r4.hapi.ctx.FhirR4` no está). Medido con `java/Probe.java` (1 JVM): `FhirContext.forR4()` y `forR5()` fallan con «HAPI-1680: Could not find the HAPI-FHIR structure JAR on the classpath for version R4».
- **Para tener un oráculo ejecutable** hacen falta `hapi-fhir-structures-r4`, `-r4b` y `-r5` (Maven Central, fijados con SHA-256) y un arnés Java chico: `FhirContext.forR4().newJsonParser().setSummaryMode(true)` / `setSuppressNarratives(true)` / `setEncodeElements(...)` sobre los ejemplos oficiales, en a lo sumo 2 JVM. Es una dependencia de test nueva y la decide el usuario (D7). HAPI tiene diferencias conocidas (sin obligatorios en `_elements`, la excepción de `CapabilityStatement`, el display del tag, narrativas anidadas), así que la paridad tendría clases de diferencia, igual que con `models` en el 004.
- La especificación sirve de oráculo independiente: un test que recorre el snapshot (sin usar el código de la proyección) calcula qué rutas deben quedar y lo compara con la salida en todos los ejemplos oficiales y los generados.

## 5. Propuesta de API

### 5.1 Forma

- **Una función aparte, no una opción de `Normalize`:** `func (c *Context) Project(data []byte, p Projection, opts ...NormalizeOption) ([]byte, error)`. `Normalize` se aplica al escribir y `Project` al leer, sobre los bytes ya normalizados. Mezclarlas complica el contrato de idempotencia del 004.
- **Una sola pasada, sobre el escáner y las `objSpec` del 004:** la proyección es un predicado `keep(field)` dentro de `walker.field`. Así hereda el orden, la resolución de la variante de `[x]` (`objSpec.keys`), el `_x` junto a `x` (`keyRef.under`), los recursos anidados por `resourceType` y los errores con ruta. Propiedades que se pueden probar: `Project(x) == Project(Normalize(x))`; `Normalize(Project(x)) == Project(x)`; `Project(Project(x)) == Project(x)` (el tag no se duplica); `Project(x, false) == Normalize(x)`; la salida es subconjunto de `Normalize(x)` salvo el tag.
- `Projection`: `{Summary SummaryMode; Elements []string}`, con `SummaryMode` en `{SummaryFalse, SummaryTrue, SummaryText, SummaryData}`. `count` no existe en la librería (es del núcleo). Un constructor validado, `c.NewProjection(resourceType string, summary SummaryMode, elements []string) (Projection, error)`, rechaza con `*Error` y su ruta los nombres que el tipo no tiene, `value[x]` y `valueQuantity` en R5 (D3) y la combinación con `_summary` si D5 es error. El núcleo parsea la URL (`_summary=TRUE`, comas, prefijos de tipo de R5) y arma una `Projection` por tipo.

### 5.2 Lo que falta en `Model`

- `IsSummary(path string) bool` e `IsModifier(path string) bool`, leídos de `ElementDefinition.isSummary` e `isModifier` del snapshot ganador. Deben responder por el camino sin sufijo y por cada variante, igual que `Cardinality`, y por los caminos de los tipos de datos (`HumanName.family`). Hay que sumar `isSummary` e `isModifier` a `elementDoc` y a `jsonFieldSources` (fuente: elementdefinition.html).
- `min` ya está en `Cardinality`.
- Opcional: cachear en `normState` los conjuntos de cada modo por alcance (`objSpec` → máscara de campos), para no evaluar el predicado por clave.

### 5.3 Nombres de FHIR y regla 5

| Necesidad | Dato que lo resuelve | Literal que queda (va a `fhirnames.go` con ADR) |
| --- | --- | --- |
| qué entra en `true` | `IsSummary` en cada nivel | ninguno |
| obligatorios (`text`, `_elements`) | `Cardinality` (`min>0`) | ninguno |
| modificadores (`_elements`, R5) | `IsModifier` | ninguno |
| variante de `[x]`, `_x` | `ChoiceTypes`, `objSpec.keys` | ninguno |
| recursos anidados | `slotTypes` del 004 | ninguno |
| `text`, `id` y `meta` del modo `text`; `text` del modo `data` | la prosa de search.html; ningún dato los marca | `text`, `id`, `meta`. ADR-005 con search.html R4 §3.1.1.5.8 y R5 §3.2.1.7.5; el test comprueba que en las tres versiones `text` es de tipo `Narrative` (base `DomainResource.text`) y que `id` y `meta` vienen de `Resource` |
| SUBSETTED | `Context.Resolve("CodeSystem", url)` da el display y confirma que el concepto existe | URL `http://terminology.hl7.org/CodeSystem/v3-ObservationValue` y código `SUBSETTED` (search.html §1.3). El test falla si el Set no tiene el concepto. Hace falta decidir qué hace `Project` si el lock no trae THO: hoy los tres locks lo traen (D6) |
| `meta.tag` (dónde va el tag) | — | `tag` (resource.html: `Meta.tag`), y `system`/`code`/`display` (datatypes.html: `Coding`) en `jsonFieldSources` |

Alternativa sin el literal `text`: «los hijos de tipo `Narrative` en la raíz». Cambia un nombre de elemento por un nombre de tipo y no gana nada, así que se propone el literal con su ADR.

### 5.4 Tests (para el plan)

Portar las sondas a Go (`TestSummaryCounts` con las cifras de §2 por versión); el oráculo de la especificación de §4.2 sobre los ejemplos oficiales R4, R4B y R5 más los generados; los metamórficos de §5.1; fuzz sobre `Project` con el corpus del 004; casos de borde: `[x]` en el resumen, `_x` de primitivos conservados y quitados, `contained`, `Bundle.entry.resource`, `Parameters.parameter.resource`, `Composition.section.text` con `data`, `modifierExtension` en un backbone, recursos con raíz obligatoria sin `isSummary` (`Appointment`, `AuditEvent`…), recursos sin `meta`, y tag ya presente.

## 6. Decisiones para el usuario

- **D1 — `isSummary` dentro de los tipos de datos.** (a) aplicarlo en cada nivel, también en los tipos de datos (HAPI); (b) aplicarlo en el SD del recurso (raíz y backbone) y copiar enteros los valores de tipos de datos que se conservan. Con (a) se quitan `Attachment.data`, `SampledData.data` y las extensiones de los tipos de datos, pero una `modifierExtension` conservada pierde `url` y `value[x]` y queda inválida. Con (b) nunca sale un recurso inválido por esta causa, pero entra `Attachment.data`, que la especificación excluye. **Recomendación:** (a), con una sola regla derivada de los datos y sin nombres: un elemento de tipo `Extension` que se conserva se copia entero (se decide por el tipo del elemento y por el `min>0` de `Extension.url`; `Extension` va a `fhirnames.go` con ADR). El plan lo verifica con el oráculo de validez.
- **D2 — Recursos anidados.** `contained` cae solo en `true` y `text`. Para `Bundle.entry.resource` y `Parameters.parameter.resource` dentro de un recurso proyectado: (a) aplicar el mismo modo de forma recursiva (HAPI); (b) copiarlos enteros. **Recomendación:** (a) para `true` y `data` (`data` quita el `text` de cada recurso anidado, también en `contained`) y (b) para `text` y `_elements`, cuyos nombres son del tipo de la raíz. `Composition.section.text` se conserva con `data`, porque la especificación dice «the text element» del recurso.
- **D3 — Alcance de `_elements`.** (a) solo nivel raíz, como la especificación; un nombre con punto o con variante es un error con ruta; (b) rutas anidadas, como HAPI. **Recomendación:** (a) en el 005. (b) queda como incremento posterior si el núcleo lo pide. En R4 y R4B también se rechazan `value[x]` y `valueQuantity`, porque «base element names» ya los excluye.
- **D4 — Qué suma `_elements` siempre.** **Recomendación:** `resourceType`, más `id` y `meta` cuando están (`meta` se necesita para el tag, `id` para identificar la entrada), más los obligatorios (SHOULD en las tres versiones) y los modificadores con valor en las tres versiones (R5 lo pide, y es más seguro que R4 dejar fuera un `doNotPerform`). Diverge de HAPI, que no suma nada.
- **D5 — `_summary` y `_elements` juntos.** (a) error, como HAPI; (b) `_elements` gana, como `server`; (c) la intersección. **Recomendación:** (a), `NewProjection` devuelve error. `_summary=false` con `_elements` vale como `_elements`.
- **D6 — Tag SUBSETTED.** **Recomendación:** marcar solo si la proyección quitó algo (quitar nada no es un subconjunto; así `Project(x, false) == Normalize(x)`), sin duplicar, con el display del CodeSystem del Set. Si el lock no trae el CodeSystem, error en `NewContext` o en `NewProjection`, nunca un tag sin verificar. Que `data` sobre un recurso sin `text` no marque es consecuencia de la misma regla.
- **D7 — Oráculo HAPI ejecutable.** Agregar como dependencia de test `hapi-fhir-structures-r4`, `-r4b` y `-r5` (8.10.0, igual que el `hapi-fhir-base` del jar, con SHA-256 fijado) y un arnés Java con a lo sumo 2 JVM, para la paridad con las clases de diferencia de §4.1. **Recomendación:** sí, como verificación no bloqueante (igual que `make hl7`). El oráculo bloqueante es el de la especificación (§4.2).
- **D8 — Recursos con raíz obligatoria sin `isSummary`** (4, 5 y 4 casos). (a) seguir los datos: `_summary=true` los quita y la salida es inválida, igual que en HAPI; (b) sumar siempre los `min>0` de la raíz en `true`. **Recomendación:** (b). R5 dice que los obligatorios se incluyen «to ensure that the payload is valid» (en `text`), y el comentario de `isSummary` exige que estén en el resumen. Se deriva de `Cardinality`, sin nombres, y se registra como desviación deliberada de los datos. La lista se puede reportar a HL7 (Jira de la especificación) como un defecto de los datos, con evidencia.
