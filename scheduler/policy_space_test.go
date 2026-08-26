package main

import (
	"math"
	"testing"
)

func TestV1PolicyPositiveScalingPreservesNodeRanking(t *testing.T) {
	states := map[string]*NodeState{
		"node-a": newNodeState("node-a", 4000, 8000, 500, 3000, 1),
		"node-b": newNodeState("node-b", 4000, 8000, 1800, 1000, 3),
		"node-c": newNodeState("node-c", 4000, 8000, 2500, 4500, 5),
	}
	req := ResourceRequest{CPUMilli: 400, MemoryBytes: 800}
	weights := ScoreWeights{FreeCPU: 1.2, FreeMem: 0.7, PodPenalty: 0.4, Packing: 0.9}
	scale := 7.5
	scaled := ScoreWeights{
		FreeCPU:    weights.FreeCPU * scale,
		FreeMem:    weights.FreeMem * scale,
		PodPenalty: weights.PodPenalty * scale,
		Packing:    weights.Packing * scale,
	}

	baseNode, _ := chooseBestNode(candidateNodes("node-a", "node-b", "node-c"), states, req, weights)
	scaledNode, _ := chooseBestNode(candidateNodes("node-a", "node-b", "node-c"), states, req, scaled)
	if baseNode != scaledNode {
		t.Fatalf("positive scaling changed selected node from %s to %s", baseNode, scaledNode)
	}

	for _, pair := range [][2]string{{"node-a", "node-b"}, {"node-a", "node-c"}, {"node-b", "node-c"}} {
		baseDelta := scoreNode(states[pair[0]], req, 5, weights) - scoreNode(states[pair[1]], req, 5, weights)
		scaledDelta := scoreNode(states[pair[0]], req, 5, scaled) - scoreNode(states[pair[1]], req, 5, scaled)
		if math.Abs(scaledDelta-scale*baseDelta) > 1e-9 {
			t.Fatalf("ranking delta for %v was not scaled linearly: base=%f scaled=%f", pair, baseDelta, scaledDelta)
		}
	}
}

func TestV1PackingWeightIsAbsorbedByFreeWeightsOnHomogeneousNodes(t *testing.T) {
	states := map[string]*NodeState{
		"node-a": newNodeState("node-a", 4000, 8000, 500, 3000, 1),
		"node-b": newNodeState("node-b", 4000, 8000, 1800, 1000, 3),
		"node-c": newNodeState("node-c", 4000, 8000, 2500, 4500, 5),
	}
	req := ResourceRequest{CPUMilli: 400, MemoryBytes: 800}
	weights := ScoreWeights{FreeCPU: 1.4, FreeMem: 1.1, PodPenalty: 0.7, Packing: 0.6}
	equivalent := ScoreWeights{
		FreeCPU:    weights.FreeCPU - weights.Packing/2,
		FreeMem:    weights.FreeMem - weights.Packing/2,
		PodPenalty: weights.PodPenalty,
	}

	baseNode, _ := chooseBestNode(candidateNodes("node-a", "node-b", "node-c"), states, req, weights)
	equivalentNode, _ := chooseBestNode(candidateNodes("node-a", "node-b", "node-c"), states, req, equivalent)
	if baseNode != equivalentNode {
		t.Fatalf("equivalent homogeneous policy changed selected node from %s to %s", baseNode, equivalentNode)
	}

	var wantOffset float64
	for i, name := range []string{"node-a", "node-b", "node-c"} {
		offset := scoreNode(states[name], req, 5, weights) - scoreNode(states[name], req, 5, equivalent)
		if i == 0 {
			wantOffset = offset
			continue
		}
		if math.Abs(offset-wantOffset) > 1e-9 {
			t.Fatalf("packing offset depends on homogeneous candidate: node=%s offset=%f want=%f", name, offset, wantOffset)
		}
	}
}

func TestV1PackingDependencyIsConditionalOnHomogeneousAllocations(t *testing.T) {
	states := map[string]*NodeState{
		"large-node": newNodeState("large-node", 8000, 16000, 2000, 4000, 1),
		"small-node": newNodeState("small-node", 4000, 8000, 1000, 2000, 1),
	}
	req := ResourceRequest{CPUMilli: 500, MemoryBytes: 1000}
	weights := ScoreWeights{FreeCPU: 0.6, FreeMem: 0.6, Packing: 1.0}
	homogeneousEquivalent := ScoreWeights{FreeCPU: 0.1, FreeMem: 0.1}

	packingNode, _ := chooseBestNode(candidateNodes("large-node", "small-node"), states, req, weights)
	equivalentNode, _ := chooseBestNode(candidateNodes("large-node", "small-node"), states, req, homogeneousEquivalent)

	if packingNode != "small-node" {
		t.Fatalf("expected packing term to prefer the smaller allocation, got %s", packingNode)
	}
	if equivalentNode != "large-node" {
		t.Fatalf("expected transformed policy to tie and use deterministic name order, got %s", equivalentNode)
	}
}
