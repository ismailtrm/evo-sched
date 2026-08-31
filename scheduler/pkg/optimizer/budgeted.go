package optimizer

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/policy"
)

func RunBudgeted(cfg Config, baselines []evaluator.ExperimentRunSpec, evaluate FreshEvaluateFunc) (result Result, err error) {
	if err := validateBudgetedConfig(cfg); err != nil {
		return Result{}, err
	}

	result = Result{
		PolicyVersion:      policy.Version,
		MeasurementVersion: evaluator.MeasurementVersion,
		StartedAt:          time.Now().UTC(),
		Config:             cfg,
		Budget: &BudgetSummary{
			ExpectedOptimizerSamples:   cfg.OptimizerSampleBudget,
			ExpectedBaselineSamples:    len(baselines),
			ExpectedWorkloadExecutions: (cfg.OptimizerSampleBudget + len(baselines)) * len(cfg.Workloads),
		},
	}
	defer func() {
		result.FinishedAt = time.Now().UTC()
		result.Budget.UniqueOptimizerCandidates = len(result.Evaluated)
	}()
	tracker := budgetTracker{summary: result.Budget, workloadsPerSample: len(cfg.Workloads)}

	for _, baseline := range baselines {
		candidate, err := newBudgetedCandidate(
			cfg,
			baseline.Name,
			"baseline",
			0,
			0,
			baseline.Weights,
			SampleKindBaseline,
			BudgetClassBaseline,
			&tracker,
			evaluate,
		)
		if err != nil {
			return result, err
		}
		result.Baselines = append(result.Baselines, candidate)
	}

	rng := rand.New(rand.NewSource(cfg.Seed))
	population := make([]Candidate, 0, cfg.PopulationSize)
	evaluatedIndex := make(map[string]int, cfg.PopulationSize+cfg.Generations*cfg.OffspringPerGeneration)
	creationOrdinal := 0
	for i := 0; i < cfg.PopulationSize; i++ {
		creationOrdinal++
		name := fmt.Sprintf("init-%03d", i+1)
		candidate, err := newBudgetedCandidate(
			cfg,
			name,
			"initial",
			0,
			creationOrdinal,
			randomWeights(rng, cfg.MinWeight, cfg.MaxWeight),
			SampleKindScreening,
			BudgetClassOptimizer,
			&tracker,
			evaluate,
		)
		if err != nil {
			return result, err
		}
		evaluatedIndex[candidate.ID] = len(result.Evaluated)
		result.Evaluated = append(result.Evaluated, candidate)
		population = append(population, candidate)
	}
	population = assignRanksAndCrowding(population)

	for generation := 1; generation <= cfg.Generations; generation++ {
		offspring := make([]Candidate, 0, cfg.OffspringPerGeneration)
		for i := 0; i < cfg.OffspringPerGeneration; i++ {
			parentA := tournamentSelect(population, rng)
			parentB := tournamentSelect(population, rng)
			childWeights := crossover(parentA.Weights, parentB.Weights, rng)
			childWeights = mutate(childWeights, rng, cfg.MutationRate, cfg.MutationSigma, cfg.MinWeight, cfg.MaxWeight)
			childWeights, err := canonicalizeOffspring(childWeights, parentA.Weights, parentB.Weights)
			if err != nil {
				return result, err
			}

			creationOrdinal++
			name := fmt.Sprintf("gen-%02d-%03d", generation, i+1)
			candidate, err := newBudgetedCandidate(
				cfg,
				name,
				"offspring",
				generation,
				creationOrdinal,
				childWeights,
				SampleKindScreening,
				BudgetClassOptimizer,
				&tracker,
				evaluate,
			)
			if err != nil {
				return result, err
			}
			evaluatedIndex[candidate.ID] = len(result.Evaluated)
			result.Evaluated = append(result.Evaluated, candidate)
			offspring = append(offspring, candidate)
		}

		combined := make([]Candidate, 0, len(population)+len(offspring))
		combined = append(combined, population...)
		combined = append(combined, offspring...)
		combined = assignRanksAndCrowding(combined)

		if cfg.PromotionsPerGeneration > 0 {
			promotionIndices, err := selectPromotionCandidates(combined, cfg.PromotionsPerGeneration)
			if err != nil {
				return result, fmt.Errorf("generation %d promotion: %w", generation, err)
			}
			for _, index := range promotionIndices {
				for sample := 0; sample < cfg.AdditionalSamplesPerPromotion; sample++ {
					if err := appendBudgetedSample(
						cfg,
						&combined[index],
						SampleKindPromotion,
						BudgetClassOptimizer,
						&tracker,
						evaluate,
					); err != nil {
						return result, err
					}
					if resultIndex, ok := evaluatedIndex[combined[index].ID]; ok {
						result.Evaluated[resultIndex] = combined[index]
					}
				}
				tracker.summary.PromotionEvents++
			}
		}

		population = selectNextPopulation(combined, cfg.PopulationSize)
	}

	if result.Budget.ActualOptimizerSamples != cfg.OptimizerSampleBudget {
		return result, fmt.Errorf(
			"optimizer sample budget mismatch after search: actual=%d expected=%d",
			result.Budget.ActualOptimizerSamples,
			cfg.OptimizerSampleBudget,
		)
	}

	result.FinalPopulation = assignRanksAndCrowding(population)
	result.ParetoFront = paretoFront(result.FinalPopulation)
	combined := make([]Candidate, 0, len(result.FinalPopulation)+len(result.Baselines))
	combined = append(combined, result.FinalPopulation...)
	combined = append(combined, result.Baselines...)
	result.CombinedParetoFront = paretoFront(assignRanksAndCrowding(combined))
	return result, nil
}

func validateBudgetedConfig(cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	if cfg.OffspringPerGeneration < 0 {
		return fmt.Errorf("offspring per generation must be non-negative")
	}
	if cfg.OptimizerSampleBudget <= 0 {
		return fmt.Errorf("optimizer sample budget must be positive")
	}

	switch cfg.SearchMode {
	case SearchModeBudgetOneShot:
		if cfg.PromotionsPerGeneration != 0 || cfg.AdditionalSamplesPerPromotion != 0 {
			return fmt.Errorf("one-shot search cannot configure promotions")
		}
	case SearchModeBudgetTwoStage:
		if cfg.PromotionsPerGeneration <= 0 || cfg.AdditionalSamplesPerPromotion <= 0 {
			return fmt.Errorf("two-stage search requires positive promotion counts and additional samples")
		}
	default:
		return fmt.Errorf("unsupported search mode %q", cfg.SearchMode)
	}

	perGeneration := cfg.OffspringPerGeneration +
		cfg.PromotionsPerGeneration*cfg.AdditionalSamplesPerPromotion
	expected := cfg.PopulationSize + cfg.Generations*perGeneration
	if cfg.OptimizerSampleBudget != expected {
		return fmt.Errorf(
			"optimizer sample budget %d does not match configured search cost %d",
			cfg.OptimizerSampleBudget,
			expected,
		)
	}
	return nil
}

func newBudgetedCandidate(
	cfg Config,
	name string,
	source string,
	generation int,
	creationOrdinal int,
	weights evaluator.WeightSet,
	sampleKind SampleKind,
	budgetClass BudgetClass,
	tracker *budgetTracker,
	evaluate FreshEvaluateFunc,
) (Candidate, error) {
	normalized, err := policy.Normalize(weights)
	if err != nil {
		return Candidate{}, fmt.Errorf("normalize candidate %s weights: %w", name, err)
	}
	candidate := Candidate{
		ID:              fmt.Sprintf("%s-%s", source, name),
		Name:            name,
		Source:          source,
		Generation:      generation,
		CreationOrdinal: creationOrdinal,
		Weights:         normalized,
	}
	if err := appendBudgetedSample(cfg, &candidate, sampleKind, budgetClass, tracker, evaluate); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

func appendBudgetedSample(
	cfg Config,
	candidate *Candidate,
	sampleKind SampleKind,
	budgetClass BudgetClass,
	tracker *budgetTracker,
	evaluate FreshEvaluateFunc,
) error {
	request := tracker.newRequest(cfg, *candidate, sampleKind, budgetClass)
	startedAt := time.Now().UTC()
	data, err := evaluate(request)
	finishedAt := time.Now().UTC()
	if err != nil {
		return fmt.Errorf("fresh evaluation %s failed: %w", request.EvaluationID, err)
	}

	tracker.complete(budgetClass)
	candidate.EvaluationSamples = append(candidate.EvaluationSamples, EvaluationSample{
		EvaluationID:                          request.EvaluationID,
		SampleKind:                            sampleKind,
		SampleOrdinal:                         request.SampleOrdinal,
		BudgetClass:                           budgetClass,
		BudgetOrdinal:                         request.BudgetOrdinal,
		CumulativeOptimizerSamplesAttempted:   request.CumulativeOptimizerSamplesAttempted,
		CumulativeWorkloadExecutionsAttempted: request.CumulativeWorkloadExecutionsAttempted,
		StartedAt:                             startedAt,
		FinishedAt:                            finishedAt,
		Data:                                  cloneBudgetedEvaluationData(data),
	})
	candidate.EvaluationCount = len(candidate.EvaluationSamples)
	aggregateCandidateSamples(candidate)
	candidate.EvaluationSamples[len(candidate.EvaluationSamples)-1].CandidateObjectivesAfterSample = candidate.Objectives
	return nil
}

func aggregateCandidateSamples(candidate *Candidate) {
	if len(candidate.EvaluationSamples) == 0 {
		candidate.Objectives = Objectives{}
		candidate.Workloads = nil
		candidate.Notes = nil
		return
	}

	var latencySum float64
	var nodeSum float64
	for _, sample := range candidate.EvaluationSamples {
		latencySum += sample.Data.Objectives.AverageP95SchedulingDecisionLatency
		nodeSum += sample.Data.Objectives.AverageMaxWorkloadOccupiedWorkerNodes
	}
	count := float64(len(candidate.EvaluationSamples))
	candidate.Objectives = Objectives{
		AverageP95SchedulingDecisionLatency:   latencySum / count,
		AverageMaxWorkloadOccupiedWorkerNodes: nodeSum / count,
	}
	if len(candidate.EvaluationSamples) == 1 {
		candidate.Workloads = append([]WorkloadMetrics(nil), candidate.EvaluationSamples[0].Data.Workloads...)
		candidate.Notes = append([]string(nil), candidate.EvaluationSamples[0].Data.Notes...)
		return
	}
	candidate.Workloads = nil
	candidate.Notes = []string{fmt.Sprintf(
		"candidate objectives are arithmetic means over %d fresh samples; raw workload metrics remain in evaluationSamples",
		len(candidate.EvaluationSamples),
	)}
}

func selectPromotionCandidates(candidates []Candidate, count int) ([]int, error) {
	if count < 0 {
		return nil, fmt.Errorf("promotion count must be non-negative")
	}
	eligible := make([]int, 0, len(candidates))
	for index, candidate := range candidates {
		if candidate.EvaluationCount == 1 {
			eligible = append(eligible, index)
		}
	}
	if len(eligible) < count {
		return nil, fmt.Errorf("found %d eligible candidates, need %d", len(eligible), count)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a := candidates[eligible[i]]
		b := candidates[eligible[j]]
		switch {
		case a.Rank != b.Rank:
			return a.Rank < b.Rank
		case a.CrowdingDistance != b.CrowdingDistance:
			return a.CrowdingDistance > b.CrowdingDistance
		case a.CreationOrdinal != b.CreationOrdinal:
			return a.CreationOrdinal < b.CreationOrdinal
		default:
			return a.ID < b.ID
		}
	})
	return append([]int(nil), eligible[:count]...), nil
}

func cloneBudgetedEvaluationData(data EvaluationData) EvaluationData {
	data.Workloads = append([]WorkloadMetrics(nil), data.Workloads...)
	data.Notes = append([]string(nil), data.Notes...)
	return data
}

type budgetTracker struct {
	summary            *BudgetSummary
	workloadsPerSample int
}

func (t *budgetTracker) newRequest(
	cfg Config,
	candidate Candidate,
	sampleKind SampleKind,
	budgetClass BudgetClass,
) EvaluationRequest {
	sampleOrdinal := len(candidate.EvaluationSamples) + 1
	budgetOrdinal := 0
	switch budgetClass {
	case BudgetClassBaseline:
		t.summary.AttemptedBaselineSamples++
		budgetOrdinal = t.summary.AttemptedBaselineSamples
	case BudgetClassOptimizer:
		t.summary.AttemptedOptimizerSamples++
		budgetOrdinal = t.summary.AttemptedOptimizerSamples
	}
	attemptedSamples := t.summary.AttemptedBaselineSamples + t.summary.AttemptedOptimizerSamples
	t.summary.AttemptedWorkloadExecutions = attemptedSamples * t.workloadsPerSample
	evaluationID := fmt.Sprintf(
		"%s-seed-%d-%s-%s-%03d",
		cfg.SearchMode,
		cfg.Seed,
		candidate.ID,
		sampleKind,
		sampleOrdinal,
	)
	return EvaluationRequest{
		EvaluationID:                          evaluationID,
		CandidateID:                           candidate.ID,
		CandidateName:                         candidate.Name,
		CandidateCreationOrdinal:              candidate.CreationOrdinal,
		Generation:                            candidate.Generation,
		Weights:                               candidate.Weights,
		SampleKind:                            sampleKind,
		SampleOrdinal:                         sampleOrdinal,
		BudgetClass:                           budgetClass,
		BudgetOrdinal:                         budgetOrdinal,
		CumulativeOptimizerSamplesAttempted:   t.summary.AttemptedOptimizerSamples,
		CumulativeWorkloadExecutionsAttempted: attemptedSamples * t.workloadsPerSample,
	}
}

func (t *budgetTracker) complete(budgetClass BudgetClass) {
	switch budgetClass {
	case BudgetClassBaseline:
		t.summary.ActualBaselineSamples++
	case BudgetClassOptimizer:
		t.summary.ActualOptimizerSamples++
	}
	t.summary.ActualWorkloadExecutions =
		(t.summary.ActualBaselineSamples + t.summary.ActualOptimizerSamples) * t.workloadsPerSample
}
