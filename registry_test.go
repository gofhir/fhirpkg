package fhirpkg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

func TestHTTPRegistry(t *testing.T) {
	core := coreR4().tgz(t)
	var metaCalls atomic.Int32
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/core.r4", func(w http.ResponseWriter, r *http.Request) {
		metaCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"versions": map[string]any{
			"4.0.1": map[string]any{"dist": map[string]string{"tarball": srv.URL + "/dl/core.r4/4.0.1"}},
			"4.0.0": map[string]any{"dist": map[string]string{"tarball": srv.URL + "/dl/gone"}},
		}})
	})
	mux.HandleFunc("/dl/core.r4/4.0.1", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(core) })
	mux.HandleFunc("/broken", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) })
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", http.StatusInternalServerError) })
	srv = httptest.NewServer(mux)
	defer srv.Close()

	reg := &HTTPRegistry{BaseURL: srv.URL + "/"}
	ctx := context.Background()
	vs, err := reg.Versions(ctx, "core.r4")
	if err != nil || !slices.Equal(vs, []string{"4.0.0", "4.0.1"}) {
		t.Fatalf("%v %v", vs, err)
	}
	b, err := reg.Fetch(ctx, "core.r4", "4.0.1")
	if err != nil || string(b) != string(core) {
		t.Fatalf("fetch: %v", err)
	}
	if metaCalls.Load() != 1 {
		t.Fatalf("metadata fetched %d times, want 1 (cached)", metaCalls.Load())
	}
	if _, err := reg.Fetch(ctx, "core.r4", "9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err := reg.Fetch(ctx, "core.r4", "4.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 on the tarball: %v", err)
	}
	if _, err := reg.Versions(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown package: %v", err)
	}
	if _, err := reg.Versions(ctx, "broken"); err == nil {
		t.Fatal("invalid metadata accepted")
	}
	if _, err := reg.Versions(ctx, "boom"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("HTTP 500: %v", err)
	}
	if _, err := reg.Versions(ctx, "../etc"); err == nil {
		t.Fatal("unsafe name accepted")
	}
	// Resolve funciona contra el servidor.
	l := mustResolve(t, []string{"core.r4#4.0.x"}, &HTTPRegistry{BaseURL: srv.URL}, Options{})
	if l.Core != "core.r4#4.0.1" {
		t.Fatalf("core %s", l.Core)
	}
}

func TestDirRegistry(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a-1.0.0.tgz", "a-1.0.10.tgz", "a-b-2.0.0.tgz", "a-b-1.0.0-rc.1.tgz", "other-1.tgz", "a-1.0.0.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg := DirRegistry{Dir: dir}
	vs, err := reg.Versions(context.Background(), "a")
	if err != nil || !slices.Equal(vs, []string{"1.0.0", "1.0.10"}) {
		t.Fatalf("versions of a: %v %v", vs, err)
	}
	vs, _ = reg.Versions(context.Background(), "a-b")
	if !slices.Equal(vs, []string{"1.0.0-rc.1", "2.0.0"}) {
		t.Fatalf("versions of a-b: %v", vs)
	}
	if _, err := reg.Versions(context.Background(), "zzz"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := reg.Fetch(context.Background(), "a", "9"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := reg.Fetch(context.Background(), "../a", "1"); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if b, err := reg.Fetch(context.Background(), "a", "1.0.0"); err != nil || string(b) != "x" {
		t.Fatal(err)
	}
}
