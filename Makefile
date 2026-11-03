.PHONY: check fmt vet test ci parity

check: fmt vet test

# Lo que corre CI: check más las pruebas con los packages oficiales.
ci: check check-packages

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

# Sin red ni packages: los tests con los packages oficiales se omiten (ver
# check-packages). Los semillas de los Fuzz* corren aquí, y el Context se prueba
# con -race (es inmutable y se usa desde varias goroutines).
test:
	go test ./...
	go test -race -count=1 -run 'TestContextIsConcurrent|TestModelIsConcurrent|TestNormalizeIsConcurrent' .

# Descarga los packages oficiales (SHA-256 fijado en scripts/packages.sha256) a
# .cache/packages y corre los tests que los usan: los locks de testdata, el
# grafo real de R4B, el núcleo R4 con fhir-version-list, y (plan 002) la cascada
# de canónicos con su dorado y la herencia de SearchParameter contra searchindex.
.PHONY: fetch check-packages
fetch:
	scripts/fetch-packages.sh

# TestRealInheritanceMatchesSearchIndex compara con ../go-searchindex y se omite si
# falta; check-packages falla en ese caso (ALLOW_NO_SEARCHINDEX=1 lo permite, p. ej.
# en un CI sin el repo hermano).
check-packages: fetch
	@if [ ! -d ../go-searchindex/definitions/data ] && [ -z "$(ALLOW_NO_SEARCHINDEX)" ]; then \
		echo "check-packages: ../go-searchindex/definitions/data is missing, so the searchindex comparison would be skipped (ALLOW_NO_SEARCHINDEX=1 to allow)"; exit 1; fi
	FHIRPKG_PACKAGES=$(CURDIR)/.cache/packages go test -count=1 -v -run 'TestReal|TestNormalize' .
	$(MAKE) parity

# Paridad del modelo de tipos (plan 003) y del normalizador (plan 004), en el módulo
# parity/ (su propio go.mod:
# gofhir/models, gofhir/fhirpath y ../go-searchindex por replace). Compara las nueve
# tablas de models con las de Context.Model(), el diferencial de searchindex.Extract
# sobre los ejemplos oficiales, el de fhirpath (is, as, type(), children(),
# Analyze) y el benchmark con los dos modelos. Falla ante una diferencia sin
# clasificar en parity/testdata/differences.json. El normalizador (Context.Normalize)
# se compara con UnmarshalResource + Marshal de models sobre los ejemplos oficiales,
# Synthea R4 (scripts/fetch-synthea.sh, SHA-256 fijado, a .cache/synthea), los
# recursos generados de ../go-searchindex (TestWriteGeneratedCorpus, a
# .cache/generated) y entradas a mano; las clases y sus cuentas están en
# parity/testdata/normalizer.json, y el benchmark contra models va en el reporte. Escribe
# docs/evidence/<fecha>-parity.md y el bloque de cifras de
# docs/issues/models-fhirpath-model-gaps.md.
parity: fetch
	scripts/fetch-packages.sh $(CURDIR)/scripts/examples.sha256 >/dev/null
	scripts/fetch-synthea.sh >/dev/null
	cd ../go-searchindex && scripts/fetch-packages.sh >/dev/null && GEN_CORPUS_OUT=$(CURDIR)/.cache/generated \
		SEARCHINDEX_PACKAGES=$$PWD/.cache/packages go test -count=1 -run '^TestWriteGeneratedCorpus$$' .
	cd parity && FHIRPKG_PACKAGES=$(CURDIR)/.cache/packages EXAMPLES_PACKAGES=$(CURDIR)/.cache/packages \
		SYNTHEA_ZIP=$(CURDIR)/.cache/synthea/synthea_sample_data_fhir_r4_nov2021.zip GEN_CORPUS=$(CURDIR)/.cache/generated \
		PARITY_REPORT=$(CURDIR)/docs/evidence/$$(date +%Y-%m-%d)-parity.md PARITY_ISSUE=$(CURDIR)/docs/issues/models-fhirpath-model-gaps.md \
		go test -count=1 -v ./...

# Dorado de Conflicts() por versión (testdata/golden, plan 002): lo reescribe con
# los packages oficiales. Revisar siempre el diff.
.PHONY: golden
golden: fetch
	FHIRPKG_PACKAGES=$(CURDIR)/.cache/packages go test -count=1 -run '^(TestRealConflictsGolden|TestRealResourceTypesFile|TestRealModelTypesGolden)$$' . -update

# Fuzzing corto de los lectores y del canónico y las versiones (FUZZTIME por objetivo).
FUZZTIME ?= 30s
.PHONY: fuzz
fuzz:
	go test -run '^$$' -fuzz '^FuzzReadManifest$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzReadTgz$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzParseCanonical$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzCompareVersions$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzModelFromStructureDefinition$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzNormalize$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzScanner$$' -fuzztime $(FUZZTIME) .

# Reporte del grafo (plan 001, pieza F): para cada raíz candidata de
# scripts/graph-candidates.txt, el grafo, los núcleos ajenos, las versiones
# dobles y los solapes de URL. Escribe docs/evidence/<fecha>-graph.md. Con
# REGISTRY (carpeta o URL) distinto de .cache/packages usa ese registro; por
# defecto lee la caché ya descargada, sin red.
REGISTRY ?= $(CURDIR)/.cache/packages
.PHONY: graph
graph: fetch
	mkdir -p docs/evidence
	go run ./cmd/fhirpkg graph -registry $(REGISTRY) -candidates scripts/graph-candidates.txt -o docs/evidence/$$(date +%Y-%m-%d)-graph.md

# Reporte de evidencia fechado: commit, comandos, duración y RSS.
.PHONY: evidence
evidence:
	scripts/evidence.sh

# Mutaciones de los oráculos (carta §10): inyecta defectos en copias del repo y
# exige que algún test los detecte. Escribe docs/evidence/<fecha>-mutations.md.
# MUTATION_WORKERS (por defecto 3); MUTATION_FRESH=1 ignora la caché.
.PHONY: mutations
mutations:
	scripts/fetch-packages.sh >/dev/null
	python3 -I scripts/mutations.py

# Compara el grafo de cada lock con los packages que validator_cli dice cargar
# (plan 001 §4). Necesita Java y el jar de scripts/validator.sha256 en
# .cache/hl7 (scripts/fetch-hl7.sh lo descarga; VALIDATOR_JAR usa otro, con el
# mismo SHA-256). Un solo JVM a la vez. Escribe docs/evidence/<fecha>-validator-graph.md.
.PHONY: validator-graph
validator-graph:
	scripts/fetch-packages.sh >/dev/null
	scripts/fetch-hl7.sh >/dev/null
	python3 -I scripts/validator_graph.py

# Evidencia de la ADR-004 (D1 a D3): qué nombres de tipo acepta el validador de
# HL7 en `as(T)`. Necesita Java y el jar de scripts/validator.sha256; un solo JVM.
.PHONY: validator-types
validator-types:
	scripts/fetch-packages.sh >/dev/null
	scripts/fetch-hl7.sh >/dev/null
	python3 -I scripts/validator_types.py
