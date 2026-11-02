package fhirpkg

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Step es un paso de la cascada con la que se elige entre las definiciones de
// un mismo canónico (ADR-003). Los valores son nombres de esta librería, no de
// FHIR.
type Step string

const (
	// Pasos del canónico sin versión (plan 002 §3.1), en su orden.
	StepSeries      Step = "series"
	StepStatus      Step = "publication-status"
	StepPublisher   Step = "publisher"
	StepVersion     Step = "version-order"
	StepLastUpdated Step = "last-updated"
	// Pasos de dos SearchParameter con el mismo base:code y distinta URL
	// (plan 002 §3.2).
	StepNotExperimental Step = "not-experimental"
	StepCore            Step = "core-first"
	// StepUnresolved: ningún paso separó a los candidatos.
	StepUnresolved Step = "unresolved"
	// StepSingle: había un solo candidato.
	StepSingle Step = "single"
)

// StepTrace es lo que hizo un paso: cuántos candidatos entraron y cuántos
// siguen.
type StepTrace struct {
	Step Step
	In   int
	Out  int
	// Detail explica el criterio cuando hace falta: el orden de versiones usado
	// o por qué el paso no pudo comparar.
	Detail string
}

// info es lo que se lee de un recurso para decidir entre candidatos. Se llena
// solo para los recursos que compiten o que definen tipos y parámetros.
type info struct {
	status       string
	experimental bool
	fhirVersions []string
	lastUpdated  string
	algorithm    string
	expression   bool
}

// cand es un recurso candidato de un canónico.
type cand struct {
	pkg   int // posición en Context.pkgs
	pkgID string
	res   *Resource
	info  *info
	// copy indica que el package publica bajo otro host que el de la URL del
	// recurso (paso del publicador).
	copy bool
}

// decision es el resultado de la cascada sobre un conjunto de candidatos.
type decision struct {
	winner *cand // nil si quedó sin resolver
	by     Step
	trace  []StepTrace
}

// decide corre la cascada del canónico sin versión (plan 002 §3.1) sobre cs,
// que debe tener al menos un candidato. coreSeries es la serie FHIR del núcleo
// del lock ("4.0", "4.3", "5.0"). No depende del orden de cs.
func decide(cs []*cand, coreSeries string) decision {
	if len(cs) == 1 {
		return decision{winner: cs[0], by: StepSingle}
	}
	steps := []struct {
		step Step
		run  func([]*cand) ([]*cand, string)
	}{
		{StepSeries, func(c []*cand) ([]*cand, string) { return stepSeries(c, coreSeries) }},
		{StepStatus, stepStatus},
		{StepPublisher, stepPublisher},
		{StepVersion, stepVersion},
		{StepLastUpdated, stepLastUpdated},
	}
	var d decision
	cur := cs
	for _, s := range steps {
		next, detail := s.run(cur)
		d.trace = append(d.trace, StepTrace{Step: s.step, In: len(cur), Out: len(next), Detail: detail})
		cur = next
		if len(cur) == 1 {
			d.winner, d.by = cur[0], s.step
			return d
		}
	}
	d.by = StepUnresolved
	return d
}

// stepSeries descarta a quien declara su propia serie FHIR (fhirVersion) y no es
// la del núcleo (A-H2). Quien no declara, o declara algo que no es una versión
// FHIR, no pierde. Si todos perderían, no se descarta a nadie.
func stepSeries(cs []*cand, coreSeries string) ([]*cand, string) {
	var keep []*cand
	for _, c := range cs {
		if !declaresOtherSeries(c.info.fhirVersions, coreSeries) {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return cs, "every candidate declares another series"
	}
	return keep, ""
}

// declaresOtherSeries dice si la lista declara alguna serie y ninguna es la del
// núcleo.
func declaresOtherSeries(versions []string, coreSeries string) bool {
	declared := false
	for _, v := range versions {
		s, ok := series(v)
		if !ok {
			continue
		}
		if s == coreSeries {
			return false
		}
		declared = true
	}
	return declared
}

// statusRank es el orden de ADR-003: active > draft > desconocido > retired.
func statusRank(s string) int {
	switch s {
	case statusActive:
		return 3
	case statusDraft:
		return 2
	case statusRetired:
		return 0
	}
	return 1
}

func stepStatus(cs []*cand) ([]*cand, string) {
	best := -1
	for _, c := range cs {
		best = max(best, statusRank(c.info.status))
	}
	var keep []*cand
	for _, c := range cs {
		if statusRank(c.info.status) == best {
			keep = append(keep, c)
		}
	}
	return keep, ""
}

// stepPublisher descarta las copias: los candidatos cuyo package publica bajo
// otro host que el de la URL. Si todos son copias, o ninguno, no separa.
func stepPublisher(cs []*cand) ([]*cand, string) {
	var keep []*cand
	for _, c := range cs {
		if !c.copy {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return cs, "every candidate is a copy"
	}
	return keep, ""
}

func stepVersion(cs []*cand) ([]*cand, string) {
	ds := make([]versionDecl, len(cs))
	for i, c := range cs {
		ds[i] = versionDecl{Version: c.res.Version, Algorithm: c.info.algorithm, Expression: c.info.expression}
	}
	o, ok := chooseOrder(ds)
	if !ok {
		return cs, "versions are not comparable"
	}
	best := cs[0]
	for _, c := range cs[1:] {
		if o.cmp(c.res.Version, best.res.Version) > 0 {
			best = c
		}
	}
	var keep []*cand
	for _, c := range cs {
		if o.cmp(c.res.Version, best.res.Version) == 0 {
			keep = append(keep, c)
		}
	}
	return keep, o.name
}

// stepLastUpdated compara meta.lastUpdated como instantes. Si alguno falta o no
// se lee, no separa.
func stepLastUpdated(cs []*cand) ([]*cand, string) {
	ts := make([]time.Time, len(cs))
	for i, c := range cs {
		t, err := time.Parse(time.RFC3339Nano, c.info.lastUpdated)
		if err != nil {
			return cs, "an instant is missing or unreadable"
		}
		ts[i] = t
	}
	best := ts[0]
	for _, t := range ts[1:] {
		if t.After(best) {
			best = t
		}
	}
	var keep []*cand
	for i, c := range cs {
		if ts[i].Equal(best) {
			keep = append(keep, c)
		}
	}
	return keep, ""
}

// host devuelve el host de una URL en minúsculas, o vacío si no tiene (urn:,
// oid:).
func host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// isCopy aplica el criterio del publicador (P-3): el package declara un
// canonical con un host distinto del de la URL del recurso.
func isCopy(pkgCanonical, resourceURL string) bool {
	p, r := host(pkgCanonical), host(resourceURL)
	return p != "" && r != "" && p != r
}

// explain arma el texto con los pasos para los errores.
func explainTrace(trace []StepTrace) string {
	parts := make([]string, len(trace))
	for i, t := range trace {
		parts[i] = fmt.Sprintf("%s %d->%d", t.Step, t.In, t.Out)
		if t.Detail != "" {
			parts[i] += " (" + t.Detail + ")"
		}
	}
	return strings.Join(parts, ", ")
}
