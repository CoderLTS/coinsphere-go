// nodes_switch.go —— 多路分支节点。
//
// condition.branch 只有 true/false 两条路;这里的 condition.switch 支持任意多路:
// 每个 case 一条分支,自上往下第一个命中的 case 胜出,都不命中走 default。
//
// 它是注册表里第一个"分支数由节点自己的配置决定"的节点 —— 靠登记项上的
// BranchesConfigKey("cases")+ ExtraBranches(["default"])声明,校验器和前端画布
// 都按这份声明算出该有几个出口,谁都不用知道 switch 的具体逻辑。

package service

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "condition.switch", Label: "多路分支",
		Kind: nodeKindBranch,
		// 分支键 = cases 数组里每项的 key,再加一个兜底的 default。
		BranchesConfigKey: "cases",
		ExtraBranches:     []string{switchDefaultBranch},
		ConfigSchema: M{
			"type": "object",
			"properties": M{
				"path": M{
					"type": "string", "title": "默认字段路径",
					"description": "各个 case 没单独写 path 时用这个",
				},
				"cases": M{
					"type": "array", "title": "分支列表(自上往下第一个命中的胜出)",
					"items": M{"type": "object", "properties": M{
						"key":       M{"type": "string", "title": "分支名(连线上显示的标识)"},
						"path":      M{"type": "string", "title": "字段路径", "description": "留空则用上面的默认路径"},
						"operator":  M{"type": "string", "title": "比较运算", "enum": []string{"eq", "ne", "contains", "gt", "gte", "lt", "lte", "truthy"}},
						"value":     M{"type": "string", "title": "比较值"},
						"valuePath": M{"type": "string", "title": "比较值路径"},
					}},
				},
			},
			"required": []string{"cases"},
		},
		Execute: conditionSwitchExecute,
	})
}

// switchDefaultBranch 所有 case 都不命中时走的兜底分支名。
const switchDefaultBranch = "default"

// conditionSwitchExecute 自上往下求值各 case,第一个成立的分支胜出;都不成立走 default。
func conditionSwitchExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	cases, _ := config["cases"].([]any)
	if len(cases) == 0 {
		return nil, bizErr("多路分支节点至少需要一个 case")
	}
	defaultPath := cfgStr(config, "path", "")

	selected := switchDefaultBranch
	matchedKey := ""
	for _, itemAny := range cases {
		clause, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		key := cfgStr(clause, "key", "")
		if key == "" {
			return nil, bizErr("多路分支节点的分支名不能为空")
		}
		if key == switchDefaultBranch {
			return nil, bizErr("分支名 %s 是保留的兜底分支,请换一个名字", switchDefaultBranch)
		}
		// case 自己没写 path 就沿用节点级的默认路径。
		if cfgStr(clause, "path", "") == "" && defaultPath != "" {
			clause["path"] = defaultPath
		}
		if evalCondition(ctx, clause) {
			selected = key
			matchedKey = key
			break
		}
	}

	output := M{"selectedBranch": selected, "matched": matchedKey != ""}
	setNodeOutput(ctx, output)
	// SelectedBranch 非 nil,引擎只会点亮 branchKey 与它相符的那条出边。
	return &nodeExecResult{Output: output, SelectedBranch: &selected}, nil
}
