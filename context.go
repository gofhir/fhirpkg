package fhirpkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ErrUnresolved lo devuelven Resolve y ResolveFrom cuando ningún paso de la
// cascada elige entre varias definiciones (ADR-003). El error lleva la
// explicación.
var ErrUnresolved = errors.New("unresolved canonical")

// ErrAmbiguousType lo devuelven Resolve, ResolveFrom y Explain cuando no se da
// el tipo de recurso y la url la usan varios tipos visibles.
var ErrAmbiguousType = errors.New("ambiguous resource type")

// Kind distingue lo que registra un Conflict.
type Kind string

const (
	// KindCanonical: un canónico (resourceType, url) con más de un candidato.
	KindCanonical Kind = "canonical"
	// KindSearchParameter: un base:code con más de un SearchParameter de URL
	// distinta (plan 002 §3.2).
	KindSearchParameter Kind = "base-code"
)

// Candidate es una definición candidata.
type Candidate struct {
	// Package es el name#version del package que la trae.
	Package string
	// File es el nombre del archivo dentro de package/.
	File    string
	URL     string
	Version string
	// Status es el campo status tal como viene.
	Status string
}

// Conflict explica una elección entre varias definiciones.
type Conflict struct {
	Kind Kind
	// ResourceType es el tipo del canónico; vacío en un KindSearchParameter.
	ResourceType string
	// Key es la URL del canónico, o "base:code" en un KindSearchParameter.
	Key string
	// Candidates están ordenados por package y archivo.
	Candidates []Candidate
	// Winner es el índice del ganador en Candidates, o -1 si quedó sin resolver.
	Winner int
	// DecidedBy es el paso que dejó un solo candidato, o StepUnresolved.
	DecidedBy Step
	Trace     []StepTrace
}

// Resolved es una definición resuelta. Resource.Raw son los bytes del Set (no
// se copian), salvo en un recurso contenido, que se extrae del recurso que lo
// contiene.
type Resolved struct {
	// Package es el name#version del package que la trae.
	Package  string
	Resource Resource
	// Fragment es el id del recurso contenido pedido con #fragmento; vacío si
	// se pidió el recurso entero.
	Fragment string
}

// SearchParameter es un SearchParameter resuelto para un tipo de recurso.
type SearchParameter struct {
	URL          string
	Version      string
	Code         string
	Status       string
	Experimental bool
	// Base son los tipos de recurso a los que aplica, tal como los declara.
	Base []string
	// Package es el name#version del package que lo trae.
	Package string
	// Raw es el JSON del recurso: los bytes del Set, sin copiar.
	Raw []byte
}

type key struct{ rt, url string }

// group son los candidatos de un canónico.
type group struct {
	cands []*cand // ordenados por (package, archivo)
	dec   decision
}

// Context resuelve canónicos y da los SearchParameter de cada tipo de recurso
// sobre un Set verificado. Se construye una vez; después es inmutable y seguro
// para uso concurrente. No copia los bytes de los recursos: referencia los del
// Set.
type Context struct {
	set        *Set
	pkgs       []string       // name#version, en el orden del Set
	pkgIndex   map[string]int // name#version -> posición en pkgs
	visible    [][]bool       // visible[i][j]: el package i ve al j (cierre de dependencias)
	coreSeries string
	core       string
	groups     map[key]*group
	typesOfURL map[string][]string // url -> tipos de recurso que la usan, ordenados
	conflicts  []Conflict
	types      []string                     // tipos de recurso (concretos y abstractos), ordenados
	concrete   []string                     // los que no son abstractos, ordenados
	chains     map[string][]string          // tipo -> él y sus bases por baseDefinition
	spByType   map[string][]SearchParameter // resultado de SearchParameters
	model      *Model                       // plan 003: se construye en NewContext
	subsetted  subsettedTag                 // plan 005: el tag SUBSETTED, armado en la primera Projection
}

// commonDoc son los campos que deciden entre candidatos, comunes a los
// recursos canónicos.
type commonDoc struct {
	Status       string     `json:"status"`
	Experimental bool       `json:"experimental"`
	FHIRVersion  stringList `json:"fhirVersion"`
	Meta         struct {
		LastUpdated string `json:"lastUpdated"`
	} `json:"meta"`
	AlgorithmString string `json:"versionAlgorithmString"`
	AlgorithmCoding *struct {
		System string `json:"system"`
		Code   string `json:"code"`
	} `json:"versionAlgorithmCoding"`
}

type spDoc struct {
	commonDoc
	Code string   `json:"code"`
	Base []string `json:"base"`
}

type sdDoc struct {
	commonDoc
	Kind           string `json:"kind"`
	Derivation     string `json:"derivation"`
	Type           string `json:"type"`
	Abstract       bool   `json:"abstract"`
	BaseDefinition string `json:"baseDefinition"`
	// Snapshot solo dice si el StructureDefinition lo trae; sus elementos se
	// leen aparte, para los tipos que se usan (modelbuild.go).
	Snapshot *struct{} `json:"snapshot"`
}

// stringList lee un campo que es una cadena o una lista de cadenas
// (fhirVersion es una en StructureDefinition y varias en ImplementationGuide).
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*l = stringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected a string or a list of strings: %w", err)
	}
	*l = many
	return nil
}

func (d *commonDoc) info() *info {
	i := &info{status: d.Status, experimental: d.Experimental, fhirVersions: d.FHIRVersion, lastUpdated: d.Meta.LastUpdated}
	switch {
	case d.AlgorithmCoding != nil:
		// Solo un código del CodeSystem version-algorithm es un algoritmo.
		if d.AlgorithmCoding.System == systemVersionAlgorithm {
			i.algorithm = d.AlgorithmCoding.Code
		}
	case d.AlgorithmString != "":
		i.expression = true
	}
	return i
}

// parsed son los campos que se leen de un recurso, según su tipo.
type parsed struct {
	info *info
	sp   *spDoc
	sd   *sdDoc
}

func parseResource(r *Resource) (parsed, error) {
	switch r.ResourceType {
	case typeSearchParameter:
		var d spDoc
		if err := json.Unmarshal(r.Raw, &d); err != nil {
			return parsed{}, err
		}
		return parsed{info: d.info(), sp: &d}, nil
	case typeStructureDefinition:
		var d sdDoc
		if err := json.Unmarshal(r.Raw, &d); err != nil {
			return parsed{}, err
		}
		return parsed{info: d.info(), sd: &d}, nil
	}
	var d commonDoc
	if err := json.Unmarshal(r.Raw, &d); err != nil {
		return parsed{}, err
	}
	return parsed{info: d.info()}, nil
}

// NewContext indexa los recursos canónicos del Set (los que tienen url): cada
// uno con todas sus versiones y su procedencia, sin copiar los bytes. Decide
// ahora, con la cascada de ADR-003, el ganador de cada canónico repetido, y
// calcula los SearchParameter de cada tipo de recurso. Es error que un recurso
// no se pueda leer, que la herencia de tipos esté rota (baseDefinition sin
// resolver, ciclo, dos StructureDefinition para un mismo tipo) o que un
// SearchParameter no tenga code o base. Un canónico sin resolver no es error:
// queda en Conflicts.
func NewContext(set *Set) (*Context, error) {
	if set == nil {
		return nil, errors.New("NewContext: nil Set")
	}
	c := &Context{
		set:        set,
		pkgIndex:   make(map[string]int, len(set.packages)),
		groups:     map[key]*group{},
		typesOfURL: map[string][]string{},
		core:       set.lock.Core,
	}
	c.coreSeries, _ = series(set.lock.FHIRVersion)
	c.pkgs = make([]string, len(set.packages))
	canonicals := make([]string, len(set.packages))
	for i, p := range set.packages {
		c.pkgs[i] = p.ID
		c.pkgIndex[p.ID] = i
		canonicals[i] = p.Manifest.Canonical
	}
	c.visible = dependencyClosures(set, c.pkgs, c.pkgIndex)

	keys := c.groupResources(set, canonicals)

	docs := map[*cand]parsed{}
	for _, k := range keys {
		g := c.groups[k]
		slices.SortFunc(g.cands, func(a, b *cand) int {
			if x := strings.Compare(a.pkgID, b.pkgID); x != 0 {
				return x
			}
			return strings.Compare(a.res.File, b.res.File)
		})
		c.typesOfURL[k.url] = append(c.typesOfURL[k.url], k.rt)
		// Solo se leen los recursos que compiten o que se usan para los tipos y
		// los parámetros de búsqueda.
		if len(g.cands) < 2 {
			g.dec = decision{winner: g.cands[0], by: StepSingle}
			if k.rt != typeSearchParameter && k.rt != typeStructureDefinition {
				continue
			}
		}
		for _, cd := range g.cands {
			p, err := parseResource(cd.res)
			if err != nil {
				return nil, fmt.Errorf("%s: %s: %w", cd.pkgID, cd.res.File, err)
			}
			cd.info = p.info
			docs[cd] = p
		}
		if len(g.cands) > 1 {
			g.dec = decide(g.cands, c.coreSeries)
			c.conflicts = append(c.conflicts, canonicalConflict(k, g.cands, g.dec))
		}
	}

	if err := c.buildTypes(keys, docs); err != nil {
		return nil, err
	}
	if err := c.buildSearchParameters(keys, docs); err != nil {
		return nil, err
	}
	var err error
	if c.model, err = c.buildModel(keys, docs); err != nil {
		return nil, err
	}
	sortConflicts(c.conflicts)
	return c, nil
}

// groupResources agrupa los recursos del Set por (tipo, url) y devuelve las
// claves ordenadas por tipo y url.
func (c *Context) groupResources(set *Set, canonicals []string) []key {
	for i, p := range set.packages {
		rs := set.resources[p.ID]
		for k := range rs {
			r := &rs[k]
			g := c.groups[key{r.ResourceType, r.URL}]
			if g == nil {
				g = &group{}
				c.groups[key{r.ResourceType, r.URL}] = g
			}
			g.cands = append(g.cands, &cand{pkg: i, pkgID: p.ID, res: r, copy: isCopy(canonicals[i], r.URL)})
		}
	}
	keys := make([]key, 0, len(c.groups))
	for k := range c.groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].rt != keys[j].rt {
			return keys[i].rt < keys[j].rt
		}
		return keys[i].url < keys[j].url
	})
	return keys
}

// sortConflicts ordena los conflictos por clase, tipo y clave.
func sortConflicts(cs []Conflict) {
	sort.Slice(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.ResourceType != b.ResourceType {
			return a.ResourceType < b.ResourceType
		}
		return a.Key < b.Key
	})
}

// dependencyClosures calcula, para cada package, los packages que ve: él mismo
// y el cierre transitivo de sus dependencias del lock. Las dependencias que no
// se leen (excluidas) no cuentan.
func dependencyClosures(set *Set, ids []string, index map[string]int) [][]bool {
	deps := map[string][]string{}
	for _, lp := range set.lock.Packages {
		deps[lp.ID()] = lp.Dependencies
	}
	out := make([][]bool, len(ids))
	for i, id := range ids {
		seen := make([]bool, len(ids))
		var walk func(string)
		walk = func(x string) {
			j, ok := index[x]
			if !ok || seen[j] {
				return
			}
			seen[j] = true
			for _, d := range deps[x] {
				walk(d)
			}
		}
		walk(id)
		out[i] = seen
	}
	return out
}

func candidateOf(cd *cand) Candidate {
	st := ""
	if cd.info != nil {
		st = cd.info.status
	}
	return Candidate{Package: cd.pkgID, File: cd.res.File, URL: cd.res.URL, Version: cd.res.Version, Status: st}
}

func canonicalConflict(k key, cs []*cand, d decision) Conflict {
	cf := Conflict{Kind: KindCanonical, ResourceType: k.rt, Key: k.url, Winner: -1, DecidedBy: d.by, Trace: slices.Clone(d.trace)}
	for i, cd := range cs {
		cf.Candidates = append(cf.Candidates, candidateOf(cd))
		if cd == d.winner {
			cf.Winner = i
		}
	}
	return cf
}

// Conflicts devuelve cada canónico con más de un candidato y cada base:code de
// SearchParameter con más de una definición, con el ganador y el paso que lo
// decidió, o «sin resolver». El orden es determinista: canónicos primero, por
// tipo y clave. Es una copia: el llamador puede modificarla.
func (c *Context) Conflicts() []Conflict {
	out := make([]Conflict, len(c.conflicts))
	for i, cf := range c.conflicts {
		out[i] = cloneConflict(cf)
	}
	return out
}

func cloneConflict(cf Conflict) Conflict {
	cf.Candidates = slices.Clone(cf.Candidates)
	cf.Trace = slices.Clone(cf.Trace)
	return cf
}

// typeFor elige el tipo de recurso de una url cuando el llamador no lo da. Si
// hay varios tipos con esa url y mas de uno es visible, es ambiguo.
func (c *Context) typeFor(rt, url string, vis []bool) (string, error) {
	if rt != "" {
		return rt, nil
	}
	var found []string
	for _, t := range c.typesOfURL[url] {
		if len(c.visibleCands(c.groups[key{t, url}], vis)) > 0 {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s: %w", url, ErrNotFound)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("%w: %s is the url of %s: pass the type", ErrAmbiguousType, url, strings.Join(found, ", "))
}

// visibleCands filtra los candidatos por visibilidad; vis nil es todo.
func (c *Context) visibleCands(g *group, vis []bool) []*cand {
	if g == nil {
		return nil
	}
	if vis == nil {
		return g.cands
	}
	var out []*cand
	for _, cd := range g.cands {
		if vis[cd.pkg] {
			out = append(out, cd)
		}
	}
	return out
}

// Resolve resuelve un canónico entre todos los packages del Set. resourceType
// puede ir vacío si la url no la usan dos tipos de recurso (A-M3). Sin versión
// elige con la cascada de ADR-003, decidida al construir el Context; con
// |version, la definición con esa versión, si es única; con #fragmento, el
// recurso contenido con ese id dentro del resuelto. Si no hay ganador devuelve
// ErrUnresolved con la explicación; si no existe, ErrNotFound.
func (c *Context) Resolve(resourceType, canonical string) (Resolved, error) {
	return c.resolve(nil, resourceType, canonical)
}

// ResolveFrom resuelve como Resolve, pero solo entre los packages que ve pkg
// (name#version): él mismo y el cierre transitivo de sus dependencias en el
// lock. Una IG que no depende de otra no ve sus definiciones.
func (c *Context) ResolveFrom(pkg, resourceType, canonical string) (Resolved, error) {
	i, ok := c.pkgIndex[pkg]
	if !ok {
		return Resolved{}, fmt.Errorf("package %s is not in the Set", pkg)
	}
	return c.resolve(c.visible[i], resourceType, canonical)
}

func (c *Context) resolve(vis []bool, resourceType, canonical string) (Resolved, error) {
	cn, err := ParseCanonical(canonical)
	if err != nil {
		return Resolved{}, err
	}
	if cn.URL == "" {
		return Resolved{}, fmt.Errorf("canonical %q is a fragment of the resource that contains it: pass the url of that resource", canonical)
	}
	rt, err := c.typeFor(resourceType, cn.URL, vis)
	if err != nil {
		return Resolved{}, err
	}
	win, err := c.pick(vis, rt, cn)
	if err != nil {
		return Resolved{}, err
	}
	out := Resolved{Package: win.pkgID, Resource: *win.res}
	if cn.Fragment == "" {
		return out, nil
	}
	contained, err := containedResource(win.res, cn.Fragment)
	if err != nil {
		return Resolved{}, fmt.Errorf("%s %s: %w", rt, cn.URL, err)
	}
	out.Resource = contained
	out.Fragment = cn.Fragment
	return out, nil
}

// pick elige el candidato de (rt, url[|version]) entre los visibles.
func (c *Context) pick(vis []bool, rt string, cn Canonical) (*cand, error) {
	g := c.groups[key{rt, cn.URL}]
	cands := c.visibleCands(g, vis)
	if len(cands) == 0 {
		return nil, fmt.Errorf("%s %s: %w", rt, cn.URL, ErrNotFound)
	}
	if cn.Version != "" {
		var same []*cand
		for _, cd := range cands {
			if cd.res.Version == cn.Version {
				same = append(same, cd)
			}
		}
		switch len(same) {
		case 0:
			return nil, fmt.Errorf("%s %s|%s: %w", rt, cn.URL, cn.Version, ErrNotFound)
		case 1:
			return same[0], nil
		}
		return nil, fmt.Errorf("%w: %s %s: %d definitions declare version %s (%s)", ErrUnresolved, rt, cn.URL, len(same), cn.Version, candidateList(same))
	}
	d := g.dec
	if vis != nil {
		d = decide(cands, c.coreSeries)
	}
	if d.winner == nil {
		return nil, fmt.Errorf("%w: %s %s: no step separates the %d candidates %s; steps: %s",
			ErrUnresolved, rt, cn.URL, len(cands), candidateList(cands), explainTrace(d.trace))
	}
	return d.winner, nil
}

func candidateList(cs []*cand) string {
	parts := make([]string, len(cs))
	for i, cd := range cs {
		v := cd.res.Version
		if v == "" {
			v = "no version"
		}
		parts[i] = fmt.Sprintf("%s (%s)", cd.pkgID, v)
	}
	return strings.Join(parts, ", ")
}

// containedResource busca el recurso contenido con ese id.
func containedResource(r *Resource, id string) (Resource, error) {
	var doc struct {
		Contained []json.RawMessage `json:"contained"`
	}
	if err := json.Unmarshal(r.Raw, &doc); err != nil {
		return Resource{}, err
	}
	for _, raw := range doc.Contained {
		var head struct {
			ResourceType string `json:"resourceType"`
			ID           string `json:"id"`
			URL          string `json:"url"`
			Version      string `json:"version"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			return Resource{}, err
		}
		if head.ID == id {
			return Resource{File: r.File, ResourceType: head.ResourceType, URL: head.URL, Version: head.Version, Raw: raw}, nil
		}
	}
	return Resource{}, fmt.Errorf("no contained resource with id %q: %w", id, ErrNotFound)
}

// Explain devuelve los pasos de la cascada para un canónico, generados por el
// mismo código que decide. El canónico va sin versión ni fragmento. Con un solo
// candidato el paso es StepSingle.
func (c *Context) Explain(resourceType, canonical string) (Conflict, error) {
	cn, err := ParseCanonical(canonical)
	if err != nil {
		return Conflict{}, err
	}
	if cn.URL == "" || cn.Version != "" || cn.Fragment != "" {
		return Conflict{}, fmt.Errorf("Explain takes a plain url, got %q", canonical)
	}
	rt, err := c.typeFor(resourceType, cn.URL, nil)
	if err != nil {
		return Conflict{}, err
	}
	g := c.groups[key{rt, cn.URL}]
	if g == nil {
		return Conflict{}, fmt.Errorf("%s %s: %w", rt, cn.URL, ErrNotFound)
	}
	cf := canonicalConflict(key{rt, cn.URL}, g.cands, g.dec)
	// Los candidatos que no compitieron no se leyeron al construir el Context:
	// se lee solo su status.
	for i, cd := range g.cands {
		if cd.info != nil {
			continue
		}
		var head struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(cd.res.Raw, &head); err != nil {
			return Conflict{}, fmt.Errorf("%s: %s: %w", cd.pkgID, cd.res.File, err)
		}
		cf.Candidates[i].Status = head.Status
	}
	return cf, nil
}

// ResourceTypes devuelve los tipos de recurso concretos del Set, ordenados: los
// StructureDefinition de kind resource que no son perfiles (derivation distinto
// de constraint) y no son abstractos (abstract true). Los abstractos (en R4 la
// raíz y el tipo de los recursos de dominio; en R5 también los de metadatos y
// canónicos) no se listan, pero cuentan para la herencia y se pueden pedir a
// SearchParameters.
func (c *Context) ResourceTypes() []string { return slices.Clone(c.concrete) }

// SearchParameters devuelve los SearchParameter de un tipo de recurso, concreto
// o abstracto: los propios y los heredados por baseDefinition, deduplicados por
// code (el del tipo gana sobre el heredado). El orden es por nivel, del tipo
// hacia la raíz, y por code dentro del nivel. Un base:code sin resolver
// (ADR-003) no aparece, ni lo hereda el tipo del nivel siguiente. Un tipo
// desconocido da nil.
func (c *Context) SearchParameters(resourceType string) []SearchParameter {
	sps, ok := c.spByType[resourceType]
	if !ok {
		return nil
	}
	out := slices.Clone(sps)
	for i := range out {
		out[i].Base = slices.Clone(out[i].Base)
	}
	return out
}
