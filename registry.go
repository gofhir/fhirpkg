package fhirpkg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// ErrNotFound lo devuelve un Registry cuando el package o la versión no existen.
var ErrNotFound = errors.New("package not found")

// Registry es de donde Resolve obtiene los packages. Es el único punto con red
// del módulo; Open nunca lo usa.
type Registry interface {
	// Versions lista las versiones publicadas de un package.
	Versions(ctx context.Context, name string) ([]string, error)
	// Fetch devuelve el .tgz de una versión exacta.
	Fetch(ctx context.Context, name, version string) ([]byte, error)
}

// DefaultRegistryURL es el registro de packages FHIR.
const DefaultRegistryURL = "https://packages.fhir.org"

// maxDownload acota lo que se lee de un registro remoto.
const maxDownload = 512 << 20

// HTTPRegistry habla con un registro de packages FHIR (la API de npm que usa
// packages.fhir.org): GET {base}/{name} da las versiones y, de cada una, la
// URL de descarga (dist.tarball).
type HTTPRegistry struct {
	BaseURL string
	Client  *http.Client

	mu   sync.Mutex
	meta map[string]map[string]string // name -> version -> tarball
}

type registryMeta struct {
	Versions map[string]struct {
		Dist struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	} `json:"versions"`
}

func (r *HTTPRegistry) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return http.DefaultClient
}

func (r *HTTPRegistry) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", u, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %s", u, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s: response larger than %d bytes", u, limit)
	}
	return body, nil
}

func (r *HTTPRegistry) versions(ctx context.Context, name string) (map[string]string, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	r.mu.Lock()
	m, ok := r.meta[name]
	r.mu.Unlock()
	if ok {
		return m, nil
	}
	base := strings.TrimRight(r.BaseURL, "/")
	body, err := r.get(ctx, base+"/"+url.PathEscape(name), 64<<20)
	if err != nil {
		return nil, err
	}
	var meta registryMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, fmt.Errorf("%s: invalid metadata: %w", name, err)
	}
	m = make(map[string]string, len(meta.Versions))
	for v, e := range meta.Versions {
		m[v] = e.Dist.Tarball
	}
	r.mu.Lock()
	if r.meta == nil {
		r.meta = map[string]map[string]string{}
	}
	r.meta[name] = m
	r.mu.Unlock()
	return m, nil
}

// Versions implementa Registry.
func (r *HTTPRegistry) Versions(ctx context.Context, name string) ([]string, error) {
	m, err := r.versions(ctx, name)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out, nil
}

// Fetch implementa Registry.
func (r *HTTPRegistry) Fetch(ctx context.Context, name, version string) ([]byte, error) {
	m, err := r.versions(ctx, name)
	if err != nil {
		return nil, err
	}
	tarball := m[version]
	if tarball == "" {
		return nil, fmt.Errorf("%s#%s: %w", name, version, ErrNotFound)
	}
	return r.get(ctx, tarball, maxDownload)
}

// DirRegistry sirve packages desde una carpeta con archivos name-version.tgz,
// como los que deja scripts/fetch-packages.sh. Sirve para trabajar sin red y
// para los tests.
type DirRegistry struct{ Dir string }

// Versions implementa Registry. Un archivo name-resto.tgz cuenta como versión
// "resto" solo si empieza con un dígito, para no confundir "a-1.0.0" con la
// versión "b-1.0.0" de un package "a" cuando también existe "a-b".
func (r DirRegistry) Versions(_ context.Context, name string) ([]string, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), name+"-")
		if !ok || !strings.HasSuffix(rest, ".tgz") {
			continue
		}
		v := strings.TrimSuffix(rest, ".tgz")
		if v != "" && v[0] >= '0' && v[0] <= '9' {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w", name, ErrNotFound)
	}
	sort.Strings(out)
	return out, nil
}

// Fetch implementa Registry.
func (r DirRegistry) Fetch(_ context.Context, name, version string) ([]byte, error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	if err := validVersion(version); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(r.Dir, PackageFile(name, version)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s#%s: %w", name, version, ErrNotFound)
	}
	return data, err
}

// PackageFile es el nombre del .tgz de un package en un directorio.
func PackageFile(name, version string) string { return name + "-" + version + ".tgz" }
