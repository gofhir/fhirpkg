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


def main():
    sds = load(sys.argv[1])
    types = {}
    for sd in sds.values():
        if defines_type(sd):
            types[sd["type"]] = sd
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
