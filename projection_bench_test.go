package fhirpkg

import (
	"fmt"
	"strings"
	"testing"
)

// D9, cierre transitivo: referencias locales encadenadas, en el orden inverso al
// del recorrido (el peor caso de un cierre ingenuo). Debe crecer casi lineal.
func BenchmarkProjectContainedChain(b *testing.B) {
	c, _ := realContext(b, "r4.lock")
	p, err := c.NewProjection("Organization", SummaryTrue, nil)
	if err != nil {
		b.Fatal(err)
	}
	for _, n := range []int{2000, 4000, 8000, 16000, 32000} {
		var s strings.Builder
		s.WriteString(`{"resourceType":"Organization","name":"Root","contained":[`)
		for i := n - 1; i >= 0; i-- {
			if i < n-1 {
				s.WriteByte(',')
			}
			fmt.Fprintf(&s, `{"resourceType":"Organization","id":"p%d","name":"Contained"`, i)
			if i+1 < n {
				fmt.Fprintf(&s, `,"partOf":{"reference":"#p%d"}`, i+1)
			}
			s.WriteByte('}')
		}
		s.WriteString(`],"partOf":{"reference":"#p0"}}`)
		in := []byte(s.String())
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.SetBytes(int64(len(in)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.Project(in, p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
