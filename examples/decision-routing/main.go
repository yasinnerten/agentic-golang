package main

import (
	"fmt"

	"github.com/yasinnerten/agentic-golang/agentic/workflow"
	"github.com/yasinnerten/agentic-golang/services/rulesengine"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

func main() {
	graph := &types.WorkflowGraph{
		Definition: types.WorkflowDefinition{
			WorkflowID:   "wf_generic_review",
			WorkflowName: "Generic Review",
			EntryNodeID:  "classify",
		},
		Nodes: map[string]types.WorkflowNode{
			"classify": {WorkflowNodeID: "classify", NodeName: "Classify request"},
			"approve":  {WorkflowNodeID: "approve", NodeName: "Approve"},
			"review":   {WorkflowNodeID: "review", NodeName: "Human review"},
		},
		Edges: []types.WorkflowEdge{
			{
				WorkflowEdgeID:      "edge_auto_approve",
				FromNodeID:          "classify",
				ToNodeID:            "approve",
				ConditionExpression: "confidence >= 0.80 && risk_level == 'low'",
				Priority:            20,
			},
			{
				WorkflowEdgeID:      "edge_human_review",
				FromNodeID:          "classify",
				ToNodeID:            "review",
				ConditionExpression: "",
				Priority:            10,
			},
		},
	}

	env := map[string]any{
		"confidence": 0.91,
		"risk_level": "low",
	}

	edge := workflow.SelectNextEdge(rulesengine.NewEngine(), graph, graph.Definition.EntryNodeID, env)
	if edge == nil {
		fmt.Println("terminal")
		return
	}

	next := graph.Nodes[edge.ToNodeID]
	fmt.Printf("next node: %s (%s)\n", next.WorkflowNodeID, next.NodeName)
}
