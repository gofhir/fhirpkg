# ADR-005 — Proyección `_summary` / `_elements` y los nombres de FHIR que la sostienen

**Estado:** Aceptada el 05-11-2026: D1 a D10 del [plan 005](../plans/005-proyeccion.md) aprobados por el usuario el 03-11-2026; code review terminado en el tope de 3 rondas con su último hallazgo corregido por decisión del dueño (plan 005 §17); mutaciones 395 de 395, paridad con el oráculo independiente sin desacuerdos y `make check-packages` en verde ([evidencia](../evidence/2026-11-05-1431.md), [mutaciones](../evidence/2026-11-05-1238-mutations.md)). **Plan:** [005](../plans/005-proyeccion.md). **Evidencia:** la que enlaza el registro de implementación del plan (§9).

## Contexto

`Context.NewProjection` y `Context.Project` aplican `_summary` (`true`, `text`, `data`, `false`) y `_elements` a un recurso FHIR en JSON, en una sola pasada con el normalizador del plan 004. Lo que se conserva sale de los datos (`ElementDefinition.isSummary`, `isModifier`, `min`); la regla 5 pide que ningún nombre de FHIR esté escrito fuera de `fhirnames.go`, y este ADR registra los pocos que ningún dato marca, con su fuente y el test que los comprueba.

## Nombres escritos (excepciones de la regla 5)

Fuente: search.html, R4 y R4B §3.1.1.5.8 y §3.1.1.5.9; R5 §3.2.1.7.5 y §3.2.1.7.6. Los tests están en `projection_names_test.go` y corren en las tres versiones con `make check-packages`.

| Literal | Por qué se escribe | Test |
| --- | --- | --- |
| `text` | `_summary=text` conserva «the text element» y `_summary=data` lo quita: la prosa de HL7, ningún dato lo marca | `TestRealProjectionNamesTextIdMeta`: en toda raíz de recurso, `text` es de tipo `Narrative` con base `DomainResource.text`; ninguna otra raíz define uno |
| `id`, `meta` | `_summary=text` conserva «the 'id' element, the 'meta' element». `_elements` los suma **por decisión D4, no por texto de HL7**: `meta` hace falta para el tag SUBSETTED; `id`, para identificar el recurso | el mismo test: `id` y `meta` vienen de `Resource.id` y `Resource.meta` |
| `contained` | D9: un contenido referido por una referencia local conservada se conserva (ref-1, dom-3) | el mismo test: base `DomainResource.contained` |
| `tag`, `system`, `code`, `display` | dónde va el tag: `Meta.tag` es un arreglo de `Coding` (resource.html, datatypes.html) | `TestRealSubsettedConceptExists` |
| `http://terminology.hl7.org/CodeSystem/v3-ObservationValue`, `SUBSETTED` | search.html: «Servers SHOULD mark the resources with the tag SUBSETTED» | el concepto existe, con display, en el CodeSystem de los tres locks |
| `Extension` | D1: una `Extension` conservada se copia entera, porque `Extension.url` es obligatorio y no está en el resumen | `TestRealExtensionTypeNames`: es el tipo de `Element.extension` y de `modifierExtension` |
| `Reference`, `reference`, `#` | D9: la referencia local es `#id` (references.html, «contained resources») | `TestRealLocalReferenceNames`: ref-1 usa `'#'` y `%rootResource.contained.id` |
| `CodeSystem` | tipo de recurso donde se verifica el concepto SUBSETTED | `TestRealSubsettedConceptExists` |

## Desviaciones deliberadas de los datos

- **D8 — obligatorios en `true`.** En cada nivel conservado se suman los hijos con `min>0` aunque su `isSummary` sea falso. Los datos tienen 4, 5 y 4 elementos obligatorios de la raíz sin `isSummary` (R4, R4B, R5): en R4 `Appointment.participant`, `Provenance.agent`, `AuditEvent.agent` y `AuditEvent.source`; R4B suma `Evidence.variableDefinition`; R5 tiene `Appointment.participant`, `Evidence.variableDefinition`, `NutritionIntake.consumedItem` y `BodyStructure.includedStructure`. Sin esta regla, `_summary=true` produce un recurso inválido. Se puede reportar a HL7 como defecto de los datos si el usuario lo pide. **Ampliación (aprobada por el usuario el 04-11-2026):** un primitivo `min>0` está presente por su valor o por su `_x` (json.html, «Representation of primitive elements»); el par `x`/`_x` se conserva entero en todo modo que suma obligatorios y en todo nivel conservado. Los `_x` de primitivos no obligatorios se siguen filtrando. **Aclaración del code review, ronda 1 (04-11-2026):** un obligatorio que el filtro dejaría vacío (p. ej. `Provenance.agent.who` solo con una extensión `data-absent-reason`) se conserva entero, en cada nivel conservado. **D9:** la referencia local es `Reference.reference` y también todo primitivo `canonical` con valor `#id` (references.html). **Aclaración por ocurrencia (code review, ronda 2, decidida por el coordinador):** el respaldo «un obligatorio que el filtro dejaría vacío se conserva entero» se aplica a cada ocurrencia, no al elemento: en `Provenance.target=[{extension:DAR},{reference:"Patient/p"}]` la primera ocurrencia sale entera aunque la segunda sobreviva al filtro (R4, R4B y R5).
- **D10 — modificadores con valor, en todos los modos.** En R4 y R4B `DomainResource.modifierExtension` tiene `isSummary=false`: `true` y `text` borrarían en silencio un modificador que cambia el significado del recurso; con `text` también caería `implicitRules` (modificador de `Resource`). Se conservan los elementos con `isModifier` y valor. R5 pide esto para `_elements` (SHOULD); aquí se extiende a todos los modos.
- **D1 — `isSummary` en cada nivel, `Extension` entera.** El `isSummary` de un hijo de un tipo de datos está en el SD del tipo (`HumanName.family`). Un elemento de tipo `Extension` que se conserva se copia entero: si no, una `modifierExtension` conservada perdería `url` y `value[x]` (ext-1). Alternativa descartada: copiar enteros los tipos de datos (conservaría `Attachment.data`, que la especificación excluye).
- **D9 — `contained` referido.** Se conserva el contenido al que apunta una referencia local conservada (también por otro contenido conservado), proyectado con el mismo modo en `true` y `data` y entero en `text` y `_elements` (regla 8); los demás caen si el modo quita `contained`.
- **Decisión de interpretación.** El texto de HL7 para `text` dice «only top-level mandatory elements»: se entiende como los elementos de la raíz con `min>0`. R5 agrega «to ensure that the payload is valid».

## Consecuencias

- La proyección no sabe nada de la URL: el núcleo parsea `_summary` y `_elements` y arma una `Projection` por tipo; `_summary=count` es del núcleo.
- Los resultados no son aptos para sobrescribir el recurso completo: de ahí el tag SUBSETTED en cada recurso que perdió algo (D6).
- Diverge de HAPI en lo que el plan 005 §1 lista (obligatorios y modificadores en `_elements`, `_elements` con `_summary` es error, display del tag del CodeSystem); `make hapi-projection` clasifica cada diferencia.
