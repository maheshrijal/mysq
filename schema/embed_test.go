package schema

import (
	"encoding/json"
	"testing"
)

func TestContextSchemaIsEmbeddedAndValidJSON(t *testing.T) {
	var value map[string]any
	if err := json.Unmarshal(ContextV1(), &value); err != nil {
		t.Fatal(err)
	}
	if value["$schema"] == nil || value["properties"] == nil {
		t.Fatalf("schema missing contract fields: %v", value)
	}
}
