package graph

import (
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/operators"
	"errors"
	"fmt"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// AccessPath 只解析静态字段访问；动态节点索引需要明确映射，不能猜测来源。
func AccessPath(e *exprpb.Expr) (string, []string, bool) {
	if e == nil {
		return "", nil, false
	}
	if id := e.GetIdentExpr(); id != nil {
		return id.Name, nil, true
	}
	if s := e.GetSelectExpr(); s != nil {
		root, path, ok := AccessPath(s.Operand)
		return root, append(path, s.Field), ok
	}
	if c := e.GetCallExpr(); c != nil && c.Function == operators.Index && len(c.Args) == 2 {
		root, path, ok := AccessPath(c.Args[0])
		key := c.Args[1].GetConstExpr()
		if key == nil {
			return root, path, false
		}
		if _, isString := key.ConstantKind.(*exprpb.Constant_StringValue); !isString {
			return root, path, false
		}
		return root, append(path, key.GetStringValue()), ok
	}
	return "", nil, false
}
func Walk(e *exprpb.Expr, visit func(*exprpb.Expr) error) error {
	if e == nil {
		return nil
	}
	children := expressionChildren(e)
	if err := visit(e); err != nil {
		return err
	}
	for _, child := range children {
		if err := Walk(child, visit); err != nil {
			return err
		}
	}
	return nil
}
func expressionChildren(e *exprpb.Expr) []*exprpb.Expr {
	children := []*exprpb.Expr{}
	if s := e.GetSelectExpr(); s != nil {
		children = append(children, s.Operand)
	}
	if c := e.GetCallExpr(); c != nil {
		children = append(children, c.Target)
		children = append(children, c.Args...)
	}
	if l := e.GetListExpr(); l != nil {
		children = append(children, l.Elements...)
	}
	if s := e.GetStructExpr(); s != nil {
		for _, entry := range s.Entries {
			children = append(children, entry.GetMapKey(), entry.Value)
		}
	}
	if c := e.GetComprehensionExpr(); c != nil {
		children = append(children, c.IterRange, c.AccuInit, c.LoopCondition, c.LoopStep, c.Result)
	}
	return children
}

func ValidateNodeReferences(expression string, allowed map[string]bool) error {
	ast, err := Compile(expression)
	if err != nil {
		return err
	}
	covered := map[*exprpb.Expr]bool{}
	return Walk(ast.Expr(), func(e *exprpb.Expr) error {
		operand, id, fixed := directReference(e, "nodes")
		if operand != nil {
			covered[operand] = true
			if !fixed {
				return errors.New("CEL node references must use a fixed node ID")
			}
			if !allowed[id] {
				return fmt.Errorf("CEL references unavailable node %q", id)
			}
		}
		if ident := e.GetIdentExpr(); ident != nil && ident.Name == "nodes" && !covered[e] {
			return errors.New("CEL must reference individual upstream nodes")
		}
		return nil
	})
}

// Only the namespace's first index is an identity. Array and dynamic field access
// inside a known node remain ordinary CEL and must survive Loop ID expansion.
func directReference(e *exprpb.Expr, namespace string) (*exprpb.Expr, string, bool) {
	if sel := e.GetSelectExpr(); sel != nil {
		if ident := sel.Operand.GetIdentExpr(); ident != nil && ident.Name == namespace {
			return sel.Operand, sel.Field, true
		}
	}
	if call := e.GetCallExpr(); call != nil && call.Function == operators.Index && len(call.Args) == 2 {
		if ident := call.Args[0].GetIdentExpr(); ident != nil && ident.Name == namespace {
			key := call.Args[1].GetConstExpr()
			if key != nil {
				if value, ok := key.ConstantKind.(*exprpb.Constant_StringValue); ok {
					return call.Args[0], value.StringValue, true
				}
			}
			return call.Args[0], "", false
		}
	}
	return nil, "", false
}
func RewriteNodeReferences(expression string, mapping map[string]string) (string, error) {
	allowed := map[string]bool{}
	for id := range mapping {
		allowed[id] = true
	}
	if err := ValidateNodeReferences(expression, allowed); err != nil {
		return "", err
	}
	return rewriteExpression(expression, func(e *exprpb.Expr) error {
		if operand, id, fixed := directReference(e, "nodes"); operand != nil && fixed {
			e.ExprKind = nodePath(mapping[id], nil).ExprKind
		}
		return nil
	})
}

// RewriteLegacyInput 接受按旧字段明确解析后的来源；冲突留给迁移计划阻断。
func RewriteLegacyInput(expression string, sources map[string]Binding) (string, error) {
	covered := map[*exprpb.Expr]bool{}
	return rewriteExpression(expression, func(e *exprpb.Expr) error {
		operand, field, fixed := directReference(e, "input")
		if operand == nil {
			if id := e.GetIdentExpr(); id != nil && id.Name == "input" && !covered[e] {
				return errors.New("whole legacy input requires an explicit expression mapping")
			}
			return nil
		}
		covered[operand] = true
		if !fixed {
			return errors.New("dynamic legacy input requires an explicit expression mapping")
		}
		binding, ok := sources[field]
		if !ok {
			return fmt.Errorf("ambiguous legacy input field %q", field)
		}
		var replacement *exprpb.Expr
		switch binding.Kind {
		case "field":
			replacement = nodePath(binding.NodeInstanceID, binding.FieldPath)
		case "input":
			replacement = &exprpb.Expr{ExprKind: &exprpb.Expr_IdentExpr{IdentExpr: &exprpb.Expr_Ident{Name: "input"}}}
			for _, key := range binding.FieldPath {
				replacement = &exprpb.Expr{ExprKind: &exprpb.Expr_CallExpr{CallExpr: &exprpb.Expr_Call{Function: operators.Index, Args: []*exprpb.Expr{replacement, stringExpression(key)}}}}
			}
		default:
			return errors.New("legacy CEL source must name a node or entry field")
		}
		e.ExprKind = replacement.ExprKind
		return nil
	})
}
func rewriteExpression(expression string, change func(*exprpb.Expr) error) (string, error) {
	ast, err := Compile(expression)
	if err != nil {
		return "", err
	}
	tree := proto.Clone(ast.Expr()).(*exprpb.Expr)
	if err := Walk(tree, change); err != nil {
		return "", err
	}
	out, err := cel.AstToString(cel.ParsedExprToAst(&exprpb.ParsedExpr{Expr: tree, SourceInfo: ast.SourceInfo()}))
	if err != nil {
		return "", err
	}
	if _, err = Compile(out); err != nil {
		return "", err
	}
	return out, nil
}
func stringExpression(value string) *exprpb.Expr {
	return &exprpb.Expr{ExprKind: &exprpb.Expr_ConstExpr{ConstExpr: &exprpb.Constant{ConstantKind: &exprpb.Constant_StringValue{StringValue: value}}}}
}
func nodePath(id string, path []string) *exprpb.Expr {
	e := &exprpb.Expr{ExprKind: &exprpb.Expr_IdentExpr{IdentExpr: &exprpb.Expr_Ident{Name: "nodes"}}}
	for _, key := range append([]string{id}, path...) {
		e = &exprpb.Expr{ExprKind: &exprpb.Expr_CallExpr{CallExpr: &exprpb.Expr_Call{Function: operators.Index, Args: []*exprpb.Expr{e, stringExpression(key)}}}}
	}
	return e
}
