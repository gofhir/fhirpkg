# ADR-001 — El lockfile de packages (D1 y D2)

**Estado:** Propuesta. Las decisiones D1 y D2 las aprobó el usuario (01-11-2026, plan 001 §3.2); pasa a Aceptada cuando CI corra su verificación (`NUCLEO.md` §3: el repo aún no tiene CI). **Plan:** [001](../plans/001-lock-y-lectura.md).

## Contexto

Cada librería de la plataforma carga un conjunto distinto de packages, sin SHA-256 y con reglas propias para la URL repetida y la versión FHIR (`docs/evidence/2026-11-01-d14-medicion-preliminar.md`). El grafo real de R4B trae un segundo núcleo: `uv.extensions.r4b` pide `terminology.r4#7.1.0`, que pide `r4.core#4.0.1` y `uv.extensions.r4#5.2.0` (serie 4.0). Comparten 3.113 canónicos con `r4b.core`, 1.333 de ellos SearchParameter, 18 con la misma versión (`docs/evidence/2026-11-01-graph.md`).

## Decisión

1. **El lock es un archivo JSON** (`fhirpkg.lock`) que escribe `fhirpkg lock` (único paso con red) y lee `Open` sin red. Guarda el núcleo, la versión FHIR, las raíces con versión exacta, cada package con su SHA-256, sus versiones FHIR declaradas, sus dependencias exactas y el camino más corto que lo trajo, y el orden topológico determinista (dependencias primero, empates por `name#version`). Es byte a byte independiente del orden de las raíces.
2. **Un solo núcleo por lock**, declarado: el único núcleo (`type` `Core` o `fhir.core`, este último el del núcleo R4) entre las raíces. Su versión FHIR sale de `fhirVersions` o, si falta, de `fhir-version-list` (campo legado que el núcleo R4 usa en lugar de `fhirVersions`); debe declarar exactamente una.
3. **D1, un núcleo de otra serie traído por dependencia se excluye.** Queda en el lock como excluido, con el camino que lo trajo; no se lee ni hace falta su `.tgz`. Un package que no es núcleo y declara solo otras series se lee únicamente si está en la lista `exceptions` del lock (a mano, con motivo); una excepción que ningún package necesita es error. Otro núcleo de la misma serie es error.
4. **D2, el mismo package en dos versiones es error**, con las dos versiones y quién pide cada una (incluidas las raíces). El comodín de patch (`4.0.x`) se resuelve a la mayor versión semver publicada sin prerelease antes de comparar; el usuario ajusta las raíces.
5. **Fallar cerrado:** una dependencia que el registro no tiene, un ciclo, un rango que no sea versión exacta o comodín de patch (`^`, `~`, `current`, `dev`) son error. Un package sin versión FHIR declarada se lee y queda registrado.
6. **`Open` verifica antes de construir nada:** SHA-256, `package.json` contra el lock (nombre, versión, versiones FHIR, dependencias), serie FHIR contra las excepciones, y un `.tgz` seguro (sin nombres que escapan, enlaces, duplicados, ni entradas, archivos o totales sobre `DefaultLimits`). Extracción en memoria; solo se conservan los JSON de primer nivel de `package/` con `resourceType` y `url` (sin `example/`, `other/`, Bundle sin `url`).

## Consecuencias

- Los conjuntos base son datos (locks), no código: ningún nombre de package en producción.
- El lock se puede revisar en un diff; cambiar un SHA o una excepción es una decisión visible.
- El validador de HL7 coincide con D1: con `-ig` de las raíces de R4B no carga `r4.core` y sí `terminology.r4` y `uv.extensions.r4` (`docs/evidence/2026-11-02-validator-graph.md`); a diferencia de D2, carga además una segunda versión de la terminología por sus defaults.
- La decisión entre canónicos repetidos (publicador antes que copia, semver, versiones que no son semver: 1.231 de 4.577 en el núcleo R4) no está aquí: es del plan 002.

## Alternativas descartadas

- Ver el núcleo ajeno solo en el subgrafo de quien lo trae: más complejo, se puede revisar en el 002.
- D2 con «gana la mayor» o «gana la raíz»: esconde una elección que cambia los canónicos; el error obliga a decidirla.
- Aceptar rangos de npm: la especificación de packages solo define versión exacta y comodín de patch.
