#!/usr/bin/env python3
# Corre las mutaciones de los oráculos (carta §10, «Los oráculos se prueban con
# mutaciones»; plan 001 §4): cada una inyecta un defecto en una copia del repo
# y exige que algún test lo detecte. Una mutación cuyo texto no aparece
# exactamente una vez, o que no compila, es un error del script, no una
# detección. Escribe docs/evidence/<fecha>-<hora>-mutations.md y termina con
# error si alguna mutación no la detecta ningún test.
#
# Una mutación ya detectada no se vuelve a correr si no cambió nada de lo que la
# puede afectar (clave: la mutación, los .go, testdata/ y scripts/packages.sha256).
# MUTATION_FRESH=1 ignora la caché (cierre de un incremento). MUTATION_WORKERS
# fija los workers (por defecto 3).
#
# Uso: python3 -I scripts/mutations.py [prefijo ...]   (p. ej. M01 M07)
import concurrent.futures, datetime, hashlib, json, os, re, shutil, subprocess, sys, tempfile, time

R = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CACHE = os.path.join(R, ".cache", "mutations.json")
CACHE_VERSION = 1

def moved_excluded_block():
    # Mueve la verificación de los excluidos de antes del bucle principal a
    # después (el texto se lee de open.go para no duplicarlo).
    src = open(os.path.join(R, "open.go")).read()
    a = src.index("\tfor _, lp := range lock.Packages {\n\t\tif !lp.Excluded {")
    b = src.index("\ts := &Set{lock: lock")
    block = src[a:b]
    return [("open.go", block, ""), ("open.go", "\treturn s, nil\n}\n\n// readLocked", block + "\treturn s, nil\n}\n\n// readLocked")]

# nombre: [(archivo, texto exacto, reemplazo), ...]
muts = {
 "M01-sha-ignored": [("open.go", "if got := hex.EncodeToString(sum[:]); got != lp.SHA256 {", "if got := hex.EncodeToString(sum[:]); false && got != lp.SHA256 {")],
 "M02-missing-dependency-tolerated": [("graph.go", "import (\n\t\"context\"", "import (\n\t\"context\"\n\t\"errors\""),
   ("graph.go", "\t\t\treturn nil, fmt.Errorf(\"%s (requested by %s): %w\", id, orRoot(n.Requests[0].From), err)", "\t\t\tif errors.Is(err, ErrNotFound) {\n\t\t\t\tdelete(g.Nodes, id)\n\t\t\t\tcontinue\n\t\t\t}\n\t\t\treturn nil, fmt.Errorf(\"%s (requested by %s): %w\", id, orRoot(n.Requests[0].From), err)")],
 "M03-foreign-core-not-excluded": [("resolve.go", "\t\texcluded[id] = true\n\t}\n\tl := Lock{", "\t\t_ = id\n\t}\n\tl := Lock{")],
 "M04-double-version-picks-one": [("resolve.go", "if len(a.Doubles) > 0 {", "if false && len(a.Doubles) > 0 {")],
 "M05-legacy-version-list-ignored": [("manifest.go", "case len(raw.FHIRVersionList) > 0:", "case false && len(raw.FHIRVersionList) > 0:")],
 "M06-unsafe-tgz-accepted": [("tgz.go", "if err := checkEntryName(h.Name); err != nil {", "if err := checkEntryName(h.Name); false && err != nil {")],
 "M07-links-accepted": [("tgz.go", "case tar.TypeDir:\n\t\t\tcontinue", "case tar.TypeDir, tar.TypeSymlink, tar.TypeLink:\n\t\t\tcontinue")],
 "M08-entry-size-limit-ignored": [("tgz.go", "if h.Size < 0 || h.Size > lim.MaxEntry {", "if h.Size < 0 {")],
 "M09-total-size-limit-ignored": [("tgz.go", "if total > lim.MaxTotal {", "if false {")],
 "M10-duplicate-entry-accepted": [("tgz.go", "if seen[name] {", "if false {")],
 "M11-cycle-tolerated": [("resolve.go", "if len(a.Cycle) > 0 {", "if false && len(a.Cycle) > 0 {")],
 "M12-unused-exception-tolerated": [("resolve.go", "if len(unused) > 0 {", "if false {")],
 "M13-foreign-package-needs-no-exception": [("resolve.go", "if _, ok := exc[id]; ok {", "if true {")],
 "M14-wildcard-picks-lowest": [("version.go", "compareSemver(sv, best) > 0", "compareSemver(sv, best) < 0")],
 "M15-wildcard-takes-prerelease": [("version.go", "if !ok || sv.pre != \"\" || fmt.Sprintf(\"%d.%d\", sv.major, sv.minor) != v.prefix {", "if !ok || fmt.Sprintf(\"%d.%d\", sv.major, sv.minor) != v.prefix {")],
 "M16-open-skips-series-check": [("open.go", "!slices.Contains(m.Series(), coreSeries) && !exc[id]", "false")],
 "M17-open-skips-manifest-check": [("open.go", "if err := matchLocked(c.manifest, lp); err != nil {", "if err := matchLocked(c.manifest, lp); false && err != nil {")],
 "M18-subfolder-files-read": [("tgz.go", "if !ok || strings.Contains(rest, \"/\") {", "if !ok {")],
 "M19-root-order-matters": [("graph.go", "\tsort.Strings(sorted)\n\tg := &Graph", "\t_ = sort.Strings\n\tg := &Graph")],
 "M20-core-with-several-versions-accepted": [("graph.go", "if len(m.FHIRVersions) != 1 {", "if len(m.FHIRVersions) < 1 {")],
 "M21-lock-order-not-checked": [("lock.go", "if j, ok := pos[d]; ok && j >= i {", "if j, ok := pos[d]; ok && j >= i && false {")],
 "M22-version-range-accepted": [("version.go", "strings.ContainsAny(s, \" \\t\\n^~<>=|#/\")", "false")],
 "M23-fhir-core-type-not-recognized": [("manifest.go", "t == \"core\" || t == \"fhir.core\"", "t == \"core\"")],
 "M24-resources-not-sorted": [("tgz.go", "\tsort.Slice(out.resources, func(i, j int) bool { return out.resources[i].File < out.resources[j].File })", "\t_ = sort.Slice")],
 "M25-overlap-identical-always": [("overlap.go", "if ra.Hash == rb.Hash {", "if true {")],
 "M26-overlap-semver-always": [("overlap.go", "if sa && sb {", "if sa || sb || true {")],
 "M27-exceptions-not-kept-in-lock": [("resolve.go", "\tl.Exceptions = append(l.Exceptions, opts.Exceptions...)\n", "")],
 "M28-lock-unknown-fields-accepted": [("lock.go", "\tdec.DisallowUnknownFields()\n", "")],
 "M29-first-version-of-a-double-wins": [("graph.go", "\t\tif len(byName[name]) < 2 {", "\t\tif len(byName[name]) < 3 {")],
 "M30-via-not-recorded": [("graph.go", "n.Via = append(append([]string(nil), g.Nodes[from].Via...), from)", "n.Via = nil")],
 "M31-sha-of-excluded-core-checked-as-read": [("lock.go", "if !ok || p.Excluded {\n\t\t\treturn fmt.Errorf(\"invalid lock: order lists", "if !ok || p.Excluded && false {\n\t\t\treturn fmt.Errorf(\"invalid lock: order lists")],
 "M32-excluded-non-core-accepted": [("open.go", "\t\tif !m.IsCore() {\n\t\t\treturn nil, fmt.Errorf(\"%s: excluded, but", "\t\tif false {\n\t\t\treturn nil, fmt.Errorf(\"%s: excluded, but")],
 "M33-excluded-core-of-the-locks-series-accepted": [("open.go", "\t\tif len(m.Series()) == 0 || slices.Contains(m.Series(), coreSeries) {\n\t\t\treturn nil, fmt.Errorf(\"%s: excluded", "\t\tif false {\n\t\t\treturn nil, fmt.Errorf(\"%s: excluded")],
 "M34-excluded-packages-not-verified": [("open.go", "\t\tif !lp.Excluded {\n\t\t\tcontinue", "\t\tif true {\n\t\t\tcontinue")],
 "M35-lock-core-need-not-be-core": [("open.go", "if id == lock.Core && !m.IsCore() {", "if false {")],
 "M36-excluded-root-accepted": [("lock.go", "\t\tif rp.Excluded {", "\t\tif false && rp.Excluded {")],
 "M37-undeclared-counts-cores": [("graph.go", "len(n.Manifest.FHIRVersions) == 0 && !n.Manifest.IsCore()", "len(n.Manifest.FHIRVersions) == 0")],
 "M38-lock-duplicate-dependency-accepted": [("lock.go", "if depNames[name] {", "if false {")],
 "M39-manifest-dependency-missing-from-lock-tolerated": [("open.go", "\t\tif !locked[name] {", "\t\tif false {")],
 "M40-excluded-core-without-series-accepted": [("open.go", "len(m.Series()) == 0 || slices.Contains(m.Series(), coreSeries)", "slices.Contains(m.Series(), coreSeries)")],
 "M41-scan-without-resources-stops-at-the-manifest": [("tgz.go", "\t\t\tmanifest = body\n\t\t\tcontinue", "\t\t\tmanifest = body\n\t\t\tif !withResources {\n\t\t\t\tbreak\n\t\t\t}\n\t\t\tcontinue")],
 "M42-excluded-verified-after-the-main-loop": moved_excluded_block(),
 "M44-gzip-trailer-not-verified": [("tgz.go", "if _, err := io.Copy(io.Discard, zr); err != nil {", "if _, err := io.Copy(io.Discard, zr); false && err != nil {")],
 "M43-scan-without-resources-ignores-tar-errors": [("tgz.go", "\t\t\treturn nil, fmt.Errorf(\"invalid tar: %w\", err)", "\t\t\tif !withResources {\n\t\t\t\tbreak\n\t\t\t}\n\t\t\treturn nil, fmt.Errorf(\"invalid tar: %w\", err)")],
}

def sha(*parts):
    h = hashlib.sha256()
    for p in parts:
        h.update(p if isinstance(p, bytes) else p.encode())
        h.update(b"\0")
    return h.hexdigest()

def tree_hash():
    items = []
    for root, dirs, files in os.walk(R):
        dirs[:] = sorted(d for d in dirs if d not in (".git", ".cache", "__pycache__", "docs"))
        for f in sorted(files):
            if f.endswith(".go") or root.startswith(os.path.join(R, "testdata")) or f in ("packages.sha256", "go.mod"):
                p = os.path.join(root, f)
                items.append(os.path.relpath(p, R))
                items.append(open(p, "rb").read())
    return sha(*items)

def load_cache():
    try:
        c = json.load(open(CACHE))
        return c.get("entries", {}) if c.get("version") == CACHE_VERSION else {}
    except Exception:
        return {}

def save_cache(entries):
    os.makedirs(os.path.dirname(CACHE), exist_ok=True)
    json.dump({"version": CACHE_VERSION, "entries": entries}, open(CACHE, "w"), indent=1, sort_keys=True)

def run_mutation(name, pairs, tmp_root):
    work = tempfile.mkdtemp(prefix="mut-", dir=tmp_root)
    try:
        dst = os.path.join(work, "repo")
        shutil.copytree(R, dst, ignore=shutil.ignore_patterns(".git", ".cache", "__pycache__", "docs"))
        for f, old, new in pairs:
            p = os.path.join(dst, f)
            s = open(p).read()
            if s.count(old) != 1:
                return {"status": "error", "detail": "%s: the text appears %d times, want 1: %r" % (f, s.count(old), old[:70])}
            open(p, "w").write(s.replace(old, new))
        env = dict(os.environ, FHIRPKG_PACKAGES=os.path.join(R, ".cache", "packages"))
        start = time.time()
        try:
            out = subprocess.run(["go", "test", "-count=1", "./..."], cwd=dst, env=env, capture_output=True, text=True, timeout=900).stdout
        except subprocess.TimeoutExpired:
            return {"status": "detected", "detail": "timeout (the mutation hangs a test)", "by": ["timeout"], "secs": 900}
        secs = round(time.time() - start, 1)
        if "[build failed]" in out or "\n# " in "\n" + out and "cannot" in out and "--- FAIL" not in out:
            return {"status": "error", "detail": "does not compile: " + out.strip().splitlines()[0][:150]}
        by = sorted(set(re.findall(r"^--- FAIL: (\S+)", out, re.M)))
        if by:
            return {"status": "detected", "by": by, "secs": secs}
        if re.search(r"^FAIL", out, re.M):
            return {"status": "error", "detail": "FAIL without a test name: " + out.strip().splitlines()[-1][:150]}
        return {"status": "undetected", "secs": secs}
    finally:
        shutil.rmtree(work, ignore_errors=True)

def main():
    prefixes = sys.argv[1:]
    names = [n for n in muts if not prefixes or any(n.startswith(p) for p in prefixes)]
    if not names:
        sys.exit("no mutation matches")
    workers = int(os.environ.get("MUTATION_WORKERS", "3"))
    fresh = os.environ.get("MUTATION_FRESH") == "1"
    base = tree_hash()
    cache = {} if fresh else load_cache()
    tmp_root = os.environ.get("TMPDIR", tempfile.gettempdir())
    keys = {n: sha(base, json.dumps(muts[n]), subprocess.run(["go", "version"], capture_output=True, text=True).stdout) for n in names}
    results = {}
    todo = []
    for n in names:
        c = cache.get(n)
        if c and c["key"] == keys[n] and c["result"]["status"] == "detected":
            results[n] = dict(c["result"], cached=True)
        else:
            todo.append(n)
    t0 = time.time()
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as ex:
        futs = {ex.submit(run_mutation, n, muts[n], tmp_root): n for n in todo}
        for f in concurrent.futures.as_completed(futs):
            n = futs[f]
            results[n] = f.result()
            print("%-48s %s" % (n, results[n]["status"]), flush=True)
    entries = load_cache() if not fresh else {}
    entries.update({n: {"key": keys[n], "result": results[n]} for n in names if results[n]["status"] == "detected"})
    save_cache(entries)

    stamp = datetime.datetime.now().strftime("%Y-%m-%d-%H%M")
    os.makedirs(os.path.join(R, "docs", "evidence"), exist_ok=True)
    path = os.path.join(R, "docs", "evidence", stamp + "-mutations.md")
    bad = [n for n in names if results[n]["status"] != "detected"]
    with open(path, "w") as f:
        f.write("# Mutations %s\n\nGenerated by `make mutations` with %s; %d mutations, %d workers, %d s (%d reused from the cache).\n\n" % (
            stamp, subprocess.run(["go", "env", "GOVERSION"], capture_output=True, text=True).stdout.strip(), len(names), workers, round(time.time() - t0), sum(1 for n in names if results[n].get("cached"))))
        f.write("| Mutation | Result | Detected by | s |\n|---|---|---|---|\n")
        for n in names:
            r = results[n]
            by = ", ".join(r.get("by", [])[:4]) + (" ..." if len(r.get("by", [])) > 4 else "")
            f.write("| %s | %s%s | %s | %s |\n" % (n, r["status"], " (cached)" if r.get("cached") else "", by or r.get("detail", ""), r.get("secs", "")))
        f.write("\n%s\n" % ("All mutations detected." if not bad else "**Not detected or invalid: %s**" % ", ".join(bad)))
    print(path)
    if bad:
        sys.exit("not detected or invalid: " + ", ".join(bad))

main()
