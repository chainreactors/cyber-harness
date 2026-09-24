package skills_test

import (
	"path/filepath"
	"testing"

	"github.com/chainreactors/cyber/tools/okf"
)

func TestScannerOKFBundlePassesProductionChecks(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("cyber", "okf"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := okf.Validate(t.Context(), root, true)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid() {
		t.Fatalf("scanner OKF bundle has production errors: %#v", report.Issues)
	}
}
