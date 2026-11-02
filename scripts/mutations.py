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
