package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/invopop/jsonschema"

	"github.com/maheshrijal/mysq/internal/model"
)

func main() {
	var output string
	flag.StringVar(&output, "out", "schema/context-1.3.0.json", "output path")
	flag.Parse()
	reflector := &jsonschema.Reflector{DoNotReference: false, ExpandedStruct: true}
	schema := reflector.Reflect(&model.Context{})
	schema.ID = "https://github.com/maheshrijal/mysq/schema/context-1.3.0.json"
	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(output, data, 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote %s\n", output)
}
