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
	if err := visit(e); err != nil {
		return err
	}
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
	for _, child := range children {
		if err := Walk(child, visit); err != nil {
			return err
		}
	}
	return nil
}
func ValidateNodeReferences(expression string, allowed map[string]bool) error {
	ast, err := Compile(expression)
	if err != nil {
		return err
	}
	return Walk(ast.Expr(), func(e *exprpb.Expr) error {
		root, path, static := AccessPath(e)
		if root == "nodes" && (e.GetSelectExpr() != nil || e.GetCallExpr() != nil) {
			if !static || len(path) == 0 {
				return errors.New("CEL node references must use a fixed node ID")
			}
			if !allowed[path[0]] {
				return fmt.Errorf("CEL references unavailable node %q", path[0])
			}
		}
		return nil
	})
}
func RewriteNodeReferences(expression string, mapping map[string]string) (string, error) {
	return rewriteExpression(expression, func(e *exprpb.Expr) error {
		if call := e.GetCallExpr(); call != nil && call.Function == operators.Index && len(call.Args) == 2 {
			root, path, static := AccessPath(e)
			if root == "nodes" {
				if !static {
					return errors.New("dynamic node reference cannot be remapped")
				}
				if len(path) == 1 {
					id, ok := mapping[path[0]]
					if !ok {
						return fmt.Errorf("unknown node reference %s", path[0])
					}
					call.Args[1] = stringExpression(id)
				}
			}
		}
		if sel := e.GetSelectExpr(); sel != nil {
			if id := sel.Operand.GetIdentExpr(); id != nil && id.Name == "nodes" {
				mapped, ok := mapping[sel.Field]
				if !ok {
					return fmt.Errorf("unknown node reference %s", sel.Field)
				}
				e.ExprKind = nodePath(mapped, nil).ExprKind
			}
		}
		return nil
	})
}

// RewriteLegacyInput 接受按旧字段明确解析后的来源；冲突留给迁移计划阻断。
func RewriteLegacyInput(expression string, sources map[string]Binding) (string, error) {
	return rewriteExpression(expression, func(e *exprpb.Expr) error {
		root, path, static := AccessPath(e)
		if root != "input" || len(path) == 0 && static {
			return nil
		}
		if !static {
			return errors.New("dynamic legacy input requires an explicit expression mapping")
		}
		binding, ok := sources[path[0]]
		if !ok {
			return fmt.Errorf("ambiguous legacy input field %q", path[0])
		}
		if binding.Kind != "field" {
			return errors.New("legacy CEL source must name a node field")
		}
		replacement := nodePath(binding.NodeInstanceID, append(append([]string(nil), binding.FieldPath...), path[1:]...))
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
