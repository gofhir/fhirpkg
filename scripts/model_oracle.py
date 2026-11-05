#!/usr/bin/env python3
"""Oráculo independiente del modelo de tipos (plan 003, ronda 1, M2).

Lee el package del núcleo (package.tgz) directamente, sin pasar por
modelbuild.go, y escribe lo que la especificación dice de cada camino de cada
tipo: destinos de referencia, cardinalidad y tipo FHIR. El test
TestRealModelOracle compara esa salida, exacta, con Model.ReferenceTargets,
Model.Cardinality y Model.FHIRTypeOf.

Uso: python3 -I scripts/model_oracle.py <núcleo.tgz>   (escribe a stdout)

Salida: una línea por dato, separada por tabuladores:
  ref   <camino>  <tipo1,tipo2,...>   destinos, en el orden de targetProfile
  card  <camino>  <min>  <max>        max -1 si es «*»
  ftype <camino>  <tipo FHIR>
  sum   <camino>                  isSummary verdadero (plan 005; las variantes de un [x] también)
  mod   <camino>                  isModifier verdadero

Con --counts escribe en cambio las cuentas de la revisión previa del plan 005
(§2), una por línea «nombre<TAB>valor», leídas del mismo package sin pasar por
modelbuild.go.

Reglas, de la especificación (elementdefinition.html y structuredefinition.html):
- un StructureDefinition define un tipo si no es logical, tiene snapshot y es una
  especialización o una raíz (sin derivation ni baseDefinition);
- un elemento cuyo id lleva «:» es una rebanada y no cuenta;
- type.targetProfile solo existe en Reference, canonical y CodeableReference; su
  destino es el type del StructureDefinition de esa URL;
- un elemento [x] tiene una variante por type.code (el camino sin [x] más el código
  con la primera letra en mayúscula) y los destinos se toman por variante;
- el tipo FHIR de un elemento de un solo tipo es el valueUrl de la extensión
  structuredefinition-fhir-type de su type; se excluye el value de un primitivo.
"""
import json
import sys
import tarfile

FHIR_TYPE_EXT = "http://hl7.org/fhir/StructureDefinition/structuredefinition-fhir-type"
TARGET_CODES = ("Reference", "canonical", "CodeableReference")


def load(path):
    sds = {}
    with tarfile.open(path, "r:gz") as tf:
        for m in tf:
            name = m.name
            if not m.isfile() or not name.startswith("package/StructureDefinition-"):
                continue
            if "/" in name[len("package/"):] or not name.endswith(".json"):
                continue
            sd = json.load(tf.extractfile(m))
            if sd.get("resourceType") == "StructureDefinition":
                sds[sd["url"]] = sd
    return sds


def defines_type(sd):
    if sd.get("kind") == "logical" or "snapshot" not in sd:
        return False
    deriv = sd.get("derivation", "")
    return deriv == "specialization" or (deriv == "" and "baseDefinition" not in sd)


def upper_first(s):
    return s[:1].upper() + s[1:]


def counts(types):
    """Las cuentas de plan 005 §2 de la revisión previa, de los SD en crudo."""
    c = dict.fromkeys(("sd", "elements", "summary", "absent", "own_required_not_summary",
                       "own_modifier_not_summary", "summary_under_non_summary", "concrete",
                       "root_required_not_summary", "root_modifier_not_summary",
                       "nested_required_or_modifier_not_summary"), 0)
    c["sd"] = len(types)
    for t, sd in sorted(types.items()):
        els = [e for e in sd["snapshot"]["element"] if ":" not in e["id"]]
        by_path = {e["path"]: e for e in els}
        concrete = sd["kind"] == "resource" and not sd.get("abstract")
        c["concrete"] += concrete
        for e in els:
            p = e["path"]
            if "." not in p:
                continue
            c["elements"] += 1
            if "isSummary" not in e:
                c["absent"] += 1
            s = e.get("isSummary", False)
            c["summary"] += s
            own = e.get("base", {}).get("path", "").startswith(t + ".")
            parent, name = p.rsplit(".", 1)
            if own and e["min"] > 0 and not s:
                c["own_required_not_summary"] += 1
            if own and e.get("isModifier") and not s and name != "modifierExtension":
                c["own_modifier_not_summary"] += 1
            if own and s and "." in parent and not by_path[parent].get("isSummary", False):
                c["summary_under_non_summary"] += 1
            if concrete and p.count(".") == 1:
                if e["min"] > 0 and not s:
                    c["root_required_not_summary"] += 1
                if e.get("isModifier") and not s and name != "modifierExtension":
                    c["root_modifier_not_summary"] += 1
            if concrete and p.count(".") >= 2 and (e["min"] > 0 or e.get("isModifier")) and not s:
                q, ok = p, True
                while q.count(".") > 1:
                    q = q.rsplit(".", 1)[0]
                    if not by_path[q].get("isSummary", False):
                        ok = False
                if ok:
                    c["nested_required_or_modifier_not_summary"] += 1
    for n in ("DomainResource", "BackboneElement"):
        for e in types[n]["snapshot"]["element"]:
            if e["path"] == n + ".modifierExtension":
                c[n + ".modifierExtension.isSummary"] = int(e.get("isSummary", False))
    return c


def main():
    sds = load(sys.argv[1])
    types = {}
    for sd in sds.values():
        if defines_type(sd):
            types[sd["type"]] = sd
    if "--counts" in sys.argv[2:]:
        for k, v in counts(types).items():
            print("%s\t%d" % (k, v))
        return
    out = []
    for tname in sorted(types):
        sd = types[tname]
        for e in sd["snapshot"]["element"]:
            if ":" in e.get("id", ""):
                continue
            path = e["path"]
            if "." not in path:
                continue  # la raíz: el modelo no guarda su cardinalidad
            ts = e.get("type", [])
            name = path.rsplit(".", 1)[1]
            lo, hi = e["min"], e["max"]
            hi = -1 if hi == "*" else int(hi)
            paths = [path]
            if name.endswith("[x]"):
                base = path[: -len("[x]")]
                codes = []
                for t in ts:
                    if t["code"] not in codes:
                        codes.append(t["code"])
                paths = [base] + [base + upper_first(c) for c in codes]
                for c in codes:
                    targets = []
                    for t in ts:
                        if t["code"] == c:
                            for p in t.get("targetProfile", []):
                                tt = sds[p]["type"]
                                if tt not in targets:
                                    targets.append(tt)
                    if targets:
                        out.append("ref\t%s\t%s" % (base + upper_first(c), ",".join(targets)))
            else:
                targets = []
                for t in ts:
                    if t.get("targetProfile"):
                        assert t["code"] in TARGET_CODES, (path, t["code"])
                    for p in t.get("targetProfile", []):
                        tt = sds[p]["type"]
                        if tt not in targets:
                            targets.append(tt)
                if targets:
                    out.append("ref\t%s\t%s" % (path, ",".join(targets)))
            for p in paths:
                out.append("card\t%s\t%d\t%d" % (p, lo, hi))
                if e.get("isSummary", False):
                    out.append("sum\t%s" % p)
                if e.get("isModifier", False):
                    out.append("mod\t%s" % p)
            if len(ts) == 1:
                ft = None
                for x in ts[0].get("extension", []):
                    if x.get("url") == FHIR_TYPE_EXT and x.get("valueUrl"):
                        ft = x["valueUrl"]
                        break
                if ft is not None and sd.get("kind") == "primitive-type":
                    root = e.get("base", {}).get("path", "").split(".")[0]
                    if root in types and types[root].get("kind") == "primitive-type":
                        ft = None
                if ft is not None:
                    fp = (path[: -len("[x]")] + upper_first(ts[0]["code"])) if name.endswith("[x]") else path
                    out.append("ftype\t%s\t%s" % (fp, ft))
    sys.stdout.write("\n".join(out) + "\n")


if __name__ == "__main__":
    main()
