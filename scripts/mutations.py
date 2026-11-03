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
 # Plan 002: la cascada de canónicos, la regla de base:code y la herencia.
 "M45-series-step-off": [("cascade.go", "\t\tif !declaresOtherSeries(c.info.fhirVersions, coreSeries) {", "\t\tif true {")],
 "M46-series-step-inverted": [("cascade.go", "\t\tif !declaresOtherSeries(c.info.fhirVersions, coreSeries) {", "\t\tif declaresOtherSeries(c.info.fhirVersions, coreSeries) {")],
 "M47-series-applied-to-resources-without-fhirVersion": [("cascade.go", "\tdeclared := false\n\tfor _, v := range versions {", "\tdeclared := true\n\tfor _, v := range versions {")],
 "M48-series-all-losers-drops-everyone": [("cascade.go", "\tif len(keep) == 0 {\n\t\treturn cs, \"every candidate declares another series\"", "\tif false {\n\t\treturn cs, \"every candidate declares another series\"")],
 "M49-series-list-uses-only-the-first-version": [("cascade.go", "\t\tif s == coreSeries {\n\t\t\treturn false\n\t\t}", "\t\tif s == coreSeries {\n\t\t\treturn false\n\t\t}\n\t\tdeclared = true\n\t\tbreak")],
 "M50-status-step-off": [("cascade.go", "\t\tif statusRank(c.info.status) == best {", "\t\tif true {")],
 "M51-status-active-lowest": [("cascade.go", "\tcase statusActive:\n\t\treturn 3", "\tcase statusActive:\n\t\treturn -1")],
 "M52-status-retired-same-as-unknown": [("cascade.go", "\t\treturn 0\n\t}\n\treturn 1\n}", "\t\treturn 1\n\t}\n\treturn 1\n}")],
 "M53-status-unknown-same-as-draft": [("cascade.go", "\t\treturn 0\n\t}\n\treturn 1\n}", "\t\treturn 0\n\t}\n\treturn 2\n}")],
 "M54-publisher-step-off": [("cascade.go", "\t\tif !c.copy {", "\t\tif true {")],
 "M55-publisher-step-inverted": [("cascade.go", "\t\tif !c.copy {", "\t\tif c.copy {")],
 "M56-publisher-by-prefix-instead-of-host": [("cascade.go", "\treturn p != \"\" && r != \"\" && p != r", "\t_, _ = p, r\n\treturn pkgCanonical != \"\" && !strings.HasPrefix(resourceURL, pkgCanonical)")],
 "M57-publisher-host-case-sensitive": [("cascade.go", "\treturn strings.ToLower(u.Host)", "\treturn u.Host")],
 "M58-version-step-off": [("cascade.go", "\to, ok := chooseOrder(ds)\n\tif !ok {", "\to, ok := chooseOrder(ds)\n\tif true || !ok {")],
 "M59-version-step-inverted": [("cascade.go", "\t\tif o.cmp(c.res.Version, best.res.Version) > 0 {", "\t\tif o.cmp(c.res.Version, best.res.Version) < 0 {")],
 "M60-version-comparator-per-valid-subset": [("order.go", "\t\t\tif !o.valid(d.Version) {\n\t\t\t\treturn false\n\t\t\t}", "\t\t\tif !o.valid(d.Version) {\n\t\t\t\tcontinue\n\t\t\t}")],
 "M61-version-declared-algorithm-ignored": [("order.go", "\tif same {\n\t\tif o, ok := orderByName", "\tif false && same {\n\t\tif o, ok := orderByName")],
 "M62-version-expression-treated-as-comparable": [("order.go", "\t\tif d.Expression {\n\t\t\treturn versionOrder{}, false", "\t\tif false && d.Expression {\n\t\t\treturn versionOrder{}, false")],
 "M63-lastupdated-step-off": [("cascade.go", "\t\tif ts[i].Equal(best) {", "\t\tif true || ts[i].Equal(best) {")],
 "M64-lastupdated-step-inverted": [("cascade.go", "\t\tif t.After(best) {", "\t\tif t.Before(best) {")],
 "M65-lastupdated-compared-without-zone": [("cascade.go", "\t\tts[i] = t\n", "\t\tts[i] = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)\n")],
 "M66-lastupdated-missing-instant-tolerated": [("cascade.go", "\t\tif err != nil {\n\t\t\treturn cs, \"an instant is missing or unreadable\"", "\t\tif err != nil {\n\t\t\tt = time.Time{}\n\t\t}\n\t\tif false {\n\t\t\treturn cs, \"an instant is missing or unreadable\"")],
 "M67-tie-picks-the-first": [("cascade.go", "\td.by = StepUnresolved\n\treturn d", "\td.winner, d.by = cur[0], StepLastUpdated\n\treturn d")],
 "M68-publisher-and-version-steps-swapped": [("cascade.go", "\t\t{StepPublisher, stepPublisher},\n\t\t{StepVersion, stepVersion},", "\t\t{StepVersion, stepVersion},\n\t\t{StepPublisher, stepPublisher},")],
 "M69-status-and-series-steps-swapped": [("cascade.go", "\t\t{StepSeries, func(c []*cand) ([]*cand, string) { return stepSeries(c, coreSeries) }},\n\t\t{StepStatus, stepStatus},", "\t\t{StepStatus, stepStatus},\n\t\t{StepSeries, func(c []*cand) ([]*cand, string) { return stepSeries(c, coreSeries) }},")],
 "M70-key-by-url-only": [("context.go", "g := c.groups[key{r.ResourceType, r.URL}]\n", "g := c.groups[key{\"\", r.URL}]\n"), ("context.go", "c.groups[key{r.ResourceType, r.URL}] = g", "c.groups[key{\"\", r.URL}] = g")],
 "M71-experimental-wins": [("searchparameters.go", "return !docs[cd].sp.Experimental }}", "return docs[cd].sp.Experimental }}")],
 "M72-base-code-core-step-off": [("searchparameters.go", "return cd.pkgID == core }}", "return false }}")],
 "M73-base-code-ig-beats-core": [("searchparameters.go", "return cd.pkgID == core }}", "return cd.pkgID != core }}")],
 "M74-base-code-unresolved-picks-the-first": [("searchparameters.go", "\t\tresolved[bc] = winner\n", "\t\tif winner == nil {\n\t\t\twinner = cs[0]\n\t\t}\n\t\tresolved[bc] = winner\n")],
 "M75-base-code-unresolved-does-not-block-ancestors": [("searchparameters.go", "\t\t\t\tseen[code] = true // un base:code sin resolver bloquea el code en los niveles siguientes\n", "\t\t\t\tif resolved[baseCode{level, code}] != nil {\n\t\t\t\t\tseen[code] = true\n\t\t\t\t}\n")],
 "M76-inheritance-cut-after-one-level": [("searchparameters.go", "\t\t\tchain = append(chain, p)\n\t\t\tt = p\n", "\t\t\tchain = append(chain, p)\n\t\t\tbreak\n")],
 "M77-inherited-wins-over-the-types-own": [("searchparameters.go", "\t\tfor _, level := range c.chains[t] {", "\t\tfor li := len(c.chains[t]) - 1; li >= 0; li-- {\n\t\t\tlevel := c.chains[t][li]")],
 "M78-profile-counts-as-resource-type": [("searchparameters.go", "d.Derivation != derivationConstraint", "true")],
 "M79-logical-model-counts-as-resource-type": [("searchparameters.go", "d != nil && d.Kind == kindResource &&", "d != nil &&")],
 "M80-resolvefrom-without-graph-restriction": [("context.go", "\treturn c.resolve(c.visible[i], resourceType, canonical)", "\t_ = i\n\treturn c.resolve(nil, resourceType, canonical)")],
 "M81-dependency-closure-not-transitive": [("context.go", "\t\t\tfor _, d := range deps[x] {\n\t\t\t\twalk(d)\n\t\t\t}", "\t\t\tfor _, d := range deps[x] {\n\t\t\t\tif j, ok := index[d]; ok {\n\t\t\t\t\tseen[j] = true\n\t\t\t\t}\n\t\t\t}")],
 "M82-fragment-ignored": [("context.go", "\tif cn.Fragment == \"\" {\n\t\treturn out, nil", "\tif true {\n\t\treturn out, nil")],
 "M83-version-ignored": [("context.go", "\tif cn.Version != \"\" {\n\t\tvar same []*cand", "\tif false && cn.Version != \"\" {\n\t\tvar same []*cand")],
 "M84-version-ambiguity-picks-the-first": [("context.go", "\t\tcase 1:\n\t\t\treturn same[0], nil\n\t\t}", "\t\tdefault:\n\t\t\treturn same[0], nil\n\t\t}")],
 "M85-type-ambiguity-tolerated": [("context.go", "\treturn \"\", fmt.Errorf(\"%w: %s is the url of %s: pass the type\", ErrAmbiguousType, url, strings.Join(found, \", \"))", "\treturn found[0], nil")],
 "M86-candidates-not-sorted": [("context.go", "\t\t\t\treturn x\n\t\t\t}\n\t\t\treturn strings.Compare(a.res.File, b.res.File)", "\t\t\t\treturn 0\n\t\t\t}\n\t\t\treturn 0")],
 "M87-conflicts-shares-its-data": [("context.go", "\t\tout[i] = cloneConflict(cf)", "\t\tout[i] = cf")],
 "M88-searchparameters-shares-its-data": [("context.go", "\tout := slices.Clone(sps)", "\tout := sps")],
 "M89-resolve-copies-the-bytes": [("context.go", "\tout := Resolved{Package: win.pkgID, Resource: *win.res}\n", "\tout := Resolved{Package: win.pkgID, Resource: *win.res}\n\tout.Resource.Raw = slices.Clone(out.Resource.Raw)\n")],
 "M90-fhir-name-outside-fhirnames": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = \"status\"\n\nfunc candidateList(cs []*cand) string {")],
 "M91-json-tag-without-source": [("context.go", "\tCode string   `json:\"code\"`", "\tCode string   `json:\"codigo\"`")],
 "M92-canonical-empty-version-accepted": [("canonical.go", "if hasVersion && (version == \"\" || strings.Contains(version, \"|\")) {", "if hasVersion && strings.Contains(version, \"|\") {")],
 "M93-semver-release-below-prerelease": [("order.go", "\tcase len(a.pre) == 0:\n\t\treturn 1 // una versión sin prerelease es mayor", "\tcase len(a.pre) == 0:\n\t\treturn -1")],
 "M94-semver-numbers-compared-as-text": [("order.go", "\tif len(a) != len(b) {\n\t\tif len(a) < len(b) {", "\tif false {\n\t\tif len(a) < len(b) {")],
 "M95-date-accepts-impossible-day": [("order.go", "di > daysIn(yi, mi)", "di > daysIn(yi, 1)")],
 "M96-date-accepts-year-zero": [("order.go", " || y == \"0000\"", "")],
 "M97-natural-compares-digits-as-text": [("order.go", "\t\t\tif c := cmpDigits(ta, tb); c != 0 {", "\t\t\tif c := strings.Compare(ta, tb); c != 0 {")],
 "M98-searchparameter-without-base-is-an-error": [("searchparameters.go", "\t\t\tcontinue\n\t\t}\n\t\tif d.Code == \"\" {\n\t\t\treturn fmt.Errorf(\"%s: %s: search parameter %s has no code\", w.pkgID", "\t\t\treturn fmt.Errorf(\"no base\")\n\t\t}\n\t\tif d.Code == \"\" {\n\t\t\treturn fmt.Errorf(\"%s: %s: search parameter %s has no code\", w.pkgID")],
 "M99-unresolved-sp-canonical-falls-back-to-the-first-candidate": [("searchparameters.go", "\t\t\tif err := collectUnresolved(k.url, c.groups[k].cands, docs, unresolvedBC, disagree); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n\t\t\tcontinue", "\t\t\tw = c.groups[k].cands[0]")],
 "M100-guard-bypass-strings-fields": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = strings.Fields(\"ValueSet CodeSystem\")\n\nfunc candidateList(cs []*cand) string {")],
 "M101-guard-bypass-leading-space": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = \" SearchParameter\"\n\nfunc candidateList(cs []*cand) string {")],
 "M102-guard-bypass-in-a-001-file": [("lock.go", "// LockSchema es la versión", "var leaked = \"lastUpdated\"\n\n// LockSchema es la versión")],
 "M103-guard-bypass-in-cmd": [("cmd/fhirpkg/main.go", "func usage() {", "var leaked = \"status\"\n\nfunc usage() {")],
 "M104-guard-bypass-verb-prefix": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = \"%sstatus\"\n\nfunc candidateList(cs []*cand) string {")],
 "M105-version-algorithm-system-not-checked": [("context.go", "if d.AlgorithmCoding.System == systemVersionAlgorithm {", "if true {")],
 "M106-explain-single-candidate-without-status": [("context.go", "\t\tcf.Candidates[i].Status = head.Status\n", "\t\t_, _ = i, head\n")],
 "M107-abstract-types-listed": [("searchparameters.go", "\t\tif !d.abstract {\n\t\t\tc.concrete = append(c.concrete, name)\n\t\t}", "\t\tc.concrete = append(c.concrete, name)")],
 "M108-abstract-types-not-inherited": [("searchparameters.go", "\t\tparent[name] = bd.Type", "\t\tif !bd.Abstract {\n\t\t\tparent[name] = bd.Type\n\t\t}")],
 "M109-ambiguous-type-without-sentinel": [("context.go", "fmt.Errorf(\"%w: %s is the url of %s: pass the type\", ErrAmbiguousType, url,", "fmt.Errorf(\"%s: %s is the url of %s: pass the type\", \"ambiguous\", url,")],
 "M110-guard-bypass-camelcase-words": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = strings.Fields(\"valueQuantity effectiveDateTime\")\n\nfunc candidateList(cs []*cand) string {")],
 "M111-guard-bypass-list-of-fields": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = strings.Fields(\"url version status\")\n\nfunc candidateList(cs []*cand) string {")],
 "M112-guard-bypass-trim-suffix-verb": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = strings.TrimSuffix(\"status %s\", \" %s\")\n\nfunc candidateList(cs []*cand) string {")],
 "M113-guard-bypass-abstract-type-name": [("context.go", "func candidateList(cs []*cand) string {", "var leaked = \"see DomainResource and Resource\"\n\nfunc candidateList(cs []*cand) string {")],
 "M114-unresolved-sp-candidate-without-code-tolerated": [("searchparameters.go", "\t\tif d.Code == \"\" {\n\t\t\treturn fmt.Errorf(\"%s: %s: search parameter %s has no code\", cd.pkgID, cd.res.File, url)\n\t\t}", "\t\t_ = url")],
 "M115-candidate-order-ignores-file": [("searchparameters.go", "\t\t\treturn strings.Compare(a.res.File, b.res.File)", "\t\t\treturn 0")],
 "R1-X1-resolvefrom-uses-global-decision": [("context.go", "\tif vis != nil {\n\t\td = decide(cands, c.coreSeries)\n\t}", "\tif false {\n\t\td = decide(cands, c.coreSeries)\n\t}")],
 "R1-X2-basecode-allfail-no-fallback": [("searchparameters.go", "\t\t\tnext = cur // todos pierden: el paso no separa", "\t\t\t_ = next")],
 "R1-X3-unresolved-resource-SD-tolerated": [("searchparameters.go", "\t\t\t\t\treturn fmt.Errorf(\"%s: the definition of resource type %q is unresolved (%s): no type can be taken from it\", k.url, d.Type, explainTrace(g.dec.trace))", "\t\t\t\t\t_ = d")],
 "R1-X4-duplicate-type-tolerated": [("searchparameters.go", "\t\t\treturn fmt.Errorf(\"resource type %q is defined by more than one structure definition (%s and %s)\", d.Type, prev.url, k.url)", "\t\t\t_ = prev")],
 "R1-X5-cycle-tolerated": [("searchparameters.go", "\t\t\t\treturn fmt.Errorf(\"inheritance cycle through resource type %s: %s\", p, strings.Join(append(chain, p), \" -> \"))", "\t\t\t\tbreak")],
 "R1-X6-typefor-ignores-visibility": [("context.go", "\t\tif len(c.visibleCands(c.groups[key{t, url}], vis)) > 0 {", "\t\tif len(c.visibleCands(c.groups[key{t, url}], nil)) > 0 {")],
 "R1-X7-base-not-deduped": [("searchparameters.go", "\t\t\tif !seen[b] {", "\t\t\tif true {")],
 "R1-X8-empty-pkg-canonical-is-copy": [("cascade.go", "\treturn p != \"\" && r != \"\" && p != r", "\treturn r != \"\" && p != r")],
 "R1-X9-natural-number-after-text": [("order.go", "\t\tcase ad:\n\t\t\treturn -1", "\t\tcase ad:\n\t\t\treturn 1")],
 "R1-X10-integer-negative-not-inverted": [("order.go", "\tif a.neg {\n\t\treturn -c\n\t}", "\tif false {\n\t\treturn -c\n\t}")],
 "R1-X11-codes-unsorted-within-level": [("searchparameters.go", "\t\treturn bcs[i].code < bcs[j].code", "\t\treturn false")],
 "R1-X12-baseDefinition-nonresource-not-end": [("searchparameters.go", "\t\tif !isResourceType(bd) {\n\t\t\tcontinue // fin de la cadena (R5: Base)", "\t\tif bd == nil {\n\t\t\tcontinue // fin de la cadena (R5: Base)")],
 "R1-X13-sp-no-code-tolerated": [("searchparameters.go", "\t\t\treturn fmt.Errorf(\"%s: %s: search parameter %s has no code\", w.pkgID, w.res.File, k.url)", "\t\t\tcontinue")],
 "R1-X14-series-uses-first-only": [("cascade.go", "\t\tif s == coreSeries {\n\t\t\treturn false\n\t\t}\n\t\tdeclared = true", "\t\treturn s != coreSeries")],
 "R1-X15-alpha-reversed": [("order.go", "return versionOrder{name, func(s string) bool { return s != \"\" }, strings.Compare}, true", "return versionOrder{name, func(s string) bool { return s != \"\" }, func(a, b string) int { return strings.Compare(b, a) }}, true")],
 "R1-X16-date-partial-padding": [("order.go", "\t\ty, m, d = s, \"01\", \"01\"", "\t\ty, m, d = s, \"12\", \"31\"")],
 "R1-X17-semver-build-counts": [("order.go", "\trest, build, hasBuild := strings.Cut(s, \"+\")", "\trest, build, hasBuild := s, \"\", false")],
 "R1-X18-conflict-winner-index-off": [("context.go", "\t\tif cd == d.winner {\n\t\t\tcf.Winner = i\n\t\t}\n\t}\n\treturn cf", "\t\tif cd == d.winner {\n\t\t\tcf.Winner = 0\n\t\t\t_ = i\n\t\t}\n\t}\n\treturn cf")],
 "R1-X19-fragment-wrong-match": [("context.go", "\t\tif head.ID == id {", "\t\tif head.ID == id || id != \"\" {")],
 "R1-Y1-codes-unsorted-within-level": [("searchparameters.go", "\t\treturn bcs[i].code < bcs[j].code", "\t\treturn bcs[i].code > bcs[j].code")],
 "R1-Y2-versioned-ignores-visibility": [("context.go", "\t\tfor _, cd := range cands {\n\t\t\tif cd.res.Version == cn.Version {", "\t\tfor _, cd := range g.cands {\n\t\t\tif cd.res.Version == cn.Version {")],
 "R1-Y3-no-date-inference": [("order.go", "\tfor _, name := range []string{algSemver, algDate} {", "\tfor _, name := range []string{algSemver} {")],
 "R1-Y4-series-nonversion-counts-as-declared": [("cascade.go", "\t\ts, ok := series(v)\n\t\tif !ok {\n\t\t\tcontinue\n\t\t}", "\t\ts, ok := series(v)\n\t\tif !ok {\n\t\t\tdeclared = true\n\t\t\tcontinue\n\t\t}")],
 "R1-Y5-conflicts-unsorted": [("context.go", "\t\tif a.Kind != b.Kind {\n\t\t\treturn a.Kind < b.Kind\n\t\t}", "\t\tif a.Kind != b.Kind {\n\t\t\treturn a.Kind > b.Kind\n\t\t}")],
 "R1-Y6-explain-unrestricted-type": [("context.go", "\trt, err := c.typeFor(resourceType, cn.URL, nil)\n\tif err != nil {\n\t\treturn Conflict{}, err\n\t}\n\tg := c.groups", "\trt := resourceType\n\tif rt == \"\" {\n\t\trt = c.typesOfURL[cn.URL][0]\n\t}\n\tg := c.groups")],
 "R1-Y7-canonical-space-in-url-accepted": [("canonical.go", "\tif strings.IndexFunc(url, unicode.IsSpace) >= 0 || strings.IndexFunc(frag, unicode.IsSpace) >= 0 {", "\tif false {")],
 "R1-Y8-contained-type-mismatch-allowed-fragment-on-version": [("context.go", "\tif cn.URL == \"\" {\n\t\treturn Resolved{}, fmt.Errorf(\"canonical %q is a fragment", "\tif cn.URL == \"x\" {\n\t\treturn Resolved{}, fmt.Errorf(\"canonical %q is a fragment")],
 "R1-Y9-unresolved-SD-tolerated": [("searchparameters.go", "\t\tif g.dec.winner == nil {\n\t\t\tfor _, cd := range g.cands {", "\t\tif g.dec.winner == nil {\n\t\t\tcontinue\n\t\t\tfor _, cd := range g.cands {")],
 "R1-Y10-sp-without-base-silently-and-code-check-first": [("searchparameters.go", "\t\tif d.Code == \"\" {\n\t\t\treturn fmt.Errorf(\"%s: %s: search parameter %s has no code\", w.pkgID", "\t\tif d.Code == \"zz\" {\n\t\t\treturn fmt.Errorf(\"%s: %s: search parameter %s has no code\", w.pkgID")],
 "R1-Y11-lastupdated-text-compare": [("cascade.go", "\t\tif t.After(best) {", "\t\tif t.String() > best.String() {")],
 # Plan 003: el modelo de tipos (reglas de §3.2, D4, H3 y H10). Los P-* corren solo
 # los oráculos de parity/ (el módulo aparte con models).
 "M116-type-rule-logical-accepted": [("modelbuild.go", "d != nil && d.Kind != kindLogical && d.Snapshot != nil &&", "d != nil && d.Snapshot != nil &&")],
 "M117-type-rule-snapshot-not-required": [("modelbuild.go", "d != nil && d.Kind != kindLogical && d.Snapshot != nil &&", "d != nil && d.Kind != kindLogical &&")],
 "M118-type-rule-constraint-accepted": [("modelbuild.go", "(d.Derivation == derivationSpecialization || (d.Derivation == \"\" && d.BaseDefinition == \"\"))", "true")],
 "M119-type-rule-root-without-derivation-rejected": [("modelbuild.go", "(d.Derivation == derivationSpecialization || (d.Derivation == \"\" && d.BaseDefinition == \"\"))", "d.Derivation == derivationSpecialization")],
 "M120-parent-by-last-url-segment": [("modelbuild.go", "\t\t\td.parent = docs[cd].sd.Type\n", "\t\t\t_ = cd\n\t\t\td.parent = d.doc.BaseDefinition[strings.LastIndex(d.doc.BaseDefinition, \"/\")+1:]\n")],
 "M121-parent-need-not-be-a-type": [("modelbuild.go", "\t\t\tif _, ok := defs[d.parent]; !ok {", "\t\t\tif false {")],
 "M122-issubtype-not-transitive": [("model.go", "for cur := child; cur != \"\"; cur = m.types[cur].parent {", "for cur := child; cur != \"\"; cur = \"\" {")],
 "M123-isresource-with-abstracts": [("modelbuild.go", "resource: d.doc.Kind == kindResource && !d.doc.Abstract,", "resource: d.doc.Kind == kindResource,")],
 "M124-isresource-without-kind": [("modelbuild.go", "resource: d.doc.Kind == kindResource && !d.doc.Abstract,", "resource: !d.doc.Abstract,")],
 "M125-slices-as-children": [("modelbuild.go", "\t\tif isSlice(e) {\n\t\t\tcontinue\n\t\t}\n\t\ti := strings", "\t\tif false {\n\t\t\tcontinue\n\t\t}\n\t\ti := strings")],
 "M126-choice-types-sorted": [("modelbuild.go", "\t\t\tm.choices[basePath] = codes\n", "\t\t\tm.choices[basePath] = slices.Sorted(slices.Values(codes))\n")],
 "M127-content-reference-hash-form-only": [("modelbuild.go", "from, to, ok := strings.Cut(e.ContentReference, \"#\")", "to, ok := strings.CutPrefix(e.ContentReference, \"#\")\n\t\t\tfrom := \"\"")],
 "M128-content-reference-keeps-the-url": [("modelbuild.go", "\t\t\tm.elsewhere[e.Path] = to\n", "\t\t\tm.elsewhere[e.Path] = e.ContentReference\n\t\t\t_ = to\n")],
 "M129-targets-not-deduplicated": [("modelbuild.go", "\t\t\t\t\tif !slices.Contains(out, tt) {", "\t\t\t\t\tif true {")],
 "M130-targets-by-url-segment": [("modelbuild.go", "\tb.targets[profile] = sd.Type\n\treturn sd.Type, nil", "\tt := profile[strings.LastIndex(profile, \"/\")+1:]\n\tb.targets[profile] = t\n\treturn t, nil")],
 "M131-typeof-takes-one-of-several": [("modelbuild.go", "\t\tif len(codes) == 1 {\n\t\t\tm.pathType[e.Path] = codes[0]", "\t\tif len(codes) >= 1 {\n\t\t\tm.pathType[e.Path] = codes[0]")],
 "M132-variant-without-capital": [("modelbuild.go", "variant := basePath + upperFirst(code)", "variant := basePath + code")],
 "M133-primitive-value-has-a-fhir-type": [("modelbuild.go", "\tif d.doc.Kind == kindPrimitive {\n\t\troot, _, _", "\tif false {\n\t\troot, _, _")],
 "M134-fhir-type-of-several-types": [("modelbuild.go", "\t\tif len(e.Type) == 1 {\n\t\t\tb.addFHIRType(d, e, e.Path)", "\t\tif len(e.Type) >= 1 {\n\t\t\tb.addFHIRType(d, e, e.Path)")],
 "M135-fhir-type-extension-url-not-checked": [("modelbuild.go", "if x.URL == urlFHIRTypeExtension && x.ValueURL != \"\" {", "if x.ValueURL != \"\" {")],
 "M136-primitive-own-elements-dropped": [("modelbuild.go", "\t\tparent, name := e.Path[:i], e.Path[i+1:]\n", "\t\tparent, name := e.Path[:i], e.Path[i+1:]\n\t\tif d.doc.Kind == kindPrimitive {\n\t\t\tcontinue\n\t\t}\n")],
 "M137-fhir-version-empty": [("modelbuild.go", "version:   c.set.lock.FHIRVersion,", "version:   \"\",")],
 "M138-cardinality-unbounded-as-one": [("modelbuild.go", "return int32(*e.Min), Unbounded, nil", "return int32(*e.Min), 1, nil")],
 "M139-cardinality-without-variants": [("modelbuild.go", "\t\t\t\tif err := add(variant); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n", "")],
 "M140-cardinality-without-choice-base": [("modelbuild.go", "\t\t\tif err := add(basePath); err != nil {\n\t\t\t\treturn err\n\t\t\t}\n", "")],
 "M141-cardinality-min-max-swapped": [("modelbuild.go", "m.card[path] = cardinality{lo, hi}", "m.card[path] = cardinality{hi, lo}")],
 "M142-two-definitions-of-a-type-tolerated": [("modelbuild.go", "\t\t\treturn nil, fmt.Errorf(\"type %q is defined by more than one structure definition (%s and %s)\", d.Type, prev.url, k.url)", "\t\t\t_ = prev\n\t\t\tcontinue")],
 "M143-unresolved-type-definition-tolerated": [("modelbuild.go", "\t\t\t\t\treturn nil, fmt.Errorf(\"%s: the definition of type %q is unresolved (%s): no type can be taken from it\", k.url, d.Type, explainTrace(g.dec.trace))", "\t\t\t\t\t_ = d")],
 "M144-type-inheritance-cycle-tolerated": [("modelbuild.go", "\t\t\t\treturn nil, fmt.Errorf(\"inheritance cycle through type %s: %s\", t, strings.Join(append(chain, t), \" -> \"))", "\t\t\t\tbreak")],
 "M145-path-outside-its-type-tolerated": [("modelbuild.go", "\t\tif top != typeName {", "\t\tif _ = top; false {")],
 "M146-repeated-path-tolerated": [("modelbuild.go", "\t\tif seen[e.Path] {", "\t\tif false {")],
 "M147-unresolved-base-definition-tolerated": [("modelbuild.go", "\t\t\t\treturn nil, fmt.Errorf(\"%s: type %s: base definition %s does not resolve: %w\", d.cand.pkgID, name, d.doc.BaseDefinition, err)", "\t\t\t\tcontinue")],
 "M148-content-reference-without-fragment-tolerated": [("modelbuild.go", "\t\t\tif !ok || to == \"\" {", "\t\t\tif false && (!ok || to == \"\") {")],
 "M149-max-not-a-count-tolerated": [("modelbuild.go", "\tif err != nil || n < 0 || n > 1<<30 {", "\tif false {")],
 "M150-min-out-of-range-tolerated": [("modelbuild.go", "\tif *e.Min < 0 || *e.Min > 1<<30 {", "\tif false {")],
 "M151-types-shares-its-slice": [("model.go", "return append([]string(nil), m.names...)", "return m.names")],
 "M152-unresolved-target-profile-tolerated": [("modelbuild.go", "\tcd, err := b.c.pick(nil, typeStructureDefinition, cn)\n\tif err != nil {\n\t\treturn \"\", err\n\t}\n\tsd := b.docs[cd].sd", "\tcd, err := b.c.pick(nil, typeStructureDefinition, cn)\n\tif err != nil {\n\t\treturn \"\", nil\n\t}\n\tsd := b.docs[cd].sd")],
 "M153-variant-targets-of-all-types": [("modelbuild.go", "rs, err := refsOf(func(x string) bool { return x == code })", "rs, err := refsOf(func(x string) bool { return true })")],
 "M154-choice-type-repeated": [("modelbuild.go", "\t\t\tif !slices.Contains(codes, t.Code) {", "\t\t\tif true {")],
 "M155-children-in-reverse-order": [("modelbuild.go", "m.children[parent] = append(m.children[parent], name)", "m.children[parent] = append([]string{name}, m.children[parent]...)")],
 "M156-type-without-code-tolerated": [("modelbuild.go", "\t\t\t\treturn fmt.Errorf(\"%s: %q: a type has no code\", where, e.Path)", "\t\t\t\tcontinue")],
 "M157-model-guard-fhir-name-in-modelbuild": [("modelbuild.go", "// definesType informa si", "var leaked = \"SimpleQuantity\"\n\n// definesType informa si")],
 "M158-model-guard-json-tag-without-source": [("modelbuild.go", "Min              *int   `json:\"min\"`", "Min              *int   `json:\"minimum\"`")],
 # Plan 003, code review ronda 1: M1 (fallar cerrado ante snapshots incoherentes), B2
 # y M2 (el oráculo independiente scripts/model_oracle.py sobre los núcleos).
 "M159-content-reference-url-not-checked": [("modelbuild.go", "\t\t\tif from != \"\" {", "\t\t\tif false {")],
 "M160-content-reference-target-not-checked": [("modelbuild.go", "\t\t\tif !seen[to] {", "\t\t\tif false {")],
 "M161-type-code-not-checked": [("modelbuild.go", "if _, ok := b.defs[t.Code]; !ok && !b.system[t.Code] {", "if false {")],
 "M162-system-type-codes-rejected": [("modelbuild.go", "if _, ok := b.defs[t.Code]; !ok && !b.system[t.Code] {", "if _, ok := b.defs[t.Code]; !ok {")],
 "M163-choice-variant-collision-tolerated": [("modelbuild.go", "\t\t\t\tif seen[variant] {", "\t\t\t\tif false {")],
 "M164-element-without-type-tolerated": [("modelbuild.go", "\t\t\treturn fmt.Errorf(\"%s: %q has neither a type nor a content reference\", where, e.Path)", "\t\t\tcontinue")],
 "M165-choice-without-types-tolerated": [("modelbuild.go", "\t\t\t\treturn fmt.Errorf(\"%s: %q: the choice element has no types\", where, e.Path)", "\t\t\t\tcontinue")],
 "M166-absent-cardinality-tolerated": [("modelbuild.go", "\t\treturn 0, 0, errors.New(\"min or max is absent (sdf-3)\")", "\t\t_ = errors.New\n\t\treturn 0, 0, nil")],
 "M167-min-above-max-tolerated": [("modelbuild.go", "\tif *e.Min > n {", "\tif false {")],
 "M168-missing-root-tolerated": [("modelbuild.go", "\tif !seen[typeName] {", "\tif false {")],
 "M169-orphan-path-tolerated": [("modelbuild.go", "\t\tif !seen[parent] {", "\t\tif false {")],
 "M170-slice-cardinality-not-checked": [("modelbuild.go", "\t\tlo, hi, err := cardinalityOf(e)\n\t\tif err != nil {\n\t\t\treturn fmt.Errorf(\"%s: %q: %w\", where, e.Path, err)\n\t\t}\n\t\tif isSlice(e) {\n\t\t\tcontinue\n\t\t}\n", "\t\tif isSlice(e) {\n\t\t\tcontinue\n\t\t}\n\t\tlo, hi, err := cardinalityOf(e)\n\t\tif err != nil {\n\t\t\treturn fmt.Errorf(\"%s: %q: %w\", where, e.Path, err)\n\t\t}\n")],
 "M171-base-definition-without-derivation-is-a-type": [("modelbuild.go", "(d.Derivation == \"\" && d.BaseDefinition == \"\")", "d.Derivation == \"\"")],
 # M2: las que sobrevivían a la ronda 1 (D y E del revisor) y sus vecinas.
 "M172-reference-element-targets-reversed": [("modelbuild.go", "\t\tif len(rs) > 0 {\n\t\t\tm.refs[e.Path] = rs\n\t\t}", "\t\tif len(rs) > 0 {\n\t\t\tif strings.HasPrefix(codes[0], \"Codeable\") {\n\t\t\t\tslices.Reverse(rs)\n\t\t\t}\n\t\t\tm.refs[e.Path] = rs\n\t\t}")],
 "M173-abstract-type-unbounded-as-one": [("modelbuild.go", "\t\tadd := func(path string) error {", "\t\tif d.doc.Abstract && hi == Unbounded {\n\t\t\thi = 1\n\t\t}\n\t\tadd := func(path string) error {")],
 "M174-variant-targets-reversed": [("modelbuild.go", "\t\t\t\tif len(rs) > 0 {\n\t\t\t\t\tm.refs[variant] = rs\n\t\t\t\t}", "\t\t\t\tif len(rs) > 0 {\n\t\t\t\t\tslices.Reverse(rs)\n\t\t\t\t\tm.refs[variant] = rs\n\t\t\t\t}")],
 "M175-variant-targets-truncated": [("modelbuild.go", "\t\t\t\tif len(rs) > 0 {\n\t\t\t\t\tm.refs[variant] = rs\n\t\t\t\t}", "\t\t\t\tif len(rs) > 0 {\n\t\t\t\t\tm.refs[variant] = rs[:1]\n\t\t\t\t}")],
 "M176-element-targets-truncated": [("modelbuild.go", "\t\t\tm.refs[e.Path] = rs\n", "\t\t\tm.refs[e.Path] = rs[:1]\n")],
 "M177-fhir-type-of-abstract-types-dropped": [("modelbuild.go", "func (b *modelBuilder) addFHIRType(d *typeDef, e elementDoc, path string) {\n", "func (b *modelBuilder) addFHIRType(d *typeDef, e elementDoc, path string) {\n\tif d.doc.Abstract {\n\t\treturn\n\t}\n")],
 "M178-abstract-type-cardinality-dropped": [("modelbuild.go", "\t\t\tm.card[path] = cardinality{lo, hi}\n\t\t\treturn nil", "\t\t\tif !d.doc.Abstract {\n\t\t\t\tm.card[path] = cardinality{lo, hi}\n\t\t\t}\n\t\t\treturn nil")],
 "M179-element-without-id-tolerated": [("modelbuild.go", "\t\tif e.ID == \"\" {", "\t\tif false {")],
 "M180-slice-mark-in-path-tolerated": [("modelbuild.go", "\t\tif strings.Contains(e.Path, \":\") {", "\t\tif false {")],
 "M181-content-reference-with-type-tolerated": [("modelbuild.go", "\t\t\tif len(e.Type) > 0 {\n\t\t\t\treturn fmt.Errorf(\"%s: %q: the element has a content reference", "\t\t\tif false {\n\t\t\t\treturn fmt.Errorf(\"%s: %q: the element has a content reference")],
 "M182-content-reference-chain-tolerated": [("modelbuild.go", "\t\t\tif crs[to] {", "\t\t\tif false {")],
 "M183-element-and-its-choice-tolerated": [("modelbuild.go", "\t\t\tif seen[basePath] {", "\t\t\tif false {")],
 "M184-system-types-from-any-package": [("modelbuild.go", "if cd.pkgID != c.core {", "if false {")],
 "M185-system-types-from-any-kind": [("modelbuild.go", "if !definesType(d) || d.Kind != kindPrimitive {", "if !definesType(d) {")],
 "M186-content-reference-version-not-cut": [("modelbuild.go", "if u, _, _ := strings.Cut(from, \"|\"); u != d.url {", "if u := from; u != d.url {")],
 "M187-single-type-choice-fhir-type-on-base": [("modelbuild.go", "b.addFHIRType(d, e, basePath+upperFirst(codes[0]))", "b.addFHIRType(d, e, basePath)")],
 "M188-slice-detection-disabled": [("modelbuild.go", "func isSlice(e elementDoc) bool { return strings.Contains(e.ID, \":\") }", "func isSlice(e elementDoc) bool { return false }")],
 "M189-choice-path-collision-tolerated": [("modelbuild.go", "if from, dup := claimed[path]; dup {", "if from, dup := claimed[path]; false && dup {")],
 "M190-id-unrelated-to-path-tolerated": [("modelbuild.go", "if stripSliceNames(e.ID) != e.Path {", "if false {")],
 "M191-slice-without-base-tolerated": [("modelbuild.go", "if !ids[sliceParentID(e.ID)] {", "if false {")],
 "M192-slice-descendant-checked-against-base-path": [("modelbuild.go", "\t\treturn id[:last+end]\n", "\t\treturn id[:last]\n")],
 "M193-system-type-in-choice-or-several-tolerated": [("modelbuild.go", "!ok && (len(e.Type) > 1 || strings.HasSuffix(name, choiceSuffix)) {", "!ok && false {")],
 "M194-system-type-among-several-tolerated": [("modelbuild.go", "!ok && (len(e.Type) > 1 || strings.HasSuffix(name, choiceSuffix)) {", "!ok && strings.HasSuffix(name, choiceSuffix) {")],
 "M195-system-type-in-choice-tolerated": [("modelbuild.go", "!ok && (len(e.Type) > 1 || strings.HasSuffix(name, choiceSuffix)) {", "!ok && len(e.Type) > 1 {")],
 "M196-content-reference-children-tolerated": [("modelbuild.go", "if i >= 0 && crs[e.Path[:i]] {", "if false {")],
 "M197-overridden-core-primitive-tolerated": [("modelbuild.go", "if g.dec.winner != cd {", "if false {")],
 # Plan 004: el normalizador (reglas 1 a 9 de §3.1, D2, D6). Los P-07 a P-12 corren
 # además el oráculo de paridad con models (TestNormalizerParity).
 "M198-is-primitive-never": [("modelbuild.go", "primitive: d.doc.Kind == kindPrimitive}", "primitive: false}")],
 "M199-is-primitive-any-non-logical": [("modelbuild.go", "primitive: d.doc.Kind == kindPrimitive}", "primitive: d.doc.Kind != kindLogical}")],
 "M200-lone-low-surrogate-accepted": [("normscan.go", "\t\t\t\tcase r >= 0xDC00 && r <= 0xDFFF:", "\t\t\t\tcase false:")],
 "M201-lone-high-surrogate-accepted": [("normscan.go", "\t\t\t\t\ts.fail(KindSyntax, p-6, \"high surrogate without its low surrogate\")\n\t\t\t\t\treturn\n", "\t\t\t\t\tcontinue\n")],
 "M202-invalid-utf8-accepted": [("normscan.go", "if r == utf8.RuneError && w <= 1 {", "if r == utf8.RuneError && w <= 1 && false {")],
 "M203-control-character-accepted": [("normscan.go", "\t\tcase c < 0x20:\n\t\t\ts.fail(KindSyntax, p, \"control character", "\t\tcase false:\n\t\t\ts.fail(KindSyntax, p, \"control character")],
 "M204-leading-zero-accepted": [("normscan.go", "\tcase p < len(d) && d[p] == '0':\n\t\tp++\n", "\tcase p < len(d) && d[p] == '0':\n\t\tfor p < len(d) && d[p] >= '0' && d[p] <= '9' {\n\t\t\tp++\n\t\t}\n")],
 "M205-trailing-data-accepted": [("normscan.go", "\tif s.pos != len(s.data) {", "\tif false {")],
 "M206-exponent-without-digits-accepted": [("normscan.go", "\t\tif p == q {\n\t\t\ts.fail(KindSyntax, start, \"invalid number: no digits in the exponent\")", "\t\tif p == q && false {\n\t\t\ts.fail(KindSyntax, start, \"invalid number: no digits in the exponent\")")],
 "M207-object-depth-off-by-one": [("normscan.go", "\tif depth > s.maxDepth {\n\t\treturn s.fail(KindTooDeep, start, \"nesting is deeper than %d\", s.maxDepth)\n\t}\n\ts.pos++\n\tn := s.newNode(kObject, start)", "\tif depth >= s.maxDepth {\n\t\treturn s.fail(KindTooDeep, start, \"nesting is deeper than %d\", s.maxDepth)\n\t}\n\ts.pos++\n\tn := s.newNode(kObject, start)")],
 "M208-array-depth-not-limited": [("normscan.go", "\tif depth > s.maxDepth {\n\t\treturn s.fail(KindTooDeep, start, \"nesting is deeper than %d\", s.maxDepth)\n\t}\n\ts.pos++\n\tn := s.newNode(kArray, start)", "\tif false {\n\t\treturn s.fail(KindTooDeep, start, \"nesting is deeper than %d\", s.maxDepth)\n\t}\n\ts.pos++\n\tn := s.newNode(kArray, start)")],
 "M209-syntax-path-keeps-popped-members": [("normscan.go", "\t\ts.stack = s.stack[:len(s.stack)-1]\n\t\ts.nodes[c].key, s.nodes[c].keyEnd", "\t\ts.nodes[c].key, s.nodes[c].keyEnd")],
 "M210-size-limit-off-by-one": [("normwalk.go", "if len(data) > lim.bytes {", "if len(data) >= lim.bytes {")],
 "M211-escaped-keys-compared-raw": [("normwalk.go", "\tif !nd.keyEsc {\n\t\treturn raw\n\t}", "\tif true {\n\t\treturn raw\n\t}")],
 "M212-strings-copied-without-rewriting": [("normwalk.go", "\t\tif !nd.dirt {", "\t\tif true {")],
 "M213-line-separators-not-escaped": [("normwalk.go", "\t\tcase r == 0x2028 || r == 0x2029:", "\t\tcase false:")],
 "M214-html-escaped": [("normwalk.go", "\t\tdefault:\n\t\t\tdst = utf8.AppendRune(dst, r)", "\t\tcase r == '<' || r == '>' || r == '&':\n\t\t\tdst = append(dst, '\\\\', 'u', '0', '0', '3', 'c')\n\t\tdefault:\n\t\t\tdst = utf8.AppendRune(dst, r)")],
 "M215-delete-escaped": [("normwalk.go", "\t\tcase r < 0x20:\n\t\t\tdst = append(dst,", "\t\tcase r < 0x20 || r == 0x7f:\n\t\t\tdst = append(dst,")],
 "M216-empty-string-accepted": [("normwalk.go", "\t\tif nd.end-nd.start == 2 {", "\t\tif false {")],
 "M217-numbers-truncated": [("normwalk.go", "\tcase kNumber, kTrue, kFalse:\n\t\tw.out = append(w.out, w.data[nd.start:nd.end]...)", "\tcase kNumber, kTrue, kFalse:\n\t\tw.out = append(w.out, w.data[nd.start:min(nd.end, nd.start+15)]...)")],
 "M218-resource-type-not-first": [("normwalk.go", "\tif rt != \"\" {\n\t\tw.out = append(w.out, '\"')", "\tif false {\n\t\tw.out = append(w.out, '\"')")],
 "M219-members-in-input-order": [("normwalk.go", "\t\tcase a.sk != b.sk:", "\t\tcase false:")],
 "M220-members-in-reverse-order": [("normwalk.go", "\t\t\tif a.sk < b.sk {\n\t\t\t\treturn -1\n\t\t\t}\n\t\t\treturn 1", "\t\t\tif a.sk > b.sk {\n\t\t\t\treturn -1\n\t\t\t}\n\t\t\treturn 1")],
 "M221-unknown-properties-in-input-order": [("normwalk.go", "string(w.keyBytes(b.n))); c != 0 {", "string(w.keyBytes(b.n))); false && c != 0 {")],
 "M222-underscore-without-prefix": [("normwalk.go", "\t\t\tw.writeKey(primitiveExtPrefix, v.key)\n\t\t\tw.objectValue(um, ext)", "\t\t\tw.writeKey(\"\", v.key)\n\t\t\tw.objectValue(um, ext)")],
 "M223-choice-conflict-tolerated": [("normwalk.go", "\t\tif x.ref.variant != g[0].ref.variant {", "\t\tif false {")],
 "M224-duplicate-property-tolerated": [("normwalk.go", "\t\tif *slot != 0 {", "\t\tif false {")],
 "M225-underscore-on-non-primitive-tolerated": [("normwalk.go", "\tif um != 0 && v.kind != vkPrimitive {", "\tif false {")],
 "M226-unknown-property-tolerated": [("normwalk.go", "\t\t\tw.report(KindUnknownProperty, ms[i].n, string(appendPropertyPath(nil, string(w.keyBytes(ms[i].n)))), \"the type has no such element\")", "\t\t\t_ = ms")],
 "M227-length-mismatch-tolerated": [("normwalk.go", "w.count(xm) != w.count(um)", "false")],
 "M228-array-not-required-for-repeating": [("normwalk.go", "\tif nd.kind != kArray {\n\t\tw.report(KindWrongShape, n, \"\", \"the element repeats: an array is expected\")", "\tif false {\n\t\tw.report(KindWrongShape, n, \"\", \"the element repeats: an array is expected\")")],
 "M229-empty-array-tolerated": [("normwalk.go", "\tif nd.first == 0 {\n\t\tw.report(KindEmpty, n, \"\", \"an empty array is not allowed\")", "\tif false {\n\t\tw.report(KindEmpty, n, \"\", \"an empty array is not allowed\")")],
 "M230-empty-object-tolerated": [("normwalk.go", "\tcase nd.first == 0:\n\t\tw.report(KindEmpty, n, \"\", \"an empty object is not allowed\")", "\tcase false:\n\t\tw.report(KindEmpty, n, \"\", \"an empty object is not allowed\")")],
 "M231-null-object-is-a-shape-error": [("normwalk.go", "\tcase nd.kind == kNull:\n\t\tw.report(KindNull, n, \"\", \"null is not allowed here\")\n\tcase nd.kind != kObject:", "\tcase false:\n\t\tw.report(KindNull, n, \"\", \"null is not allowed here\")\n\tcase nd.kind != kObject:")],
 "M232-null-without-underscore-partner-tolerated": [("normwalk.go", "\t\t\t\tif p == 0 || w.nodes[p].kind == kNull {", "\t\t\t\tif false {")],
 "M233-null-without-value-partner-tolerated": [("normwalk.go", "\t\t\t\tif xm == 0 {\n\t\t\t\t\tw.report(KindNull, c, \"\", \"null needs a value in the value array", "\t\t\t\tif false {\n\t\t\t\t\tw.report(KindNull, c, \"\", \"null needs a value in the value array")],
 "M234-root-path-missing": [("normwalk.go", "\tif len(w.path) == 0 {\n\t\tw.push(rt)\n\t\tdefer w.pop()\n\t}", "\t_ = rt")],
 "M235-array-index-not-in-the-path": [("normwalk.go", "\t\tw.setIdx(i)\n\t\tw.element(v, c)", "\t\t_ = i\n\t\tw.element(v, c)")],
 "M236-any-type-is-a-resource": [("normwalk.go", "!w.m.IsResource(rt) || (slot != \"\" && !w.m.IsSubtype(rt, slot))", "false")],
 "M237-slot-type-not-checked": [("normwalk.go", "(slot != \"\" && !w.m.IsSubtype(rt, slot))", "false")],
 "M238-missing-resource-type-tolerated": [("normwalk.go", "\tcase count == 0:\n\t\tw.report(KindMissingResourceType", "\tcase false:\n\t\tw.report(KindMissingResourceType")],
 "M239-duplicate-resource-type-tolerated": [("normwalk.go", "\tcase count > 1:", "\tcase false:")],
 "M240-slot-types-from-every-type": [("normspec.go", "ancestors(func(i typeInfo) bool { return i.resource })", "ancestors(func(i typeInfo) bool { return !i.primitive })")],
 "M241-slot-types-keep-the-shared-root": [("normspec.go", "\t\t\tdelete(res, t)", "\t\t\t_ = t")],
 "M242-underscore-object-takes-the-value": [("normspec.go", "return !slices.Contains(inherited, name) })", "return !slices.Contains(inherited, name) && false })")],
 "M243-underscore-object-from-the-wrong-base": [("normspec.go", "inherited := m.ChildElements(base)", "inherited := m.ChildElements(prim)")],
 "M244-max-zero-element-allowed": [("normspec.go", "ok && hi == 0 {", "ok && hi == 0 && false {")],
 "M245-unbounded-does-not-repeat": [("normspec.go", "v.repeat = hi == Unbounded || hi > 1", "v.repeat = hi > 1")],
 "M246-content-reference-not-followed": [("normspec.go", "\trp := m.ResolvePath(path)", "\trp := path")],
 "M247-inline-children-ignored": [("normspec.go", "\tcase len(m.ChildElements(rp)) > 0:", "\tcase false:")],
 "M248-primitives-as-objects": [("normspec.go", "\tcase m.IsPrimitive(t):", "\tcase false:")],
 "M249-system-types-as-objects": [("normspec.go", "\tcase !m.HasType(t):", "\tcase false:")],
 "M250-underscore-key-not-registered": [("normspec.go", "\t\t\tsp.keys[primitiveExtPrefix+v.key] = keyRef{field: fi, variant: uint16(vi), under: true}\n", "")],
 "M251-error-limit-off-by-one": [("normwalk.go", "\tif len(w.errs) >= w.max {", "\tif len(w.errs) > w.max {")],
 "M252-truncation-not-reported": [("normwalk.go", "\t\tw.trunc = true\n\t\treturn", "\t\treturn")],
 "M253-limits-not-validated": [("normalize.go", "\tif lim.depth < 1 || lim.bytes < 1 || lim.errs < 1 {", "\tif false {")],
 "M254-options-ignored": [("normalize.go", "\tfor _, o := range opts {\n\t\to(&lim)\n\t}", "\t_ = opts")],
 "M255-guard-bypass-resource-type-in-the-walker": [("normwalk.go", "const unknownSK = math.MaxUint32", "const unknownSK = math.MaxUint32\n\nvar leaked = \"resourceType\"")],
 "M256-guard-bypass-resource-name-in-the-walker": [("normwalk.go", "const unknownSK = math.MaxUint32", "const unknownSK = math.MaxUint32\n\nvar leaked = \"see Patient and Bundle\"")],
 "M257-guard-bypass-element-name-in-the-spec": [("normspec.go", "type valueKind uint8", "var leaked = strings.Fields(\"deceased multipleBirth\")\n\ntype valueKind uint8")],
 "M258-error-is-not-an-error-list": [("normwalk.go", "return nil, &ErrorList{Errors: w.errs, Truncated: w.trunc}", "return nil, w.errs[0]")],
 "M259-normalize-output-not-copied-on-error": [("normwalk.go", "\tif len(w.errs) > 0 {\n\t\treturn nil, &ErrorList", "\tif len(w.errs) > 0 {\n\t\treturn w.out, &ErrorList")],
 # Plan 004, code review ronda 1 (H1, M1, M2 y las bajas).
 "M260-underscore-children-from-the-primitive-base": [("normspec.go", "\tfor m.IsPrimitive(base) {\n\t\tbase = m.ParentType(base)\n\t}\n", "")],
 "M261-raw-line-separators-not-rewritten": [("normscan.go", "\t\t\tif c == 0xE2 {", "\t\t\tif c == 0xE2 && false {")],
 "M262-scanner-path-without-the-root-type": [("normscan.go", "\tif len(s.nodes) < 2 || s.nodes[1].kind != kObject {\n\t\treturn rootFallback\n\t}", "\tif true {\n\t\treturn rootFallback\n\t}")],
 "M263-scanner-path-with-raw-keys": [("normscan.go", "\t\tif g.esc {\n\t\t\traw = appendUnescaped(nil, raw)\n\t\t}", "")],
 "M264-scanner-path-root-is-empty": [("fhirnames.go", "rootFallback = \"Resource\"", "rootFallback = \"\"")],
 "M265-property-name-never-delimited": [("normwalk.go", "\tplain := name != \"\"\n", "\tplain := true\n")],
 "M266-unknown-properties-sorted-by-raw-key": [("normwalk.go", "strings.Compare(string(w.keyBytes(a.n)), string(w.keyBytes(b.n)))", "strings.Compare(string(w.data[w.nodes[a.n].key:w.nodes[a.n].keyEnd]), string(w.data[w.nodes[b.n].key:w.nodes[b.n].keyEnd]))")],
 "M267-output-keeps-the-capacity-of-the-input": [("normwalk.go", "\tif out := w.out; cap(out)-len(out) > len(out)/8 {", "\tif out := w.out; cap(out)-len(out) > len(out)/8 && false {")],
 "M268-delimited-name-not-escaped": [("normwalk.go", "\t\tcase r == '`' || r == '\\\\':\n\t\t\tb = append(b, '\\\\', byte(r))\n", "")],
 "M269-property-path-keeps-the-underscore": [("normwalk.go", "return appendPathName(b, strings.TrimPrefix(key, primitiveExtPrefix))", "return appendPathName(b, key)")],
 "M270-scanner-root-type-not-checked-as-identifier": [("normscan.go", "if string(appendPathName(nil, t)) != t {", "if false {")],
 "M271-scanner-path-ignores-the-underscore-rule": [("normscan.go", "b = appendPropertyPath(b, string(raw))", "b = appendPathName(b, string(raw))")],
 "M272-walker-unknown-path-ignores-the-underscore-rule": [("normwalk.go", "string(appendPropertyPath(nil, string(w.keyBytes(ms[i].n))))", "string(appendPathName(nil, string(w.keyBytes(ms[i].n))))")],
 "P-23-delimited-name-cr-not-escaped": [("normwalk.go", "\t\tcase r == '\\r':\n\t\t\tb = append(b, '\\\\', 'r')\n", "")],
 "P-24-delimited-name-ff-not-escaped": [("normwalk.go", "\t\tcase r == '\\f':\n\t\t\tb = append(b, '\\\\', 'f')\n", "")],
 "P-25-delimited-name-control-not-escaped": [("normwalk.go", "\t\tcase r < 0x20:\n\t\t\tb = append(b, '\\\\', 'u', '0', '0', hexDigits[r>>4], hexDigits[r&0xF])\n", "")],
 "P-22-oracle-covered-compares-the-literal-path": [("parity/normparity_test.go", "e.Path == \"\" || d.Elem == e.Path ||", "e.Path == \"\" || d.Path == e.Path ||")],
 "P-18-underscore-children-from-the-primitive-base": [("normspec.go", "\tfor m.IsPrimitive(base) {\n\t\tbase = m.ParentType(base)\n\t}\n", "")],
 "P-19-raw-line-separators-not-rewritten": [("normscan.go", "\t\t\tif c == 0xE2 {", "\t\t\tif c == 0xE2 && false {")],
 "P-20-oracle-does-not-strip-the-underscore-of-the-element-path": [("parity/normparity_test.go", "p, e := join(path, pathName(k)), join(elem, pathName(strings.TrimPrefix(k, \"_\")))", "p, e := join(path, pathName(k)), join(elem, pathName(k))")],
 "P-21-oracle-does-not-delimit-path-names": [("parity/normparity_test.go", "\tplain := name != \"\"\n\tfor i := 0; i < len(name) && plain; i++ {\n\t\tc := name[i]\n\t\tplain = c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9'\n\t}\n\tif plain {", "\tplain := true\n\tif plain {")],
 "P-07-depth-default-below-the-measured-maximum": [("normalize.go", "DefaultMaxDepth = 64", "DefaultMaxDepth = 40")],
 "P-08-members-in-reverse-order": [("normwalk.go", "\t\t\tif a.sk < b.sk {\n\t\t\t\treturn -1\n\t\t\t}\n\t\t\treturn 1", "\t\t\tif a.sk > b.sk {\n\t\t\t\treturn -1\n\t\t\t}\n\t\t\treturn 1")],
 "P-09-strings-copied-without-rewriting": [("normwalk.go", "\t\tif !nd.dirt {", "\t\tif true {")],
 "P-10-unknown-property-tolerated": [("normwalk.go", "\t\t\tw.report(KindUnknownProperty, ms[i].n, string(appendPropertyPath(nil, string(w.keyBytes(ms[i].n)))), \"the type has no such element\")", "\t\t\t_ = ms")],
 "P-11-numbers-truncated": [("normwalk.go", "\tcase kNumber, kTrue, kFalse:\n\t\tw.out = append(w.out, w.data[nd.start:nd.end]...)", "\tcase kNumber, kTrue, kFalse:\n\t\tw.out = append(w.out, w.data[nd.start:min(nd.end, nd.start+15)]...)")],
 "P-12-underscore-without-prefix": [("normwalk.go", "\t\t\tw.writeKey(primitiveExtPrefix, v.key)\n\t\t\tw.objectValue(um, ext)", "\t\t\tw.writeKey(\"\", v.key)\n\t\t\tw.objectValue(um, ext)")],
 "P-13-oracle-covers-only-ancestors": [("parity/normparity_test.go", "if e.Path == \"\" || d.Elem == e.Path || strings.HasPrefix(d.Elem, e.Path+\".\")", "if e.Path == \"\" || strings.HasPrefix(d.Elem, e.Path+\".\")")],
 "P-14-oracle-calls-every-accepted-resource-unchanged": [("parity/normparity_test.go", "\tcase err == nil && bytes.Equal(out, mr.out):", "\tcase err == nil:")],
 "P-15-oracle-ignores-models-failures": [("parity/normparity_test.go", "\tcase mr.err != nil:\n\t\trule := \"normalize accepts\"", "\tcase false:\n\t\trule := \"normalize accepts\"")],
 "P-16-oracle-does-not-detect-repaired-text": [("parity/normparity_test.go", "\t\tif bytes.Contains(mr.out, []byte(\"\\ufffd\")) {", "\t\tif false {")],
 "P-17-oracle-counts-no-unknown-resource": [("parity/normparity_test.go", "\tcase hasKind(list, fhirpkg.KindUnknownResourceType):", "\tcase false:")],
 "P-01-classified-difference-disappears": [("modelbuild.go", "\t\t\t\t\tif !slices.Contains(out, tt) {", "\t\t\t\t\tif true {")],
 "P-02-children-order-differs-from-models": [("modelbuild.go", "m.children[parent] = append(m.children[parent], name)", "m.children[parent] = append([]string{name}, m.children[parent]...)")],
 "P-03-fhir-type-always-id": [("modelbuild.go", "\t\t\tb.m.fhirType[path] = x.ValueURL\n", "\t\t\tb.m.fhirType[path] = \"id\"\n")],
 "P-04-abstract-type-elements-dropped": [("modelbuild.go", "\t\tparent, name := e.Path[:i], e.Path[i+1:]\n", "\t\tparent, name := e.Path[:i], e.Path[i+1:]\n\t\tif d.doc.Abstract {\n\t\t\tcontinue\n\t\t}\n")],
 "P-05-choice-variants-lose-their-targets": [("modelbuild.go", "\t\t\t\tif len(rs) > 0 {\n\t\t\t\t\tm.refs[variant] = rs\n\t\t\t\t}", "\t\t\t\t_ = rs")],
 "P-06-nested-backbone-typed-like-models": [("modelbuild.go", "\t\t\tm.pathType[e.Path] = codes[0]\n", "\t\t\tm.pathType[e.Path] = codes[0]\n\t\t\tif codes[0] == \"Element\" {\n\t\t\t\tm.pathType[e.Path] = \"BackboneElement\"\n\t\t\t}\n")],
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
            if f.endswith(".go") or "testdata" in os.path.relpath(root, R).split(os.sep) or f in ("packages.sha256", "go.mod", "model_oracle.py"):
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
        pk = os.path.join(R, ".cache", "packages")
        env = dict(os.environ, FHIRPKG_PACKAGES=pk, EXAMPLES_PACKAGES=pk)
        cmd, cwd = ["go", "test", "-failfast", "-count=1", "./..."], dst
        if name.startswith("P-"):
            # Solo los oráculos de parity/ (otro módulo, con replace a ../../go-searchindex).
            os.symlink(os.path.join(os.path.dirname(R), "go-searchindex"), os.path.join(work, "go-searchindex"))
            cmd = ["go", "test", "-short", "-count=1", "-run", "TestParityWithModels|TestSearchIndexDifferential|TestFHIRPathAnalyzeDifferential|TestKnownLoss|TestNormalizerParity|TestPathNameAgreesWithTheLibrary", "./..."]
            cwd = os.path.join(dst, "parity")
        start = time.time()
        try:
            out = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=900).stdout
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
