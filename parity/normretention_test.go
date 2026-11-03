package parity

// Memoria que retiene la salida de Normalize (code review ronda 1, plan 004
// §10): la salida se reservaba con la capacidad de la entrada y se devolvía con
// ella, de modo que quien guardara el resultado retenía el espacio que la
// entrada tenía de más (espacios, sangría). Mide, con el Bundle más grande de
// cada versión y con un ejemplo con mucha sangría, los bytes de la entrada, de
// la salida, su capacidad y el heap que queda retenido con la salida viva.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/gofhir/fhirpkg"
)

type retentionRow struct {
	Version, Case string
	In, Out, Cap  int
	HeldMB        float64 // heap retenido con la salida viva, menos el de antes de normalizar
}

var (
	retMu   sync.Mutex
	retRows []retentionRow
)

func heapInUse() uint64 {
	var ms runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func TestNormalizeRetainsOnlyItsOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	for _, v := range loadVersions(t) {
		picks := benchPicks(t, v.name) // pequeño, ~100 KB, ~2 MB y el más grande
		big, mid := picks[len(picks)-1], picks[1]
		var padded bytes.Buffer
		if err := json.Indent(&padded, exampleBody(t, v.name, mid.Name), "", strings.Repeat(" ", 16)); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name string
			raw  []byte
		}{
			{"largest official example (" + big.Name + ")", exampleBody(t, v.name, big.Name)},
			{"indented with 16 spaces (" + mid.Name + ")", padded.Bytes()},
		} {
			t.Run(v.name+"/"+c.name, func(t *testing.T) {
				before := heapInUse()
				out, err := v.ctx.Normalize(c.raw, fhirpkg.WithMaxBytes(1<<30))
				if err != nil {
					t.Fatal(err)
				}
				after := heapInUse()
				held := float64(int64(after)-int64(before)) / (1 << 20)
				runtime.KeepAlive(out)
				row := retentionRow{Version: v.name, Case: c.name, In: len(c.raw), Out: len(out), Cap: cap(out), HeldMB: held}
				retMu.Lock()
				retRows = append(retRows, row)
				retMu.Unlock()
				t.Logf("%s: in %d, out %d, cap %d, +%.1f MB retained", c.name, row.In, row.Out, row.Cap, held)
				// Lo que se devuelve no retiene más de un octavo de espacio sin usar.
				if limit := len(out) + len(out)/8 + 64; cap(out) > limit {
					t.Errorf("the output holds %d bytes for %d of content (limit %d)", cap(out), len(out), limit)
				}
			})
		}
	}
}

func renderRetention() string {
	retMu.Lock()
	rows := append([]retentionRow(nil), retRows...)
	retMu.Unlock()
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Version+rows[i].Case < rows[j].Version+rows[j].Case })
	var b strings.Builder
	b.WriteString("\n### Memory retained by the output of `Normalize`\n\nBytes of the input, of the output and of the capacity of the returned slice, and the heap that stays alive while the output is held (measured after two collections; the input is already in memory).\n\n| FHIR | Case | Input bytes | Output bytes | Output capacity | Retained heap MB |\n| --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d | %.1f |\n", r.Version, r.Case, r.In, r.Out, r.Cap, r.HeldMB)
	}
	return b.String()
}
