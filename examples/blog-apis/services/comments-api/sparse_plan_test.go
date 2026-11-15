package main

import (
	"os"
	"testing"
)

// The A6 report remains observational; only the selective sparse lookup is
// asserted here. Generic field-name parameters still prevent implication.
func TestSparsePredicatePlans(t *testing.T) {
	if os.Getenv("POSTGRES_SPARSE_TEST") != "1" {
		t.Skip("manual sparse regressions: make postgres-ha-sparse-test")
	}
	report := runDeclaredIndexExplain(t, false)
	checked := 0
	for _, result := range report.Results {
		if result.Case == "unique-sparse-explicit-presence" && (result.Mode == "auto" || result.Mode == "force_custom_plan") {
			requireSelectiveIndexCondition(t, result.Plan, result.ExpectedIndex)
			report.RegressionChecks = append(report.RegressionChecks, result.Case+"/"+result.Mode+"/index_cond")
			checked++
		}
	}
	if checked != 2 {
		t.Fatalf("sparse plan checks=%d want=2", checked)
	}
}
