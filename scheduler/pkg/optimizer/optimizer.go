package optimizer

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/policy"
)

const crowdingInfinity = 1e9

func Run(cfg Config, baselines []evaluator.ExperimentRunSpec, evaluate EvaluateFunc) (Result, error) {
	if err := validateConfig(cfg); err != nil {
		return Result{}, err
	}

	rng := rand.New(rand.NewSource(cfg.Seed))
	result := Result{
		PolicyVersion:      policy.Version,
		MeasurementVersion: evaluator.MeasurementVersion,
		StartedAt:          time.Now().UTC(),
		Config:             cfg,
	}

	for _, baseline := range baselines {
		candidate, err := evaluateCandidate(baseline.Name, "baseline", 0, baseline.Weights, evaluate)
		if err != nil {
			return result, err
		}
		result.Baselines = append(result.Baselines, candidate)
	}

	population := make([]Candidate, 0, cfg.PopulationSize)
	for i := 0; i < cfg.PopulationSize; i++ {
		name := fmt.Sprintf("init-%03d", i+1)
		weights := randomWeights(rng, cfg.MinWeight, cfg.MaxWeight)
		candidate, err := evaluateCandidate(name, "initial", 0, weights, evaluate)
		if err != nil {
			return result, err
		}
		population = append(population, candidate)
		result.Evaluated = append(result.Evaluated, candidate)
	}
	population = assignRanksAndCrowding(population)

	for gen := 1; gen <= cfg.Generations; gen++ {
		offspring := make([]Candidate, 0, cfg.PopulationSize)
		for i := 0; i < cfg.PopulationSize; i++ {
			parentA := tournamentSelect(population, rng)
			parentB := tournamentSelect(population, rng)
			childWeights := crossover(parentA.Weights, parentB.Weights, rng)
			childWeights = mutate(childWeights, rng, cfg.MutationRate, cfg.MutationSigma, cfg.MinWeight, cfg.MaxWeight)
			childWeights, err := canonicalizeOffspring(childWeights, parentA.Weights, parentB.Weights)
			if err != nil {
				return result, err
			}

			name := fmt.Sprintf("gen-%02d-%03d", gen, i+1)
			candidate, err := evaluateCandidate(name, "offspring", gen, childWeights, evaluate)
			if err != nil {
				return result, err
			}
			offspring = append(offspring, candidate)
			result.Evaluated = append(result.Evaluated, candidate)
		}

		combined := make([]Candidate, 0, len(population)+len(offspring))
		combined = append(combined, population...)
		combined = append(combined, offspring...)
		population = selectNextPopulation(combined, cfg.PopulationSize)
	}

	result.FinalPopulation = assignRanksAndCrowding(population)
	result.ParetoFront = paretoFront(result.FinalPopulation)

	combined := make([]Candidate, 0, len(result.FinalPopulation)+len(result.Baselines))
	combined = append(combined, result.FinalPopulation...)
	combined = append(combined, result.Baselines...)
	result.CombinedParetoFront = paretoFront(assignRanksAndCrowding(combined))
	result.FinishedAt = time.Now().UTC()
	return result, nil
}

func validateConfig(cfg Config) error {
	if cfg.PopulationSize <= 0 {
		return fmt.Errorf("population size must be positive")
	}
	if cfg.Generations < 0 {
		return fmt.Errorf("generations must be zero or positive")
	}
	if math.IsNaN(cfg.MutationRate) || math.IsInf(cfg.MutationRate, 0) || cfg.MutationRate < 0 || cfg.MutationRate > 1 {
		return fmt.Errorf("mutation rate must be between 0 and 1")
	}
	if math.IsNaN(cfg.MutationSigma) || math.IsInf(cfg.MutationSigma, 0) || cfg.MutationSigma < 0 {
		return fmt.Errorf("mutation sigma must be non-negative")
	}
	if math.IsNaN(cfg.MinWeight) || math.IsInf(cfg.MinWeight, 0) || cfg.MinWeight < 0 {
		return fmt.Errorf("min weight must be non-negative")
	}
	if math.IsNaN(cfg.MaxWeight) || math.IsInf(cfg.MaxWeight, 0) || cfg.MaxWeight <= 0 {
		return fmt.Errorf("max weight must be positive")
	}
	if cfg.MaxWeight < cfg.MinWeight {
		return fmt.Errorf("max weight must be >= min weight")
	}
	if len(cfg.Workloads) == 0 {
		return fmt.Errorf("at least one workload is required")
	}
	return nil
}

func evaluateCandidate(name, source string, generation int, weights evaluator.WeightSet, evaluate EvaluateFunc) (Candidate, error) {
	normalized, err := policy.Normalize(weights)
	if err != nil {
		return Candidate{}, fmt.Errorf("normalize candidate %s weights: %w", name, err)
	}
	data, err := evaluate(normalized)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		ID:         fmt.Sprintf("%s-%s", source, name),
		Name:       name,
		Source:     source,
		Generation: generation,
		Weights:    normalized,
		Objectives: data.Objectives,
		Workloads:  append([]WorkloadMetrics(nil), data.Workloads...),
		Notes:      append([]string(nil), data.Notes...),
	}, nil
}

func randomWeights(rng *rand.Rand, minWeight, maxWeight float64) evaluator.WeightSet {
	if minWeight == maxWeight {
		return evaluator.WeightSet{
			FreeCPU:       minWeight,
			FreeMem:       minWeight,
			Balance:       minWeight,
			Consolidation: minWeight,
		}
	}

	sample := func() float64 {
		return minWeight + rng.Float64()*(maxWeight-minWeight)
	}
	return evaluator.WeightSet{
		FreeCPU:       sample(),
		FreeMem:       sample(),
		Balance:       sample(),
		Consolidation: sample(),
	}
}

func crossover(a, b evaluator.WeightSet, rng *rand.Rand) evaluator.WeightSet {
	pick := func(x, y float64) float64 {
		if rng.Intn(2) == 0 {
			return x
		}
		return y
	}
	return evaluator.WeightSet{
		FreeCPU:       pick(a.FreeCPU, b.FreeCPU),
		FreeMem:       pick(a.FreeMem, b.FreeMem),
		Balance:       pick(a.Balance, b.Balance),
		Consolidation: pick(a.Consolidation, b.Consolidation),
	}
}

func mutate(weights evaluator.WeightSet, rng *rand.Rand, rate, sigma, minWeight, maxWeight float64) evaluator.WeightSet {
	out := weights
	if rng.Float64() < rate {
		out.FreeCPU = clampWeight(out.FreeCPU+rng.NormFloat64()*sigma, minWeight, maxWeight)
	}
	if rng.Float64() < rate {
		out.FreeMem = clampWeight(out.FreeMem+rng.NormFloat64()*sigma, minWeight, maxWeight)
	}
	if rng.Float64() < rate {
		out.Balance = clampWeight(out.Balance+rng.NormFloat64()*sigma, minWeight, maxWeight)
	}
	if rng.Float64() < rate {
		out.Consolidation = clampWeight(out.Consolidation+rng.NormFloat64()*sigma, minWeight, maxWeight)
	}
	return out
}

func canonicalizeOffspring(weights, parentA, parentB evaluator.WeightSet) (evaluator.WeightSet, error) {
	normalized, err := policy.Normalize(weights)
	if err == nil {
		return normalized, nil
	}

	fallback, fallbackErr := policy.Normalize(evaluator.WeightSet{
		FreeCPU:       parentA.FreeCPU + parentB.FreeCPU,
		FreeMem:       parentA.FreeMem + parentB.FreeMem,
		Balance:       parentA.Balance + parentB.Balance,
		Consolidation: parentA.Consolidation + parentB.Consolidation,
	})
	if fallbackErr != nil {
		return evaluator.WeightSet{}, fmt.Errorf("repair offspring from parents: %w", fallbackErr)
	}
	return fallback, nil
}

func clampWeight(value, minWeight, maxWeight float64) float64 {
	if value < minWeight {
		return minWeight
	}
	if value > maxWeight {
		return maxWeight
	}
	return value
}

func dominates(a, b Candidate) bool {
	betterOrEqualP95 := a.Objectives.AverageP95SchedulingDecisionLatency <= b.Objectives.AverageP95SchedulingDecisionLatency
	betterOrEqualNodes := a.Objectives.AverageMaxWorkloadOccupiedWorkerNodes <= b.Objectives.AverageMaxWorkloadOccupiedWorkerNodes
	strictlyBetterP95 := a.Objectives.AverageP95SchedulingDecisionLatency < b.Objectives.AverageP95SchedulingDecisionLatency
	strictlyBetterNodes := a.Objectives.AverageMaxWorkloadOccupiedWorkerNodes < b.Objectives.AverageMaxWorkloadOccupiedWorkerNodes
	return betterOrEqualP95 && betterOrEqualNodes && (strictlyBetterP95 || strictlyBetterNodes)
}

func assignRanksAndCrowding(candidates []Candidate) []Candidate {
	out := append([]Candidate(nil), candidates...)
	fronts := nonDominatedFronts(out)
	for i, front := range fronts {
		for _, idx := range front {
			out[idx].Rank = i + 1
			out[idx].CrowdingDistance = 0
		}
		assignCrowding(out, front)
	}
	return out
}

func nonDominatedFronts(candidates []Candidate) [][]int {
	n := len(candidates)
	dominationCounts := make([]int, n)
	dominated := make([][]int, n)
	fronts := make([][]int, 0)
	firstFront := make([]int, 0)

	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			switch {
			case dominates(candidates[i], candidates[j]):
				dominated[i] = append(dominated[i], j)
			case dominates(candidates[j], candidates[i]):
				dominationCounts[i]++
			}
		}
		if dominationCounts[i] == 0 {
			firstFront = append(firstFront, i)
		}
	}
	fronts = append(fronts, firstFront)

	for frontIndex := 0; frontIndex < len(fronts); frontIndex++ {
		nextFront := make([]int, 0)
		for _, idx := range fronts[frontIndex] {
			for _, dominatedIdx := range dominated[idx] {
				dominationCounts[dominatedIdx]--
				if dominationCounts[dominatedIdx] == 0 {
					nextFront = append(nextFront, dominatedIdx)
				}
			}
		}
		if len(nextFront) > 0 {
			fronts = append(fronts, nextFront)
		}
	}

	return fronts
}

func assignCrowding(candidates []Candidate, front []int) {
	if len(front) == 0 {
		return
	}
	if len(front) == 1 {
		candidates[front[0]].CrowdingDistance = crowdingInfinity
		return
	}

	for _, idx := range front {
		candidates[idx].CrowdingDistance = 0
	}

	objectiveFns := []func(Candidate) float64{
		func(c Candidate) float64 { return c.Objectives.AverageP95SchedulingDecisionLatency },
		func(c Candidate) float64 { return c.Objectives.AverageMaxWorkloadOccupiedWorkerNodes },
	}

	for _, objective := range objectiveFns {
		sort.Slice(front, func(i, j int) bool {
			return objective(candidates[front[i]]) < objective(candidates[front[j]])
		})

		candidates[front[0]].CrowdingDistance = crowdingInfinity
		candidates[front[len(front)-1]].CrowdingDistance = crowdingInfinity

		minValue := objective(candidates[front[0]])
		maxValue := objective(candidates[front[len(front)-1]])
		if maxValue == minValue {
			continue
		}

		for i := 1; i < len(front)-1; i++ {
			prev := objective(candidates[front[i-1]])
			next := objective(candidates[front[i+1]])
			candidates[front[i]].CrowdingDistance += (next - prev) / (maxValue - minValue)
		}
	}
}

func selectNextPopulation(candidates []Candidate, populationSize int) []Candidate {
	ranked := assignRanksAndCrowding(candidates)
	fronts := nonDominatedFronts(ranked)
	selected := make([]Candidate, 0, populationSize)

	for _, front := range fronts {
		if len(selected)+len(front) <= populationSize {
			for _, idx := range front {
				selected = append(selected, ranked[idx])
			}
			continue
		}

		frontCandidates := make([]Candidate, 0, len(front))
		for _, idx := range front {
			frontCandidates = append(frontCandidates, ranked[idx])
		}
		sort.Slice(frontCandidates, func(i, j int) bool {
			if frontCandidates[i].CrowdingDistance == frontCandidates[j].CrowdingDistance {
				return frontCandidates[i].Name < frontCandidates[j].Name
			}
			return frontCandidates[i].CrowdingDistance > frontCandidates[j].CrowdingDistance
		})

		remaining := populationSize - len(selected)
		selected = append(selected, frontCandidates[:remaining]...)
		break
	}

	return assignRanksAndCrowding(selected)
}

func tournamentSelect(population []Candidate, rng *rand.Rand) Candidate {
	a := population[rng.Intn(len(population))]
	b := population[rng.Intn(len(population))]

	switch {
	case a.Rank < b.Rank:
		return a
	case b.Rank < a.Rank:
		return b
	case a.CrowdingDistance > b.CrowdingDistance:
		return a
	case b.CrowdingDistance > a.CrowdingDistance:
		return b
	default:
		if rng.Intn(2) == 0 {
			return a
		}
		return b
	}
}

func paretoFront(candidates []Candidate) []Candidate {
	ranked := assignRanksAndCrowding(candidates)
	front := make([]Candidate, 0)
	for _, candidate := range ranked {
		if candidate.Rank == 1 {
			front = append(front, candidate)
		}
	}
	sort.Slice(front, func(i, j int) bool {
		if front[i].Objectives.AverageP95SchedulingDecisionLatency == front[j].Objectives.AverageP95SchedulingDecisionLatency {
			return front[i].Objectives.AverageMaxWorkloadOccupiedWorkerNodes < front[j].Objectives.AverageMaxWorkloadOccupiedWorkerNodes
		}
		return front[i].Objectives.AverageP95SchedulingDecisionLatency < front[j].Objectives.AverageP95SchedulingDecisionLatency
	})
	return front
}
