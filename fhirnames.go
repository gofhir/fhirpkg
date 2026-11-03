package fhirpkg

// Los nombres de FHIR que el código de producción de este incremento (plan 002)
// necesita. Es el único archivo nuevo donde pueden aparecer; el guardián
// TestFHIRNamesOnlyInFHIRNames lo hace cumplir. Cada nombre lleva su fuente.

// Tipos de recurso que el módulo busca por nombre. No salen de un
// StructureDefinition porque son la materia del módulo: dar los
// SearchParameter y la herencia de tipos. Excepción de la regla 5, ADR-003.
const (
	// typeSearchParameter: searchparameter.html.
	typeSearchParameter = "SearchParameter"
	// typeStructureDefinition: structuredefinition.html; define los tipos de
	// recurso y su herencia (baseDefinition).
	typeStructureDefinition = "StructureDefinition"
)

// Valores de StructureDefinition.kind y .derivation que separan un tipo de
// recurso de un perfil (structuredefinition.html; ValueSets
// structure-definition-kind y type-derivation-rule). Un tipo es kind resource
// y no es un perfil (derivation constraint): la raíz de la herencia en R4 y
// R4B no declara derivation, y en R5 es specialization.
const (
	kindResource         = "resource"
	derivationConstraint = "constraint"
)

// Vocabulario de StructureDefinition del modelo de tipos (plan 003 §7).
const (
	// kindLogical: un modelo lógico no es un tipo que tenga instancias
	// (structuredefinition.html, ValueSet structure-definition-kind).
	kindLogical = "logical"
	// kindPrimitive: un tipo primitivo (structure-definition-kind).
	kindPrimitive = "primitive-type"
	// derivationSpecialization: el SD define un tipo propio (type-derivation-rule).
	// La raíz de R4 (Element, Resource) no declara derivation.
	derivationSpecialization = "specialization"
	// choiceSuffix: el nombre de un elemento con varios tipos
	// (elementdefinition.html, «Choice types»: value[x]).
	choiceSuffix = "[x]"
	// urlFHIRTypeExtension: la extensión que da el tipo FHIR de un elemento
	// cuyo código es un tipo del sistema de FHIRPath
	// (extension-structuredefinition-fhir-type.html).
	urlFHIRTypeExtension = "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type"
	// systemTypePrefix: prefijo de los códigos de tipo del sistema de FHIRPath
	// (http://hl7.org/fhirpath/System.String...), que un elemento declara en
	// type.code junto a la extensión fhir-type
	// (extension-structuredefinition-fhir-type.html; fhirpath.html).
	systemTypePrefix = "http://hl7.org/fhirpath/System."
	// maxUnbounded: ElementDefinition.max cuando no hay tope
	// (elementdefinition.html).
	maxUnbounded = "*"
)

// Códigos de PublicationStatus (ValueSet publication-status, R4 y R5). El
// orden entre ellos no es de la especificación: es la decisión de ADR-003.
const (
	statusActive  = "active"
	statusDraft   = "draft"
	statusRetired = "retired"
)

// systemVersionAlgorithm es la URL del CodeSystem version-algorithm (R5,
// codesystem-version-algorithm.html): solo un Coding de este sistema declara el
// algoritmo de versión.
const systemVersionAlgorithm = "http://hl7.org/fhir/version-algorithm"

// Códigos del CodeSystem version-algorithm (R5, resource.html:
// versionAlgorithm[x]): cómo se ordenan las versiones de un recurso.
const (
	algSemver  = "semver"
	algInteger = "integer"
	algAlpha   = "alpha"
	algDate    = "date"
	algNatural = "natural"
)

// jsonFieldSources lista los nombres de campo JSON que leen las estructuras de
// este incremento, con su fuente. Las etiquetas `json:"..."` de los archivos
// nuevos deben estar aquí; el guardián lo comprueba.
var jsonFieldSources = map[string]string{
	"resourceType":           "json.html: discrimina todo recurso",
	"url":                    "canonicalresource.html (R5) y la página de cada recurso canónico en R4: url",
	"version":                "canonicalresource.html: version",
	"status":                 "canonicalresource.html: status (PublicationStatus)",
	"experimental":           "canonicalresource.html: experimental",
	"fhirVersion":            "structuredefinition.html, capabilitystatement.html, implementationguide.html: fhirVersion",
	"meta":                   "resource.html: Resource.meta",
	"lastUpdated":            "resource.html: Meta.lastUpdated (instant)",
	"versionAlgorithmString": "R5 canonicalresource.html: versionAlgorithm[x], string (expresión FHIRPath)",
	"versionAlgorithmCoding": "R5 canonicalresource.html: versionAlgorithm[x], Coding (CodeSystem version-algorithm)",
	"code":                   "datatypes.html: Coding.code; searchparameter.html: SearchParameter.code",
	"system":                 "datatypes.html: Coding.system",
	"base":                   "searchparameter.html: SearchParameter.base",
	"baseDefinition":         "structuredefinition.html: StructureDefinition.baseDefinition",
	"kind":                   "structuredefinition.html: StructureDefinition.kind",
	"abstract":               "structuredefinition.html: StructureDefinition.abstract",
	"derivation":             "structuredefinition.html: StructureDefinition.derivation",
	"type":                   "structuredefinition.html: StructureDefinition.type; packages.html: package.json type",
	"snapshot":               "structuredefinition.html: StructureDefinition.snapshot",
	"element":                "elementdefinition.html: StructureDefinition.snapshot.element",
	"path":                   "elementdefinition.html: ElementDefinition.path y .base.path",
	"min":                    "elementdefinition.html: ElementDefinition.min",
	"max":                    "elementdefinition.html: ElementDefinition.max",
	"targetProfile":          "elementdefinition.html: ElementDefinition.type.targetProfile",
	"extension":              "element.html: Element.extension",
	"valueUrl":               "extension-structuredefinition-fhir-type.html: valueUrl",
	"contentReference":       "elementdefinition.html: ElementDefinition.contentReference",
	"contained":              "domainresource.html: DomainResource.contained",
	"id":                     "resource.html: Resource.id",
	"canonical":              "packages.html: package.json canonical",
	"name":                   "packages.html: package.json name",
	"dependencies":           "packages.html: package.json dependencies",
	"fhirVersions":           "packages.html: package.json fhirVersions",
	"fhir-version-list":      "packages.html: package.json fhir-version-list (campo legado del núcleo R4)",
}
