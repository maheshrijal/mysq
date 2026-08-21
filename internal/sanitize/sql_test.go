package sanitize

import "testing"

func TestSQLRedactsLiterals(t *testing.T) {
	got := SQL(`SELECT * FROM users WHERE email='mahesh@example.com' AND id=42 AND ratio=1.5`)
	want := `SELECT * FROM users WHERE email=? AND id=? AND ratio=?`
	if got != want {
		t.Fatalf("SQL() = %q, want %q", got, want)
	}
}

func TestTextOnlyNormalizesSQLLines(t *testing.T) {
	got := Text("TRANSACTIONS\nSELECT * FROM t WHERE secret='x'\nHistory list length 123")
	if got != "TRANSACTIONS\nSELECT * FROM t WHERE secret=?\nHistory list length 123" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestTextRedactsQuotedValuesInDiagnosticErrors(t *testing.T) {
	got := Text("Could not apply row: Duplicate entry 'customer@example.com' for key 'email'")
	if got != "Could not apply row: Duplicate entry ? for key ?" {
		t.Fatalf("Text() = %q", got)
	}
}

func TestSensitiveName(t *testing.T) {
	if !SensitiveName("report_password") || !SensitiveName("oauth_access_token") || SensitiveName("ssl_key") {
		t.Fatal("unexpected sensitive variable classification")
	}
}
