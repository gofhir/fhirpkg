# 003 — Modelo de tipos

**Estado:** cerrado el 02-11-2026, sin commit. Plan aprobado por el usuario el 02-11-2026, con la ronda adversarial de §8. Implementación en §9 (Sonnet); D2 decidido por el usuario (excluido, [ADR-004](../adr/004-tipos-del-modelo.md)); code review en tres rondas (§10 a §12, convergió en la tercera); cierre en §13.

Tercer incremento de `gofhir/fhirpkg` (tajada 1a, `LINEAMIENTOS.md` §4.2 y §5). Sobre el `Context` del 002, construye el modelo de tipos dinámico que consumen `fhirpath`, `searchindex` y, después, el normalizador (004) y el núcleo. Reemplaza a `gofhir/models` como modelo en tiempo de ejecución; `models` queda como su referencia independiente (revisión 02, §5).

## 1. Revisión previa

### 1.1 `server` (congelado)

Revisado en la revisión 02 del núcleo (§1.4), sin cambios desde entonces:

- `Registry` (`context.go:268-390`, `registry.go:780-797`) es un modelo dinámico parcial: recorre linealmente el snapshot del recurso y no resuelve tipos de datos, `[x]` ni `contentReference`. **Descartar.**
- `fhirtypes/r4/marshaler.go` usa los structs de `gofhir/models` por versión, con ramas por versión en el código. **Descartar:** aquí la versión es un dato.

### 1.2 `gofhir/models` (`internal/codegen/generator/fhirpath_codegen.go`)

Genera por versión, desde `definitions.json.zip`, los mapas de `FHIRPathModelData`: `choiceTypePaths`, `path2Type`, `path2RefType`, `type2Parent`, `pathsDefinedElsewhere`, `childElements`, `fhirTypes`, `resources` y `types`.

- **Tomar** (con sus casos como tests):
  - `definesType`: un SD define un tipo si no es `logical`, tiene snapshot y su `derivation` es `specialization` o falta (la raíz de R4);
  - `childElements` en el orden del snapshot, sin slices, con `[x]` en el nombre;
  - `fhirTypes` desde la extensión `structuredefinition-fhir-type`, sin el `value` de los primitivos;
  - los elementos propios de cada primitivo (`date.id`, `date.extension`, `date.value`);
  - `path2Type` con la clave por variante de `[x]` (`Observation.valueQuantity`);
  - la jerarquía por `baseDefinition`.
- **Mejorar:**
  - **la fuente:** el `Context` del lock (los SD ganadores de la cascada del 002), no un zip embebido;
  - **los destinos de referencia:** se resuelven por canónico en el `Context` (el `type` del SD de cada `targetProfile`), no por el último segmento de la URL;
  - **el padre:** sale del `type` del SD de `baseDefinition` resuelto, no del último segmento de la URL;
  - **el conjunto de tipos:** sale de una regla sobre los datos (§3.2), no de qué SD trae cada bundle del zip.
- **Descartar:** la generación de código por versión y el costo de inicialización de mapas literales (`sync.OnceValue` sobre 1,2 MB de código por versión).

### 1.3 Especificación

- R4, R4B y R5 `structuredefinition.html`: `kind` (`primitive-type`, `complex-type`, `resource`, `logical`), `abstract`, `derivation`, `baseDefinition`, `type`.
- `elementdefinition.html`: `path`, `type.code`, `type.targetProfile`, `contentReference` (R4 `#Questionnaire.item`; R5 admite la URL del SD con fragmento), elementos `[x]` (§«Choice types»), `sliceName`.
- `fhirpath.html` (FHIR) y FHIRPath N1 §«Types and reflection»: el modelo de tipos, `is`/`as`, `children()`.
- La extensión `http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type`.

### 1.4 Medido (sonda en el scratchpad sobre los núcleos oficiales y `models` v2.10.0)

| Versión | Tipos `models` | Tipos `definesType` del package | Diferencias |
| --- | --- | --- | --- |
| R4 | 212 | 209 | `models` además: `SimpleQuantity`, `MoneyQuantity` (constraints de Quantity) y `MetadataResource` (`logical` en R4) |
| R4B | 207 | 206 | `models` además: `SimpleQuantity`, `MoneyQuantity`; el package además: `DataType` (conocida, revisión 02) |
| R5 | 233 | 231 | `models` además: `SimpleQuantity`, `MoneyQuantity` |

`IsResource`: `models` excluye los abstractos (`Resource`, `DomainResource` y, en R5, `CanonicalResource` y `MetadataResource`).

## 2. Alcance

| # | Pieza | Qué entrega |
| --- | --- | --- |
| A | Modelo | `Context.Model()`: un valor inmutable que implementa `fhirpath.Model`, `VersionedModel`, `TypeRegistry`, `ElementOrder` y `FHIRTypes` por tipado estructural, sin importar `fhirpath`. Se construye una vez, la primera vez que se pide |
| B | Paridad | Módulo aparte `parity/` (su propio `go.mod`, con `gofhir/models` v2.10.0) que compara los nueve mapas en R4, R4B y R5. El módulo principal sigue con solo la biblioteca estándar |
| C | Diferencial por consumidor | En `parity/`: `searchindex.Extract` sobre los ejemplos oficiales con cada modelo da el mismo resultado; y el benchmark de extracción con ambos |
| D | Costo | Tiempo, RSS y heap retenido de `NewContext` más `Model()` por versión, en `make evidence` |

**Dentro también (H10, decisión del usuario):** `min` y `max` de cada elemento, guardados en el mismo recorrido del snapshot (`Cardinality(path)`), porque `searchindex` ya los usa para saber si un elemento es arreglo.

**Fuera:** el normalizador y la proyección `_summary`/`_elements` (004), `isSummary` y la correspondencia entre variante y `value[x]` (004), los perfiles como tipos, el `ElementDefinition` completo que necesitará el validador.

## 3. Diseño

### 3.1 Fuente

Los `StructureDefinition` ganadores del `Context` (002). Ningún nombre de tipo, recurso o elemento en el código: todo sale de los SD. Si un tipo lo definen dos SD que ganan con URL distinta, es error de construcción.

### 3.2 Reglas

1. **Tipos:** los SD que cumplen `definesType` (§1.2), de cualquier package. `HasType(t)` es verdadero para ellos.
2. **Jerarquía:** `ParentType(t)` es el `type` del SD de `baseDefinition`, resuelto en el `Context`. `IsSubtype` recorre esa cadena.
3. **Recursos:** `IsResource(t)` es verdadero para `kind` `resource` y no `abstract`, igual que `ResourceTypes()` del 002.
4. **Rutas:** por cada elemento del snapshot de un tipo, sin slices (`id` con `:`):
   - `TypeOf(path)`: el `type.code` si es único. Si el elemento es `[x]`, `TypeOf` de cada variante (`valueQuantity`) da su tipo, y `ChoiceTypes(base)` da los tipos en el orden del SD;
   - `ReferenceTargets(path)`: el `type` del SD de cada `targetProfile`, en orden y sin repetir;
   - `ResolvePath(path)`: el destino de `contentReference`, aceptando `#ruta` y `url#ruta`;
   - `ChildElements(path)`: los hijos en el orden del snapshot, y para un tipo, los de su SD;
   - `FHIRTypeOf(path)`: la extensión `structuredefinition-fhir-type` del único `type`.
5. **Primitivos:** sus elementos propios (`id`, `extension`, `value`) entran en `TypeOf` y `ChildElements`.
6. **`FHIRVersion()`:** el `fhirVersion` del núcleo del lock.

### 3.3 Decisiones abiertas (para la ronda adversarial y el usuario)

- **D1 — `SimpleQuantity` y `MoneyQuantity`:** son `constraint` de `Quantity` en el núcleo. `models` los trata como tipos (`HasType`, padre `Quantity`, hijos de `Quantity`). La regla 1 los excluye. Se decide con evidencia del validador de HL7 (`Quantity.is(SimpleQuantity)` y `as`), con la regla de datos que resulte, y se registra en una ADR.
- **D2 — `MetadataResource` en R4:** es `logical`. La regla 1 lo excluye y `models` lo incluye. Propuesta: diferencia deliberada (structuredefinition.html: un modelo lógico no es un tipo que tenga una instancia), con la misma verificación contra el validador.
- **D3 — `DataType` en R4B:** está en el package y no en `models`. Propuesta: incluirlo (diferencia deliberada, ya anotada en la revisión 02).
- **D4 — Construcción:** en `NewContext`, para fallar cerrado (H8).

### 3.4 Paridad (pieza B)

- Las claves de la referencia salen de leer con `go/parser` los literales de `fhirpath_model.go` de `models` en la caché de módulos (`go list -m -json`), porque sus mapas son privados. Los valores se piden por los métodos públicos.
- Por cada mapa y versión: claves solo de un lado y valores distintos. Cada diferencia se clasifica como defecto propio, defecto de `models` (reportado con evidencia, sin rodearlo) o deliberada (con su sección de la especificación), en `parity/testdata/differences.json`. El test falla ante una diferencia sin clasificar o ante una clasificada que ya no ocurre.

## 4. Tests y fuentes de datos

- **Casos a mano** desde la especificación, en R4, R4B y R5:
  - `[x]` y el orden de sus tipos;
  - `contentReference` en las dos formas (`#ruta` y `url#ruta`; la segunda, sintética en R4);
  - un primitivo y sus elementos;
  - `fhirTypes` (`Patient.id` es `string` en R4 e `id` en R5);
  - destinos de referencia (`Reference(Any)` en R4 es `Resource`);
  - la jerarquía (`Age` → `Quantity`, `Patient` → `DomainResource` → `Resource`);
  - un slice que no es hijo;
  - un tipo con dos SD ganadores (error).
- **Paridad** con `models` (pieza B) en las tres versiones.
- **Diferencial** con `searchindex.Extract` sobre los ejemplos oficiales de R4, R4B y R5: mismo índice con los dos modelos, o cada diferencia explicada por una entrada de `differences.json`.
- **Metamórficos:** el orden de los packages y de los recursos no cambia el modelo (comparado mapa por mapa).
- **Concurrencia:** `Model()` y sus métodos con `-race`.
- **Fuzzing:** `FuzzModelFromStructureDefinition`, con SD sintéticos (rutas, `[x]` y `contentReference` arbitrarios): el modelo se construye o da error, nunca entra en pánico.
- **Mutaciones:** cada regla de §3.2 apagada o invertida; slices como hijos; el orden de `[x]` cambiado; el padre por último segmento de URL con un SD cuyo `id` difiere de su `type`; `contentReference` en una sola forma; `IsResource` con abstractos; una diferencia clasificada que desaparece.

## 5. Criterio de salida

- `make check` en verde; `make check-packages` corre la paridad y el diferencial en las tres versiones.
- Cada diferencia con `models` clasificada con evidencia; D1 a D3 decididas en una ADR (ADR-004).
- Costo de `NewContext` más `Model()` por versión en el reporte de evidencia, y el benchmark de `searchindex` con los dos modelos.
- Mutaciones detectadas; code review hasta converger.

## 6. Dependencias nuevas

Ninguna en el módulo principal. `parity/` es un módulo aparte que depende de `gofhir/models` v2.10.0 y de `gofhir/searchindex` (por `replace` a la carpeta hermana, como el test de herencia del 002). Justificación: la referencia y el consumidor no pueden entrar en el módulo de la librería sin romper la regla de solo la biblioteca estándar.

## 7. Excepciones a la regla 5

- Los valores de `kind` (`resource`, `logical`), `derivation` (`specialization`, `constraint`) y la URL de la extensión `structuredefinition-fhir-type` son vocabulario de `StructureDefinition`: van en `fhirnames.go` con su fuente, bajo el guardián del 002.
- D1 puede agregar una regla de datos; si no hay una regla sin nombres, es una excepción con ADR.

Lista completa de los literales de FHIR que el modelo de tipos nombra (todos en `fhirnames.go`, que lo cuida `TestFHIRNamesOnlyInFHIRNames`; ronda 2, hallazgo B6):

| Literal | Valor | Fuente |
| --- | --- | --- |
| `kindResource` | `resource` | structuredefinition.html, ValueSet `structure-definition-kind` (002) |
| `kindLogical` | `logical` | ídem: un modelo lógico no es un tipo con instancias |
| `kindPrimitive` | `primitive-type` | ídem: separa el `value` de un primitivo (H3) y da el conjunto de tipos del sistema |
| `derivationConstraint` | `constraint` | ValueSet `type-derivation-rule` (002): un perfil no define tipo |
| `derivationSpecialization` | `specialization` | ídem: el SD define un tipo propio |
| `choiceSuffix` | `[x]` | elementdefinition.html, «Choice types» |
| `urlFHIRTypeExtension` | `http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type` | extension-structuredefinition-fhir-type.html |
| `systemTypePrefix` | `http://hl7.org/fhirpath/System.` | ídem y fhirpath.html: los códigos de tipo del sistema; solo identifica qué `type.code` examinar, el conjunto admitido sale de los primitivos del núcleo (§11, B4) |
| `maxUnbounded` | `*` | elementdefinition.html: `max` sin tope |
| Campos JSON (`jsonFieldSources`) | `snapshot`, `element`, `path`, `min`, `max`, `type`, `code`, `targetProfile`, `contentReference`, `extension`, `valueUrl`, `base`, `id`, `kind`, `abstract`, `derivation`, `baseDefinition` | cada uno con su página en `jsonFieldSources` |

Sintaxis (no nombres de FHIR) que el código también usa: `.` (separador de `path`), `:` (marca de rebanada en `id`, elementdefinition.html), `|` (versión de un canónico, canonical.html) y `#` (fragmento de `contentReference`).

## 8. Ronda adversarial

Revisor: Opus (Codex sin cuota hasta el 04-11-2026). Sondas en `/Volumes/dev/tmp/review-fhirpkg003/`; H1 reproducido por Opus en `/Volumes/dev/tmp/h1probe/`.

| # | Sev. | Hallazgo | Resolución propuesta |
| --- | --- | --- | --- |
| H1 | alta | Con los elementos de los tipos abstractos en `TypeOf` (`Resource.id`), `fhirpath.Analyze` (v1.16.6 y `main` 84be8dc, `analyze.go:345-356`) rechaza `contained.contained.empty()` (dom-2 del núcleo), `Bundle.entry.resource.name` y `contained.text.empty()`: «Resource has no element …». Con `models` pasan. Un elemento ausente en un tipo abstracto no prueba que no exista en la instancia | Defecto de `fhirpath` (regla 10): issue redactado, sin rodearlo. El modelo sigue la especificación; el diferencial incluye `Analyze` sobre los invariantes de los packages y la pérdida queda conocida, con test, hasta que `fhirpath` lo corrija |
| H2 | media | Tres clases de diferencia no previstas: (a) elementos anidados de tipos de datos, `Element` en el snapshot y `BackboneElement` en `models` (14/13/17 claves); (b) `models` no da `ReferenceTargets` para variantes `[x]` (76/79/81) y repite `Media.device` en R4 y R4B; (c) rutas de tipos abstractos (12/14/80) | (a) y (b): defectos de `models`, se reportan con evidencia. (c): deliberada, está en el snapshot. Todo en `differences.json` |
| H3 | media | §3.2 no excluía `<primitivo>.value` de `FHIRTypeOf` | Aceptado: se excluyen los elementos de un primitivo cuyo `base.path` empieza en un tipo primitivo (regla de datos; 20/20/21) |
| H4 | media | No hay regla de datos que dé exactamente `SimpleQuantity` y `MoneyQuantity` | D1 se decide con el veredicto del validador de HL7; si son tipos, es una excepción con ADR |
| H5 | media | Leer las claves de `models` con `go/parser` es frágil | Aceptado: `reflect` sobre los campos privados (`MapKeys`, sin `Interface()`), en `parity/` |
| H6 | media | El diferencial con `searchindex` no ejercita `Analyze`, `ReferenceTargets`, `ParentType` ni las clases de H2 | Aceptado: diferencial directo de `fhirpath` con `is`, `as`, `type()`, `children()` y `Analyze` sobre esas clases; el benchmark compara los modelos ya construidos |
| H7 | baja | `url#ruta` no aparece en ningún núcleo; la mutación del último segmento no tiene caso real | Aceptado: casos sintéticos, declarados como tales |
| H8 | media | Construcción perezosa con `Model()` sin error rompe «fallar cerrado» | Aceptado: D4 = el modelo se construye en `NewContext`, con su costo medido |
| H9 | baja | La regla 1 admite tipos de cualquier package; hoy solo los define el núcleo | Aceptado: el dorado fija los tipos por versión (209/206/231 más D1 a D3); un tipo nuevo cambia el dorado |
| H10 | baja | El 004 necesitará `min`, `max`, `isSummary` y la correspondencia entre la variante y `value[x]` | Decisión del usuario: `min` y `max` ahora; lo demás en el 004 |

**Decisiones del usuario (02-11-2026):** H1 se reporta a `fhirpath` y el 003 sigue con la pérdida conocida y fijada con un test; el resto de la tabla, aprobado. Issues redactados en `docs/issues/` (se publican solo si el usuario lo pide).

## 9. Implementación

**Estado:** implementada y verificada (02-11-2026), sin commit. Próximo paso: code review, ronda 1.

### 9.1 Lo hecho

- `Context.Model()` (`model.go`, `modelbuild.go`), construido en `NewContext` (D4/H8). Tipos por la regla 1 (`definesType`, tomada de `models`), padre por el `type` del SD de `baseDefinition`, `[x]` con sus tipos en el orden del SD y una clave por variante, destinos de referencia por el `type` del SD de cada `targetProfile`, `contentReference` en `#ruta` y `url#ruta`, hijos en el orden del snapshot sin slices, `FHIRTypeOf` (H3: sin el `value` de un primitivo, por una regla de datos sobre `base.path`) y `Cardinality(path)` con `min` y `max` en el mismo recorrido (H10). Implementa por tipado estructural `Model`, `VersionedModel`, `TypeRegistry`, `ElementOrder` y `FHIRTypes` de `fhirpath` v1.16.6, sin importarlo.
- Errores de construcción (fallar cerrado): dos SD ganadores de URL distinta para un tipo, definición de un tipo sin resolver, `baseDefinition` que no resuelve o que no es un tipo, ciclo, camino fuera de su tipo o repetido, tipo sin código, `contentReference` sin fragmento, `min` o `max` ilegibles, `targetProfile` que no resuelve.
- `fhirnames.go`: vocabulario de `StructureDefinition` del 003 (`kind`, `derivation`, `choiceSuffix`, la extensión `fhir-type`, `maxUnbounded`) y los nombres de campo del snapshot, cada uno con su fuente; el guardián pasa sin excepciones nuevas.
- Módulo `parity/` (`go.mod` propio; `replace` a `..` y a `../../go-searchindex`; `gofhir/models` r4, r4b, r5 v2.10.0 y `gofhir/fhirpath` v1.16.6):
  - las nueve tablas de `models` (claves por `reflect`, valores por los métodos públicos) contra el `Model`, en R4, R4B y R5;
  - `testdata/differences.json` clasifica cada diferencia (ocho clases más la de `Analyze`); el test falla ante una sin clasificar, una con dos clases o una clase cuya cuenta cambia;
  - `searchindex.Extract` sobre los ejemplos oficiales con cada modelo; el diferencial directo de `fhirpath` (`is`, `as`, `type()`, `children()` y `Analyze` sobre los invariantes y los SearchParameter de los núcleos) y la pérdida conocida de H1 (`TestKnownLossAnalyzeAbstractTypes`, referencia `docs/issues/fhirpath-analyze-abstract-types.md`), sin rodearla en el modelo;
  - el benchmark con los dos modelos ya construidos;
  - el reporte y el bloque de cifras del issue de `models` los escribe el propio test.
- `make parity` (también dentro de `make check-packages` y `make evidence`), `make validator-types` (`scripts/validator_types.py`), `scripts/examples.sha256`, dorado `testdata/golden/*-types.txt` (`make golden`), `FuzzModelFromStructureDefinition` en `make fuzz`, `TestModelIsConcurrent` con `-race` en `make check`.
- Mutaciones M116 a M158 (una por regla de §3.2, las de §4 y las de D4) y P-01 a P-06 (solo los oráculos de `parity/`, p. ej. la clase que desaparece) en `scripts/mutations.py`.
- Tests a mano en R4, R4B y R5 sintéticos (H7 declarado) y sobre los núcleos: `model_test.go`, `real_model_test.go`; los valores salen de la especificación, citada.

### 9.2 Resultados

- Tipos: 209, 206 y 231 en R4, R4B y R5 (§1.4, H9), fijados en el dorado.
- Paridad (`[`2026-11-02-parity.md`](../evidence/2026-11-02-parity.md)`): `resources`, `pathsDefinedElsewhere` y `choiceTypePaths` (salvo las rutas de tipos abstractos) coinciden con `models` en las tres versiones. Diferencias por clase (R4 / R4B / R5): `quantity-constraints` 20 / 20 / 20 (D1, deliberada); `metadata-resource-r4` 22 / 0 / 0 (D2, deliberada, decidida por el usuario el 02-11-2026: [ADR-004 §D2](../adr/004-tipos-del-modelo.md)); `data-type-r4b` 0 / 4 / 0 (D3, deliberada); `abstract-type-paths` 12 / 14 / 82 (deliberada); `nested-backbone-type` 14 / 13 / 17, `no-targets-on-choice-variants` 76 / 79 / 81, `no-targets-on-codeable-reference` 0 / 20 / 120 y `repeated-target` 1 / 1 / 0 (defectos de `models`, en `docs/issues/models-fhirpath-model-gaps.md`). Las cifras de H2 del plan (14/13/17, 76/79/81, 12/14/80) se reproducen; las dos de más de R5 en `abstract-type-paths` son `PrimitiveType.id` y `.extension`.
- `searchindex.Extract`: 5309, 2840 y 2822 ejemplos oficiales (R4, R4B, R5) con el mismo índice con los dos modelos; ninguna diferencia.
- `fhirpath`: la evaluación (`is`, `as`, `type()`, `children()`) solo difiere por `nested-backbone-type` (con las rutas de esa clase corregidas en `models`, el resultado es idéntico al del `Model`: 145, 141 y 147 evaluaciones). `Analyze` sobre 16.471, 16.495 y 19.821 expresiones: solo difieren dom-2 y bdl-16 (187, 177 y 157), la pérdida de H1.
- Benchmark: la extracción con los dos modelos cuesta lo mismo (diferencia dentro del ruido); las consultas del modelo, 47 a 51 ns por camino, sin asignaciones, en ambos.
- Costo del modelo dentro de `NewContext`: en el reporte de evidencia (`[`2026-11-02-1829.md`](../evidence/2026-11-02-1829.md)`): tiempo, heap retenido y RSS por versión.

### 9.3 D1 a D3 y decisiones fuera del plan

- **D1:** `SimpleQuantity` y `MoneyQuantity` no son tipos: el validador (`validator_cli` 7.0.2, `as(T)`) los rechaza igual que un nombre inventado, en las tres versiones. Sin excepción a la regla 5.
- **D2: excluido (decisión del usuario, 02-11-2026).** El validador acepta `as(MetadataResource)` en R4, pero también `as(Event)`, `as(FiveWs)` y `as(Participant)`, patrones lógicos que no son tipos: `as` mide si el nombre es de un SD no perfil, no si es un tipo. Se decide con la especificación (fhirpath.html: primitivos, tipos de datos y recursos; `kind: logical`; ningún recurso de R4 deriva de él). ADR-004 §D2.
- **D3:** `DataType` en R4B es un tipo; el validador lo acepta.
- Evidencia de D1 a D3: [`2026-11-02-validator-types.md`](../evidence/2026-11-02-validator-types.md) (`make validator-types`, un JVM, `-no-http-access`; se usó el subcomando `fhirpath` del validador en vez de un perfil sintético con invariante).
- `Cardinality(path) (min, max int, ok bool)`, con `Unbounded = -1`, responde por la base de un `[x]` y por cada variante.
- `Types()` y `Paths()` (exportados) existen para las comparaciones; `Paths()` no es parte del contrato de evaluación.
- Un elemento con varios tipos y sin `[x]` no tiene `TypeOf` (el plan dice «si es único»).
- Un `targetProfile` que no resuelve en el `Context` es error de `NewContext` (no se omite).
- Los ejemplos oficiales para `parity` se descargan a `.cache/packages` con `scripts/examples.sha256` (los SHA-256 fijados por `searchindex`).
- Una clase `fhirpath-defect` en `differences.json` para `Analyze` (H1), además de las tres del plan.
- El diferencial de `fhirpath` demuestra que cada diferencia está explicada: aplica a `models` las rutas de `nested-backbone-type` corregidas y exige el resultado idéntico.

### 9.4 Evidencia

`make check`, `make check-packages` (incluye `make parity`) y `make evidence` en verde: `[`2026-11-02-1829.md`](../evidence/2026-11-02-1829.md)`. Paridad: `[`2026-11-02-parity.md`](../evidence/2026-11-02-parity.md)`. Mutaciones completas sin caché, todas detectadas: `[`2026-11-02-1829-mutations.md`](../evidence/2026-11-02-1829-mutations.md) (194 de 194)`. Validador: [`2026-11-02-validator-types.md`](../evidence/2026-11-02-validator-types.md).

### 9.5 Pendiente

- Code review, ronda 1 (revisores: Opus mientras Codex no tenga cuota).
- El paso de ADR-004 a Aceptada (code review y CI).
- Los issues de `docs/issues/` siguen sin publicarse (solo a pedido del usuario).
- Medir el costo de `NewContext` con la máquina en reposo (las cifras de esta corrida pueden estar infladas por otras cargas).

## 10. Code review, ronda 1

Revisor: Opus. Hallazgos verificados y corregidos el 02-11-2026 (Sonnet), cada uno con su test que fallaba antes. Evidencia: [`2026-11-02-2026.md`](../evidence/2026-11-02-2026.md) (`make check`, `make check-packages` con el oráculo y `make parity`); mutaciones completas sin caché: [`2026-11-02-2025-mutations.md`](../evidence/2026-11-02-2025-mutations.md) (214 de 214; la corrida sin caché tardó 1088 s con `-failfast` en las mutaciones y detectó todas salvo M127, cuya ancla había cambiado: reajustada, detectada y reutilizada del caché en este reporte); paridad: [`2026-11-02-parity.md`](../evidence/2026-11-02-parity.md).

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| M1 | media | `NewContext` aceptaba snapshots incoherentes | `addElements` falla cerrado (mensaje con package, SD y camino) ante: `contentReference` a un camino que no es del mismo SD o con la URL de otro; `type.code` que no es un tipo del modelo ni un tipo del sistema de FHIRPath (`systemTypePrefix`, en `fhirnames.go`); `[x]` junto a una variante explícita; elemento no raíz sin `type` ni `contentReference`; `[x]` sin tipos; `min` o `max` ausentes (sdf-3, también en rebanadas); `min` > `max` (eld-2, igual en R4, R4B y R5, medido en los `StructureDefinition` de `ElementDefinition` de los tres núcleos); snapshot sin raíz; camino huérfano. Un test por caso en `TestModelBrokenDefinitionsAreErrors` (R4, R4B y R5). La fixture sintética ahora declara los tipos que usa. Ningún package oficial chocó: los tres núcleos y los locks cargan sin relajar nada |
| M2 | media | Sin oráculo independiente para `ReferenceTargets` y `Cardinality` | `scripts/model_oracle.py` (Python con `-I`) lee el `package.tgz` del núcleo sin pasar por `modelbuild.go` y deriva destinos (en el orden de `targetProfile`, por variante de `[x]`), `min`/`max` y tipo FHIR de cada camino; `TestRealModelOracle` los compara exactos y en los dos sentidos con el modelo, en `make check-packages` (unos 3 s en total, cada corrida de Python unos 0,4 s). Mutaciones nuevas M172 a M178 (D, E, orden y truncamiento de destinos en `[x]` y en elementos, `FHIRTypeOf` y cardinalidad de abstractos): todas detectadas |
| B1 | baja | El fuzz no afirmaba nada | `checkModelInvariants` (extraído de `TestRealModelInvariants`, no copiado) se exige en el fuzz sobre todo modelo que se construye; ahora además exige que el destino de un `contentReference` sea un elemento del modelo. Semillas ajustadas a SD válidos |
| B2 | baja | `derivation` vacía valía con `baseDefinition` | `definesType`: vacía solo sin `baseDefinition` (R5 sdf-27, mismo criterio en R4 y R4B). `TestModelBaseDefinitionWithoutDerivationIsNotAType`; M171 |
| B3 | baja | Texto desactualizado de D2 | Razón de `metadata-resource-r4` en `parity/testdata/differences.json` y §9.2 corregidas: decidido por el usuario, excluido por la especificación ([ADR-004 §D2](../adr/004-tipos-del-modelo.md)) |
| B4 | baja | `Context.modelTime` era un campo de producción que solo leían los tests | Eliminado; `TestRealModelCost` mide el tiempo de `NewContext` (que incluye el modelo) y el heap que retiene el modelo |

Las mutaciones M118, M119, M125, M138 y otras cuyo ancla cambió se reajustaron. **Próximo paso:** code review ronda 2.

## 11. Code review, ronda 2

Revisor: Opus. Hallazgos corregidos el 02-11-2026, con su test (`model_test.go`, `TestModelBrokenDefinitionsAreErrors` y `TestModelVersionedReferencesAndSingleTypeChoice`) y su mutación (M179 a M188). Evidencia: [`2026-11-02-2107.md`](../evidence/2026-11-02-2107.md) (`make check`, `make check-packages` con el oráculo y `make parity`); mutaciones completas sin caché, todas detectadas: [`2026-11-02-2102-mutations.md`](../evidence/2026-11-02-2102-mutations.md); paridad: [`2026-11-02-parity.md`](../evidence/2026-11-02-parity.md). Ningún package oficial chocó con las reglas nuevas: los tres núcleos y los locks cargan sin relajar nada.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| M1 | media | `NewContext` aceptaba un elemento sin `id` y un `path` con `:`; el modelo rompía su invariante («the path has a slice mark») | sdf-14 («All element definitions must have an id», `StructureDefinition`, idéntico en R4, R4B y R5, medido en los núcleos): sin `id` o con `id` vacío es error, también en rebanadas. Un `path` con `:` es error. Cita honesta: eld-19 (error, R4/R4B/R5) admite sintácticamente `:nombre` en un componente del `path`, así que no lo prohíbe; la razón es que `path` nombra el elemento (elementdefinition.html) y el nombre de la rebanada va en `id` y `sliceName`, que ningún núcleo usa `:` en un `path` y que el modelo indexa por `path`. Semillas del fuzz con `id` ausente y `:` en el camino (el parámetro `id` del fuzz es el camino por defecto; `!` lo omite) |
| B1 | baja | eld-5: `contentReference` y `type` juntos se aceptaban e ignoraban el tipo | Error (eld-5, `ElementDefinition`, R4/R4B/R5) |
| B2 | baja | Cadena o ciclo de `contentReference` | Error. Decisión: elementdefinition.html dice que el `contentReference` trae «all the rules that are in the ElementDefinition» del destino y que referencia «the non-constrained definition»; un destino que es a su vez `contentReference` no aporta tipo ni hijos. Ninguna cadena en los núcleos (medido) |
| B3 | baja | `Bad.value` junto a `Bad.value[x]` sobrescribía la cardinalidad | Error, en los dos órdenes (misma familia que M1-4 de la ronda 1) |
| B4 | baja | Cualquier `http://hl7.org/fhirpath/System.*` pasaba | El conjunto de tipos del sistema sale de los `type.code` de los elementos de los SD primitivos del package núcleo (los siete que usan en R4, R4B y R5: Boolean, Date, DateTime, Decimal, Integer, String y Time, medido en `.cache/packages`); un código fuera de él es error. Solo cuentan los primitivos del núcleo: uno de un IG no amplía el conjunto (test con un IG). `systemTypePrefix` solo elige qué códigos examinar |
| B5 | baja | Mutaciones que sobrevivían | (A) aplica a `contentReference`: es `uri` y su versión no está definida, pero el código tolera `url\|versión` como un canónico; ahora hay un caso (`Linked.item.again`) y un `targetProfile` con `Organization\|versión`. (I) un `[x]` de un solo tipo con la extensión fhir-type (`Linked.single[x]`): `FHIRTypeOf` de la variante da `string` y el de la base, vacío. M186 y M187 las detectan; M179 a M185 y M188 cubren el resto, y M161 y M162 se reajustaron |
| B6 | baja | §7 incompleto | Tabla de literales con su fuente en §7 |

### Pérdidas conocidas (no se corrigen)

- **eld-17** (`targetProfile` solo con `Reference`, `canonical` o, desde R4B, `CodeableReference`): comprobarlo exige nombres de tipo escritos en el código y una ADR (regla 5). Un `targetProfile` en otro tipo se resuelve y se guarda sin error.
- **El oráculo `model_oracle.py` no es independiente de la interpretación:** lo es en la implementación (otro lenguaje, lee el `package.tgz` sin pasar por `modelbuild.go`) pero comparte la lectura de las reglas; la sostienen los casos a mano de `TestRealModelSpecCases`. No cubre IG con tipos propios.

**Próximo paso:** code review ronda 3 (la última).

## 12. Code review, ronda 3

Revisor: Opus. **Convergió:** sin hallazgos de severidad media o mayor. Cuatro hallazgos bajos, todos de fallar cerrado ante snapshots incoherentes, corregidos el 02-11-2026 con su caso en `TestModelBrokenDefinitionsAreErrors` (R4, R4B y R5; fallaban antes) y su mutación (M189 a M197). Verificado antes de cada regla contra los 19 `.tgz` de `.cache/packages` (sonda en el scratchpad, sobre los 1292 `StructureDefinition` que definen un tipo): ninguna regla nueva choca con un package oficial y no se relajó nada.

| # | Sev. | Hallazgo | Resolución |
| --- | --- | --- | --- |
| 1 | baja | `Bad.value[x]` (Quantity) junto a `Bad.valueQuantity[x]`: `Bad.valueQuantity` queda como variante y como base de otro `[x]`, y la cardinalidad se pisa | Cada camino que el modelo indexa (el del elemento, la base de un `[x]` y cada variante) lo reclama un solo elemento (`claimed`); otro con el mismo nombre es error, en los dos órdenes (elementdefinition.html, «Choice types»). M189 |
| 2 | baja | `isSlice` decide por el `id` sin comprobar coherencia: `Bad.x:s` sin `Bad.x` hace desaparecer el elemento y su subárbol; un `id` sin relación con el `path` se acepta | El `id` sin los `:nombre` debe ser igual al `path` (elementdefinition.html, «id»: el id es el path con el nombre de cada rebanada; medido en los núcleos, los IG y las extensiones: 0 excepciones). Toda rebanada exige su elemento base ya visto: la raíz de una rebanada (`X.x:s`), el elemento `X.x`; un descendiente (`X.x:s.y`), la rebanada `X.x:s`. M190, M191, M192 |
| 3 | baja | Un tipo del sistema dentro de un `[x]` genera una variante basura (`Bad.vHttp://…`) | Un tipo del sistema solo vale en un elemento de un solo tipo que no sea `[x]`; si no, error (extension-structuredefinition-fhir-type.html: es el tipo de un valor primitivo). Medido: ningún núcleo lo infringe. M193, M194, M195. Efecto en un test: `Quantity.alt` de la fixture usaba un tipo del sistema entre varios; ahora usa la extensión `fhir-type` sobre un tipo común |
| 4 | baja | Un `contentReference` con hijos en el snapshot deja el modelo ambiguo (`ResolvePath` y `ChildElements` discrepan); un `contentReference` a un hijo de otro evade la regla de cadenas | Un elemento con `contentReference` no puede tener hijos (elementdefinition.html: su contenido es el del destino); error. Con eso un destino bajo otro `contentReference` ya no puede existir. Medido: ningún núcleo tiene hijos bajo un `contentReference`. M196 |

**Sospechado, confirmado:** `systemTypes` solo contaba los primitivos *ganadores* del núcleo; si otro package ganaba la URL de un primitivo, el conjunto de tipos del sistema quedaba incompleto y el error decía «is not a type», engañoso (test con un IG que gana la URL de `date`, único que declara `System.Date`: fallaba con ese mensaje). Ahora es un error propio que nombra al primitivo y al package que lo gana («the primitive "date" of the core package is overridden by …»); el conjunto sigue saliendo solo del núcleo (B4 de la ronda 2 intacto). M197. Las anclas de M184 y M185 se reajustaron (y, por el movimiento del código, las de M125, M139, M140, M173 y M178).

## 13. Cierre

**Estado: cerrado (02-11-2026), sin commit.** El modelo de tipos (`Context.Model()`) está implementado, con tres rondas de code review convergidas, y alimenta a `fhirpath` y `searchindex` por tipado estructural sin importarlos.

**Evidencia** (generada, sin cifras a mano; las cuentas están en los reportes): `make check`, `make check-packages` (con el oráculo y `make parity`) y `make evidence` en verde: [`2026-11-02-2149.md`](../evidence/2026-11-02-2149.md). Mutaciones completas sin caché (`MUTATION_FRESH=1`), todas detectadas, tras reajustar las anclas de M125, M139, M140, M173, M178, M184 y M185 y repetir la corrida completa: [`2026-11-02-2149-mutations.md`](../evidence/2026-11-02-2149-mutations.md). Paridad: [`2026-11-02-parity.md`](../evidence/2026-11-02-parity.md). Validador: [`2026-11-02-validator-types.md`](../evidence/2026-11-02-validator-types.md).

**Pérdidas conocidas** (no se corrigen aquí):

- **H1 de `fhirpath`:** `Analyze` rechaza expresiones válidas sobre elementos de tipos abstractos (`contained.contained.empty()`, dom-2, bdl-16). El modelo sigue la especificación y no lo rodea; fijado con `TestKnownLossAnalyzeAbstractTypes` hasta que `fhirpath` lo corrija (issue redactado).
- **eld-17:** `targetProfile` solo con `Reference`, `canonical` o `CodeableReference` no se comprueba (exigiría nombres de tipo en el código y una ADR); un `targetProfile` en otro tipo se resuelve y se guarda.
- **Límite del oráculo:** `scripts/model_oracle.py` es independiente en la implementación, pero comparte la lectura de las reglas; lo sostienen los casos a mano de `TestRealModelSpecCases`. No cubre IG con tipos propios.

**Issues redactados, sin publicar** (solo si el usuario lo pide): [`docs/issues/fhirpath-analyze-abstract-types.md`](../issues/fhirpath-analyze-abstract-types.md) y [`docs/issues/models-fhirpath-model-gaps.md`](../issues/models-fhirpath-model-gaps.md).

**Estado de la ADR-004:** sigue **Propuesta**; pasa a Aceptada cuando CI corra en verde (no ha corrido: no hay commit).

**Pendiente:**

1. Commit, si el usuario lo pide (el cierre no lo hace).
2. CI en verde y entonces ADR-004 a Aceptada.
3. Publicar los issues, si el usuario lo pide.
4. Medir el costo de `NewContext` con la máquina en reposo (las cifras pueden estar infladas por otras cargas).
5. Plan 004.
