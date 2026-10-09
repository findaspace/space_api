package trust

import (
	"strings"
	"testing"
)

func TestReportValidation(t *testing.T) {
	for _, reason := range []string{"suspected_scam", "unavailable", "incorrect_details", "abuse", "other"} {
		if !ValidReport(reason, strings.Repeat("é", 2000)) {
			t.Fatalf("valid reason %s rejected", reason)
		}
	}
	for _, reason := range []string{"", "delete_listing", "admin"} {
		if ValidReport(reason, "evidence") {
			t.Fatalf("unknown reason %s accepted", reason)
		}
	}
	if ValidReport("other", strings.Repeat("é", 2001)) {
		t.Fatal("oversize report accepted")
	}
}
