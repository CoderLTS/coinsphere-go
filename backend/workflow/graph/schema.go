package graph

import (
	"encoding/json"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var schemaMu sync.Mutex
var schemas = map[string]*jsonschema.Schema{}

func ValidateValue(raw json.RawMessage, value any) error {
	key := string(raw)
	schemaMu.Lock()
	schema := schemas[key]
	if schema == nil {
		var document any
		if err := json.Unmarshal(raw, &document); err != nil {
			schemaMu.Unlock()
			return err
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		if err := compiler.AddResource("schema.json", document); err != nil {
			schemaMu.Unlock()
			return err
		}
		var err error
		schema, err = compiler.Compile("schema.json")
		if err != nil {
			schemaMu.Unlock()
			return err
		}
		// ponytail: bounded catalog cache; use LRU only when actual schema churn warrants it.
		if len(schemas) >= 4096 {
			schemas = map[string]*jsonschema.Schema{}
		}
		schemas[key] = schema
	}
	schemaMu.Unlock()
	return schema.Validate(value)
}
