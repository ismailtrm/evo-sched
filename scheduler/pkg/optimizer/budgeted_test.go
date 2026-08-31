package optimizer

import (
	"errors"
	"math"
	"strings"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

var errBudgetTestFailure = errors.New("budget test failure")

func TestRunBudgetedOneShotUsesExactFreshBudget(t *testing.T) {
	cfg := Config{
		PopulationSize:         4,
		Generations:            1,
		MutationRate:           0.8,
		MutationSigma:          0.2,
		MinWeight:              0,
		MaxWeight:              2,
		Seed:                   42,
		Workloads:              []string{"cpu", "memory", "mixed"},
		SearchMode:             SearchModeBudgetOneShot,
		OffspringPerGeneration: 4,
		OptimizerSampleBudget:  8,
	}

	calls := 0
	seenIDs := map[string]struct{}{}
	evaluate := func(request EvaluationRequest) (EvaluationData, error) {
		calls++
		if request.SampleKind != SampleKindScreening {
			t.Fatalf("sample kind = %q, want %q", request.SampleKind, SampleKindScreening)
		}
		if _, exists := seenIDs[request.EvaluationID]; exists {
			t.Fatalf("duplicate evaluation ID %q", request.EvaluationID)
		}
		seenIDs[request.EvaluationID] = struct{}{}
		return evaluationForBudgetTest(float64(calls), 4), nil
	}

	result, err := RunBudgeted(cfg, nil, evaluate)
	if err != nil {
		t.Fatalf("RunBudgeted() error = %v", err)
	}
	if calls != 8 {
		t.Fatalf("fresh evaluation calls = %d, want 8", calls)
	}
	if result.Budget == nil {
		t.Fatal("result budget = nil")
	}
	if got, want := result.Budget.ExpectedOptimizerSamples, 8; got != want {
		t.Fatalf("expected optimizer samples = %d, want %d", got, want)
	}
	if got, want := result.Budget.ActualOptimizerSamples, 8; got != want {
		t.Fatalf("actual optimizer samples = %d, want %d", got, want)
	}
	if got, want := result.Budget.ActualWorkloadExecutions, 24; got != want {
		t.Fatalf("actual workload executions = %d, want %d", got, want)
	}
	if got, want := len(result.Evaluated), 8; got != want {
		t.Fatalf("evaluated candidates = %d, want %d", got, want)
	}
	for _, candidate := range result.Evaluated {
		if candidate.EvaluationCount != 1 || len(candidate.EvaluationSamples) != 1 {
			t.Fatalf("candidate %s sample state = count %d samples %d, want 1/1", candidate.ID, candidate.EvaluationCount, len(candidate.EvaluationSamples))
		}
	}
}

func TestRunBudgetedTwoStagePromotesAndAveragesFreshSamples(t *testing.T) {
	cfg := Config{
		PopulationSize:                4,
		Generations:                   1,
		MutationRate:                  0.8,
		MutationSigma:                 0.2,
		MinWeight:                     0,
		MaxWeight:                     2,
		Seed:                          42,
		Workloads:                     []string{"cpu", "memory", "mixed"},
		SearchMode:                    SearchModeBudgetTwoStage,
		OffspringPerGeneration:        2,
		PromotionsPerGeneration:       1,
		AdditionalSamplesPerPromotion: 2,
		OptimizerSampleBudget:         8,
	}

	calls := 0
	evaluate := func(request EvaluationRequest) (EvaluationData, error) {
		calls++
		return evaluationForBudgetTest(float64(calls), 4), nil
	}

	result, err := RunBudgeted(cfg, nil, evaluate)
	if err != nil {
		t.Fatalf("RunBudgeted() error = %v", err)
	}
	if calls != 8 {
		t.Fatalf("fresh evaluation calls = %d, want 8", calls)
	}
	if got, want := len(result.Evaluated), 6; got != want {
		t.Fatalf("unique evaluated candidates = %d, want %d", got, want)
	}
	if result.Budget == nil || result.Budget.ActualOptimizerSamples != 8 || result.Budget.PromotionEvents != 1 {
		t.Fatalf("unexpected budget summary: %+v", result.Budget)
	}

	var promoted *Candidate
	for i := range result.Evaluated {
		if result.Evaluated[i].EvaluationCount == 3 {
			promoted = &result.Evaluated[i]
			break
		}
	}
	if promoted == nil {
		t.Fatal("no candidate retained three fresh samples")
	}
	if len(promoted.EvaluationSamples) != 3 {
		t.Fatalf("promoted sample count = %d, want 3", len(promoted.EvaluationSamples))
	}

	latencySum := 0.0
	nodeSum := 0.0
	for index, sample := range promoted.EvaluationSamples {
		wantKind := SampleKindPromotion
		if index == 0 {
			wantKind = SampleKindScreening
		}
		if sample.SampleKind != wantKind {
			t.Fatalf("sample %d kind = %q, want %q", index, sample.SampleKind, wantKind)
		}
		latencySum += sample.Data.Objectives.AverageP95SchedulingDecisionLatency
		nodeSum += sample.Data.Objectives.AverageMaxWorkloadOccupiedWorkerNodes
		if sample.CumulativeOptimizerSamplesAttempted <= 0 || sample.CumulativeWorkloadExecutionsAttempted <= 0 {
			t.Fatalf("sample %d is missing cumulative budget provenance: %+v", index, sample)
		}
	}
	if got, want := promoted.Objectives.AverageP95SchedulingDecisionLatency, latencySum/3; math.Abs(got-want) > 1e-12 {
		t.Fatalf("promoted latency mean = %v, want %v", got, want)
	}
	if got, want := promoted.Objectives.AverageMaxWorkloadOccupiedWorkerNodes, nodeSum/3; math.Abs(got-want) > 1e-12 {
		t.Fatalf("promoted node mean = %v, want %v", got, want)
	}
	if got, want := promoted.EvaluationSamples[len(promoted.EvaluationSamples)-1].CandidateObjectivesAfterSample, promoted.Objectives; got != want {
		t.Fatalf("last sample aggregate = %+v, want %+v", got, want)
	}
}

func TestRunBudgetedPreservesCompletedPromotionSampleBeforeLaterFailure(t *testing.T) {
	cfg := Config{
		PopulationSize:                4,
		Generations:                   1,
		MutationRate:                  0.8,
		MutationSigma:                 0.2,
		MinWeight:                     0,
		MaxWeight:                     2,
		Seed:                          42,
		Workloads:                     []string{"cpu", "memory", "mixed"},
		SearchMode:                    SearchModeBudgetTwoStage,
		OffspringPerGeneration:        2,
		PromotionsPerGeneration:       1,
		AdditionalSamplesPerPromotion: 2,
		OptimizerSampleBudget:         8,
	}

	calls := 0
	result, err := RunBudgeted(cfg, nil, func(request EvaluationRequest) (EvaluationData, error) {
		calls++
		if calls == 8 {
			return EvaluationData{}, errBudgetTestFailure
		}
		return evaluationForBudgetTest(float64(calls), 4), nil
	})
	if err == nil || !strings.Contains(err.Error(), errBudgetTestFailure.Error()) {
		t.Fatalf("RunBudgeted() error = %v, want injected failure", err)
	}
	if result.Budget.ActualOptimizerSamples != 7 || result.Budget.AttemptedOptimizerSamples != 8 {
		t.Fatalf("partial budget = %+v, want actual=7 attempted=8", result.Budget)
	}
	if result.Budget.AttemptedWorkloadExecutions != 24 || result.Budget.ActualWorkloadExecutions != 21 {
		t.Fatalf("partial workload budget = %+v, want attempted=24 actual=21", result.Budget)
	}
	if result.Budget.UniqueOptimizerCandidates != 6 {
		t.Fatalf("partial unique candidates = %d, want 6", result.Budget.UniqueOptimizerCandidates)
	}
	if result.FinishedAt.IsZero() {
		t.Fatal("partial result is missing finishedAt")
	}
	foundPartialPromotion := false
	for _, candidate := range result.Evaluated {
		if candidate.EvaluationCount == 2 {
			foundPartialPromotion = true
			break
		}
	}
	if !foundPartialPromotion {
		t.Fatal("successful first promotion sample was not preserved in partial result")
	}
}

func TestRunBudgetedEvaluatesBaselinesOutsideOptimizerBudget(t *testing.T) {
	cfg := Config{
		PopulationSize:         2,
		Generations:            0,
		MutationRate:           0.8,
		MutationSigma:          0.2,
		MinWeight:              0,
		MaxWeight:              2,
		Seed:                   42,
		Workloads:              []string{"cpu", "memory", "mixed"},
		SearchMode:             SearchModeBudgetOneShot,
		OffspringPerGeneration: 2,
		OptimizerSampleBudget:  2,
	}
	baselines := []evaluator.ExperimentRunSpec{
		{Name: "balanced", Weights: evaluator.WeightSet{FreeCPU: 1}},
		{Name: "consolidation", Weights: evaluator.WeightSet{Consolidation: 1}},
	}

	requests := make([]EvaluationRequest, 0)
	evaluate := func(request EvaluationRequest) (EvaluationData, error) {
		requests = append(requests, request)
		return evaluationForBudgetTest(float64(len(requests)), 3), nil
	}

	result, err := RunBudgeted(cfg, baselines, evaluate)
	if err != nil {
		t.Fatalf("RunBudgeted() error = %v", err)
	}
	if len(requests) != 4 {
		t.Fatalf("fresh evaluation calls = %d, want 4", len(requests))
	}
	if result.Budget.ActualOptimizerSamples != 2 || result.Budget.ActualBaselineSamples != 2 {
		t.Fatalf("unexpected budget classes: %+v", result.Budget)
	}
	if result.Budget.ExpectedBaselineSamples != 2 || result.Budget.ExpectedWorkloadExecutions != 12 || result.Budget.AttemptedWorkloadExecutions != 12 {
		t.Fatalf("unexpected expected/attempted budget: %+v", result.Budget)
	}
	for index, request := range requests {
		wantClass := BudgetClassBaseline
		wantKind := SampleKindBaseline
		if index >= 2 {
			wantClass = BudgetClassOptimizer
			wantKind = SampleKindScreening
		}
		if request.BudgetClass != wantClass || request.SampleKind != wantKind {
			t.Fatalf("request %d class/kind = %q/%q, want %q/%q", index, request.BudgetClass, request.SampleKind, wantClass, wantKind)
		}
	}
}

func TestRunBudgetedRejectsMismatchedDeclaredBudget(t *testing.T) {
	cfg := Config{
		PopulationSize:         4,
		Generations:            1,
		MutationRate:           0.8,
		MutationSigma:          0.2,
		MinWeight:              0,
		MaxWeight:              2,
		Seed:                   42,
		Workloads:              []string{"cpu"},
		SearchMode:             SearchModeBudgetOneShot,
		OffspringPerGeneration: 4,
		OptimizerSampleBudget:  7,
	}

	_, err := RunBudgeted(cfg, nil, func(EvaluationRequest) (EvaluationData, error) {
		return EvaluationData{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "sample budget") {
		t.Fatalf("RunBudgeted() error = %v, want sample budget rejection", err)
	}
}

func TestSelectPromotionCandidatesUsesRankCrowdingAndCreationOrder(t *testing.T) {
	candidates := []Candidate{
		{Name: "rank-two", Rank: 2, CrowdingDistance: 99, CreationOrdinal: 1, EvaluationCount: 1},
		{Name: "low-crowding", Rank: 1, CrowdingDistance: 1, CreationOrdinal: 3, EvaluationCount: 1},
		{Name: "later", Rank: 1, CrowdingDistance: 2, CreationOrdinal: 4, EvaluationCount: 1},
		{Name: "earlier", Rank: 1, CrowdingDistance: 2, CreationOrdinal: 2, EvaluationCount: 1},
		{Name: "already-promoted", Rank: 1, CrowdingDistance: 100, CreationOrdinal: 0, EvaluationCount: 3},
	}

	indices, err := selectPromotionCandidates(candidates, 3)
	if err != nil {
		t.Fatalf("selectPromotionCandidates() error = %v", err)
	}
	want := []string{"earlier", "later", "low-crowding"}
	for i, index := range indices {
		if candidates[index].Name != want[i] {
			t.Fatalf("promotion %d = %s, want %s", i, candidates[index].Name, want[i])
		}
	}
}

func TestSelectPromotionCandidatesRejectsInsufficientEligibleCandidates(t *testing.T) {
	candidates := []Candidate{
		{Name: "eligible", Rank: 1, CrowdingDistance: 1, CreationOrdinal: 1, EvaluationCount: 1},
		{Name: "promoted", Rank: 1, CrowdingDistance: 2, CreationOrdinal: 2, EvaluationCount: 3},
	}
	_, err := selectPromotionCandidates(candidates, 2)
	if err == nil || !strings.Contains(err.Error(), "eligible") {
		t.Fatalf("selectPromotionCandidates() error = %v, want eligible-count rejection", err)
	}
}

func evaluationForBudgetTest(latency, nodes float64) EvaluationData {
	return EvaluationData{
		Objectives: Objectives{
			AverageP95SchedulingDecisionLatency:   latency,
			AverageMaxWorkloadOccupiedWorkerNodes: nodes,
		},
		Workloads: []WorkloadMetrics{{Workload: "test"}},
	}
}
