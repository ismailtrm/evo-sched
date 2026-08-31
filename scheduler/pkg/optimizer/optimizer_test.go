package optimizer

import (
	"encoding/csv"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

func TestDominates(t *testing.T) {
	a := Candidate{Objectives: Objectives{AverageP95SchedulingDecisionLatency: 1.0, AverageMaxWorkloadOccupiedWorkerNodes: 2.0}}
	b := Candidate{Objectives: Objectives{AverageP95SchedulingDecisionLatency: 1.2, AverageMaxWorkloadOccupiedWorkerNodes: 3.0}}
	c := Candidate{Objectives: Objectives{AverageP95SchedulingDecisionLatency: 0.9, AverageMaxWorkloadOccupiedWorkerNodes: 4.0}}

	if !dominates(a, b) {
		t.Fatalf("expected a to dominate b")
	}
	if dominates(a, c) {
		t.Fatalf("did not expect a to dominate c")
	}
}

func TestParetoFront(t *testing.T) {
	candidates := []Candidate{
		{Name: "a", Objectives: Objectives{AverageP95SchedulingDecisionLatency: 1.0, AverageMaxWorkloadOccupiedWorkerNodes: 4.0}},
		{Name: "b", Objectives: Objectives{AverageP95SchedulingDecisionLatency: 1.2, AverageMaxWorkloadOccupiedWorkerNodes: 2.0}},
		{Name: "c", Objectives: Objectives{AverageP95SchedulingDecisionLatency: 1.5, AverageMaxWorkloadOccupiedWorkerNodes: 5.0}},
	}

	front := paretoFront(candidates)
	if len(front) != 2 {
		t.Fatalf("expected 2 pareto candidates, got %d", len(front))
	}
	if front[0].Name != "a" || front[1].Name != "b" {
		t.Fatalf("unexpected pareto order: %+v", front)
	}
}

func TestMutateClampsWeights(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	weights := evaluator.WeightSet{FreeCPU: 0.5, FreeMem: 0.5, Balance: 0, Consolidation: 0}
	got := mutate(weights, rng, 1.0, 10.0, 0.0, 2.0)

	values := []float64{got.FreeCPU, got.FreeMem, got.Balance, got.Consolidation}
	for _, value := range values {
		if value < 0 || value > 2 {
			t.Fatalf("weight out of bounds: %+v", got)
		}
	}
}

func TestEvaluateCandidateCanonicalizesWeights(t *testing.T) {
	var evaluated evaluator.WeightSet
	evaluate := func(weights evaluator.WeightSet) (EvaluationData, error) {
		evaluated = weights
		return EvaluationData{}, nil
	}

	candidate, err := evaluateCandidate(
		"scaled",
		"test",
		0,
		evaluator.WeightSet{FreeCPU: 2, FreeMem: 1, Balance: 1},
		evaluate,
	)
	if err != nil {
		t.Fatalf("evaluateCandidate() error = %v", err)
	}
	want := evaluator.WeightSet{FreeCPU: 0.5, FreeMem: 0.25, Balance: 0.25}
	if candidate.Weights != want || evaluated != want {
		t.Fatalf("candidate=%+v evaluated=%+v want=%+v", candidate.Weights, evaluated, want)
	}
}

func TestCanonicalizeOffspringRepairsAllZeroChild(t *testing.T) {
	parentA := evaluator.WeightSet{FreeCPU: 1}
	parentB := evaluator.WeightSet{Consolidation: 1}

	got, err := canonicalizeOffspring(evaluator.WeightSet{}, parentA, parentB)
	if err != nil {
		t.Fatalf("canonicalizeOffspring() error = %v", err)
	}
	want := evaluator.WeightSet{FreeCPU: 0.5, Consolidation: 0.5}
	if got != want {
		t.Fatalf("canonicalizeOffspring() = %+v, want %+v", got, want)
	}
}

func TestRunKeepsPopulationSize(t *testing.T) {
	cfg := Config{
		PopulationSize: 4,
		Generations:    1,
		MutationRate:   0.8,
		MutationSigma:  0.2,
		MinWeight:      0,
		MaxWeight:      2,
		Seed:           42,
		Workloads:      []string{"cpu-tight"},
	}

	evaluate := func(weights evaluator.WeightSet) (EvaluationData, error) {
		return EvaluationData{
			Objectives: Objectives{
				AverageP95SchedulingDecisionLatency:   math.Round((weights.FreeCPU+weights.FreeMem)*1000) / 1000,
				AverageMaxWorkloadOccupiedWorkerNodes: math.Round((weights.Balance+weights.Consolidation)*1000) / 1000,
			},
		}, nil
	}

	result, err := Run(cfg, nil, evaluate)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.FinalPopulation) != cfg.PopulationSize {
		t.Fatalf("expected final population size %d, got %d", cfg.PopulationSize, len(result.FinalPopulation))
	}
	if len(result.ParetoFront) == 0 {
		t.Fatalf("expected non-empty pareto front")
	}
}

func TestWriteCandidatesCSVIncludesMeasurementContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.csv")
	protocol := evaluator.MeasurementProtocol{
		Version:                      evaluator.MeasurementVersion,
		WorkloadResourceMetricSource: evaluator.WorkloadRequestedResourceMetricSource,
		SchedulerIntervalSeconds:     0.5,
		PodPollIntervalSeconds:       0.25,
	}
	candidates := []Candidate{{
		ID:   "candidate-1",
		Name: "candidate-1",
		Objectives: Objectives{
			AverageP95SchedulingDecisionLatency:   0.75,
			AverageMaxWorkloadOccupiedWorkerNodes: 2,
		},
	}}

	if err := WriteCandidatesCSV(path, protocol, candidates); err != nil {
		t.Fatalf("WriteCandidatesCSV() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read candidates CSV: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "policy_version,measurement_version,scheduler_interval_seconds,pod_poll_interval_seconds,workload_resource_metric_source") {
		t.Fatalf("missing measurement contract header: %s", content)
	}
	if !strings.Contains(content, "v2,v2,0.500000,0.250000,"+evaluator.WorkloadRequestedResourceMetricSource+",candidate-1") {
		t.Fatalf("missing measurement contract row: %s", content)
	}
}

func TestWriteSamplesCSVIncludesFreshEvaluationProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.csv")
	protocol := evaluator.MeasurementProtocol{
		Version:                      evaluator.MeasurementVersion,
		WorkloadResourceMetricSource: evaluator.WorkloadRequestedResourceMetricSource,
		SchedulerIntervalSeconds:     0.25,
		PodPollIntervalSeconds:       0.25,
	}
	candidates := []Candidate{{
		ID:              "offspring-gen-01-001",
		Name:            "gen-01-001",
		Source:          "offspring",
		Generation:      1,
		CreationOrdinal: 5,
		Weights: evaluator.WeightSet{
			FreeCPU:       0.1,
			FreeMem:       0.2,
			Balance:       0.3,
			Consolidation: 0.4,
		},
		EvaluationSamples: []EvaluationSample{{
			EvaluationID:                          "budget-two-stage-seed-42-offspring-gen-01-001-promotion-002",
			SampleKind:                            SampleKindPromotion,
			SampleOrdinal:                         2,
			BudgetClass:                           BudgetClassOptimizer,
			BudgetOrdinal:                         7,
			CumulativeOptimizerSamplesAttempted:   7,
			CumulativeWorkloadExecutionsAttempted: 30,
			CandidateObjectivesAfterSample: Objectives{
				AverageP95SchedulingDecisionLatency:   0.75,
				AverageMaxWorkloadOccupiedWorkerNodes: 3,
			},
			StartedAt:  time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
			FinishedAt: time.Date(2026, 8, 31, 12, 1, 0, 0, time.UTC),
			Data: EvaluationData{
				Objectives: Objectives{
					AverageP95SchedulingDecisionLatency:   0.5,
					AverageMaxWorkloadOccupiedWorkerNodes: 2,
				},
				Workloads: []WorkloadMetrics{{
					Workload:                       "cpu-tight",
					P95SchedulingDecisionLatency:   0.5,
					MaxWorkloadOccupiedWorkerNodes: 2,
				}},
			},
		}},
	}}

	cfg := Config{SearchMode: SearchModeBudgetTwoStage, Seed: 42}
	if err := WriteSamplesCSV(path, protocol, cfg, candidates); err != nil {
		t.Fatalf("WriteSamplesCSV() error = %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open samples CSV: %v", err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read samples CSV: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("samples CSV rows = %d, want header plus one sample", len(records))
	}
	row := csvRecordMap(records[0], records[1])
	want := map[string]string{
		"search_mode":                            string(SearchModeBudgetTwoStage),
		"seed":                                   "42",
		"candidate_id":                           "offspring-gen-01-001",
		"evaluation_id":                          "budget-two-stage-seed-42-offspring-gen-01-001-promotion-002",
		"sample_kind":                            string(SampleKindPromotion),
		"sample_ordinal":                         "2",
		"cumulative_optimizer_samples_attempted": "7",
		"cumulative_workload_executions_attempted":              "30",
		"sample_avg_p95_scheduling_decision_latency_seconds":    "0.500000",
		"aggregate_avg_p95_scheduling_decision_latency_seconds": "0.750000",
	}
	for key, value := range want {
		if row[key] != value {
			t.Fatalf("samples CSV %s = %q, want %q; row=%v", key, row[key], value, row)
		}
	}
}

func csvRecordMap(header, record []string) map[string]string {
	out := make(map[string]string, len(header))
	for i, key := range header {
		if i < len(record) {
			out[key] = record[i]
		}
	}
	return out
}
