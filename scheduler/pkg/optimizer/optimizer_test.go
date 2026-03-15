package optimizer

import (
	"math"
	"math/rand"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

func TestDominates(t *testing.T) {
	a := Candidate{Objectives: Objectives{AverageP95Pending: 1.0, AverageMaxActiveNodes: 2.0}}
	b := Candidate{Objectives: Objectives{AverageP95Pending: 1.2, AverageMaxActiveNodes: 3.0}}
	c := Candidate{Objectives: Objectives{AverageP95Pending: 0.9, AverageMaxActiveNodes: 4.0}}

	if !dominates(a, b) {
		t.Fatalf("expected a to dominate b")
	}
	if dominates(a, c) {
		t.Fatalf("did not expect a to dominate c")
	}
}

func TestParetoFront(t *testing.T) {
	candidates := []Candidate{
		{Name: "a", Objectives: Objectives{AverageP95Pending: 1.0, AverageMaxActiveNodes: 4.0}},
		{Name: "b", Objectives: Objectives{AverageP95Pending: 1.2, AverageMaxActiveNodes: 2.0}},
		{Name: "c", Objectives: Objectives{AverageP95Pending: 1.5, AverageMaxActiveNodes: 5.0}},
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
	weights := evaluator.WeightSet{FreeCPU: 2, FreeMem: 2, PodPenalty: 0, Packing: 0}
	got := mutate(weights, rng, 1.0, 10.0, 0.0, 2.0)

	values := []float64{got.FreeCPU, got.FreeMem, got.PodPenalty, got.Packing}
	for _, value := range values {
		if value < 0 || value > 2 {
			t.Fatalf("weight out of bounds: %+v", got)
		}
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
				AverageP95Pending:     math.Round((weights.FreeCPU+weights.FreeMem)*1000) / 1000,
				AverageMaxActiveNodes: math.Round((weights.PodPenalty+weights.Packing)*1000) / 1000,
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
