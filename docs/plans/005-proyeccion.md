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
