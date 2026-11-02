# Punto de reanudación — gofhir/fhirpkg

Se actualiza al terminar cada paso (carta `NUCLEO.md` §10). **Leer primero al reanudar.**

## Ahora

**Actualizado:** 02-11-2026. **Incremento 001 (lock y lectura verificada): cerrado, sin commit** ([plan](001-lock-y-lectura.md); code review en tres rondas, §10 a §12; cierre en §13). Evidence más reciente: [`2026-11-02-0604.md`](../evidence/2026-11-02-0604.md); mutaciones completas sin caché, todas detectadas: [`2026-11-02-0608-mutations.md`](../evidence/2026-11-02-0608-mutations.md).

El 001 quedó en el commit `63fccc8`. **Incremento activo: 002** ([plan](002-canonicos-y-searchparameter.md)), implementación terminada y verificada por Opus (`make check` verde, cuentas del dorado iguales a §8.2, 98 de 98 mutaciones detectadas); paso 8, code review ronda 1 (Opus) en curso sobre una copia en `/Volumes/dev/tmp/review-fhirpkg002-r1`. **Próximo paso:** verificar los hallazgos de la ronda 1 del plan 002, canónicos y `SearchParameter` con herencia (P-3 y P-4 de la ronda adversarial); (3) ADR-001 y ADR-002 pasan de Propuesta a Aceptadas cuando CI corra su verificación.

**Actualización 02-11-2026 (plan 002):** implementación terminada, sin commit ([plan §9](002-canonicos-y-searchparameter.md), [ADR-003](../adr/003-precedencia-canonicos.md)); evidencia [`2026-11-02-1155.md`](../evidence/2026-11-02-1155.md), mutaciones [`2026-11-02-1155-mutations.md`](../evidence/2026-11-02-1155-mutations.md). **Próximo paso:** code review ronda 1.

**Actualización 02-11-2026 (plan 002, code review ronda 1):** hallazgos corregidos ([plan §10](002-canonicos-y-searchparameter.md)); evidencia y mutaciones en la entrada de abajo. Ronda 2 (Opus) en curso sobre `/Volumes/dev/tmp/review-fhirpkg002-r2`, y en paralelo `MUTATION_FRESH=1 make mutations`. **Próximo paso:** verificar los hallazgos de la ronda 2 y el resultado de las mutaciones sin caché.

**Actualización 02-11-2026 (plan 002, code review ronda 2):** hallazgos corregidos ([plan §11](002-canonicos-y-searchparameter.md)); verificado por Opus: `make check` verde, 145 de 145 mutaciones detectadas (corrida sin caché más 3 anclas reajustadas). Ronda 3 convergida; **002 cerrado** ([plan §12 y §13](002-canonicos-y-searchparameter.md), evidencia [`2026-11-02-1309.md`](../evidence/2026-11-02-1309.md)), sin commit. **Próximo paso:** commit del 002 si el usuario lo pide; luego el plan 003 de fhirpkg (modelo de tipos).
