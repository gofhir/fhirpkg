# Decisiones del oráculo de proyección (`scripts/projection_oracle.py`)

Escrito solo desde `docs/plan-005-reglas-consolidadas.md` (manda), las decisiones
D1-D10, el ADR-005, el ADR-003 y la especificación FHIR. Cada ambigüedad que
resolví está aquí con su fuente. Ninguna se obtuvo leyendo código de la
implementación.

## Modelo

1. **Qué SD define un tipo.** Los de `model_oracle.py`: no `logical`, con
   snapshot, `derivation=specialization` o raíz sin `baseDefinition`. Se leen de
   todos los packages del Set (los del lock sin `excluded`), no solo del núcleo;
   ver 25. Dos ganadores con el mismo `type` hacen fallar al oráculo. Fuente:
   structuredefinition.html (`derivation`, `kind`).
2. **Rebanadas.** Se ignoran los elementos con `:` en el `id`
   (elementdefinition.html, slicing): no son elementos del modelo base.
3. **Tipo de un elemento.** (a) `contentReference` → hijos del camino citado en
   el mismo SD; (b) elemento con hijos locales (BackboneElement/Element con
   hijos) → grupo; (c) un solo `type.code` → el SD de ese código: `kind`
   `primitive-type` es hoja, `resource` es recurso anidado, el resto es un tipo
   complejo. Un código que no es un tipo (`http://hl7.org/fhirpath/System.String`,
   que usan `Element.id` y `Resource.id`) es un primitivo hoja. Un `[x]` genera una
   variante por `type.code` (primera letra en mayúscula, elementdefinition.html,
   «Choice of Data Types») con la cardinalidad, `isSummary` e `isModifier` del
   elemento. Un elemento sin hijos y con más de un código no `[x]` hace fallar el
   oráculo (no ocurre en los tres núcleos).
4. **Recurso anidado = elemento cuyo tipo tiene `kind` `resource`** (`Resource`):
   `contained`, `Bundle.entry.resource`, `Bundle.entry.response.outcome`,
   `Bundle.issues` (R5), `Parameters.parameter.resource`. No se escribe ninguno
   de esos nombres.
5. **Parte `_x` de un primitivo.** Es un `Element` (json.html §2.6). Su nivel se
   filtra con los hijos del tipo `Element`, que el oráculo obtiene de la
   `baseDefinition` de un SD primitivo (sin escribir el nombre). Se comprobó
   aparte que `id` y `extension` de los SD primitivos tampoco son resumen (R4:
   20 SD, R5: 21, cero con `isSummary` verdadero), así que usar el `Element` o el
   SD propio del primitivo da lo mismo.
6. **`Resource.id` como primitivo.** Admite `_id`; se trata igual que cualquier
   primitivo.

## Reglas (consolidadas)

7. **Obligatorio = `min>0` del elemento en el SD de su nivel**; en un `[x]`,
   la del elemento (todas sus variantes). Un obligatorio gana a «resumen» para
   decidir si su par `x`/`_x` va entero (regla 4, D8).
8. **Un modificador conservado que es un primitivo solo con `_x`** se filtra
   como `Element` (regla 4 literal: el par entero es solo para obligatorios).
   Solo es observable para modificadores opcionales (por ejemplo `active`
   con solo `_active`): sale vacío y se quita.
9. **`_x` de un primitivo que no se conserva** se quita con su elemento.
10. **Arreglos de primitivos (regla 4 y 5).** Se alinean por posición sobre
    `max(len(x), len(_x))`. Una ocurrencia con `x` nulo (o ausente) cuya parte
    `Element` queda vacía tras filtrar es una ocurrencia vacía y se quita
    (regla 5); las demás ocurrencias siguen alineadas. Si el `_x` resultante es
    solo `null`, desaparece. Fuente: json.html §2.6.2.3.
11. **`false` con `elements`** es modo `_elements`. `true`/`text`/`data` con
    `elements` no nulo es error (D5, «`_summary` distinto de `false`»).
12. **Errores del oráculo.** Un nombre de `_elements` que no es un elemento raíz
    (incluidos `valueQuantity`, `value[x]` y `resourceType`, que no es un
    elemento del SD), una clave que el modelo no tiene, un tipo de recurso
    desconocido, claves duplicadas y constantes no JSON producen la línea
    `{"id":..,"error":".."}`, sin `output`. La regla 9 dice que esas entradas no
    se juzgan: el error solo informa por qué no hay salida.
13. **`data`.** Pierde algo solo si el recurso tiene `text` (se compara el nombre
    `text` en la raíz de cada recurso; `Composition.section.text` no es la raíz).
    Recorre los hijos tipados que pueden contener recursos; los `contained` se
    proyectan con `data` como cualquier recurso anidado (no se quitan, así D9 no
    interviene).
14. **`text` y `_elements` y los anidados.** Si el elemento anidado se conserva,
    va entero y no se marca (no perdió nada). Un `contained` que no se conserva
    sigue D9 con el contenido entero.
15. **Qué cuenta como «perdió algo».** Se quitó un elemento (también uno que
    queda vacío al quitar), una ocurrencia vacía, un `_x`, un `contained` no
    referido, o `text` en `data`; o un anidado (incluido un `contained`
    conservado y proyectado) perdió algo. Una ocurrencia obligatoria que se
    conserva entera por D8 no cuenta como pérdida. Un `contained` que se conserva
    en todos los casos no cuenta.

## D9

16. **Ámbito.** Cada recurso proyectado con `true`, o la raíz de `text` y
    `_elements`, que tenga `contained` y no lo conserve por regla. Las referencias
    se leen de su salida ya proyectada (sin `contained`), de las extensiones
    enteras y de las copias D8, con recorrido tipado. Los recursos anidados de
    otro elemento (por ejemplo `Bundle.entry.resource`) son otro ámbito y no se
    recorren; sus referencias se resuelven en su propio `contained`
    (regla 7 final).
17. **Qué es una referencia local.** `Reference.reference` (primitivo `reference`
    del tipo `Reference`) y todo primitivo de tipo `canonical` cuyo valor empieza
    con `#` y tiene al menos un carácter más. `#` solo no apunta a ningún
    contenido. No se interpreta `#id|versión` ni fragmentos: el id es todo lo que
    sigue al `#`. Fuente: references.html, «Contained Resources».
18. **Cierre.** Se parte de las referencias de la raíz proyectada; cada contenido
    apuntado se proyecta según la regla 6 (`true`: proyectado; `text` y
    `_elements`: entero) y sus referencias (de su salida) se suman hasta no haber
    nuevas; un ciclo termina porque cada índice se procesa una vez. Un `id`
    repetido entre contenidos conserva todos los que lo llevan. Un contenido sin
    `id` string nunca se apunta. Los contenidos conservados salen en su orden
    original y en la posición de `contained` de la entrada.
19. **Referencias dentro de partes `_x`.** Se recorren (pueden traer
    `Extension`s con `valueReference`) con el nodo `Element`.

## SUBSETTED

20. **Dónde va y cuándo.** Después de proyectar un recurso (raíz o anidado) que
    perdió algo, sobre su salida. Es un `Coding` `{system, code[, display]}` al
    final de `meta.tag`. Existe ya un tag igual si algún `Coding` de la salida
    tiene el mismo `system` y `code` (aunque difiera el `display` u otros
    campos): no se agrega nada y no se toca el display.
21. **`meta` o `tag` faltantes** se crean en la posición del orden del modelo
    (índice del elemento en el SD; variantes `[x]` y `_x` usan el índice de su
    elemento; claves desconocidas no cuentan para la inserción). `meta` filtrado
    a vacío con `true` (por ejemplo solo con `extension`) se quita y se vuelve a
    crear con el tag.
22. **Ganador del CodeSystem (ADR-003).** Candidatos: los packages del lock sin
    `excluded`, y de cada uno los `CodeSystem-*.json` con esa URL. Cascada:
    serie (los CodeSystem no declaran `fhirVersion`: no separa), estado,
    publicador (copia = el `canonical` de `package.json` tiene un host distinto
    al de la URL; sin `canonical` no es copia), versión (algoritmo declarado,
    SemVer estricto o fecha FHIR), `meta.lastUpdated`. Sin ganador, el oráculo
    falla. Medido: en R4 y R4B compiten el núcleo R4 (`2018-08-12`, host
    `hl7.org`) y `terminology.r4` (`terminology.hl7.org`): gana la terminología
    por el publicador; en R5 solo hay un candidato. En los tres el display es
    `subsetted`. Sin CodeSystem o sin el concepto, el tag va sin `display`.

## Salida

23. **Serialización.** Compacta, claves en el orden del modelo (ver 26), números con su
    lexema exacto, cadenas en UTF-8 sin escapar salvo `"`, `\`, controles
    (`\n \r \t \b \f`, el resto `\u00XX`) y sustitutos sueltos (`\uDXXX`). No se
    escapan `<`, `>`, `&` ni `/` (JSON, RFC 8259 §7: es opcional).
24. **Contadores de `--rules-report`** (`modo<TAB>regla<TAB>cuenta`): se cuentan
    disparos por modo efectivo (`elements` para `_elements`). Informativo.

## Costo medido (`--bench`, un proceso por versión, un recurso a la vez)

Máquina de desarrollo, Python 3.9.6, `python3 -I`. Solo se cronometra
`project` (que desde el cambio 26 incluye ordenar la entrada cruda al orden del
modelo) + serialización de la salida; no el parseo. Antes del cambio 26 el
banco reordenaba los ejemplos fuera del cronómetro; la tabla es la de después.

| Versión | Recursos | Pares recurso-modo | Errores | ms por par | Total | RSS pico | Carga (modelo + CodeSystem) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| R4 | 5309 | 15927 | 0 | 0,38 | 6,09 s | 423 MB | 0,75 s |
| R4B | 2840 | 8520 | 0 | 0,37 | 3,18 s | 445 MB | 0,89 s |
| R5 | 2822 | 8466 | 0 | 0,52 | 4,37 s | 480 MB | 0,82 s |

Antes del cambio: 0,20 / 0,19 / 0,24 ms por par, RSS 365 / 422 / 450 MB, carga
0,63 / 0,96 / 0,73 s (el ordenamiento entraba antes fuera del cronómetro).

El presupuesto es de menos de 9 ms por par; el uso es ~17 veces menor. El RSS
incluye la lectura en flujo del `.tgz` de ejemplos (recursos grandes).

## Autoprueba

`--self-test`: 83 casos de tabla escritos a mano (cada uno cita regla/D/sección en
su comentario) más el caso de tag sin display, por versión (84 por R4, R4B y R5),
3 casos de línea (clave duplicada, `NaN`, paso directo) y 5 de SD duplicado
(cambio 25). Costo medido: `--self-test` 2,05 s y 283 MB de RSS pico antes, 5,0 s
y 350 MB después (los 5 casos sintéticos construyen 5 modelos por versión; se
reusa la lectura de los packages con una caché local que se suelta al terminar);
corrida por lotes de `scratch/in.jsonl` (R4/R4B/R5): 0,66/0,70/0,71 s y
114/164/182 MB antes, 0,84/0,98/0,90 s y 149/233/217 MB después (la carga lee
los SD de todos los packages del Set). Salida idéntica byte a byte.

## Cambios por la revisión de código (F1, F2)

25. **F2: regla 10 aplicada a los StructureDefinition.** Antes el oráculo leía
    solo el núcleo y un SD repetido sobrescribía al anterior en silencio. Ahora
    lee los SD de todos los packages del Set (los del lock sin `excluded`),
    agrupa por `(resourceType, url)` y, si una clave tiene varios candidatos,
    aplica la cascada del ADR-003 (la misma función que ya resolvía el
    CodeSystem de SUBSETTED, reimplementada desde el texto del ADR). Sin ganador
    falla con `OracleError` («ADR-003: no winner for StructureDefinition <url>»).
    El filtro «define un tipo» se aplica al ganador, no a los candidatos: un
    duplicado sin snapshot o `retired` también compite y su presencia cuenta.
    Casos en la autoprueba (copia sintética del SD de Patient, con
    `gender.isSummary` invertido para verlo): `retired` pierde (paso 2);
    misma status y versión `99.0.0` gana (paso 4); versión mayor pero package
    con `canonical` de otro host pierde (paso 3 antes del 4); `status` `active`
    pero `fhirVersion` `3.0.1` pierde (paso 1 antes del 2); copia idéntica, sin
    ganador, falla (paso 6). En los tres núcleos el SD de Patient ya es `active`,
    así que el caso «`active` le gana a `draft`» no se puede armar con él y no se
    incluye. Razón: sin esto un SD repetido de un IG cambiaba el modelo sin
    aviso (un `retired` repetido de Patient volteaba `gender.isSummary`).
26. **F1: la entrada puede venir en cualquier orden.** Antes `project` conservaba
    el orden de los miembros de la entrada, de modo que había que dársela ya
    normalizada y una corrupción del normalizador se escapaba. Ahora lo primero
    que hace `project` es construir una copia en el orden del modelo (plan 004
    regla 2): `resourceType` primero en un recurso; luego los hijos en el orden
    de los elementos del snapshot sin rebanadas; la variante presente de un
    `[x]` ocupa el lugar del `[x]`; `_x` va justo después de `x` (o solo) y su
    contenido se ordena como `Element`; `Extension` y todos los tipos complejos
    se ordenan con su propio nodo; los recursos anidados (`contained`,
    `Bundle.entry.resource`, `Parameters.parameter.resource`) por su propio
    `resourceType`. Los arreglos conservan su orden (json.html) y los valores
    (lexemas numéricos, cadenas) no se tocan. El rango de cada nombre y de su
    `_nombre` se precalcula por nodo al construir el modelo, así que ordenar un
    objeto es una sola ordenación estable (sin recorridos cuadráticos). Efecto
    lateral deliberado: con `false` y sin `_elements` la salida es
    `Normalize(x)` (regla 1), así que una clave que el modelo no tiene ahora
    también es error en ese modo (regla 9); antes pasaba tal cual. Las
    expectativas de la autoprueba que estaban escritas en un orden que no era
    el del modelo (`Extension`: `id`, `extension`, `url`; `Quantity`: `id`
    antes de `value`; `PlanDefinition`: `title` antes de `status`) se corrigieron
    contra la especificación, no contra la salida. Casos nuevos: `order-raw-*`
    (11) y `err-raw-unknown-member`. El banco ya no reordena los ejemplos: los
    alimenta crudos.
27. **Arreglos de primitivos de distinto largo.** Decisión del dueño (05-11-2026),
    igual en R4, R4B y R5: R5 json.html §2.1.6.4.2 dice «implementations SHALL
    infer null values in the suffix of the shorter array»; R4 y R4B callan y no
    hay regla por versión. Cuando `x` y `_x` son ambos arreglos de largo
    distinto, el ordenamiento de la entrada (26) rellena el más corto con
    `null` al final hasta igualarlos, en todo modo y nivel, y después valen las
    reglas de siempre (4 y 5: el `_x` que queda solo de `null` desaparece en un
    nivel que filtra, se conserva en uno que no; una ocurrencia vacía se quita).
    Si falta `x` o `_x`, o alguno no es arreglo, no se rellena. Casos:
    `array-pad-*` (6, en las tres versiones; la autoprueba pasa a 90 por versión).
28. **Los SD se eligen por `resourceType`, no por el nombre del archivo.** R5
    packages.html §2.1.10.1 no exige archivos con prefijo de tipo; un SD en
    `package/patient.json` era invisible y la cascada del ADR-003 (25) veía un
    conjunto incompleto. Ahora se consideran todos los `.json` directamente bajo
    `package/` (sin subcarpetas; se excluyen `package.json`, el manifiesto, y
    `.index.json`, el índice) y se queda el que tiene `resourceType`
    `StructureDefinition` y `url`. Lo mismo para el CodeSystem de SUBSETTED (22).
    **Prefiltro** (para no parsear ~4.000 JSON por package): se parsea solo si
    los bytes traen `"resourceType" : "StructureDefinition"` (con espacios
    opcionales) o cualquier `\u`, que cubre una clave o valor escritos con
    escapes; es un superconjunto del conjunto correcto (un SD contenido pasa y
    la lectura completa lo descarta), así que no pierde candidatos. Buscar solo
    el texto `StructureDefinition` no sirve: 4449 de 4739 archivos del núcleo R4
    lo citan. Para CodeSystem se busca la URL, con `\u` y `\/` como respaldo.
    Casos nuevos (8 de SD duplicado por versión): SD en `patient.json` que gana
    por versión; el mismo sin ganador (falla); archivo `StructureDefinition-*`
    cuyo `resourceType` es otro y que ganaría por versión (se ignora).
    Costo medido (`scratch/in.jsonl`, tiempo / RSS pico, R4, R4B, R5): antes
    0,92 s 150 MB / 1,05 s 234 MB / 0,97 s 220 MB; después 1,29 s 181 MB /
    1,53 s 251 MB / 1,47 s 252 MB. `--self-test`: 5,0 s 350 MB antes, 7,2 s
    377 MB después. Salida del lote idéntica byte a byte.
