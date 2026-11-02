.PHONY: check fmt vet test ci

check: fmt vet test

# Lo que corre CI: check más las pruebas con los packages oficiales.
ci: check check-packages

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

# Sin red ni packages: los tests con los packages oficiales se omiten (ver
# check-packages). Los semillas de FuzzReadManifest y FuzzReadTgz corren aquí.
test:
	go test ./...

# Descarga los packages oficiales (SHA-256 fijado en scripts/packages.sha256) a
# .cache/packages y corre los tests que los usan: los locks de testdata, el
# grafo real de R4B, el núcleo R4 con fhir-version-list.
.PHONY: fetch check-packages
fetch:
	scripts/fetch-packages.sh

check-packages: fetch
	FHIRPKG_PACKAGES=$(CURDIR)/.cache/packages go test -count=1 -v -run 'TestReal' .

# Fuzzing corto de los dos lectores (FUZZTIME por objetivo).
FUZZTIME ?= 30s
.PHONY: fuzz
fuzz:
	go test -run '^$$' -fuzz '^FuzzReadManifest$$' -fuzztime $(FUZZTIME) .
	go test -run '^$$' -fuzz '^FuzzReadTgz$$' -fuzztime $(FUZZTIME) .

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
