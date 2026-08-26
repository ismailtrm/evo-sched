package main

import (
	"math"
	"testing"
)

type v1ResourceRequest struct {
	CPUMilli    int64
	MemoryBytes int64
}

type v1NodeState struct {
	Name     string
	AllocCPU int64
	AllocMem int64
	FreeCPU  int64
	FreeMem  int64
	PodCount int
}

type v1ScoreWeights struct {
	FreeCPU    float64
	FreeMem    float64
	PodPenalty float64
	Packing    float64
}

func TestV1PolicyPositiveScalingPreservesNodeRanking(t *testing.T) {
	states := map[string]*v1NodeState{
		"node-a": newV1NodeState("node-a", 4000, 8000, 500, 3000, 1),
		"node-b": newV1NodeState("node-b", 4000, 8000, 1800, 1000, 3),
		"node-c": newV1NodeState("node-c", 4000, 8000, 2500, 4500, 5),
	}
	req := v1ResourceRequest{CPUMilli: 400, MemoryBytes: 800}
	weights := v1ScoreWeights{FreeCPU: 1.2, FreeMem: 0.7, PodPenalty: 0.4, Packing: 0.9}
	scale := 7.5
	scaled := v1ScoreWeights{
		FreeCPU:    weights.FreeCPU * scale,
		FreeMem:    weights.FreeMem * scale,
		PodPenalty: weights.PodPenalty * scale,
		Packing:    weights.Packing * scale,
	}

	baseNode, _ := chooseBestNodeV1([]string{"node-a", "node-b", "node-c"}, states, req, weights)
	scaledNode, _ := chooseBestNodeV1([]string{"node-a", "node-b", "node-c"}, states, req, scaled)
	if baseNode != scaledNode {
		t.Fatalf("positive scaling changed selected node from %s to %s", baseNode, scaledNode)
	}

	for _, pair := range [][2]string{{"node-a", "node-b"}, {"node-a", "node-c"}, {"node-b", "node-c"}} {
		baseDelta := scoreNodeV1(states[pair[0]], req, 5, weights) - scoreNodeV1(states[pair[1]], req, 5, weights)
		scaledDelta := scoreNodeV1(states[pair[0]], req, 5, scaled) - scoreNodeV1(states[pair[1]], req, 5, scaled)
		if math.Abs(scaledDelta-scale*baseDelta) > 1e-9 {
			t.Fatalf("ranking delta for %v was not scaled linearly: base=%f scaled=%f", pair, baseDelta, scaledDelta)
		}
	}
}

func TestV1PackingWeightIsAbsorbedByFreeWeightsOnHomogeneousNodes(t *testing.T) {
	states := map[string]*v1NodeState{
		"node-a": newV1NodeState("node-a", 4000, 8000, 500, 3000, 1),
		"node-b": newV1NodeState("node-b", 4000, 8000, 1800, 1000, 3),
		"node-c": newV1NodeState("node-c", 4000, 8000, 2500, 4500, 5),
	}
	req := v1ResourceRequest{CPUMilli: 400, MemoryBytes: 800}
	weights := v1ScoreWeights{FreeCPU: 1.4, FreeMem: 1.1, PodPenalty: 0.7, Packing: 0.6}
	equivalent := v1ScoreWeights{
		FreeCPU:    weights.FreeCPU - weights.Packing/2,
		FreeMem:    weights.FreeMem - weights.Packing/2,
		PodPenalty: weights.PodPenalty,
	}

	baseNode, _ := chooseBestNodeV1([]string{"node-a", "node-b", "node-c"}, states, req, weights)
	equivalentNode, _ := chooseBestNodeV1([]string{"node-a", "node-b", "node-c"}, states, req, equivalent)
	if baseNode != equivalentNode {
		t.Fatalf("equivalent homogeneous policy changed selected node from %s to %s", baseNode, equivalentNode)
	}

	var wantOffset float64
	for i, name := range []string{"node-a", "node-b", "node-c"} {
		offset := scoreNodeV1(states[name], req, 5, weights) - scoreNodeV1(states[name], req, 5, equivalent)
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
	states := map[string]*v1NodeState{
		"large-node": newV1NodeState("large-node", 8000, 16000, 2000, 4000, 1),
		"small-node": newV1NodeState("small-node", 4000, 8000, 1000, 2000, 1),
	}
	req := v1ResourceRequest{CPUMilli: 500, MemoryBytes: 1000}
	weights := v1ScoreWeights{FreeCPU: 0.6, FreeMem: 0.6, Packing: 1.0}
	homogeneousEquivalent := v1ScoreWeights{FreeCPU: 0.1, FreeMem: 0.1}

	packingNode, _ := chooseBestNodeV1([]string{"large-node", "small-node"}, states, req, weights)
	equivalentNode, _ := chooseBestNodeV1([]string{"large-node", "small-node"}, states, req, homogeneousEquivalent)

	if packingNode != "small-node" {
		t.Fatalf("expected packing term to prefer the smaller allocation, got %s", packingNode)
	}
	if equivalentNode != "large-node" {
		t.Fatalf("expected transformed policy to tie and use deterministic name order, got %s", equivalentNode)
	}
}

func newV1NodeState(name string, allocCPU, allocMem, usedCPU, usedMem int64, podCount int) *v1NodeState {
	return &v1NodeState{
		Name:     name,
		AllocCPU: allocCPU,
		AllocMem: allocMem,
		FreeCPU:  allocCPU - usedCPU,
		FreeMem:  allocMem - usedMem,
		PodCount: podCount,
	}
}

func chooseBestNodeV1(
	names []string,
	states map[string]*v1NodeState,
	req v1ResourceRequest,
	weights v1ScoreWeights,
) (string, float64) {
	feasibleNames := make([]string, 0, len(names))
	maxPodCount := 0
	for _, name := range names {
		state := states[name]
		if state == nil || state.FreeCPU < req.CPUMilli || state.FreeMem < req.MemoryBytes {
			continue
		}
		feasibleNames = append(feasibleNames, name)
		if state.PodCount > maxPodCount {
			maxPodCount = state.PodCount
		}
	}

	bestName := ""
	bestScore := math.Inf(-1)
	for _, name := range feasibleNames {
		state := states[name]
		score := scoreNodeV1(state, req, maxPodCount, weights)
		if score > bestScore+1e-9 || (math.Abs(score-bestScore) <= 1e-9 && (bestName == "" || name < bestName)) {
			bestName = name
			bestScore = score
		}
	}
	return bestName, bestScore
}

func scoreNodeV1(state *v1NodeState, req v1ResourceRequest, maxPodCount int, weights v1ScoreWeights) float64 {
	normFreeCPU := fractionV1(state.FreeCPU, state.AllocCPU)
	normFreeMem := fractionV1(state.FreeMem, state.AllocMem)

	normPodCount := 0.0
	if maxPodCount > 0 {
		normPodCount = float64(state.PodCount) / float64(maxPodCount)
	}

	remainingCPUFraction := fractionV1(max(int64(0), state.FreeCPU-req.CPUMilli), state.AllocCPU)
	remainingMemFraction := fractionV1(max(int64(0), state.FreeMem-req.MemoryBytes), state.AllocMem)
	normPacking := 1.0 - ((remainingCPUFraction + remainingMemFraction) / 2.0)

	return (weights.FreeCPU * normFreeCPU) +
		(weights.FreeMem * normFreeMem) -
		(weights.PodPenalty * normPodCount) +
		(weights.Packing * normPacking)
}

func fractionV1(value, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(value) / float64(total)
}
