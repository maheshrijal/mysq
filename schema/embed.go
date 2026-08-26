package schema

import _ "embed"

//go:generate go run ../tools/schemagen -out context-1.4.0.json

//go:embed context-1.0.0.json
var contextV1 []byte

//go:embed context-1.4.0.json
var contextLatest []byte

func ContextV1() []byte {
	result := make([]byte, len(contextV1))
	copy(result, contextV1)
	return result
}

func ContextLatest() []byte {
	result := make([]byte, len(contextLatest))
	copy(result, contextLatest)
	return result
}
