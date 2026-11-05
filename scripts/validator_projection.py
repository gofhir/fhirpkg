#!/usr/bin/env python3
# Validez de la proyección con validator_cli (plan 005 §5.3, make hl7-projection).
# Valida la muestra que TestWriteProjectionSample escribió (la entrada y cada
# proyección de cada ejemplo) y guarda, por archivo, los errores que el validador
# da, en testdata/projection-hl7/<versión>.jsonl. TestProjectionHL7Verdicts
# (make check-packages, sin Java) compara cada salida con su entrada.
#
# Como el make hl7 de go-searchindex: el jar de scripts/validator.sha256,
# -tx n/a y -no-http-access, un «home» aislado con los packages fijados (los del
# lock y los de scripts/hl7-packages.sha256), la salida -output en JSON (un Bundle
# de OperationOutcome con la extensión operationoutcome-file), y una caché por
# SHA-256 del archivo: solo se juzga lo que cambió, y un cambio de ambiente
# (jar, packages) invalida todo. A lo sumo 2 JVM a la vez.
#
# Uso: python3 -I scripts/validator_projection.py <carpeta de la muestra>
import datetime, hashlib, json, os, re, shutil, subprocess, sys, tarfile, tempfile, threading, time

R = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
PK = os.path.join(R, ".cache", "packages")
JAR = os.environ.get("VALIDATOR_JAR", os.path.join(R, ".cache", "hl7", "validator_cli.jar"))
OUT = os.path.join(R, "testdata", "projection-hl7")
WORKERS = max(1, min(2, int(os.environ.get("HL7_WORKERS", "2"))))
BATCH = int(os.environ.get("HL7_BATCH", "30"))
LOCKS = {"R4": "r4.lock", "R4B": "r4b.lock", "R5": "r5.lock"}
VERSION = {"R4": "4.0.1", "R4B": "4.3.0", "R5": "5.0.0"}


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def env_digest():
    # El ambiente que fija un veredicto: el jar, los packages extra y los locks.
    h = hashlib.sha256()
    for name in ("validator.sha256", "hl7-packages.sha256"):
        h.update(open(os.path.join(R, "scripts", name), "rb").read())
    for lock in sorted(LOCKS.values()):
        h.update(open(os.path.join(R, "testdata", lock), "rb").read())
    h.update(b"-tx n/a -no-http-access")
    return h.hexdigest()


def extract(file, dest):
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


def make_home(base, ver):
    lock = json.load(open(os.path.join(R, "testdata", LOCKS[ver])))
    ids = [p["name"] + "#" + p["version"] for p in lock["packages"]]
    for l in open(os.path.join(R, "scripts", "hl7-packages.sha256")):
        if l.strip():
            stem = l.split()[1][:-4]
            ids.append(stem.rsplit("-", 1)[0] + "#" + stem.rsplit("-", 1)[1])
    home = os.path.join(base, "home-" + ver)
    cache = os.path.join(home, ".fhir", "packages")
    os.makedirs(cache)
    for pid in sorted(set(ids)):
        extract(pkg_file(pid), os.path.join(cache, pid))
    return home


def ext_value(exts, suffix):
    for e in exts or []:
        if e.get("url", "").endswith(suffix):
            return e.get("valueString") or e.get("valueCode") or ""
    return ""


IDX = re.compile(r"\[\d+\]")


def read_outcomes(path):
    doc = json.load(open(path))
    if doc.get("resourceType") == "Bundle":
        ocs = [e["resource"] for e in doc.get("entry", [])]
    elif doc.get("resourceType") == "OperationOutcome":
        ocs = [doc]
    else:
        raise RuntimeError("%s: unexpected resourceType %r" % (path, doc.get("resourceType")))
    out = {}
    for oc in ocs:
        if oc.get("resourceType") != "OperationOutcome":
            raise RuntimeError("%s: an entry is not an OperationOutcome" % path)
        f = os.path.normpath(ext_value(oc.get("extension"), "/operationoutcome-file"))
        if f in out or f == ".":
            raise RuntimeError("%s: a missing or repeated file name %r" % (path, f))
        if not oc.get("issue"):
            raise RuntimeError("%s: %s: an OperationOutcome without issues" % (path, f))
        errs = set()
        for i in oc["issue"]:
            sev = i.get("severity")
            if sev not in ("error", "fatal", "warning", "information"):
                raise RuntimeError("%s: %s: unknown severity %r" % (path, f, sev))
            if sev not in ("error", "fatal"):
                continue
            loc = (i.get("expression") or i.get("location") or [""])[0]
            text = i.get("details", {}).get("text", "")
            mid = ext_value(i.get("extension"), "/operationoutcome-message-id")
            if not text and not mid:
                raise RuntimeError("%s: %s: an error without text or message id" % (path, f))
            # La clave no lleva índices: quitar un elemento de un arreglo los corre.
            errs.add("|".join([sev, i.get("code", ""), IDX.sub("", loc), IDX.sub("", text)]))
        out[f] = sorted(errs)
    return out


def crash_reason(text):
    # La primera línea de la excepción que mató a validator_cli.
    for l in text.splitlines():
        if re.match(r"^[\w.]+(Exception|Error)\b", l):
            return re.sub(r"\s*\(.*|\[.*", "", l)[:160]
    return "no output"


def run_batch(base, home, ver, files, n):
    # validator_cli a veces muere al armar el OperationOutcome de un archivo y se
    # lleva el lote (Ambiguous type Extension): se parte el lote a la mitad hasta
    # aislar el archivo, que queda con un «crash|...» como veredicto (no se oculta).
    try:
        return run_once(base, home, ver, files, n)
    except RuntimeError as e:
        if len(files) == 1:
            return {os.path.normpath(files[0]): ["crash||" + str(e).splitlines()[0]]}, 0.0, 0
        h = len(files) // 2
        a, sa, ra = run_batch(base, home, ver, files[:h], n * 1000 + 1)
        b, sb, rb = run_batch(base, home, ver, files[h:], n * 1000 + 2)
        a.update(b)
        return a, sa + sb, max(ra, rb)


def run_once(base, home, ver, files, n):
    out = os.path.join(base, "out-%s-%d.json" % (ver, n))
    args = ["/usr/bin/time", "-l", "java", "-Xmx2g", "-Duser.home=" + home, "-jar", JAR, "-version", VERSION[ver], "-tx", "n/a",
            "-no-http-access", "-output", out] + files
    t0 = time.time()
    p = subprocess.run(args, cwd=base, capture_output=True, text=True, timeout=1800)
    secs = time.time() - t0
    m = re.search(r"(\d+)\s+maximum resident set size", p.stderr)
    rss = int(m.group(1)) // (1 << 20) if m else 0
    if not os.path.exists(out):
        raise RuntimeError("%s\nvalidator_cli wrote no output (exit %d):\n%s" % (crash_reason(p.stdout + p.stderr), p.returncode, (p.stdout + p.stderr)[-2000:]))
    res = read_outcomes(out)
    for f in files:
        if os.path.normpath(f) not in res:
            raise RuntimeError("validator_cli wrote no outcome for %s" % f)
    os.remove(out)
    return {os.path.normpath(f): res[os.path.normpath(f)] for f in files}, secs, rss


def main():
    if len(sys.argv) != 2:
        sys.exit("usage: validator_projection.py <sample dir>")
    sample = os.path.abspath(sys.argv[1])
    if sha256(JAR) != open(os.path.join(R, "scripts", "validator.sha256")).read().split()[0]:
        sys.exit("%s does not match scripts/validator.sha256" % JAR)
    manifest = [json.loads(l) for l in open(os.path.join(sample, "manifest.jsonl"))]
    os.makedirs(OUT, exist_ok=True)
    envf = os.path.join(OUT, "environment.json")
    digest = env_digest()
    old = json.load(open(envf)) if os.path.exists(envf) else {}
    fresh = old.get("digest") != digest
    verdicts = {v: {} for v in LOCKS}
    if not fresh:
        for v in LOCKS:
            path = os.path.join(OUT, v + ".jsonl")
            if os.path.exists(path):
                for l in open(path):
                    d = json.loads(l)
                    verdicts[v][d["sha256"]] = d
    # La poda: solo se guarda lo que la muestra de hoy usa.
    current = {v: {m["sha256"] for m in manifest if m["version"] == v} for v in LOCKS}
    for v in LOCKS:
        verdicts[v] = {k: d for k, d in verdicts[v].items() if k in current[v]}
    pending = {v: [m for m in manifest if m["version"] == v and m["sha256"] not in verdicts[v]] for v in LOCKS}
    total = sum(len(p) for p in pending.values())
    print("sample: %d files, %d with a cached verdict, %d to judge (%s)" % (len(manifest), len(manifest) - total, total, "ambiente nuevo" if fresh else "ambiente igual"), flush=True)
    stats = []
    base = tempfile.mkdtemp(prefix="validator-projection-", dir=os.environ.get("TMPDIR", tempfile.gettempdir()))
    lock = threading.Lock()
    try:
        for ver in LOCKS:
            if not pending[ver]:
                continue
            home = make_home(base, ver)
            work = os.path.join(base, "work-" + ver)
            shutil.copytree(os.path.join(sample, ver), os.path.join(work, ver))
            batches = [pending[ver][i:i + BATCH] for i in range(0, len(pending[ver]), BATCH)]
            counter = [0]

            def worker():
                while True:
                    with lock:
                        if not batches:
                            return
                        b = batches.pop(0)
                        counter[0] += 1
                        n = counter[0]
                    files = [os.path.join(work, m["file"]) for m in b]
                    res, secs, rss = run_batch(base, home, ver, files, n)
                    with lock:
                        for m in b:
                            verdicts[ver][m["sha256"]] = {"sha256": m["sha256"], "name": m["name"], "mode": m["mode"],
                                                          "errors": res[os.path.normpath(os.path.join(work, m["file"]))]}
                        stats.append((ver, len(b), secs, rss))
                        print("%-4s batch of %2d files: %6.1f s, %5.1f s/file, max RSS %d MB" % (ver, len(b), secs, secs / len(b), rss), flush=True)

            ts = [threading.Thread(target=worker) for _ in range(WORKERS)]
            [t.start() for t in ts]
            [t.join() for t in ts]
    finally:
        shutil.rmtree(base, ignore_errors=True)
    for v in LOCKS:
        with open(os.path.join(OUT, v + ".jsonl"), "w") as f:
            for d in sorted(verdicts[v].values(), key=lambda d: (d["name"], d["mode"], d["sha256"])):
                f.write(json.dumps(d, ensure_ascii=False, sort_keys=True) + "\n")
    json.dump({"digest": digest, "validator": "validator_cli 7.0.2 (scripts/validator.sha256)"}, open(envf, "w"), indent=1)
    if stats:
        files = sum(s[1] for s in stats)
        wall = sum(s[2] for s in stats) / WORKERS
        print("judged %d files with %d JVM: %.0f s of JVM time (about %.0f s of wall time), %.1f s/file, max RSS %d MB" % (
            files, WORKERS, sum(s[2] for s in stats), wall, sum(s[2] for s in stats) / files, max(s[3] for s in stats)))
        with open(os.path.join(R, "docs", "evidence", datetime.date.today().isoformat() + "-projection-hl7-cost.md"), "w") as f:
            f.write("# Cost of make hl7-projection\n\nGenerated by `scripts/validator_projection.py`. validator_cli 7.0.2, `-tx n/a -no-http-access`, %d JVM, batches of %d files.\n\n" % (WORKERS, BATCH))
            f.write("- Files judged in this run: %d (of %d in the sample; the rest came from the cache by SHA-256).\n" % (files, len(manifest)))
            f.write("- JVM time: %.0f s in total, %.1f s per file; about %.0f s of wall time with %d JVM.\n" % (sum(s[2] for s in stats), sum(s[2] for s in stats) / files, wall, WORKERS))
            f.write("- Peak RSS of one JVM: %d MB.\n" % max(s[3] for s in stats))


if __name__ == "__main__":
    main()
