package sanitize

import (
	"strings"
	"testing"
)

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

func TestSQLRedactsCommentsTruncationAndMySQLNumbers(t *testing.T) {
	for _, input := range []string{
		"SELECT 1 /* token=review-secret */", "SELECT 1 # token=review-secret\nFROM t",
		"SELECT 1 -- token=review-secret\nFROM t", "SELECT 'review-secret",
		`SELECT "review-secret`, "SELECT 0xDEADBEEF, 0b101010, 1.5e-12, .45",
		"SELECT /* unterminated review-secret", "SELECT 'a\\'review-secret'",
	} {
		got := SQL(input)
		for _, secret := range []string{"review-secret", "DEADBEEF", "101010", "1.5e", ".45"} {
			if strings.Contains(got, secret) {
				t.Errorf("SQL(%q) leaked %q: %q", input, secret, got)
			}
		}
	}
	if got := SQL("SELECT `table42`.`a1`, col2 FROM `odd``name` WHERE id=10"); got != "SELECT `table42`.`a1`, col2 FROM `odd``name` WHERE id=?" {
		t.Fatal(got)
	}
}

func TestTextOmitsPhysicalRecordDataAndTerminalControls(t *testing.T) {
	got := Text("History list length 123\n0: len 7; hex 736563726574; asc secret;;\n\x1b[31mSELECT 'secret'\x1b[0m")
	if strings.Contains(got, "secret") || strings.Contains(got, "736563") || strings.Contains(got, "\x1b") || !strings.Contains(got, "123") {
		t.Fatal(got)
	}
}
