#!/usr/bin/env python3
# Compara el grafo de cada lock de testdata/ con los packages que validator_cli
# dice cargar (plan 001 §4). El validador, con -verbose, escribe una línea
# «Package Summary: [name#version, ...]»; se lee esa lista. Un solo JVM a la
# vez. Sin red: el «home» de cada corrida trae los packages del lock y los que el
# validador carga por defecto (scripts/hl7-packages.sha256). Escribe
# docs/evidence/<fecha>-validator-graph.md y termina con error si el validador
# no carga un package que el lock lee o carga uno que el lock excluye.
#
# Uso: python3 -I scripts/validator_graph.py   (lo llama make validator-graph)
import datetime, hashlib, json, os, re, shutil, subprocess, sys, tarfile, tempfile, time

R = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PK = os.path.join(R, ".cache", "packages")
JAR = os.environ.get("VALIDATOR_JAR", os.path.join(R, ".cache", "hl7", "validator_cli.jar"))

def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()

def check_jar():
    want = open(os.path.join(R, "scripts", "validator.sha256")).read().split()[0]
    if sha256(JAR) != want:
        sys.exit("%s does not match scripts/validator.sha256" % JAR)

def extract(file, dest):
    # Los packages vienen de scripts/*.sha256 (SHA fijado); aun así no se
    # extrae ninguna entrada que escape de dest.
    os.makedirs(dest, exist_ok=True)
    real = os.path.realpath(dest)
    with tarfile.open(file) as t:
        for m in t:
            target = os.path.realpath(os.path.join(dest, m.name))
            if not target.startswith(real + os.sep) or not (m.isfile() or m.isdir()):
                continue
            t.extract(m, dest)

def pkg_file(pid):
    n, v = pid.split("#")
    return os.path.join(PK, "%s-%s.tgz" % (n, v))

def run_validator(work, home, version, igs):
    patient = os.path.join(work, "p.json")
    open(patient, "w").write('{"resourceType":"Patient","id":"x"}')
    args = ["/usr/bin/time", "-l", "java", "-Duser.home=" + home, "-jar", JAR, "-version", version, "-tx", "n/a",
            "-no-http-access", "-verbose", "-output", os.path.join(work, "out.json")]
    for ig in igs:
        args += ["-ig", ig]
    args.append(patient)
    t0 = time.time()
    p = subprocess.run(args, cwd=work, capture_output=True, text=True, timeout=900)
    secs = time.time() - t0
    m = re.search(r"(\d+)\s+maximum resident set size", p.stderr)
    rss = int(m.group(1)) // (1 << 20) if m else 0
    return p.stdout, secs, rss

def main():
    check_jar()
    locks = sorted(f for f in os.listdir(os.path.join(R, "testdata")) if f.endswith(".lock") and f != "r4-core.lock")
    extras = []
    for l in open(os.path.join(R, "scripts", "hl7-packages.sha256")):
        if l.strip():
            stem = l.split()[1][:-4]
            extras.append(stem.rsplit("-", 1)[0] + "#" + stem.rsplit("-", 1)[1])
    tmp_root = os.environ.get("TMPDIR", tempfile.gettempdir())
    base = tempfile.mkdtemp(prefix="validator-graph-", dir=tmp_root)
    lines, bad = [], []
    try:
        for name in locks:
            lock = json.load(open(os.path.join(R, "testdata", name)))
            work = os.path.join(base, name)
            home = os.path.join(work, "home")
            cache = os.path.join(home, ".fhir", "packages")
            os.makedirs(cache)
            lock_ids = [p["name"] + "#" + p["version"] for p in lock["packages"]]
            for pid in sorted(set(lock_ids + extras)):
                extract(pkg_file(pid), os.path.join(cache, pid))
            read = [p["name"] + "#" + p["version"] for p in lock["packages"] if not p.get("excluded")]
            excluded = [p["name"] + "#" + p["version"] for p in lock["packages"] if p.get("excluded")]
            roots = [r for r in lock["roots"] if r != lock["core"]]
            out, secs, rss = run_validator(work, home, lock["fhirVersion"], roots)
            m = re.search(r"Package Summary: \[(.*)\]", out)
            lines.append("## %s\n\nFHIR %s, core `%s`, roots passed with -ig: %s. One JVM: %.1f s, max RSS %d MB.\n" % (
                name, lock["fhirVersion"], lock["core"], ", ".join("`%s`" % r for r in roots) or "none", secs, rss))
            if not m:
                lines.append("**The validator did not print a package summary.**\n\n```text\n%s\n```\n" % out[-1500:])
                bad.append(name)
                continue
            loaded = [x.strip() for x in m.group(1).split(",")]
            missing = [p for p in read if p not in loaded]
            wrongly = [p for p in excluded if p in loaded]
            implicit = [p for p in loaded if p not in read]
            names = {}
            for p in loaded:
                names.setdefault(p.split("#")[0], []).append(p.split("#")[1])
            doubles = {n: v for n, v in names.items() if len(v) > 1}
            lines.append("- The validator loaded %d packages: %s.\n" % (len(loaded), ", ".join("`%s`" % p for p in loaded)))
            lines.append("- Packages the lock reads that the validator did not load: %s.\n" % (", ".join("`%s`" % p for p in missing) or "none"))
            lines.append("- Packages the lock excludes that the validator loaded: %s.\n" % (", ".join("`%s`" % p for p in wrongly) or "none"))
            lines.append("- Loaded by the validator and not read by the lock (its implicit defaults and the latest local versions): %s.\n" % (", ".join("`%s`" % p for p in implicit) or "none"))
            lines.append("- Packages the validator loads in two versions: %s.\n" % (", ".join("`%s`: %s" % (n, ", ".join(v)) for n, v in sorted(doubles.items())) or "none"))
            if missing or wrongly:
                bad.append(name)
            print("%-28s read %d, loaded %d, missing %d, excluded-but-loaded %d, %.1f s, %d MB" % (name, len(read), len(loaded), len(missing), len(wrongly), secs, rss), flush=True)
    finally:
        shutil.rmtree(base, ignore_errors=True)
    stamp = datetime.datetime.now().strftime("%Y-%m-%d")
    path = os.path.join(R, "docs", "evidence", stamp + "-validator-graph.md")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write("# Lock graph against validator_cli\n\nGenerated by `make validator-graph`. validator_cli 7.0.2 (SHA-256 in `scripts/validator.sha256`), `-no-http-access`, `-verbose`; the list is the `Package Summary` line it prints. The validator also loads packages by itself (a terminology package by version, `hl7.fhir.xver-extensions`, the latest local `hl7.terminology` and `hl7.fhir.uv.extensions`), so the comparison is a containment, not an equality: every package the lock reads must be loaded and no excluded package may be. The containment goes one way only: a lock that lacks a dependency still passes if the validator loads it by default. Completeness of each lock (every dependency present) is covered by the `TestReal*` tests, which resolve the roots again and compare byte for byte.\n\n")
        f.write("\n".join(lines))
        f.write("\n%s\n" % ("The validator agrees with every lock." if not bad else "**Disagreement in: %s**" % ", ".join(bad)))
    print(path)
    if bad:
        sys.exit("disagreement: " + ", ".join(bad))

main()
