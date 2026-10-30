package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/maheshrijal/mysq/internal/model"
)

func TestFocusedQueriesShowReadRatio(t *testing.T) {
	ctx := &model.Context{Queries: []model.Query{
		{Digest: "a", Calls: 10, TotalLatencyMillis: 100, RowsExamined: 4000000000, RowsSent: 1, Statement: "SELECT `id` FROM `orders` WHERE `email` = ?"},
		{Digest: "b", Calls: 10, TotalLatencyMillis: 50, RowsExamined: 5, RowsSent: 10, Statement: "SELECT ?"},
		{Digest: "c", Calls: 10, TotalLatencyMillis: 25, RowsExamined: 12, RowsSent: 0, Statement: "UPDATE `orders` SET `status` = ? WHERE `id` = ?"},
	}}
	var out bytes.Buffer
	if err := Focused(&out, "queries", ctx); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 || !strings.Contains(lines[0], "READ/RET") {
		t.Fatalf("unexpected queries output:\n%s", out.String())
	}
	column := -1
	for i, heading := range strings.Fields(lines[0]) {
		if heading == "READ/RET" {
			column = i // Headings and fields align one to one up to READ/RET.
		}
	}
	if column < 0 {
		t.Fatalf("no READ/RET heading:\n%s", lines[0])
	}
	for i, expected := range []string{"4.0Bx", "<1x", "—"} {
		fields := strings.Fields(lines[i+1])
		if len(fields) <= column || fields[column] != expected {
			t.Fatalf("row %d READ/RET = %v, want %q:\n%s", i+1, fields, expected, out.String())
		}
	}
}
