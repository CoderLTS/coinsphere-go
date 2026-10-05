package graph

import (
	"encoding/json"
	"strings"

	"cel.dev/cel-go/common/operators"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// schemas keys identify namespace roots, for example input and nodes.strategy.
func HasDecimalArithmetic(expression string, schemas map[string]json.RawMessage) bool {
	ast, err := Compile(expression)
	if err != nil {
		return true
	}
	fields := map[string]bool{}
	var collect func(map[string]any, string)
	collect = func(schema map[string]any, path string) {
		if schema["x-coinsphere-decimal"] == true {
			fields[path] = true
		}
		properties, _ := schema["properties"].(map[string]any)
		for key, raw := range properties {
			if property, ok := raw.(map[string]any); ok {
				collect(property, path+"."+key)
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			collect(items, path)
		}
		if extra, ok := schema["additionalProperties"].(map[string]any); ok {
			collect(extra, path)
		}
	}
	for key, raw := range schemas {
		var schema map[string]any
		_ = json.Unmarshal(raw, &schema)
		collect(schema, key)
	}
	var references func(*exprpb.Expr) bool
	references = func(e *exprpb.Expr) bool {
		root, path, static := AccessPath(e)
		if root == "nodes" || root == "input" || root == "incoming" {
			key := strings.Join(append([]string{root}, path...), ".")
			for field := range fields {
				if key == field || strings.HasPrefix(field, key+".") || !static && strings.HasPrefix(key, field+".") {
					return true
				}
			}
			return false
		}
		for _, child := range expressionChildren(e) {
			if child != nil && references(child) {
				return true
			}
		}
		return false
	}
	blocked := false
	_ = Walk(ast.Expr(), func(e *exprpb.Expr) error {
		if c := e.GetComprehensionExpr(); c != nil && references(c.IterRange) {
			for _, child := range expressionChildren(c.LoopStep) {
				if call := child.GetCallExpr(); call != nil && (call.Function == operators.Add || call.Function == operators.Multiply || call.Function == operators.Divide || call.Function == operators.Subtract) {
					blocked = true
				}
			}
		}
		if call := e.GetCallExpr(); call != nil {
			switch call.Function {
			case operators.Add, operators.Subtract, operators.Multiply, operators.Divide, operators.Modulo, operators.Negate:
				for _, arg := range call.Args {
					if references(arg) {
						blocked = true
					}
				}
			}
		}
		return nil
	})
	return blocked
}
