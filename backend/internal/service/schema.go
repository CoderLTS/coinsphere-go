// schema.go —— JSON Schema 的通用小工具。
//
// 项目里两处用到 JSON Schema:任务定义的参数 schema(taskdef.go)、节点定义的配置 schema(nodes.go)。
// 两边都需要"取默认值""按声明的类型/范围校验一个值",所以这些函数放在这里共用,
// 谁都不拥有它们。这里只实现实际用得到的那一小部分 Schema 语义:
// properties / default / type / enum / minimum / maximum,不追求完整实现。

package service

import (
	"encoding/json"
)

// extractSchemaDefaultParams 从 schema 的 properties 里,把每个字段声明的 default 值挑出来。
func extractSchemaDefaultParams(schema M) M {
	defaults := M{}
	properties, _ := schema["properties"].(M)
	if properties == nil {
		if raw, ok := schema["properties"].(map[string]any); ok {
			properties = raw
		}
	}
	for key, propertyAny := range properties {
		property, ok := propertyAny.(M)
		if !ok {
			if raw, isMap := propertyAny.(map[string]any); isMap {
				property = raw
			} else {
				continue
			}
		}
		if defaultValue, exists := property["default"]; exists {
			defaults[key] = defaultValue
		}
	}
	return defaults
}

func buildEffectiveDefaultParams(schema, overrides M) M {
	result := extractSchemaDefaultParams(schema)
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func buildConfiguredOverrides(schema, effective M) M {
	schemaDefaults := extractSchemaDefaultParams(schema)
	overrides := M{}
	for key, value := range effective {
		if schemaDefault, exists := schemaDefaults[key]; exists && jsonEqual(schemaDefault, value) {
			continue
		}
		overrides[key] = value
	}
	return overrides
}

func jsonEqual(left, right any) bool { return dumpJSON(left) == dumpJSON(right) }

func validatePartialParams(params, schema M) error {
	properties, _ := schema["properties"].(M)
	if properties == nil {
		if raw, ok := schema["properties"].(map[string]any); ok {
			properties = raw
		}
	}
	if properties == nil {
		if len(params) > 0 {
			return bizErr("Task definition does not declare configurable params")
		}
		return nil
	}
	for key, value := range params {
		fieldSchemaAny, exists := properties[key]
		if !exists {
			return bizErr("Task definition param is not allowed: %s", key)
		}
		fieldSchema, ok := fieldSchemaAny.(map[string]any)
		if !ok {
			return bizErr("Task definition param is not allowed: %s", key)
		}
		if err := validateFieldValue(key, value, fieldSchema); err != nil {
			return err
		}
	}
	return nil
}

func validateFieldValue(key string, value any, fieldSchema map[string]any) error {
	if expectedType, exists := fieldSchema["type"]; exists && !matchesSchemaType(value, expectedType) {
		return bizErr("Task definition param type is invalid: %s", key)
	}
	if enumValues, ok := fieldSchema["enum"].([]any); ok {
		found := false
		for _, candidate := range enumValues {
			if jsonEqual(candidate, value) {
				found = true
				break
			}
		}
		if !found {
			return bizErr("Task definition param value is invalid: %s", key)
		}
	}
	if _, isBool := value.(bool); isBool {
		return nil
	}
	if number, isNumber := toFloat(value); isNumber {
		if minimum, ok := toFloatAny(fieldSchema["minimum"]); ok && number < minimum {
			return bizErr("Task definition param is below minimum: %s", key)
		}
		if maximum, ok := toFloatAny(fieldSchema["maximum"]); ok && number > maximum {
			return bizErr("Task definition param is above maximum: %s", key)
		}
	}
	return nil
}

// matchesSchemaType 判断一个值是否符合 schema 声明的类型(type 为数组时"满足其一即可")。
func matchesSchemaType(value, expectedType any) bool {
	if typeList, ok := expectedType.([]any); ok {
		for _, item := range typeList {
			if matchesSchemaType(value, item) {
				return true
			}
		}
		return false
	}
	// switch 按类型名分派;Go 的 case 默认不"穿透"到下一个,不用手写 break。见 GO入门笔记『其它会撞见的小语法』。
	// 每个 case 里再用类型断言 value.(T) 检查这个值实际是不是对应的 Go 类型。
	switch asString(expectedType) {
	case "string":
		_, ok := value.(string)
		return ok
	case "integer":
		if _, isBool := value.(bool); isBool {
			return false
		}
		number, ok := toFloat(value)
		return ok && number == float64(int64(number))
	case "number":
		if _, isBool := value.(bool); isBool {
			return false
		}
		_, ok := toFloat(value)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

// toFloat 尽量把一个 any 值转成 float64;第二个返回值表示能不能转成。
// switch value.(type) 是"类型 switch":按值的真实类型分支,进入 case 后 typed 就已是那个具体类型。
// 见 GO入门笔记『其它会撞见的小语法』。
func toFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func toFloatAny(value any) (float64, bool) {
	if value == nil {
		return 0, false
	}
	return toFloat(value)
}
