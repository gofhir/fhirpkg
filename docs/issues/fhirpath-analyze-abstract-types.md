# fhirpath: `Analyze` rechaza navegaciones válidas sobre un tipo abstracto

**Publicado:** [gofhir/fhirpath#257](https://github.com/gofhir/fhirpath/issues/257) (03-11-2026), reproducido también en `main` 62dfbe6.

**Librería:** `gofhir/fhirpath` v1.16.6 y `main` (84be8dc), `analyze.go:345-356` (`modelDescribes`).

**Reproducción** (`/Volumes/dev/tmp/h1probe/main.go`): un `Model` que, además de lo que da `models/r4` v2.10.0, describe `Resource.id`, `DomainResource.id`, `DomainResource.contained` y `Resource.meta`, como hace un modelo construido desde los snapshot de los StructureDefinition:

| Expresión | Contexto | `models/r4` | modelo completo |
| --- | --- | --- | --- |
| `contained.contained.empty()` (dom-2 del núcleo) | Patient | sin error | `Resource has no element contained` |
| `Bundle.entry.resource.name` | Bundle | sin error | `Resource has no element name` |
| `contained.text.empty()` | Patient | sin error | `Resource has no element text` |

**Esperado:** sin error. `contained` y `Bundle.entry.resource` son de tipo `Resource`, que es abstracto (structuredefinition.html, `abstract`): la instancia es siempre de un tipo concreto que deriva de él, y ese tipo puede tener el elemento. El validador de HL7 evalúa dom-2 en todo recurso sin error.

**Causa probable:** `modelDescribes(inputType)` toma «el modelo describe `Resource.id`» como prueba de que conoce todos los elementos del tipo, lo que solo vale para un tipo concreto.

**Propuesta:** no dar error cuando el tipo de entrada tiene subtipos (es abstracto), o pedirle al modelo esa información por una interfaz opcional (p. ej. `IsAbstract(type) bool`), como ya hace con `TypeRegistry` y `ElementOrder`.
