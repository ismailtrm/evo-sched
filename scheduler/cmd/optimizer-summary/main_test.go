package main

import (
	"testing"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

func TestBuildSummarySeparatesNovelAndRediscoveredParetoPoints(t *testing.T) {
	baselineA := candidate("balanced", "baseline", 1.4, 4, evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25})
	baselineB := candidate("packing", "baseline", 1.5, 3, evaluator.WeightSet{FreeCPU: 0.1, FreeMem: 0.1, Balance: 0.2, Consolidation: 0.6})
	novel := candidate("candidate-a", "offspring", 1.2, 4, evaluator.WeightSet{FreeCPU: 0.4, FreeMem: 0.3, Balance: 0.2, Consolidation: 0.1})
	rediscovery := candidate("candidate-b", "initial", 1.5, 3, baselineB.Weights)

	results := []seedResult{
		{
			Path: "seed-42/result.json",
			Result: optimizer.Result{
				Config:              optimizer.Config{Seed: 42, Workloads: []string{"a", "b", "c"}},
				Baselines:           []optimizer.Candidate{baselineA, baselineB},
				Evaluated:           []optimizer.Candidate{novel, rediscovery},
				CombinedParetoFront: []optimizer.Candidate{novel, baselineB, rediscovery},
			},
		},
	}

	got, err := buildSummary(results)
	if err != nil {
		t.Fatalf("buildSummary() error = %v", err)
	}
	if got.Aggregate.SeedCount != 1 || got.Aggregate.OptimizerCandidateCount != 2 || got.Aggregate.TotalSolutionCount != 4 {
		t.Fatalf("unexpected aggregate counts: %+v", got.Aggregate)
	}
	if got.Aggregate.WorkloadExecutionCount != 12 {
		t.Fatalf("WorkloadExecutionCount = %d, want 12", got.Aggregate.WorkloadExecutionCount)
	}
	seed := got.Seeds[0]
	if seed.RawCombinedParetoCount != 3 || seed.UniqueCombinedParetoPointCount != 2 {
		t.Fatalf("unexpected Pareto counts: %+v", seed)
	}
	if len(seed.BaselinesKept) != 1 || seed.BaselinesKept[0] != "packing" {
		t.Fatalf("BaselinesKept = %v, want packing", seed.BaselinesKept)
	}
	if len(seed.BaselinesDropped) != 1 || seed.BaselinesDropped[0] != "balanced" {
		t.Fatalf("BaselinesDropped = %v, want balanced", seed.BaselinesDropped)
	}
	if len(seed.Points) != 2 || !seed.Points[0].NovelOptimizerPoint || !seed.Points[1].RediscoveredBaselinePoint {
		t.Fatalf("unexpected point classification: %+v", seed.Points)
	}
	if got.Aggregate.SeedsWithNovelOptimizerPoint != 1 {
		t.Fatalf("SeedsWithNovelOptimizerPoint = %d, want 1", got.Aggregate.SeedsWithNovelOptimizerPoint)
	}
	if len(got.Aggregate.Finalists) != 2 {
		t.Fatalf("Finalists = %d, want 2 optimizer Pareto members", len(got.Aggregate.Finalists))
	}
}

func TestBuildSummaryRejectsDuplicateSeed(t *testing.T) {
	results := []seedResult{
		{Path: "one", Result: optimizer.Result{Config: optimizer.Config{Seed: 42}}},
		{Path: "two", Result: optimizer.Result{Config: optimizer.Config{Seed: 42}}},
	}
	if _, err := buildSummary(results); err == nil {
		t.Fatal("buildSummary() error = nil, want duplicate seed rejection")
	}
}

func candidate(name, source string, latency, nodes float64, weights evaluator.WeightSet) optimizer.Candidate {
	return optimizer.Candidate{
		Name:    name,
		Source:  source,
		Weights: weights,
		Objectives: optimizer.Objectives{
			AverageP95SchedulingDecisionLatency:   latency,
			AverageMaxWorkloadOccupiedWorkerNodes: nodes,
		},
	}
}
