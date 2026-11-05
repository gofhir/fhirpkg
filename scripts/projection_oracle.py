#!/usr/bin/env python3
"""Oráculo independiente de la proyección `_summary` / `_elements` (plan 005).

Escrito solo desde docs/plan-005-reglas-consolidadas.md (que manda), las
decisiones D1-D10 y la especificación FHIR (search.html, json.html,
elementdefinition.html). No comparte código con la implementación.

Uso:
  python3 -I scripts/projection_oracle.py --lock locks/r5.lock --packages packages < in.jsonl > out.jsonl
  python3 -I scripts/projection_oracle.py --self-test [--packages packages]
  python3 -I scripts/projection_oracle.py --bench --lock locks/r5.lock --packages packages

Entrada (una línea JSON): {"id", "mode": "false|true|text|data", "elements": [..]|null,
"resource": "<texto JSON>"}. Salida: {"id", "output": "<texto JSON>"} o, si la
entrada no se puede juzgar, {"id", "error": "..."}.

Los tipos salen solo de los StructureDefinition del package `core` del lock
(los que definen un tipo, como scripts/model_oracle.py). El display de SUBSETTED
sale del CodeSystem v3-ObservationValue de los packages del lock (ADR-003 si
hay varios). Sin modelo de datos escrito a mano: los únicos nombres de FHIR en
el código son los de la prosa permitida por el ADR-005 (ver NAMES más abajo).
Solo biblioteca estándar.
"""
import argparse
import datetime
import json
import os
import platform
import re
import resource
import sys
import tarfile
import time
from urllib.parse import urlparse

# ---------------------------------------------------------------------------
# Nombres escritos (ADR-005, tabla «Nombres escritos»): prosa de HL7 que ningún
# dato marca.
SUBSETTED_SYSTEM = "http://terminology.hl7.org/CodeSystem/v3-ObservationValue"
SUBSETTED_CODE = "SUBSETTED"
N_TEXT, N_ID, N_META, N_CONTAINED = "text", "id", "meta", "contained"
T_EXTENSION, T_REFERENCE, F_REFERENCE, LOCAL = "Extension", "Reference", "reference", "#"
T_CANONICAL = "canonical"  # primitivo cuyo valor `#id` es una referencia local
T_RESOURCE_KEY = "resourceType"  # clave JSON del tipo de recurso (json.html)
T_META_TAG = "tag"
# Se anota que `Resource`/`Element` etc. se reconocen por `kind` del SD, no por nombre.

MISS = object()


# ---------------------------------------------------------------------------
# JSON con el lexema exacto de los números, sin claves duplicadas ni constantes.
class Num(str):
    """Número: conserva su texto original."""
    __slots__ = ()


class OracleError(Exception):
    pass


def _pairs(pairs):
    d = {}
    for k, v in pairs:
        if k in d:
            raise OracleError("duplicate key %r" % k)
        d[k] = v
    return d


def _const(c):
    raise OracleError("non-JSON constant %s" % c)


def parse(text):
    try:
        return json.loads(text, object_pairs_hook=_pairs, parse_float=Num,
                          parse_int=Num, parse_constant=_const)
    except OracleError:
        raise
    except ValueError as e:
        raise OracleError("invalid JSON: %s" % e)


_ESC = {'"': '\\"', "\\": "\\\\", "\n": "\\n", "\r": "\\r", "\t": "\\t",
        "\b": "\\b", "\f": "\\f"}
_ESC_RE = re.compile('[\x00-\x1f"\\\\\ud800-\udfff]')


def _esc(m):
    c = m.group()
    return _ESC.get(c) or "\\u%04x" % ord(c)


def dumps(v):
    out = []
    _w(v, out)
    return "".join(out)


def _w(v, out):
    t = type(v)
    if t is str:
        out.append('"' + _ESC_RE.sub(_esc, v) + '"')
    elif t is Num:
        out.append(v)
    elif t is dict:
        out.append("{")
        first = True
        for k, x in v.items():
            if not first:
                out.append(",")
            first = False
            out.append('"' + _ESC_RE.sub(_esc, k) + '":')
            _w(x, out)
        out.append("}")
    elif t is list:
        out.append("[")
        for i, x in enumerate(v):
            if i:
                out.append(",")
            _w(x, out)
        out.append("]")
    elif v is True:
        out.append("true")
    elif v is False:
        out.append("false")
    elif v is None:
        out.append("null")
    elif t is int:
        out.append(str(v))
    else:
        raise OracleError("cannot serialize %r" % (v,))


# ---------------------------------------------------------------------------
# Modelo: StructureDefinition crudos del package `core` del lock.
def read_lock(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def _root_json(mem):
    """¿Es `mem` un JSON directamente bajo `package/` que puede ser un recurso?
    packages.html (R5 §2.1.10.1): los recursos están en la raíz de `package/` con
    cualquier nombre; `package.json` es el manifiesto y `.index.json` el índice."""
    n = mem.name
    if not mem.isfile() or not n.startswith("package/") or not n.endswith(".json"):
        return False
    base = n[len("package/"):]
    return "/" not in base and base not in ("package.json", ".index.json")


_SD_TYPE = re.compile(rb'"resourceType"\s*:\s*"StructureDefinition"')


def _may_be_sd(raw):
    """Prefiltro de StructureDefinition, completo: un JSON cuyo `resourceType` es
    StructureDefinition trae el par literal (también si es un recurso contenido,
    que la lectura completa descarta), salvo que escriba la clave o el valor con
    secuencias `\\uXXXX`; esos archivos (~1 %) pasan siempre a la lectura completa.
    Mirar solo el nombre `StructureDefinition` no filtra: lo citan casi todos los
    perfiles (medido: 4449 de 4739 archivos del núcleo R4)."""
    return _SD_TYPE.search(raw) is not None or b"\\u" in raw


def _may_contain(raw, needle):
    """Prefiltro de CodeSystem por su URL (needle), completo: pasan también los
    archivos con escapes `\\uXXXX` o `\\/` (la URL tiene barras)."""
    return needle in raw or b"\\u" in raw or b"\\/" in raw


def pkg_file(pkgdir, name, version):
    return os.path.join(pkgdir, "%s-%s.tgz" % (name, version))


def r_ok(k, node):
    """¿`k` es el `_x` de un primitivo del nodo?"""
    kid = node.kids.get(k[1:])
    return kid is not None and kid.kind == "prim"


def _first(t):
    return t[0]


def _upper_first(s):
    return s[:1].upper() + s[1:]


class Kid:
    """Un hijo (o una variante de un [x]) de un nivel."""
    __slots__ = ("name", "elname", "min", "summary", "modifier", "kind", "tname", "nkey", "idx")


class Node:
    __slots__ = ("key", "kids", "order", "elnames", "rr", "rank")


class Model:
    def __init__(self, lock, pkgdir, cache=None):
        # `cache`: {ruta real: (canonical, [SD])}; solo lo usa la autoprueba para no
        # releer los mismos packages en cada modelo sintético.
        name, ver = lock["core"].split("#")
        self.fhir_version = ver
        self.lock = lock
        self.pkgdir = pkgdir
        # Regla 10 / ADR-003: los SD de todos los packages del Set (los no
        # excluidos del lock), agrupados por (resourceType, url); si una clave
        # tiene varios candidatos decide la cascada y sin ganador falla. Solo
        # después se queda uno con los ganadores que definen un tipo.
        m = _SERIES.match(lock.get("fhirVersion", ""))
        core_series = "%s.%s" % (m.group(1), m.group(2)) if m else None
        groups = {}
        for p in lock.get("packages", []):
            if p.get("excluded"):
                continue
            path = pkg_file(pkgdir, p["name"], p["version"])
            if not os.path.exists(path):
                if p["name"] == name:
                    raise OracleError("core package %s is missing" % path)
                continue
            real = os.path.realpath(path)
            if cache is not None and real in cache:
                canonical, found = cache[real]
            else:
                canonical = None
                found = []
                with tarfile.open(path, "r:gz") as tf:
                    for mem in tf:
                        if mem.name == "package/package.json":
                            canonical = json.load(tf.extractfile(mem)).get("canonical")
                        elif _root_json(mem):
                            raw = tf.extractfile(mem).read()
                            if not _may_be_sd(raw):
                                continue
                            sd = json.loads(raw)
                            del raw
                            if type(sd) is dict and sd.get("resourceType") == "StructureDefinition" and "url" in sd:
                                found.append(sd)
                if cache is not None:
                    cache[real] = (canonical, found)
            for sd in found:
                groups.setdefault(sd["url"], []).append(_Cand(sd, p["name"], canonical))
        self.types = {}
        for url, cands in groups.items():
            win = cascade(cands, core_series, "StructureDefinition " + url).res
            if _defines_type(win):
                if win["type"] in self.types:
                    raise OracleError("two definitions for type %s: no winner" % win["type"])
                self.types[win["type"]] = win
        del groups
        # por tipo: elementos sin rebanadas, agrupados por camino del padre
        self.by_parent = {}
        for t, sd in self.types.items():
            bp = {}
            for e in sd["snapshot"]["element"]:
                if ":" in e.get("id", ""):
                    continue
                p = e["path"]
                if "." not in p:
                    continue
                bp.setdefault(p.rsplit(".", 1)[0], []).append(e)
            self.by_parent[t] = bp
        # La parte `_x` de un primitivo es un `Element` (json.html §2.6): el tipo
        # sale de la baseDefinition de un SD primitivo, sin escribir su nombre.
        prim = next(sd for t, sd in sorted(self.types.items()) if sd.get("kind") == "primitive-type")
        base_url = prim["baseDefinition"]
        self.element_type = next(t for t, sd in self.types.items() if sd["url"] == base_url)
        self.nodes = {}
        for t in sorted(self.types):
            self._explore((t, t))
        self._reach_resources()

    # -- construcción de nodos (eager: se recorre todo el grafo una vez)
    def _explore(self, key):
        stack = [key]
        while stack:
            k = stack.pop()
            if k in self.nodes:
                continue
            node = self._build(k)
            for kid in node.kids.values():
                if kid.nkey is not None and kid.nkey not in self.nodes:
                    stack.append(kid.nkey)

    def node(self, key):
        return self.nodes[key]

    def _kind_of_code(self, code):
        sd = self.types.get(code)
        if sd is None:
            return "prim", None  # System.String etc.: hoja
        k = sd.get("kind")
        if k == "primitive-type":
            return "prim", None
        if k == "resource":
            return "res", None
        if code == T_EXTENSION:
            return "ext", (code, code)
        return "cx", (code, code)

    def _build(self, key):
        sdtype, prefix = key
        node = Node()
        node.key = key
        node.kids = {}
        node.order = {}
        node.elnames = set()
        node.rr = False
        node.rank = {}
        self.nodes[key] = node
        bp = self.by_parent[sdtype]
        for idx, e in enumerate(bp.get(prefix, [])):
            path = e["path"]
            name = path.rsplit(".", 1)[1]
            codes = []
            for t in e.get("type", []):
                if t["code"] not in codes:
                    codes.append(t["code"])
            choice = name.endswith("[x]")
            base = name[:-3] if choice else name
            node.order[base] = idx
            node.elnames.add(base)
            variants = []
            if choice:
                for c in codes:
                    variants.append((base + _upper_first(c), c))
            else:
                variants.append((base, None))
            for vname, code in variants:
                node.order[vname] = idx
                kid = Kid()
                kid.name = vname
                kid.elname = base
                kid.min = e["min"]
                kid.summary = bool(e.get("isSummary", False))
                kid.modifier = bool(e.get("isModifier", False))
                kid.idx = idx
                kid.tname = code
                kid.nkey = None
                if choice:
                    kid.kind, kid.nkey = self._kind_of_code(code)
                elif "contentReference" in e:
                    kid.kind, kid.nkey = "cx", (sdtype, e["contentReference"][1:])
                elif path in bp:  # tiene hijos locales: BackboneElement / Element con hijos
                    kid.kind, kid.nkey = "cx", (sdtype, path)
                elif len(codes) == 1:
                    kid.tname = codes[0]
                    kid.kind, kid.nkey = self._kind_of_code(codes[0])
                else:
                    raise OracleError("cannot type %s (codes %s)" % (path, codes))
                if kid.kind == "prim" and kid.tname is None and codes:
                    kid.tname = codes[0]
                node.kids[vname] = kid
                # Rango precalculado (plan 004, regla 2): la variante presente de
                # un [x] ocupa el lugar del [x]; `_x` va justo después de `x`.
                node.rank[vname] = 2 * idx
                node.rank["_" + vname] = 2 * idx + 1
        return node

    def _reach_resources(self):
        for n in self.nodes.values():
            n.rr = any(k.kind == "res" for k in n.kids.values())
        changed = True
        while changed:
            changed = False
            for n in self.nodes.values():
                if n.rr:
                    continue
                for k in n.kids.values():
                    if k.kind in ("cx", "ext") and self.nodes[k.nkey].rr:
                        n.rr = True
                        changed = True
                        break

    def model_order(self, obj, node, root):
        """Copia de `obj` con los miembros en el orden del modelo (plan 004,
        regla 2); los arreglos conservan su orden y los valores no se tocan."""
        rank = node.rank
        items = []
        for k, v in obj.items():
            if root and k == T_RESOURCE_KEY:
                items.append((-1, k, v))
                continue
            r = rank.get(k)
            if r is None:
                raise OracleError("unknown element %r in %s" % (k, node.key[0]))
            items.append((r, k, v))
        items.sort(key=_first)  # estable: empates solo entre variantes de un mismo [x]
        out = {}
        for r, k, v in items:
            if r < 0:
                pass
            elif k[:1] == "_":
                kid = node.kids.get(k[1:])
                if kid is not None and kid.kind == "prim":
                    v = self._ord_val(v, self.nodes[(self.element_type, self.element_type)], False)
            else:
                kid = node.kids[k]
                if kid.kind in ("cx", "ext"):
                    v = self._ord_val(v, self.nodes[kid.nkey], False)
                elif kid.kind == "res":
                    v = self._ord_val(v, None, True)
            out[k] = v
        # Primitivos repetidos: json.html §2.1.6.4.2 (R5), decisión del dueño para
        # las tres versiones: se infieren nulls en el sufijo del arreglo más corto.
        for k in [k for k in out if k[:1] == "_" and r_ok(k, node)]:
            xv, ev = out.get(k[1:]), out[k]
            if type(xv) is list and type(ev) is list and len(xv) != len(ev):
                n = max(len(xv), len(ev))
                out[k[1:]] = xv + [None] * (n - len(xv))
                out[k] = ev + [None] * (n - len(ev))
        return out

    def _ord_val(self, v, node, nested):
        """`node` None y `nested`: recurso anidado, ordenado por su propio tipo."""
        if type(v) is list:
            return [self._ord_one(x, node, nested) for x in v]
        return self._ord_one(v, node, nested)

    def _ord_one(self, x, node, nested):
        if type(x) is not dict:
            return x
        if nested:
            rt = x.get(T_RESOURCE_KEY)
            sd = self.types.get(rt) if type(rt) is str else None
            if sd is None or sd.get("kind") != "resource":
                return x  # el error de tipo lo da la proyección
            return self.model_order(x, self.nodes[(rt, rt)], True)
        return self.model_order(x, node, False)

    def root(self, rtype):
        sd = self.types.get(rtype)
        if sd is None or sd.get("kind") != "resource":
            raise OracleError("unknown resource type %r" % (rtype,))
        return self.nodes[(rtype, rtype)]


def _defines_type(sd):
    if sd.get("kind") == "logical" or "snapshot" not in sd:
        return False
    deriv = sd.get("derivation", "")
    return deriv == "specialization" or (deriv == "" and "baseDefinition" not in sd)


# ---------------------------------------------------------------------------
# ADR-003: ganador del CodeSystem de SUBSETTED
_SEMVER = re.compile(r"^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)"
                     r"(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?"
                     r"(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$")
_FHIRDATE = re.compile(r"^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?$")
_SERIES = re.compile(r"^(\d+)\.(\d+)(?:\.\d+)?")
VERSION_ALGORITHM_SYSTEM = "http://hl7.org/fhir/version-algorithm"


class _Cand:
    def __init__(self, res, pkg, canonical):
        self.res, self.pkg, self.canonical = res, pkg, canonical


def _semver_key(v):
    m = _SEMVER.match(v)
    core = tuple(int(m.group(i)) for i in (1, 2, 3))
    pre = m.group(4)
    if pre is None:
        return core, (1,)
    ids = []
    for p in pre.split("."):
        ids.append((0, int(p), "") if p.isdigit() else (1, 0, p))
    return core, (0, tuple(ids))


def _date_key(v):
    m = _FHIRDATE.match(v)
    return (int(m.group(1)), int(m.group(2) or 1), int(m.group(3) or 1))


def _natural_key(v):
    return [(0, int(x), "") if x.isdigit() else (1, 0, x) for x in re.findall(r"\d+|\D+", v)]


_ALGOS = {
    "semver": (lambda v: bool(_SEMVER.match(v)), _semver_key),
    "integer": (lambda v: v.isdigit(), lambda v: int(v)),
    "alpha": (lambda v: True, lambda v: v),
    "date": (lambda v: bool(_FHIRDATE.match(v)), _date_key),
    "natural": (lambda v: True, _natural_key),
}


def _declared_algo(res):
    c = res.get("versionAlgorithmCoding")
    if isinstance(c, dict) and c.get("system") == VERSION_ALGORITHM_SYSTEM:
        return c.get("code")
    return None


def _step_version(cs):
    if any("versionAlgorithmString" in c.res for c in cs):
        return cs
    versions = [c.res.get("version") for c in cs]
    if any(not isinstance(v, str) or v == "" for v in versions):
        return cs
    algos = {_declared_algo(c.res) for c in cs}
    keyfn = None
    if len(algos) == 1:
        a = next(iter(algos))
        if a in _ALGOS and all(_ALGOS[a][0](v) for v in versions):
            keyfn = _ALGOS[a][1]
    if keyfn is None and all(_SEMVER.match(v) for v in versions):
        keyfn = _semver_key
    if keyfn is None and all(_FHIRDATE.match(v) for v in versions):
        keyfn = _date_key
    if keyfn is None:
        return cs
    best = max(keyfn(v) for v in versions)
    return [c for c, v in zip(cs, versions) if keyfn(v) == best]


def _instant(s):
    if not isinstance(s, str):
        return None
    try:
        return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))
    except ValueError:
        return None


def cascade(cands, core_series, what=SUBSETTED_SYSTEM):
    """Cascada del ADR-003 §1, reimplementada desde su texto."""
    cs = list(cands)
    if len(cs) == 1:
        return cs[0]
    # 1. serie
    def series(c):
        fv = c.res.get("fhirVersion")
        if fv is None:
            return None
        vals = fv if isinstance(fv, list) else [fv]
        out = set()
        for x in vals:
            m = _SERIES.match(x) if isinstance(x, str) else None
            if m:
                out.add("%s.%s" % (m.group(1), m.group(2)))
        return out or None
    keep = [c for c in cs if series(c) is None or core_series in series(c)]
    if keep:
        cs = keep
    if len(cs) == 1:
        return cs[0]
    # 2. status
    rank = {"active": 3, "draft": 2, "retired": 0}
    top = max(rank.get(c.res.get("status"), 1) for c in cs)
    cs = [c for c in cs if rank.get(c.res.get("status"), 1) == top]
    if len(cs) == 1:
        return cs[0]
    # 3. publicador
    def is_copy(c):
        uh = (urlparse(c.res.get("url", "")).hostname or "").lower()
        ph = (urlparse(c.canonical or "").hostname or "").lower()
        return bool(uh) and bool(ph) and uh != ph
    copies = [is_copy(c) for c in cs]
    if any(copies) and not all(copies):
        cs = [c for c, k in zip(cs, copies) if not k]
    if len(cs) == 1:
        return cs[0]
    # 4. versión
    cs = _step_version(cs)
    if len(cs) == 1:
        return cs[0]
    # 5. meta.lastUpdated
    ts = [_instant((c.res.get("meta") or {}).get("lastUpdated")) for c in cs]
    if all(t is not None for t in ts):
        best = max(ts)
        cs = [c for c, t in zip(cs, ts) if t == best]
    if len(cs) == 1:
        return cs[0]
    raise OracleError("ADR-003: no winner for %s among %d candidates"
                      % (what, len(cs)))


def find_subsetted_display(lock, pkgdir):
    """Display de SUBSETTED en el CodeSystem ganador; None si no hay CodeSystem
    o concepto."""
    core_series = None
    m = _SERIES.match(lock.get("fhirVersion", ""))
    if m:
        core_series = "%s.%s" % (m.group(1), m.group(2))
    needle = SUBSETTED_SYSTEM.encode()
    cands = []
    for p in lock.get("packages", []):
        if p.get("excluded"):
            continue
        path = pkg_file(pkgdir, p["name"], p["version"])
        if not os.path.exists(path):
            continue
        canonical = None
        found = []
        with tarfile.open(path, "r:gz") as tf:
            for mem in tf:
                if mem.name == "package/package.json":
                    canonical = json.load(tf.extractfile(mem)).get("canonical")
                elif _root_json(mem):
                    raw = tf.extractfile(mem).read()
                    if _may_contain(raw, needle):
                        d = json.loads(raw)
                        if (type(d) is dict and d.get("resourceType") == "CodeSystem"
                                and d.get("url") == SUBSETTED_SYSTEM):
                            found.append(d)
        for d in found:
            cands.append(_Cand(d, p["name"], canonical))
    if not cands:
        return None
    win = cascade(cands, core_series)

    def walk(concepts):
        for c in concepts:
            if c.get("code") == SUBSETTED_CODE:
                return c.get("display")
            r = walk(c.get("concept", []))
            if r is not None:
                return r
        return None
    return walk(win.res.get("concept", []))


# ---------------------------------------------------------------------------
# Proyección
class Projector:
    def __init__(self, model, display, counts=None):
        self.model = model
        self.display = display
        self.counts = counts if counts is not None else {}
        self.cur = "-"
        self.element_node = model.nodes[(model.element_type, model.element_type)]

    def hit(self, rule):
        k = (self.cur, rule)
        self.counts[k] = self.counts.get(k, 0) + 1

    # -- API
    def project(self, mode, elements, resource_obj):
        if type(resource_obj) is not dict or type(resource_obj.get(T_RESOURCE_KEY)) is not str:
            raise OracleError("input is not a resource")
        if elements is not None:
            if mode != "false":
                raise OracleError("_summary together with _elements")
            eff = "elements"
            node = self.model.root(resource_obj[T_RESOURCE_KEY])
            names = set()
            for n in elements:
                if n not in node.elnames:
                    raise OracleError("unknown element name %r" % (n,))
                names.add(n)
        else:
            if mode not in ("false", "true", "text", "data"):
                raise OracleError("unknown mode %r" % (mode,))
            eff, names = mode, None
        # Entrada cruda: el orden de sus miembros es cualquiera; todo lo que sigue
        # trabaja sobre el orden del modelo (plan §13.2.1).
        rnode = self.model.root(resource_obj[T_RESOURCE_KEY])
        resource_obj = self.model.model_order(resource_obj, rnode, True)
        self.cur = eff
        if eff == "false":
            return resource_obj
        out, _ = self.resource(resource_obj, eff, names)
        return out

    def resource(self, obj, mode, names):
        rt = obj.get(T_RESOURCE_KEY)
        if type(rt) is not str:
            raise OracleError("nested resource without resourceType")
        node = self.model.root(rt)
        if mode == "data":
            out, lost = self.data_walk(obj, node, True)
        else:
            out, lost = self.level(obj, node, mode, names, True)
        if lost:
            out = self.add_tag(out, node)
        return out, lost

    # -- data
    def data_walk(self, obj, node, root):
        out = {}
        lost = False
        for k, v in obj.items():
            if root and k == T_RESOURCE_KEY:
                out[k] = v
                continue
            base = k[1:] if k[:1] == "_" else k
            kid = node.kids.get(base)
            if kid is None:
                raise OracleError("unknown element %r in %s" % (k, node.key[0]))
            if root and k == N_TEXT:
                lost = True
                self.hit("R1.data_text_removed")
                continue
            if k == base and kid.kind == "res":
                occs = v if type(v) is list else [v]
                res = []
                for o in occs:
                    if type(o) is not dict:
                        raise OracleError("nested resource is not an object")
                    self.hit("R6.nested_projected")
                    r, l = self.resource(o, "data", None)
                    res.append(r)
                    lost = lost or l
                out[k] = res if type(v) is list else res[0]
            elif k == base and kid.kind in ("cx", "ext") and self.model.nodes[kid.nkey].rr:
                child = self.model.nodes[kid.nkey]
                occs = v if type(v) is list else [v]
                res = []
                for o in occs:
                    if type(o) is not dict:
                        raise OracleError("%s is not an object" % k)
                    r, l = self.data_walk(o, child, False)
                    res.append(r)
                    lost = lost or l
                out[k] = res if type(v) is list else res[0]
            else:
                out[k] = v
        return out, lost

    # -- true / text / elements
    def retained(self, kid, mode, names, root):
        """(¿se conserva?, motivo)"""
        if mode == "true":
            if kid.min > 0:
                return True, "required"
            if kid.summary:
                return True, "summary"
            if kid.modifier:
                return True, "modifier"
            return False, None
        # text / elements: solo la raíz filtra
        if mode == "text":
            if kid.elname in (N_TEXT, N_ID, N_META):
                return True, "named"
        else:
            if kid.elname in names or kid.elname in (N_ID, N_META):
                return True, "named"
        if kid.min > 0:
            return True, "required"
        if kid.modifier:
            return True, "modifier"
        return False, None

    def level(self, obj, node, mode, names, root):
        out = {}
        lost = False
        done = {}
        slot = False
        for k, v in obj.items():
            if root and k == T_RESOURCE_KEY:
                out[k] = v
                continue
            base = k[1:] if k[:1] == "_" else k
            kid = node.kids.get(base)
            if kid is None:
                raise OracleError("unknown element %r in %s" % (k, node.key[0]))
            keep, why = self.retained(kid, mode, names, root)
            if not keep:
                if root and k == N_CONTAINED:
                    slot = True
                    out[k] = MISS  # posición reservada para D9
                    continue
                lost = True
                self.hit("R2.dropped")
                continue
            if mode != "true":
                self.hit("R2.kept_whole_" + why)
                if kid.kind == "res":
                    self.hit("R6.nested_whole")
                out[k] = v
                continue
            self.hit("R2.kept_" + why)
            if why == "modifier":
                self.hit("D10.modifier_kept")
            kind = kid.kind
            if kind == "prim":
                if base not in done:
                    done[base] = self.prim_pair(obj, base, kid)
                xo, eo, l = done[base]
                if l:
                    lost = True
                if k == base:
                    if xo is not MISS:
                        out[k] = xo
                elif eo is not MISS:
                    out[k] = eo
            elif kind == "ext":
                self.hit("R3.extension_whole")
                out[k] = v
            elif kind == "res":
                if k != base:
                    raise OracleError("underscore key on a resource element")
                occs = v if type(v) is list else [v]
                res = []
                for o in occs:
                    if type(o) is not dict:
                        raise OracleError("nested resource is not an object")
                    self.hit("R6.nested_projected")
                    r, l = self.resource(o, "true", None)
                    res.append(r)
                    lost = lost or l
                out[k] = res if type(v) is list else res[0]
            else:  # cx
                if k != base:
                    raise OracleError("underscore key on a non-primitive element %r" % k)
                child = self.model.nodes[kid.nkey]
                occs = v if type(v) is list else [v]
                res = []
                for o in occs:
                    if type(o) is not dict:
                        raise OracleError("%s is not an object" % k)
                    r, l = self.level(o, child, "true", None, False)
                    if not r:
                        if kid.min > 0:
                            self.hit("R5.mandatory_occurrence_whole")
                            res.append(o)
                        else:
                            self.hit("R5.empty_removed")
                            lost = True
                        continue
                    res.append(r)
                    lost = lost or l
                if res:
                    out[k] = res if type(v) is list else res[0]
        if slot:
            lost = self.contained_closure(obj, out, node, mode, lost)
        return out, lost

    def prim_pair(self, obj, base, kid):
        xv = obj.get(base, MISS)
        ev = obj.get("_" + base, MISS)
        if kid.min > 0:
            self.hit("R4.mandatory_pair_whole")
            return xv, ev, False
        is_list = type(xv) is list or type(ev) is list
        xs = xv if type(xv) is list else ([] if xv is MISS else [xv])
        es = ev if type(ev) is list else ([] if ev is MISS else [ev])
        n = max(len(xs), len(es))
        lost = False
        kx, ke = [], []
        for i in range(n):
            xi = xs[i] if i < len(xs) else None
            ei = es[i] if i < len(es) else None
            fe = None
            if ei is not None:
                if type(ei) is not dict:
                    raise OracleError("_%s part is not an object" % base)
                self.hit("R4.underscore_filtered")
                fe, l = self.level(ei, self.element_node, "true", None, False)
                if l:
                    lost = True
                if not fe:
                    fe = None
            if xi is None and fe is None:
                lost = True
                self.hit("R5.empty_removed")
                continue
            kx.append(xi)
            ke.append(fe)
        has_e = any(e is not None for e in ke)
        if is_list and has_e and any(e is None for e in ke):
            self.hit("R4.array_aligned_with_null")
        if is_list and ev is not MISS and not has_e and kx:
            self.hit("R4.underscore_array_removed")
        if not kx:
            return MISS, MISS, lost
        if is_list:
            return kx, (ke if has_e else MISS), lost
        xo = kx[0] if xv is not MISS else MISS
        eo = ke[0] if has_e else MISS
        return xo, eo, lost

    # -- D9: contained referido
    def scan(self, obj, node, refs):
        for k, v in obj.items():
            if k == T_RESOURCE_KEY:
                continue
            if k[:1] == "_":
                parts = v if type(v) is list else [v]
                for p in parts:
                    if type(p) is dict:
                        self.scan(p, self.element_node, refs)
                continue
            kid = node.kids.get(k)
            if kid is None:
                continue
            if kid.kind == "prim":
                if kid.tname == T_CANONICAL or (node.key[0] == T_REFERENCE and k == F_REFERENCE):
                    for s in (v if type(v) is list else [v]):
                        if type(s) is str and s[:1] == LOCAL and len(s) > 1:
                            refs.add(s[1:])
            elif kid.kind in ("cx", "ext"):
                child = self.model.nodes[kid.nkey]
                for o in (v if type(v) is list else [v]):
                    if type(o) is dict:
                        self.scan(o, child, refs)
            # res: otro ámbito

    def contained_closure(self, obj, out, node, mode, lost):
        contained = obj[N_CONTAINED]
        if type(contained) is not list:
            raise OracleError("contained is not an array")
        del out[N_CONTAINED]
        by_id = {}
        for i, c in enumerate(contained):
            if type(c) is not dict:
                raise OracleError("contained entry is not an object")
            if type(c.get(N_ID)) is str:
                by_id.setdefault(c[N_ID], []).append(i)
        refs = set()
        self.scan(out, node, refs)
        kept = {}
        todo = list(refs)
        seen = set()
        while todo:
            rid = todo.pop()
            if rid in seen:
                continue
            seen.add(rid)
            for i in by_id.get(rid, []):
                if i in kept:
                    continue
                c = contained[i]
                if mode == "true":
                    self.hit("R7.contained_kept")
                    proj, l = self.resource(c, "true", None)
                    lost = lost or l
                else:
                    self.hit("R7.contained_kept")
                    proj = c
                kept[i] = proj
                sub = set()
                self.scan(proj, self.model.root(c[T_RESOURCE_KEY]), sub)
                todo.extend(sub)
        if len(kept) < len(contained):
            lost = True
            self.hit("R7.contained_dropped")
        if kept:
            res = [kept[i] for i in sorted(kept)]
            # reinsertar `contained` en su posición original
            items = []
            placed = False
            for k in obj:
                if k == N_CONTAINED:
                    items.append((k, res))
                    placed = True
                elif k in out:
                    items.append((k, out[k]))
            if not placed:
                items.append((N_CONTAINED, res))
            out.clear()
            out.update(items)
        return lost

    # -- SUBSETTED
    def add_tag(self, out, node):
        coding = {"system": SUBSETTED_SYSTEM, "code": SUBSETTED_CODE}
        if self.display is not None:
            coding["display"] = self.display
        meta = out.get(N_META)
        meta_node = self.model.nodes[node.kids[N_META].nkey]
        if type(meta) is dict:
            tag = meta.get(T_META_TAG)
            if type(tag) is list:
                for t in tag:
                    if (type(t) is dict and t.get("system") == SUBSETTED_SYSTEM
                            and t.get("code") == SUBSETTED_CODE):
                        self.hit("R8.tag_already_present")
                        return out
                nm = dict(meta)
                nm[T_META_TAG] = tag + [coding]
            else:
                nm = _insert_ordered(meta, T_META_TAG, [coding], meta_node.order)
            self.hit("R8.tag_added")
            out[N_META] = nm
            return out
        self.hit("R8.tag_added")
        self.hit("R8.meta_created")
        return _insert_ordered(out, N_META, {T_META_TAG: [coding]}, node.order)


def _insert_ordered(d, key, val, order):
    ki = order[key]
    items = list(d.items())
    pos = len(items)
    for i, (k, _) in enumerate(items):
        b = k[1:] if k[:1] == "_" else k
        j = order.get(b)
        if j is not None and j > ki:
            pos = i
            break
    items.insert(pos, (key, val))
    return dict(items)


# ---------------------------------------------------------------------------
# Ejecución
def load_projector(lock_path, pkgdir, counts=None):
    lock = read_lock(lock_path)
    model = Model(lock, pkgdir)
    display = find_subsetted_display(lock, pkgdir)
    return Projector(model, display, counts)


def run_line(proj, line):
    rid = None
    try:
        rec = parse(line)
        rid = rec.get("id")
        res = parse(rec["resource"])
        out = proj.project(rec["mode"], rec.get("elements"), res)
        return dumps({"id": rid, "output": dumps(out)})
    except (OracleError, KeyError, TypeError, AttributeError) as e:
        return dumps({"id": rid, "error": "%s: %s" % (type(e).__name__, e)})


def peak_rss_mb():
    r = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
    return r / 1048576.0 if platform.system() == "Darwin" else r / 1024.0


def write_report(counts, dest):
    lines = ["mode\trule\tcount"]
    for (m, r), n in sorted(counts.items()):
        lines.append("%s\t%s\t%d" % (m, r, n))
    text = "\n".join(lines) + "\n"
    if dest:
        with open(dest, "w", encoding="utf-8") as f:
            f.write(text)
    else:
        sys.stderr.write(text)


# ---------------------------------------------------------------------------
# Medición de costo sobre el package de ejemplos (entrada cruda, sin reordenar)
def bench(lock_path, pkgdir):
    lock = read_lock(lock_path)
    t0 = time.time()
    proj = load_projector(lock_path, pkgdir)
    t_load = time.time() - t0
    name, ver = lock["core"].split("#")
    ex = pkg_file(pkgdir, name.replace(".core", ".examples"), ver)
    nres = pairs = errors = 0
    first_err = {}
    dt = 0.0
    with tarfile.open(ex, "r:gz") as tf:
        for m in tf:
            n = m.name
            if not m.isfile() or not n.endswith(".json") or "/" in n[len("package/"):]:
                continue
            if n in ("package/package.json",) or n.endswith(".index.json"):
                continue
            try:
                d = parse(tf.extractfile(m).read().decode("utf-8"))
            except (OracleError, UnicodeDecodeError):
                continue
            rt = d.get(T_RESOURCE_KEY) if type(d) is dict else None
            if rt not in proj.model.types or proj.model.types[rt].get("kind") != "resource":
                continue
            nres += 1
            for mode in ("true", "text", "data"):
                pairs += 1
                t0 = time.perf_counter()
                try:
                    dumps(proj.project(mode, None, d))
                except (OracleError, KeyError, TypeError, AttributeError) as e:
                    errors += 1
                    first_err.setdefault(str(e)[:100], rt)
                dt += time.perf_counter() - t0
    sys.stderr.write(
        "%s: model+codesystem load %.2fs; %d resources, %d resource-mode pairs, %d errors; "
        "%.3f ms/pair; total %.2fs; peak RSS %.0f MB\n"
        % (lock["core"], t_load, nres, pairs, errors, dt * 1000.0 / max(pairs, 1), dt, peak_rss_mb()))
    for m, r in list(first_err.items())[:5]:
        sys.stderr.write("  error sample (%s): %s\n" % (r, m))


# ---------------------------------------------------------------------------
# Autoprueba: casos escritos a mano desde la especificación y las reglas
# consolidadas (docs/plan-005-reglas-consolidadas.md, §13.6), NO obtenidos
# corriendo este código. Cada caso cita su fuente en un comentario.
SYS = SUBSETTED_SYSTEM
T = {"system": SYS, "code": "SUBSETTED", "display": "subsetted"}
TX = {"status": "generated", "div": "<div xmlns=\"http://www.w3.org/1999/xhtml\">x</div>"}
EXT = {"url": "http://x/e", "valueString": "v"}
DAR = {"url": "http://hl7.org/fhir/StructureDefinition/data-absent-reason", "valueCode": "unknown"}
ALL = ("r4", "r4b", "r5")
CASES = []


class PerV(dict):
    """Resultado esperado que depende de la versión."""


def case(name, cite, vers, mode, elements, inp, exp):
    CASES.append((name, cite, vers, mode, elements, inp, exp))


def _ev(x, v):
    if callable(x):
        x = x(v)
    if isinstance(x, PerV):
        x = x[v]
    return x


def tagged(meta_extra=None):
    m = dict(meta_extra or {})
    m["tag"] = list(m.get("tag", [])) + [T]
    return m


P = {"resourceType": "Patient", "id": "p1",
     "meta": {"versionId": "1", "lastUpdated": "2020-01-01T00:00:00Z", "profile": ["http://x/p"]},
     "text": TX, "extension": [EXT],
     "identifier": [{"use": "official", "system": "http://s", "value": "123"}],
     "active": True,
     "name": [{"id": "n1", "extension": [EXT], "use": "official", "family": "Smith",
               "given": ["John", "Q"], "period": {"start": "2000"}}],
     "gender": "male", "birthDate": "1974-12-25", "maritalStatus": {"text": "M"}}


def _p(**over):
    d = json.loads(json.dumps(P))
    for k, v in over.items():
        if v is None:
            d.pop(k, None)
        else:
            d[k] = v
    return d


# -- Regla 1, 2 (true): isSummary por nivel, D1, D10, D8 --------------------
# search.html §3.1.1.5.8 (R4/R4B) y §3.2.1.7.5 (R5), regla 1-2: `true` conserva
# isSummary en cada nivel; Element.id/extension no lo son (D1).
case("true-summary-per-level", "regla 1-2, D1", ALL, "true", None, P, {
    "resourceType": "Patient", "id": "p1", "meta": tagged({"versionId": "1", "lastUpdated": "2020-01-01T00:00:00Z", "profile": ["http://x/p"]}),
    "identifier": P["identifier"], "active": True,
    "name": [{"use": "official", "family": "Smith", "given": ["John", "Q"], "period": {"start": "2000"}}],
    "gender": "male", "birthDate": "1974-12-25"})

# regla 2 (text): text, id, meta + modificadores con valor (active es modificador, D10)
case("text-mode", "regla 2, D10", ALL, "text", None, P, {
    "resourceType": "Patient", "id": "p1", "meta": tagged({"versionId": "1", "lastUpdated": "2020-01-01T00:00:00Z", "profile": ["http://x/p"]}),
    "text": TX, "active": True})

# regla 1 (data): quita text de la raíz; no filtra nada más
_d = _p(text=None)
_d["meta"] = tagged(P["meta"])
case("data-mode", "regla 1", ALL, "data", None, P, _d)

# regla 1 (false): identidad
case("false-identity", "regla 1", ALL, "false", None, P, P)

# regla 2 (_elements): nombres pedidos + id + meta + modificadores (D4); entero
case("elements-basic", "regla 2, D4", ALL, "false", ["name"], P, {
    "resourceType": "Patient", "id": "p1", "meta": tagged(P["meta"]), "active": True, "name": P["name"]})

case("elements-empty-list", "regla 2, D4", ALL, "false", [], P, {
    "resourceType": "Patient", "id": "p1", "meta": tagged(P["meta"]), "active": True})

# -- Regla 3: Extension entera (D1) -------------------------------------------
_ext = {"id": "x1", "url": "http://x/m", "extension": [{"url": "http://x/n", "valueString": "s"}],
        "valueReference": {"reference": "Patient/1", "display": "d"}}
# Orden del modelo de Extension (plan 004 regla 2; json.html): id, extension, url, value[x]
_extm = {"id": "x1", "extension": [{"url": "http://x/n", "valueString": "s"}], "url": "http://x/m",
         "valueReference": {"reference": "Patient/1", "display": "d"}}
case("extension-whole-modifier", "regla 3, D1, D10 (R4: isSummary=false, R5: true)", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "extension": [EXT], "modifierExtension": [_ext], "gender": "male"},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "modifierExtension": [_extm], "gender": "male"})
case("modifier-text-mode", "regla 2, D10", ALL, "text", None,
     {"resourceType": "Patient", "id": "p", "implicitRules": "http://x/r", "language": "en", "text": TX,
      "modifierExtension": [_ext], "gender": "male"},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "implicitRules": "http://x/r", "text": TX,
      "modifierExtension": [_extm]})
case("modifier-elements", "regla 2, D4", ALL, "false", ["gender"],
     {"resourceType": "Patient", "id": "p", "implicitRules": "http://x/r", "language": "en", "modifierExtension": [_ext],
      "active": False, "name": [{"family": "F"}], "gender": "male"},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "implicitRules": "http://x/r", "modifierExtension": [_extm],
      "active": False, "gender": "male"})

# -- Regla 4: primitivos y su parte _x ---------------------------------------
# json.html §2.6 (Representation of primitive elements): `_x` es un Element
case("underscore-dropped-when-filtering", "regla 4", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "gender": "male", "_gender": {"id": "g"},
      "birthDate": "2000", "_birthDate": {"extension": [EXT]}},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "gender": "male", "birthDate": "2000"})
case("underscore-only-optional-summary", "regla 4", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "_birthDate": {"extension": [EXT]}, "gender": "female"},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "gender": "female"})
case("underscore-only-not-summary", "regla 4", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "_language": {"extension": [EXT]}, "gender": "female"},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "gender": "female"})
case("underscore-kept-whole-in-elements", "regla 4 (nivel que no filtra)", ALL, "false", ["birthDate"],
     {"resourceType": "Patient", "id": "p", "_birthDate": {"id": "b", "extension": [EXT]}, "gender": "male"},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "_birthDate": {"id": "b", "extension": [EXT]}})
# D8 (ampliación 04-11-2026): primitivo obligatorio: el par entero (id y extension incluidos)
_obs_pair = {"resourceType": "Observation", "id": "o", "status": "final", "_status": {"id": "s1", "extension": [DAR]},
             "code": {"text": "c"}, "subject": {"reference": "Patient/1"}}
case("mandatory-pair-true", "regla 4, D8", ALL, "true", None, _obs_pair, _obs_pair)
case("mandatory-underscore-only-true", "regla 4, D8 (ampliación)", ALL, "true", None,
     {"resourceType": "Observation", "id": "o", "_status": {"id": "s1", "extension": [DAR]}, "code": {"text": "c"}},
     {"resourceType": "Observation", "id": "o", "_status": {"id": "s1", "extension": [DAR]}, "code": {"text": "c"}})
case("mandatory-pair-text", "regla 4, D8", ALL, "text", None,
     dict(_obs_pair, effectiveDateTime="2020"),
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]}, "status": "final", "_status": {"id": "s1", "extension": [DAR]},
      "code": {"text": "c"}})
case("mandatory-pair-elements", "regla 4, D8", ALL, "false", ["subject"], _obs_pair, _obs_pair)
case("mandatory-array-pair-whole", "regla 4, D8 (arreglo)", ALL, "true", None,
     {"resourceType": "CapabilityStatement", "id": "c", "status": "active", "kind": "instance",
      "format": ["json", "xml"], "_format": [None, {"id": "f2"}]},
     {"resourceType": "CapabilityStatement", "id": "c", "status": "active", "kind": "instance",
      "format": ["json", "xml"], "_format": [None, {"id": "f2"}]})
# json.html §2.6.2.3: arreglos de primitivos con null de alineación
case("array-underscore-only-null-removed", "regla 4, 5", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B"], "_given": [None, {"extension": [EXT]}]}]},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "name": [{"given": ["A", "B"]}]})
case("array-null-occurrence-removed-realigned", "regla 4, 5", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": [None, "B"], "_given": [{"id": "z"}, None]}]},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "name": [{"given": ["B"]}]})
case("array-null-occurrence-removed-middle", "regla 4, 5", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", None, "C"], "_given": [None, {"id": "z"}, {"id": "y"}]}]},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "name": [{"given": ["A", "C"]}]})
case("array-underscore-untouched-when-not-filtering", "regla 4", ALL, "data", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B"], "_given": [None, None]}]},
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B"], "_given": [None, None]}]})
case("array-underscore-whole-in-elements", "regla 2, 4", ALL, "false", ["name"],
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", None], "_given": [None, {"id": "z"}]}]},
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", None], "_given": [None, {"id": "z"}]}]})

# -- Regla 5: vacíos y D8 por ocurrencia ------------------------------------
case("empty-removed-up", "regla 5", ALL, "true", None,
     {"resourceType": "Bundle", "id": "b", "type": "collection", "entry": [{"id": "e1", "extension": [EXT]}]},
     {"resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "collection"})
case("empty-removed-patient", "regla 5", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "name": [{"extension": [EXT]}], "identifier": [{"id": "i"}], "active": True},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "active": True})
# D8, aclaración por ocurrencia: Provenance.target (1..*)
_dar_ref = {"extension": [DAR]}
case("mandatory-occurrence-whole", "regla 5, D8 por ocurrencia", ALL, "true", None,
     {"resourceType": "Provenance", "id": "pv", "target": [_dar_ref, {"id": "z", "reference": "Patient/p"}],
      "recorded": "2020-01-01T00:00:00Z", "agent": [{"who": {"reference": "Practitioner/1"}}]},
     {"resourceType": "Provenance", "id": "pv", "meta": {"tag": [T]}, "target": [_dar_ref, {"reference": "Patient/p"}],
      "recorded": "2020-01-01T00:00:00Z", "agent": [{"who": {"reference": "Practitioner/1"}}]})
case("mandatory-occurrence-whole-no-loss", "regla 5, 8", ALL, "true", None,
     {"resourceType": "Provenance", "id": "pv", "target": [{"reference": "Patient/p"}],
      "recorded": "2020-01-01T00:00:00Z", "agent": [{"who": _dar_ref}]},
     {"resourceType": "Provenance", "id": "pv", "target": [{"reference": "Patient/p"}],
      "recorded": "2020-01-01T00:00:00Z", "agent": [{"who": _dar_ref}]})
case("optional-occurrence-empty-removed", "regla 5", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "identifier": [{"extension": [EXT]}, {"value": "1"}]},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "identifier": [{"value": "1"}]})
# D8 (obligatorios no resumen en la raíz): R4 Provenance.agent no es resumen y es 1..1; R5 sí es resumen
case("mandatory-backbone-filtered", "regla 2, D8", ALL, "true", None,
     {"resourceType": "Provenance", "id": "pv", "target": [{"reference": "Patient/p"}], "recorded": "2020-01-01T00:00:00Z",
      "agent": [{"id": "a", "role": [{"text": "r"}], "who": {"reference": "Practitioner/1"}}]},
     {"resourceType": "Provenance", "id": "pv", "meta": {"tag": [T]}, "target": [{"reference": "Patient/p"}],
      "recorded": "2020-01-01T00:00:00Z", "agent": [{"who": {"reference": "Practitioner/1"}}]})
# D8 en `text`: R4 Provenance.recorded es 1..1; R5 es 0..1 (se pierde y lleva tag)
_prov = {"resourceType": "Provenance", "id": "pv", "target": [{"reference": "Patient/p"}], "recorded": "2020-01-01T00:00:00Z",
         "agent": [{"role": [{"text": "r"}], "who": {"reference": "Practitioner/1"}}]}
case("text-mandatory-differs-by-version", "regla 2, D8 (R4 recorded 1..1, R5 0..1)", ALL, "text", None, _prov,
     PerV(r4=_prov, r4b=_prov, r5={"resourceType": "Provenance", "id": "pv", "meta": {"tag": [T]},
                                   "target": [{"reference": "Patient/p"}],
                                   "agent": [{"role": [{"text": "r"}], "who": {"reference": "Practitioner/1"}}]}))
# D1: Signature.data/sigFormat no son resumen (Bundle.signature sí)
case("datatype-level-isSummary", "regla 2, D1", ALL, "true", None,
     {"resourceType": "Bundle", "id": "b", "type": "collection",
      "signature": {"type": [{"system": "s", "code": "c"}], "when": "2020-01-01T00:00:00Z", "who": {"reference": "Practitioner/1"},
                    "sigFormat": "application/jws", "data": "AAAA"}},
     {"resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "collection",
      "signature": {"type": [{"system": "s", "code": "c"}], "when": "2020-01-01T00:00:00Z", "who": {"reference": "Practitioner/1"}}})

# -- Choice [x] ---------------------------------------------------------------
# regla 2: un [x] se juzga por su elemento y se conserva por la variante presente
def _medreq(v):
    med = {"medicationCodeableConcept": {"text": "m"}} if v != "r5" else {"medication": {"concept": {"text": "m"}}}
    d = {"resourceType": "MedicationRequest", "id": "mr", "status": "active", "intent": "order", "doNotPerform": False}
    d.update(med)
    d.update({"subject": {"reference": "Patient/1"}, "authoredOn": "2020"})
    return d
def _medreq_text(v):
    d = _medreq(v)
    d.pop("authoredOn")
    out = {}
    for k, x in d.items():
        out[k] = x
        if k == "id":
            out["meta"] = {"tag": [T]}
    return out
case("choice-mandatory-text", "regla 2 (obligatorio [x] por la variante presente), D10 doNotPerform",
     ALL, "text", None, _medreq, _medreq_text)
case("choice-summary-true", "regla 2", ALL, "true", None,
     {"resourceType": "Observation", "id": "o", "status": "final", "code": {"text": "c"},
      "valueQuantity": {"value": 5.0, "unit": "mg"}, "interpretation": [{"text": "i"}]},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]}, "status": "final", "code": {"text": "c"},
      "valueQuantity": {"value": 5.0, "unit": "mg"}})
case("choice-elements-name-without-x", "regla 2, D3 (`value` es el nombre del [x])", ALL, "false", ["value"],
     {"resourceType": "Observation", "id": "o", "status": "final", "code": {"text": "c"}, "subject": {"reference": "Patient/1"},
      "valueQuantity": {"value": 5.0, "unit": "mg", "id": "q"}},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]}, "status": "final", "code": {"text": "c"},
      "valueQuantity": {"id": "q", "value": 5.0, "unit": "mg"}})
case("choice-primitive-variant-underscore", "regla 2, 4", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "deceasedBoolean": True, "_deceasedBoolean": {"extension": [EXT]}},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "deceasedBoolean": True})

# -- Lexemas numéricos y cadenas (json.html §2.6; salida exacta) ---------------
case("number-lexemes-raw", "regla de salida: lexema exacto", ALL, "true", None,
     '{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"valueQuantity":{"value":1.50,"unit":"mg"},'
     '"referenceRange":[{"low":{"value":1E+2}}]}',
     '{"resourceType":"Observation","id":"o","meta":{"tag":[{"system":"%s","code":"SUBSETTED","display":"subsetted"}]},'
     '"status":"final","code":{"text":"c"},"valueQuantity":{"value":1.50,"unit":"mg"}}' % SYS)
case("number-lexemes-big-and-neg-zero", "salida exacta", ALL, "data", None,
     '{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"valueQuantity":{"value":-0.0},'
     '"component":[{"code":{"text":"c"},"valueQuantity":{"value":12345678901234567890.0e-3}}]}',
     '{"resourceType":"Observation","id":"o","status":"final","code":{"text":"c"},"valueQuantity":{"value":-0.0},'
     '"component":[{"code":{"text":"c"},"valueQuantity":{"value":12345678901234567890.0e-3}}]}')
case("string-escapes-raw", "salida compacta, UTF-8 sin escapar", ALL, "false", None,
     '{ "resourceType" : "Patient", "id":"p", "name":[{"family":"\\u00d1and\\u00fa \\"x\\" \\\\ \\u0001 \\/ \\ud83d\\ude00"}] }',
     '{"resourceType":"Patient","id":"p","name":[{"family":"Ñandú \\"x\\" \\\\ \\u0001 / \U0001F600"}]}')

# -- Regla 6: recursos anidados -----------------------------------------------
_bundle = {"resourceType": "Bundle", "id": "b", "type": "searchset", "timestamp": "2020-01-01T00:00:00Z",
           "entry": [{"fullUrl": "http://x/Patient/p", "resource": {"resourceType": "Patient", "id": "p", "text": TX, "active": True}},
                     {"fullUrl": "http://x/Patient/q", "resource": {"resourceType": "Patient", "id": "q", "active": True}}]}
case("bundle-true-nested-tags", "regla 6, 8 (SUBSETTED en anidado y raíz)", ALL, "true", None, _bundle, {
    "resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "searchset", "timestamp": "2020-01-01T00:00:00Z",
    "entry": [{"fullUrl": "http://x/Patient/p", "resource": {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "active": True}},
              {"fullUrl": "http://x/Patient/q", "resource": {"resourceType": "Patient", "id": "q", "active": True}}]})
case("bundle-root-tag-only-from-nested", "regla 8", ALL, "true", None,
     {"resourceType": "Bundle", "id": "b", "type": "searchset",
      "entry": [{"resource": {"resourceType": "Patient", "id": "p", "text": TX}}]},
     {"resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "searchset",
      "entry": [{"resource": {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}}}]})
case("bundle-text-nested-whole", "regla 6 (text: anidados enteros)", ALL, "text", None, _bundle, {
    "resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "searchset"})
case("bundle-elements-nested-whole-no-loss", "regla 6, 8", ALL, "false", ["entry"],
     {"resourceType": "Bundle", "id": "b", "type": "searchset",
      "entry": [{"resource": {"resourceType": "Patient", "id": "p", "text": TX, "active": True}}]},
     {"resourceType": "Bundle", "id": "b", "type": "searchset",
      "entry": [{"resource": {"resourceType": "Patient", "id": "p", "text": TX, "active": True}}]})
case("bundle-elements-nested-whole-root-tag", "regla 6, 8", ALL, "false", ["entry"], _bundle, {
    "resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "searchset",
    "entry": _bundle["entry"]})
case("bundle-data-nested", "regla 1, 6 (data: text de cada anidado)", ALL, "data", None, _bundle, {
    "resourceType": "Bundle", "id": "b", "meta": {"tag": [T]}, "type": "searchset", "timestamp": "2020-01-01T00:00:00Z",
    "entry": [{"fullUrl": "http://x/Patient/p", "resource": {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "active": True}},
              {"fullUrl": "http://x/Patient/q", "resource": {"resourceType": "Patient", "id": "q", "active": True}}]})
case("parameters-data-nested", "regla 1, 6 (Parameters.parameter.resource)", ALL, "data", None,
     {"resourceType": "Parameters", "id": "pa", "parameter": [{"name": "p", "resource": {"resourceType": "Patient", "id": "x", "text": TX}}]},
     {"resourceType": "Parameters", "id": "pa", "meta": {"tag": [T]},
      "parameter": [{"name": "p", "resource": {"resourceType": "Patient", "id": "x", "meta": {"tag": [T]}}}]})
case("composition-section-text-kept-in-data", "regla 1 (Composition.section.text se conserva)", ALL, "data", None,
     {"resourceType": "Composition", "id": "c", "text": TX, "status": "final", "section": [{"title": "s", "text": TX}]},
     {"resourceType": "Composition", "id": "c", "meta": {"tag": [T]}, "status": "final", "section": [{"title": "s", "text": TX}]})

# -- Regla 7: D9 --------------------------------------------------------------
PR = {"resourceType": "Practitioner", "id": "pr", "active": True}
ORG = {"resourceType": "Organization", "id": "org", "name": "Org"}
PT = {"resourceType": "Patient", "id": "pt", "managingOrganization": {"reference": "#org"}}
UNREF = {"resourceType": "Medication", "id": "unref", "code": {"text": "x"}}
case("d9-chain", "regla 7 (cierre transitivo, posición original)", ALL, "true", None,
     {"resourceType": "Observation", "id": "o", "contained": [PR, ORG, PT, UNREF], "status": "final", "code": {"text": "c"},
      "subject": {"reference": "#pt"}},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]}, "contained": [ORG, PT], "status": "final", "code": {"text": "c"},
      "subject": {"reference": "#pt"}})
_A = {"resourceType": "Patient", "id": "a", "managingOrganization": {"reference": "#b"}}
_B = {"resourceType": "Organization", "id": "b", "partOf": {"reference": "#a"}}
_cyc = {"resourceType": "Observation", "id": "o", "contained": [_A, _B], "status": "final", "code": {"text": "c"},
        "subject": {"reference": "#a"}}
case("d9-cycle-terminates-no-loss", "regla 7 (un ciclo termina)", ALL, "true", None, _cyc, _cyc)
case("d9-hash-alone-points-to-itself", "regla 7 (`#` solo apunta al propio recurso)", ALL, "true", None,
     {"resourceType": "Observation", "id": "o", "contained": [PR], "status": "final", "code": {"text": "c"},
      "subject": {"reference": "#"}},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]}, "status": "final", "code": {"text": "c"},
      "subject": {"reference": "#"}})
case("d9-canonical-hash", "regla 7 (canonical `#id`), references.html", ALL, "true", None,
     {"resourceType": "CarePlan", "id": "cp",
      "contained": [{"resourceType": "PlanDefinition", "id": "pd", "status": "draft", "title": "T"},
                    {"resourceType": "PlanDefinition", "id": "other", "status": "draft"}],
      "instantiatesCanonical": ["#pd"], "status": "active", "intent": "plan", "subject": {"reference": "Patient/1"}},
     {"resourceType": "CarePlan", "id": "cp", "meta": {"tag": [T]},
      "contained": [{"resourceType": "PlanDefinition", "id": "pd", "title": "T", "status": "draft"}],
      "instantiatesCanonical": ["#pd"], "status": "active", "intent": "plan", "subject": {"reference": "Patient/1"}})
_mx = {"id": "x1", "url": "http://x/m", "valueReference": {"reference": "#org"}}
case("d9-inside-whole-extension", "regla 7 (dentro de extensiones enteras), D1/D10", ALL, "true", None,
     {"resourceType": "Observation", "id": "o", "contained": [PR, ORG], "modifierExtension": [_mx], "status": "final", "code": {"text": "c"}},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]}, "contained": [ORG], "modifierExtension": [_mx], "status": "final",
      "code": {"text": "c"}})
_loc = {"resourceType": "Location", "id": "loc", "name": "L"}
_inner = {"extension": [{"url": "http://x/e", "valueReference": {"reference": "#loc"}}]}
_pv = {"resourceType": "Provenance", "id": "pv", "contained": [_loc], "target": [_inner], "recorded": "2020-01-01T00:00:00Z",
       "agent": [{"who": {"reference": "Practitioner/1"}}]}
case("d9-inside-whole-D8-copy", "regla 7 (dentro de copias enteras de D8)", ALL, "true", None, _pv, _pv)
_e1 = {"resourceType": "Observation", "id": "e1", "contained": [{"resourceType": "Patient", "id": "a"}], "status": "final",
       "code": {"text": "c"}, "subject": {"reference": "#b"}}
_e2 = {"resourceType": "Observation", "id": "e2", "contained": [{"resourceType": "Patient", "id": "b"}], "status": "final",
       "code": {"text": "c"}, "subject": {"reference": "Patient/z"}}
case("d9-nested-bundle-scope", "regla 7 (alcance: el recurso anidado, no la raíz)", ALL, "true", None,
     {"resourceType": "Bundle", "id": "bb", "type": "collection", "entry": [{"resource": _e1}, {"resource": _e2}]},
     {"resourceType": "Bundle", "id": "bb", "meta": {"tag": [T]}, "type": "collection",
      "entry": [{"resource": {"resourceType": "Observation", "id": "e1", "meta": {"tag": [T]}, "status": "final",
                              "code": {"text": "c"}, "subject": {"reference": "#b"}}},
                {"resource": {"resourceType": "Observation", "id": "e2", "meta": {"tag": [T]}, "status": "final",
                              "code": {"text": "c"}, "subject": {"reference": "Patient/z"}}}]})
def _mr_d9(v):
    d = _medreq(v)
    d.pop("authoredOn")
    ref = {"reference": "#med"}
    if v == "r5":
        d["medication"] = {"reference": ref}
    else:
        d.pop("medicationCodeableConcept")
        d["medicationReference"] = ref
        # el orden del modelo R4: doNotPerform, medication[x], subject
        d = {k: d[k] for k in ("resourceType", "id", "status", "intent", "doNotPerform", "medicationReference", "subject")}
    med = {"resourceType": "Medication", "id": "med", "text": TX, "code": {"text": "c"}}
    out = {}
    for k, x in d.items():
        out[k] = x
        if k == "id":
            out["contained"] = [med, {"resourceType": "Medication", "id": "zz", "code": {"text": "no"}}]
    return out
def _mr_d9_text(v):
    d = _mr_d9(v)
    d["contained"] = [d["contained"][0]]
    out = {}
    for k, x in d.items():
        out[k] = x
        if k == "id":
            out["meta"] = {"tag": [T]}
    return out
case("d9-text-whole-contained", "regla 7, 6 (text: el contenido va entero; medication es 1..1)", ALL, "text", None, _mr_d9, _mr_d9_text)
case("d9-elements-contained-requested-all", "regla 7, 2 (contained pedido: todo entero)", ALL, "false", ["contained"],
     {"resourceType": "Observation", "id": "o", "contained": [PR, ORG], "status": "final", "code": {"text": "c"}},
     {"resourceType": "Observation", "id": "o", "contained": [PR, ORG], "status": "final", "code": {"text": "c"}})
case("d9-data-contained-text", "regla 1, 6, 8", ALL, "data", None,
     {"resourceType": "Observation", "id": "o", "contained": [{"resourceType": "Patient", "id": "pt", "text": TX, "active": True}],
      "status": "final", "code": {"text": "c"}},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]},
      "contained": [{"resourceType": "Patient", "id": "pt", "meta": {"tag": [T]}, "active": True}],
      "status": "final", "code": {"text": "c"}})
case("d9-contained-projected-true", "regla 6, 7 (contenido proyectado con el modo)", ALL, "true", None,
     {"resourceType": "Observation", "id": "o", "contained": [{"resourceType": "Patient", "id": "pt", "text": TX, "gender": "male",
                                                                "maritalStatus": {"text": "M"}}],
      "status": "final", "code": {"text": "c"}, "subject": {"reference": "#pt"}},
     {"resourceType": "Observation", "id": "o", "meta": {"tag": [T]},
      "contained": [{"resourceType": "Patient", "id": "pt", "meta": {"tag": [T]}, "gender": "male"}],
      "status": "final", "code": {"text": "c"}, "subject": {"reference": "#pt"}})

# -- Regla 8: SUBSETTED ----------------------------------------------------------
case("tag-existing-not-duplicated", "regla 8", ALL, "true", None,
     {"resourceType": "Patient", "id": "q",
      "meta": {"tag": [{"system": "http://x", "code": "a"}, {"system": SYS, "code": "SUBSETTED"}]}, "text": TX, "active": True},
     {"resourceType": "Patient", "id": "q",
      "meta": {"tag": [{"system": "http://x", "code": "a"}, {"system": SYS, "code": "SUBSETTED"}]}, "active": True})
case("tag-appended-at-end", "regla 8", ALL, "data", None,
     {"resourceType": "Patient", "id": "q", "meta": {"tag": [{"system": "http://x", "code": "a"}, {"system": SYS, "code": "other"}]}, "text": TX},
     {"resourceType": "Patient", "id": "q", "meta": {"tag": [{"system": "http://x", "code": "a"}, {"system": SYS, "code": "other"}, T]}})
case("tag-meta-created-model-position", "regla 8 (meta en su posición del modelo)", ALL, "data", None,
     {"resourceType": "Patient", "text": TX, "active": True},
     {"resourceType": "Patient", "meta": {"tag": [T]}, "active": True})
case("tag-meta-created-after-id", "regla 8", ALL, "data", None,
     {"resourceType": "Patient", "id": "p", "language": "en", "text": TX},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "language": "en"})
case("tag-in-meta-model-position", "regla 8 (tag después de security)", ALL, "data", None,
     {"resourceType": "Patient", "id": "p", "meta": {"versionId": "2", "security": [{"code": "N"}]}, "text": TX},
     {"resourceType": "Patient", "id": "p", "meta": {"versionId": "2", "security": [{"code": "N"}], "tag": [T]}})
case("no-loss-no-tag", "regla 8", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "active": True, "gender": "male"},
     {"resourceType": "Patient", "id": "p", "active": True, "gender": "male"})
case("no-text-data-no-tag", "regla 8", ALL, "data", None,
     {"resourceType": "Patient", "id": "p", "active": True}, {"resourceType": "Patient", "id": "p", "active": True})
case("true-then-meta-extension-only", "regla 8, 5 (meta vacío se quita y se recrea con el tag)", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "meta": {"extension": [EXT]}, "active": True},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "active": True})

# -- Errores (regla 9 y D3/D5): sin salida esperada; el oráculo informa error ---
case("err-unknown-element-name", "D3", ALL, "false", ["nope"], P, ("ERR", "unknown element name"))
case("err-variant-name", "D3 (valueQuantity)", ALL, "false", ["valueQuantity"],
     {"resourceType": "Observation", "id": "o"}, ("ERR", "unknown element name"))
case("err-choice-suffix-name", "D3 (value[x])", ALL, "false", ["value[x]"],
     {"resourceType": "Observation", "id": "o"}, ("ERR", "unknown element name"))
case("err-summary-with-elements", "D5", ALL, "true", ["name"], P, ("ERR", "_summary together with _elements"))
case("err-unknown-key", "regla 9", ALL, "true", None, {"resourceType": "Patient", "id": "p", "nope": 1}, ("ERR", "unknown element"))
case("err-unknown-resource-type", "regla 9", ALL, "true", None, {"resourceType": "Nope", "id": "p"}, ("ERR", "unknown resource type"))

# -- Arreglos de primitivos de distinto largo (json.html §2.1.6.4.2, R5; igual en R4 y R4B por decisión del dueño, 05-11-2026)
case("array-pad-shorter-underscore", "json.html §2.1.6.4.2", ALL, "false", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B", "C"], "_given": [{"id": "x"}]}]},
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B", "C"], "_given": [{"id": "x"}, None, None]}]})
case("array-pad-shorter-value", "json.html §2.1.6.4.2", ALL, "false", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A"], "_given": [None, {"id": "y"}, {"id": "z"}]}]},
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", None, None], "_given": [None, {"id": "y"}, {"id": "z"}]}]})
# el relleno deja `_given` solo de null bajo `true`: desaparece (regla 4); lo perdido pone el tag
case("array-pad-then-null-only-removed", "json.html §2.1.6.4.2; regla 4", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B"], "_given": [{"id": "x"}]}]},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "name": [{"given": ["A", "B"]}]})
# valor todo null tras rellenar y `_x` que se vacía al filtrar: las ocurrencias vacías se quitan (regla 5)
case("array-pad-all-null-values-removed", "json.html §2.1.6.4.2; regla 5", ALL, "true", None,
     {"resourceType": "Patient", "id": "p", "name": [{"family": "F", "given": [None], "_given": [{"id": "a"}, {"id": "b"}]}]},
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "name": [{"family": "F"}]})
# un nivel que no filtra rellena igual y no toca el `_x` (regla 4)
case("array-pad-not-filtering-null-only-kept", "json.html §2.1.6.4.2; regla 4", ALL, "data", None,
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B"], "_given": [None]}]},
     {"resourceType": "Patient", "id": "p", "name": [{"given": ["A", "B"], "_given": [None, None]}]})
case("array-pad-mandatory-pair-whole", "json.html §2.1.6.4.2; regla 4, D8", ALL, "true", None,
     {"resourceType": "CapabilityStatement", "id": "c", "status": "active", "kind": "instance",
      "format": ["json", "xml"], "_format": [{"id": "f1"}]},
     {"resourceType": "CapabilityStatement", "id": "c", "status": "active", "kind": "instance",
      "format": ["json", "xml"], "_format": [{"id": "f1"}, None]})

# -- Orden del modelo con entrada cruda (plan 004 regla 2; plan 005 §13.2.1) ------
# Las entradas llevan sus miembros en un orden cualquiera; lo esperado se escribió
# desde las tablas de elementos de la especificación (resourcelist / datatypes).
# Patient: id, active, name, gender, birthDate; HumanName: use, family, given.
case("order-raw-patient", "plan 004 regla 2; patient.html; datatypes.html#HumanName", ALL, "false", None,
     '{"gender":"male","name":[{"given":["B","A"],"family":"F","use":"official"},{"family":"Z"}],'
     '"birthDate":"2000-01-01","active":true,"id":"p","resourceType":"Patient"}',
     '{"resourceType":"Patient","id":"p","active":true,'
     '"name":[{"use":"official","family":"F","given":["B","A"]},{"family":"Z"}],'
     '"gender":"male","birthDate":"2000-01-01"}')
# `_x` justo después de `x`, o solo; en un arreglo de primitivos conserva sus posiciones
case("order-raw-underscore", "plan 004 regla 2 (`_x`); json.html §2.6", ALL, "false", None,
     '{"_birthDate":{"id":"b"},"_gender":{"extension":[{"valueString":"v","url":"u"}],"id":"g"},'
     '"gender":"male","birthDate":"2000","_active":{"id":"a"},"name":[{"_given":[null,{"id":"x"}],'
     '"given":["A","B"],"family":"F"}],"resourceType":"Patient"}',
     '{"resourceType":"Patient","_active":{"id":"a"},"name":[{"family":"F","given":["A","B"],"_given":[null,{"id":"x"}]}],'
     '"gender":"male","_gender":{"id":"g","extension":[{"url":"u","valueString":"v"}]},'
     '"birthDate":"2000","_birthDate":{"id":"b"}}')
# [x]: la variante presente ocupa el lugar del [x]; los lexemas numéricos no cambian
case("order-raw-choice", "plan 004 regla 2 ([x]); observation.html; json.html (números)", ALL, "false", None,
     '{"component":[{"valueInteger":3,"code":{"text":"k"}}],"valueQuantity":{"unit":"mg","value":5.10,'
     '"id":"q","comparator":"<"},"effectiveDateTime":"2020","_effectiveDateTime":{"id":"e"},'
     '"code":{"text":"c"},"status":"final","resourceType":"Observation"}',
     '{"resourceType":"Observation","status":"final","code":{"text":"c"},"effectiveDateTime":"2020",'
     '"_effectiveDateTime":{"id":"e"},"valueQuantity":{"id":"q","value":5.10,"comparator":"<","unit":"mg"},'
     '"component":[{"code":{"text":"k"},"valueInteger":3}]}')
case("order-raw-number-lexemes", "json.html (números); plan 004 regla 2", ALL, "false", None,
     '{"valueQuantity":{"value":1.50E+2,"unit":"x"},"status":"final","code":{"text":"c"},"resourceType":"Observation"}',
     '{"resourceType":"Observation","status":"final","code":{"text":"c"},"valueQuantity":{"value":1.50E+2,"unit":"x"}}')
# Recursos anidados: cada uno por su propio tipo (contained, Bundle.entry.resource, Parameters.parameter.resource)
case("order-raw-contained", "plan 004 regla 2; domainresource.html; plandefinition.html", ALL, "false", None,
     '{"gender":"male","contained":[{"title":"T","status":"draft","id":"o","resourceType":"PlanDefinition"}],'
     '"id":"p","resourceType":"Patient"}',
     '{"resourceType":"Patient","id":"p","contained":[{"resourceType":"PlanDefinition","id":"o","title":"T","status":"draft"}],'
     '"gender":"male"}')
case("order-raw-bundle-entry", "plan 004 regla 2; bundle.html", ALL, "false", None,
     '{"entry":[{"resource":{"gender":"male","id":"q","resourceType":"Patient"},"fullUrl":"u"}],'
     '"type":"collection","resourceType":"Bundle"}',
     '{"resourceType":"Bundle","type":"collection","entry":[{"fullUrl":"u",'
     '"resource":{"resourceType":"Patient","id":"q","gender":"male"}}]}')
case("order-raw-parameters", "plan 004 regla 2; parameters.html", ALL, "false", None,
     '{"parameter":[{"resource":{"active":true,"resourceType":"Patient"},"name":"n"}],"resourceType":"Parameters"}',
     '{"resourceType":"Parameters","parameter":[{"name":"n","resource":{"resourceType":"Patient","active":true}}]}')
# Extension: se ordena por el modelo de Extension (id, extension, url, value[x]), también anidada
case("order-raw-extension", "plan 004 regla 2; extensibility.html; json.html", ALL, "false", None,
     '{"extension":[{"valueString":"v","url":"u","extension":[{"valueCode":"c","url":"n"}],"id":"e"}],'
     '"name":[{"extension":[{"valueInteger":1,"url":"w"}],"family":"F"}],"resourceType":"Patient"}',
     '{"resourceType":"Patient","extension":[{"id":"e","extension":[{"url":"n","valueCode":"c"}],"url":"u","valueString":"v"}],'
     '"name":[{"extension":[{"url":"w","valueInteger":1}],"family":"F"}]}')
# Los arreglos conservan su orden (json.html: el orden dentro de un arreglo se mantiene)
case("order-raw-arrays-keep-order", "json.html (orden en un arreglo)", ALL, "false", None,
     '{"name":[{"family":"Z","given":["Z","A"]},{"family":"A"}],"resourceType":"Patient"}',
     '{"resourceType":"Patient","name":[{"family":"Z","given":["Z","A"]},{"family":"A"}]}')
# En los modos que filtran la entrada cruda también sale en el orden del modelo (D8: meta se crea en su lugar)
case("order-raw-summary-true", "plan 004 regla 2; regla 8", ALL, "true", None,
     '{"gender":"male","text":{"status":"generated","div":"<div xmlns=\\"http://www.w3.org/1999/xhtml\\">x</div>"},'
     '"name":[{"given":["A"],"family":"F"}],"active":true,"id":"p","resourceType":"Patient"}',
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]}, "active": True,
      "name": [{"family": "F", "given": ["A"]}], "gender": "male"})
case("order-raw-data-mode", "plan 004 regla 2; regla 1 (data)", ALL, "data", None,
     '{"gender":"male","contained":[{"status":"draft","id":"o","resourceType":"PlanDefinition",'
     '"text":{"status":"generated","div":"<div xmlns=\\"http://www.w3.org/1999/xhtml\\">x</div>"}}],"id":"p","resourceType":"Patient"}',
     {"resourceType": "Patient", "id": "p", "meta": {"tag": [T]},
      "contained": [{"resourceType": "PlanDefinition", "id": "o", "meta": {"tag": [T]}, "status": "draft"}],
      "gender": "male"})
case("err-raw-unknown-member", "regla 9 (Normalize rechaza una clave del modelo ausente)", ALL, "false", None,
     '{"resourceType":"Patient","bogus":1}', ("ERR", "unknown element"))


def _run_case(proj, v, c):
    name, cite, vers, mode, elements, inp, exp = c
    inp, exp = _ev(inp, v), _ev(exp, v)
    text_in = inp if isinstance(inp, str) else json.dumps(inp)
    try:
        got = dumps(proj.project(mode, elements, parse(text_in)))
    except OracleError as e:
        got = "ERR:" + str(e)
    if isinstance(exp, tuple):
        return got.startswith("ERR:") and exp[1] in got, got, "ERR containing " + exp[1]
    want = exp if isinstance(exp, str) else dumps(parse(json.dumps(exp)))
    return got == want, got, want


def _line_cases(proj):
    """Casos de la entrada JSONL: duplicados y constantes no JSON."""
    bad = []
    for name, line in (
            ("dup-key", '{"id":"1","mode":"true","elements":null,"resource":"{\\"resourceType\\":\\"Patient\\",\\"id\\":\\"a\\",\\"id\\":\\"b\\"}"}'),
            ("nan", '{"id":"2","mode":"true","elements":null,"resource":"{\\"resourceType\\":\\"Observation\\",\\"valueQuantity\\":{\\"value\\":NaN}}"}')):
        out = json.loads(run_line(proj, line))
        if "error" not in out:
            bad.append(name)
    ok = json.loads(run_line(proj, '{"id":"3","mode":"false","elements":null,"resource":"{\\"resourceType\\":\\"Patient\\",\\"id\\":\\"a\\"}"}'))
    if ok.get("output") != '{"resourceType":"Patient","id":"a"}':
        bad.append("passthrough")
    return bad

# -- Regla 10 sobre los StructureDefinition (ADR-003): duplicados sintéticos -------
# Cada caso agrega al Set un package de prueba con una copia del SD de Patient
# modificada, y dice quién gana según la cascada del ADR (escrito desde su texto):
#   1 serie (solo si declara fhirVersion), 2 status active > draft > otro > retired,
#   3 publicador (copia = host del canonical del package distinto del de la URL),
#   4 versión (semver), 5 lastUpdated, 6 sin ganador: el oráculo falla.
def _dup_package(tmp, name, sd, canonical, fname):
    import io
    pkg = os.path.join(tmp, "%s-1.0.0.tgz" % name)
    with tarfile.open(pkg, "w:gz") as tf:
        for fn, doc in (("package/package.json", {"name": name, "version": "1.0.0", "canonical": canonical}),
                        ("package/" + fname, sd)):
            raw = json.dumps(doc).encode()
            ti = tarfile.TarInfo(fn)
            ti.size = len(raw)
            tf.addfile(ti, io.BytesIO(raw))


def _dup_case(base, pkgdir, v, real, edit, expect, cache, fname):
    """expect: 'dup' (gana la copia: gender.isSummary vale lo que puso `edit`),
    'core' (gana el SD del núcleo) o 'fail' (sin ganador)."""
    import copy
    import shutil
    import tempfile
    tmp = tempfile.mkdtemp(prefix="oracle-dup-")
    try:
        for f in os.listdir(pkgdir):
            os.symlink(os.path.realpath(os.path.join(pkgdir, f)), os.path.join(tmp, f))
        lock = read_lock(os.path.join(base, "locks", v + ".lock"))
        sd = copy.deepcopy(real.types["Patient"])
        canonical = edit(sd) or "http://hl7.org/fhir/dup"
        _dup_package(tmp, "test.dup", sd, canonical, fname)
        lock["packages"].append({"name": "test.dup", "version": "1.0.0"})
        try:
            m = Model(lock, tmp, cache)
        except OracleError as e:
            return expect == "fail" and "no winner" in str(e), "ERR " + str(e)
        gender = m.nodes[("Patient", "Patient")].kids["gender"].summary
        want_core = real.nodes[("Patient", "Patient")].kids["gender"].summary
        got = "dup" if gender != want_core else "core"
        return got == expect, got
    finally:
        shutil.rmtree(tmp)


def _dup_tests(base, pkgdir, v, real):
    core = real.types["Patient"]
    core_status = core.get("status")
    out = []
    cache = {}  # se suelta al volver: no se retiene entre versiones

    def flip(sd):
        for e in sd["snapshot"]["element"]:
            if e["path"] == "Patient.gender":
                e["isSummary"] = not e.get("isSummary", False)

    def retired(sd):
        flip(sd)
        sd["status"] = "retired"

    def active_dup(sd):
        flip(sd)
        sd["status"] = "active"

    def higher_version(sd):
        flip(sd)
        sd["version"] = "99.0.0"

    def higher_version_copy(sd):
        higher_version(sd)
        return "http://example.org/ig"  # publicador distinto del host de la URL: es copia

    def old_series(sd):
        flip(sd)
        sd["status"] = "active"
        sd["fhirVersion"] = "3.0.1"

    def identical(sd):
        pass  # idéntico al del núcleo: ningún paso separa

    def not_an_sd(sd):
        higher_version(sd)  # ganaría por versión si contara como candidato...
        sd["resourceType"] = "Basic"  # ...pero su resourceType no es StructureDefinition

    cases = [("retired-loses", retired, "core"),                # paso 2 (un retired no gana aunque sea el último)
             ("higher-version-same-status", higher_version, "dup"),   # paso 4: el status es el mismo
             ("copy-loses-despite-version", higher_version_copy, "core"),  # paso 3 antes que el 4
             ("other-series-loses-despite-status", old_series, "core"),    # paso 1 antes que el 2
             ("identical-no-winner", identical, "fail"),
             # packages.html (R5 §2.1.10.1): el nombre del archivo no dice el tipo
             ("unprefixed-filename-wins", higher_version, "dup", "patient.json"),
             ("unprefixed-filename-no-winner", identical, "fail", "patient.json"),
             ("prefixed-filename-not-an-sd", not_an_sd, "core", "StructureDefinition-Patient-fake.json")]
    if core_status != "active":  # active (3) > draft (2)
        cases.append(("active-beats-" + str(core_status), active_dup, "dup"))
    for c in cases:
        name, edit, expect = c[:3]
        fname = c[3] if len(c) > 3 else "StructureDefinition-Patient-dup.json"
        ok, got = _dup_case(base, pkgdir, v, real, edit, expect, cache, fname)
        out.append((name, ok, got, expect))
    return out


def self_test(pkgdir):
    base = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    failed = 0
    for v in ALL:
        t0 = time.time()
        proj = load_projector(os.path.join(base, "locks", v + ".lock"), pkgdir)
        n = 0
        for c in CASES:
            if v not in c[2]:
                continue
            ok, got, want = _run_case(proj, v, c)
            n += 1
            if not ok:
                failed += 1
                sys.stderr.write("FAIL %s [%s]\n  got : %s\n  want: %s\n" % (c[0], v, got, want))
        extra = _line_cases(proj)
        for e in extra:
            failed += 1
            sys.stderr.write("FAIL line-case %s [%s]\n" % (e, v))
        # display ausente: el tag va sin display
        nd = Projector(proj.model, None)
        r = dumps(nd.project("data", None, parse('{"resourceType":"Patient","id":"p","text":{"status":"generated","div":"x"}}')))
        want = '{"resourceType":"Patient","id":"p","meta":{"tag":[{"system":"%s","code":"SUBSETTED"}]}}' % SYS
        n += 1
        if r != want:
            failed += 1
            sys.stderr.write("FAIL tag-without-display [%s]\n  got : %s\n  want: %s\n" % (v, r, want))
        dups = _dup_tests(base, pkgdir, v, proj.model)
        for name, ok, got, expect in dups:
            if not ok:
                failed += 1
                sys.stderr.write("FAIL dup-sd-%s [%s]\n  got : %s\n  want: %s\n" % (name, v, got, expect))
        sys.stdout.write("%s: %d cases (+%d line cases, +%d duplicate-SD cases), display=%r, %.1fs\n"
                         % (v, n, 3, len(dups), proj.display, time.time() - t0))
    sys.stdout.write("FAILED %d\n" % failed if failed else "OK\n")
    return 1 if failed else 0


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--lock")
    ap.add_argument("--packages", default="packages")
    ap.add_argument("--self-test", action="store_true")
    ap.add_argument("--rules-report", action="store_true")
    ap.add_argument("--report")
    ap.add_argument("--bench", action="store_true")
    a = ap.parse_args()
    if a.self_test:
        return self_test(a.packages)
    if a.bench:
        return bench(a.lock, a.packages)
    counts = {} if (a.rules_report or a.report) else None
    proj = load_projector(a.lock, a.packages, counts)
    out = sys.stdout
    for line in sys.stdin:
        line = line.strip()
        if line:
            out.write(run_line(proj, line) + "\n")
    out.flush()
    if counts is not None:
        write_report(counts, a.report)
    return 0


if __name__ == "__main__":
    sys.exit(main())
