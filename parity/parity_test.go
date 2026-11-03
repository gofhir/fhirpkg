package parity

import "testing"

// TestParityWithModels compara las nueve tablas de models con las de fhirpkg
// en R4, R4B y R5: toda diferencia está clasificada (testdata/differences.json).
func TestParityWithModels(t *testing.T) {
	cl := classify(t)
	setClassified(cl)
	for _, e := range cl.errs {
		t.Error(e)
	}
}
