// Command fhirpkg fija, verifica y describe los packages FHIR de un lock.
//
//	fhirpkg lock   -o fhirpkg.lock [-registry URL|DIR] [-except name#version=motivo] raíz...
//	fhirpkg verify -lock fhirpkg.lock -dir DIR
//	fhirpkg graph  -candidates FILE [-registry URL|DIR] [-o reporte.md]
//	fhirpkg context -lock fhirpkg.lock -dir DIR
//
// Solo lock y graph (con un registro remoto) usan la red.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gofhir/fhirpkg"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "lock":
		err = runLock(os.Args[2:])
	case "verify":
		err = runVerify(os.Args[2:])
	case "graph":
		err = runGraph(os.Args[2:])
	case "context":
		err = runContext(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "fhirpkg:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: fhirpkg lock|verify|graph|context [flags] (see the package comment)")
	os.Exit(2)
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// newRegistry elige el registro: una URL, o una carpeta con name-version.tgz.
func newRegistry(s string) fhirpkg.Registry {
	if strings.Contains(s, "://") {
		return &fhirpkg.HTTPRegistry{BaseURL: s, Client: &httpClient}
	}
	return fhirpkg.DirRegistry{Dir: s}
}

func runLock(args []string) error {
	fs := flag.NewFlagSet("lock", flag.ExitOnError)
	out := fs.String("o", "fhirpkg.lock", "lock file to write")
	registry := fs.String("registry", fhirpkg.DefaultRegistryURL, "registry URL, or a directory of name-version.tgz")
	var except listFlag
	fs.Var(&except, "except", "name#version=reason: read this package although its FHIR series differs from the core's (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts := fhirpkg.Options{}
	// Las excepciones del lock existente se conservan (están escritas a mano).
	if old, err := fhirpkg.ReadLockFile(*out); err == nil {
		opts.Exceptions = old.Exceptions
	}
	for _, e := range except {
		id, reason, ok := strings.Cut(e, "=")
		if !ok {
			return fmt.Errorf("-except %q: expected name#version=reason", e)
		}
		replaced := false
		for i := range opts.Exceptions {
			if opts.Exceptions[i].Package == id {
				opts.Exceptions[i].Reason, replaced = reason, true
			}
		}
		if !replaced {
			opts.Exceptions = append(opts.Exceptions, fhirpkg.Exception{Package: id, Reason: reason})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	lock, err := fhirpkg.Resolve(ctx, fs.Args(), newRegistry(*registry), opts)
	if err != nil {
		return err
	}
	if err := lock.WriteFile(*out); err != nil {
		return err
	}
	fmt.Printf("%s: FHIR %s, core %s, %d packages (%d excluded)\n", *out, lock.FHIRVersion, lock.Core, len(lock.Packages), len(lock.Packages)-len(lock.Order))
	return nil
}

func runVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	lockFile := fs.String("lock", "fhirpkg.lock", "lock file")
	dir := fs.String("dir", ".cache/packages", "directory with the name-version.tgz files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lock, err := fhirpkg.ReadLockFile(*lockFile)
	if err != nil {
		return err
	}
	start := time.Now()
	set, err := fhirpkg.Open(lock, *dir)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	total := 0
	for _, p := range set.Packages() {
		n := len(set.Resources(p.ID))
		total += n
		fmt.Printf("%-48s %6d resources\n", p.ID, n)
	}
	fmt.Printf("%s: FHIR %s, %d packages, %d resources, open %s, retained heap %d MB\n",
		*lockFile, set.FHIRVersion(), len(set.Packages()), total, elapsed.Round(time.Millisecond), ms.HeapAlloc>>20)
	return nil
}

// runContext abre el lock y construye el Context (plan 002): imprime el tiempo,
// el heap retenido y las cuentas de la cascada por paso.
func runContext(args []string) error {
	fs := flag.NewFlagSet("context", flag.ExitOnError)
	lockFile := fs.String("lock", "fhirpkg.lock", "lock file")
	dir := fs.String("dir", ".cache/packages", "directory with the name-version.tgz files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lock, err := fhirpkg.ReadLockFile(*lockFile)
	if err != nil {
		return err
	}
	set, err := fhirpkg.Open(lock, *dir)
	if err != nil {
		return err
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	start := time.Now()
	c, err := fhirpkg.NewContext(set)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	runtime.GC()
	runtime.ReadMemStats(&after)
	byStep := map[fhirpkg.Kind]map[fhirpkg.Step]int{}
	for _, cf := range c.Conflicts() {
		if byStep[cf.Kind] == nil {
			byStep[cf.Kind] = map[fhirpkg.Step]int{}
		}
		byStep[cf.Kind][cf.DecidedBy]++
	}
	for _, kind := range []fhirpkg.Kind{fhirpkg.KindCanonical, fhirpkg.KindSearchParameter} {
		var parts []string
		for _, st := range []fhirpkg.Step{fhirpkg.StepSeries, fhirpkg.StepStatus, fhirpkg.StepPublisher, fhirpkg.StepVersion, fhirpkg.StepLastUpdated, fhirpkg.StepNotExperimental, fhirpkg.StepCore, fhirpkg.StepUnresolved} {
			if n := byStep[kind][st]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", st, n))
			}
		}
		fmt.Printf("%s conflicts: %s\n", kind, strings.Join(parts, ", "))
	}
	// El modelo de tipos se construye dentro de NewContext (plan 003, D4): su
	// tiempo y su heap ya están en las cifras de abajo.
	fmt.Printf("%s: FHIR %s, %d resource types, %d model types, NewContext %s, heap retained by the Context %d MB\n",
		*lockFile, set.FHIRVersion(), len(c.ResourceTypes()), len(c.Model().Types()), elapsed.Round(time.Millisecond), (int64(after.HeapAlloc)-int64(before.HeapAlloc))>>20)
	return nil
}
