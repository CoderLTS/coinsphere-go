package service

import (
	"coinsphere/backend/plugin/sdk"
)

func workflowIncomingOutputs(incoming []workflowGraphEdge, outputs map[string]map[string]any, event map[string]string, input map[string]any) []sdk.NodeOutput {
	result := make([]sdk.NodeOutput, 0, len(incoming))
	for _, edge := range incoming {
		output := outputs[edge.SourceNodeInstanceID]
		if output == nil {
			continue
		}
		reached, err := workflowEdgeReached(edge, outputs, event, input)
		if err == nil && reached {
			result = append(result, sdk.NodeOutput{NodeInstanceID: edge.SourceNodeInstanceID, SourcePort: edge.SourcePort, Output: mustJSON(output)})
		}
	}
	return result
}
