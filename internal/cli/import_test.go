package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

const smallExport = "../../testdata/small/export.xml"

func TestImportAppleJSONAndIdempotent(t *testing.T) {
	db := tempDB(t)
	var inserted float64
	for i, wantNew := range []bool{true, false} {
		out, errOut, err := run(t, "--db", db, "import", "apple", smallExport, "--json")
		if err != nil {
			t.Fatal(err)
		}
		if errOut != "" {
			t.Errorf("unexpected stderr output %q", errOut)
		}
		var sum map[string]any
		if err := json.Unmarshal([]byte(out), &sum); err != nil {
			t.Fatalf("bad JSON %q: %v", out, err)
		}
		inserted = sum["inserted"].(float64)
		if (inserted > 0) != wantNew {
			t.Errorf("import #%d inserted %v rows", i+1, inserted)
		}
	}
}

func TestImportAppleTextSummary(t *testing.T) {
	out, _, err := run(t, "--db", tempDB(t), "import", "apple", smallExport)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Read:", "Inserted:", "Duplicates:", "Skipped:", "unknown_type", "Elapsed:"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestImportAppleMissingFile(t *testing.T) {
	_, _, err := run(t, "--db", tempDB(t), "import", "apple", "no-such-export.zip")
	if err == nil || !strings.Contains(err.Error(), "file not found") {
		t.Errorf("err = %v, want a 'file not found' error", err)
	}
}
