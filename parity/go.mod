module github.com/gofhir/fhirpkg/parity

go 1.26.0

require (
	github.com/gofhir/fhirpath v1.16.6
	github.com/gofhir/fhirpkg v0.0.0
	github.com/gofhir/models/r4/v2 v2.10.0
	github.com/gofhir/models/r4b/v2 v2.10.0
	github.com/gofhir/models/r5/v2 v2.10.0
	github.com/gofhir/searchindex v0.0.0
)

require (
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/gofhir/ucum/v4 v4.3.0 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	golang.org/x/exp v0.0.0-20240506185415-9bf2ced13842 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/gofhir/fhirpkg => ..

replace github.com/gofhir/searchindex => ../../go-searchindex
