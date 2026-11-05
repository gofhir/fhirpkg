package fhirpkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Limits acota la lectura de un .tgz. Un archivo que las supera es error: el
// package puede venir de la red o de un directorio que no controlamos.
type Limits struct {
	// MaxEntry es el tamaño máximo de un archivo dentro del .tgz.
	MaxEntry int64
	// MaxTotal es la suma máxima de los tamaños de todos los archivos.
	MaxTotal int64
	// MaxEntries es la cantidad máxima de entradas.
	MaxEntries int
}

// DefaultLimits deja un margen amplio sobre lo medido en los packages oficiales
// (mayor archivo 8 MB, mayor total 82 MB, 5.046 entradas).
var DefaultLimits = Limits{MaxEntry: 64 << 20, MaxTotal: 1 << 30, MaxEntries: 100_000}

// Resource es un recurso de conformidad de un package: un archivo JSON de
// primer nivel de package/ con resourceType y url. No se interpreta más.
type Resource struct {
	// File es el nombre del archivo dentro de package/.
	File         string
	ResourceType string
	URL          string
	// Version es el campo version del recurso, vacío si no lo trae.
	Version string
	Raw     []byte
}

type contents struct {
	manifest  Manifest
	resources []Resource // ordenados por File
	// skipped cuenta los JSON de primer nivel que no son recursos de
	// conformidad (sin resourceType o sin url).
	skipped int
}

// scanTgz recorre el .tgz entero validando cada entrada (duplicados, tipos,
// rutas, límites, gzip y tar completos). Con withResources lee los recursos de
// conformidad; sin él solo el manifiesto, pero con las mismas validaciones y sin
// guardar los cuerpos de los recursos (el grafo y los núcleos excluidos no los
// necesitan).
func scanTgz(data []byte, lim Limits, withResources bool) (*contents, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a gzip stream: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	var (
		out      contents
		manifest []byte
		total    int64
		count    int
		seen     = map[string]bool{}
	)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid tar: %w", err)
		}
		count++
		if count > lim.MaxEntries {
			return nil, fmt.Errorf("more than %d entries", lim.MaxEntries)
		}
		if err := checkEntryName(h.Name); err != nil {
			return nil, err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("entry %q: unsupported type %q (links and special files are not allowed)", h.Name, string(h.Typeflag))
		}
		if h.Size < 0 || h.Size > lim.MaxEntry {
			return nil, fmt.Errorf("entry %q: size %d exceeds the %d-byte limit", h.Name, h.Size, lim.MaxEntry)
		}
		total += h.Size
		if total > lim.MaxTotal {
			return nil, fmt.Errorf("total size exceeds the %d-byte limit", lim.MaxTotal)
		}
		name := strings.TrimPrefix(h.Name, "./")
		if seen[name] {
			return nil, fmt.Errorf("duplicate entry %q", h.Name)
		}
		seen[name] = true
		rest, ok := strings.CutPrefix(name, "package/")
		if !ok || strings.Contains(rest, "/") {
			continue // fuera de package/ o en una subcarpeta (ejemplos, other/): no se lee
		}
		isManifest := rest == "package.json"
		isResource := withResources && strings.HasSuffix(rest, ".json") && !isManifest && !strings.HasPrefix(rest, ".")
		if !isManifest && !isResource {
			continue
		}
		// Tamaño exacto: io.ReadAll deja capacidad de sobra, que se retendría.
		body := make([]byte, h.Size)
		if _, err := io.ReadFull(tr, body); err != nil {
			return nil, fmt.Errorf("entry %q: %w", h.Name, err)
		}
		if isManifest {
			manifest = body
			continue
		}
		var head struct {
			ResourceType string `json:"resourceType"`
			URL          string `json:"url"`
			Version      string `json:"version"`
		}
		if err := json.Unmarshal(body, &head); err != nil {
			return nil, fmt.Errorf("entry %q: invalid JSON: %w", h.Name, err)
		}
		if head.ResourceType == "" || head.URL == "" {
			out.skipped++
			continue
		}
		out.resources = append(out.resources, Resource{File: rest, ResourceType: head.ResourceType, URL: head.URL, Version: head.Version, Raw: body})
	}
	// tar se detiene en los bloques de cierre sin leer el final del gzip: se
	// drena para que se verifique su CRC y su largo (un gzip truncado es error).
	if _, err := io.Copy(io.Discard, zr); err != nil { //nolint:gosec // G110: hallazgo abierto, el drenaje del final del gzip no tiene tope; ver el informe de lint de docs/plans/README.md
		return nil, fmt.Errorf("invalid gzip stream: %w", err)
	}
	if manifest == nil {
		return nil, errors.New("package/package.json is missing")
	}
	m, err := ReadManifest(manifest)
	if err != nil {
		return nil, err
	}
	out.manifest = m
	sort.Slice(out.resources, func(i, j int) bool { return out.resources[i].File < out.resources[j].File })
	return &out, nil
}

// checkEntryName rechaza los nombres que escaparían de la carpeta de
// extracción (zip-slip): absolutos, con "..", con "\" o con NUL. Aunque el
// módulo no escribe a disco, un package con esos nombres es sospechoso.
func checkEntryName(name string) error {
	if name == "" {
		return errors.New("entry with an empty name")
	}
	if strings.ContainsAny(name, "\\\x00") {
		return fmt.Errorf("entry %q: unsafe name", name)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("entry %q: absolute path", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("entry %q: path escapes the package folder", name)
		}
	}
	return nil
}
