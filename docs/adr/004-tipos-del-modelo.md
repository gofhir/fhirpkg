# ADR-004 — Qué es un tipo del modelo de FHIRPath

**Estado:** Aceptada el 03-11-2026: code review del plan 003 convergido y CI en verde ([corrida 41611913463](https://github.com/gofhir/fhirpkg/actions/runs/41611913463)). D1 a D3 decididos (D2 por el usuario el 02-11-2026, con la especificación). **Plan:** [003](../plans/003-modelo-de-tipos.md). **Evidencia:** [`2026-11-02-validator-types.md`](../evidence/2026-11-02-validator-types.md) (`make validator-types`).

## Contexto

`Context.Model()` (plan 003) construye el modelo de tipos desde los `StructureDefinition` ganadores. La regla 1 (§3.2) dice qué SD define un tipo (tomada de `definesType` de `gofhir/models`): no es `logical`, tiene snapshot y su `derivation` es `specialization` o falta. Tres casos de los núcleos oficiales no encajan limpiamente y `gofhir/models` los resuelve distinto; el plan los dejó como decisiones abiertas (§3.3) y pidió resolverlos con el comportamiento del validador de HL7.

## Evidencia

`make validator-types` evalúa con `validator_cli` 7.0.2 (SHA-256 fijado en `scripts/validator.sha256`, sin red, un JVM) `value.as(T)` y `value.is(T)` sobre una `Observation` con `valueQuantity`. El control es un nombre inventado. La especificación de FHIRPath dice que el argumento de `is` y `as` «must resolve to the name of a type in a model»; el validador comprueba el nombre en `as` (`The type FHIR.T is not valid`) y no en `is` (da `false`). **Lo que mide `as`:** acepta todo `StructureDefinition` cargado que no sea un perfil, también los modelos lógicos que no son tipos (los patrones `Event`, `FiveWs` y `Participant`), y rechaza los perfiles (`derivation: constraint`) y los nombres inventados. Distingue «nombre de un SD no perfil» de lo demás, no «tipo» de «no tipo»: sirve como evidencia para D1 y no decide D2 ni D3, que se deciden con la especificación.

| FHIR | T | `as(T)` | Lectura |
| --- | --- | --- | --- |
| R4, R4B, R5 | `SimpleQuantity` | error: `The type FHIR.SimpleQuantity is not valid` | igual que el control: no es un tipo |
| R4, R4B, R5 | `MoneyQuantity` | ídem | ídem |
| R4 | `MetadataResource` | vacío, sin error | nombre conocido (como los patrones) |
| R4, R4B, R5 | patrones lógicos: `Event`; R4 `FiveWs`; R5 `Participant` | vacío, sin error | nombre conocido, y no son tipos |
| R4B | `DataType` | vacío, sin error | nombre conocido |
| R4B, R5 | `DomainResource`, R5 `CanonicalResource`, `MetadataResource`, `DataType` | vacío o valor, sin error | nombres conocidos |

## D1 — `SimpleQuantity` y `MoneyQuantity`

**Decisión propuesta: no son tipos.** Son perfiles de `Quantity` (`derivation: constraint`); la regla 1 los excluye sin nombres en el código. El validador los rechaza como nombre de tipo en `as` en las tres versiones. `gofhir/models` los trata como tipos (`HasType`, padre `Quantity`, hijos de `Quantity`): diferencia deliberada, clase `quantity-constraints` de `parity/testdata/differences.json` (20 diferencias por versión). Consecuencia: `Quantity.as(SimpleQuantity)` es un error de tipo de FHIRPath, como en el validador; quien necesite el perfil usa el `StructureDefinition`.

## D2 — `MetadataResource` en R4

**Decisión (usuario, 02-11-2026): no es un tipo.** Se decide con la especificación:

- FHIRPath en FHIR R4 (fhirpath.html): «The namespace for the types defined in FHIR (primitive data types, data types, resources) is FHIR». Un modelo lógico no es ninguno de los tres.
- El package `hl7.fhir.r4.core` 4.0.1 lo declara `kind: logical` («Common Ancestor declaration for conformance and knowledge artifact resources»): un patrón, como `Event` o `FiveWs`.
- Ningún recurso de R4 deriva de él: `CodeSystem`, `ValueSet` y los demás tienen `baseDefinition` `DomainResource`. Como tipo no tendría subtipos, y `CodeSystem is MetadataResource` sería falso igual.

El validador acepta `as(MetadataResource)` en R4, pero acepta del mismo modo los patrones `Event` y `FiveWs` (tabla de Evidencia): no lo trata como tipo, lo reconoce como nombre de un SD. La regla 1 lo excluye sin nombres en el código. Diferencia deliberada con `models`, clase `metadata-resource-r4` (22 diferencias en R4).

## D3 — `DataType` en R4B

**Decisión propuesta: es un tipo** (diferencia deliberada, ya anotada en la revisión 02). El núcleo R4B lo define como especialización con snapshot (base de los tipos complejos); `models` r4b no lo lista y `models` r5 sí; el validador reconoce el nombre (no decide: ver Evidencia). Clase `data-type-r4b` (4 diferencias en R4B).

## D4 — Construcción

El modelo se construye en `NewContext` (no perezosamente) para fallar cerrado: un `StructureDefinition` ilegible, una `baseDefinition` que no resuelve a un tipo, un ciclo, un camino fuera de su tipo o repetido, un `targetProfile` que no resuelve o dos SD ganadores de URL distinta para un tipo son errores de `NewContext`. Costo medido en `make evidence`.

## Consecuencias

- Los tipos por versión están fijados en `testdata/golden/*-types.txt` (209, 206 y 231): un tipo nuevo en un package cambia el dorado.
- La regla 5 no necesita excepciones nuevas para D1 y D3: el vocabulario de `StructureDefinition` (`kind`, `derivation`, la extensión `fhir-type`) vive en `fhirnames.go`.
