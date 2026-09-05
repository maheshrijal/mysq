package schema

import (
	"encoding/json"
	"testing"
)

func TestContextSchemaIsEmbeddedAndValidJSON(t *testing.T) {
	var value map[string]any
	if err := json.Unmarshal(ContextLatest(), &value); err != nil {
		t.Fatal(err)
	}
	if value["$schema"] == nil || value["properties"] == nil {
		t.Fatalf("schema missing contract fields: %v", value)
	}
	if value["$id"] != "https://github.com/maheshrijal/mysq/schema/context-1.5.0.json" {
		t.Fatalf("schema id = %v", value["$id"])
	}
	properties, ok := value["properties"].(map[string]any)
	if !ok || properties["sample_intervals_ms"] == nil {
		t.Fatalf("schema missing per-family sample intervals: %v", value["properties"])
	}
}
