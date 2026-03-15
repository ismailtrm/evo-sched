package optimizer

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

const crowdingInfinity = 1e9

func Run(cfg Config, baselines []evaluator.ExperimentRunSpec, evaluate EvaluateFunc) (Result, error) {
	if err := validateConfig(cfg); err != nil {
		return Result{}, err
	}

	rng := rand.New(rand.NewSource(cfg.Seed))
	result := Result{
		StartedAt: time.Now().UTC(),
		Config:    cfg,
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
	if cfg.MutationRate < 0 || cfg.MutationRate > 1 {
		return fmt.Errorf("mutation rate must be between 0 and 1")
	}
	if cfg.MutationSigma < 0 {
		return fmt.Errorf("mutation sigma must be non-negative")
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
	data, err := evaluate(weights)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		ID:         fmt.Sprintf("%s-%s", source, name),
		Name:       name,
		Source:     source,
		Generation: generation,
		Weights:    weights,
		Objectives: data.Objectives,
		Workloads:  append([]WorkloadMetrics(nil), data.Workloads...),
		Notes:      append([]string(nil), data.Notes...),
	}, nil
}

func randomWeights(rng *rand.Rand, minWeight, maxWeight float64) evaluator.WeightSet {
	if minWeight == maxWeight {
		return evaluator.WeightSet{
			FreeCPU:    minWeight,
			FreeMem:    minWeight,
			PodPenalty: minWeight,
			Packing:    minWeight,
		}
	}

	sample := func() float64 {
		return minWeight + rng.Float64()*(maxWeight-minWeight)
	}
	return evaluator.WeightSet{
		FreeCPU:    sample(),
		FreeMem:    sample(),
		PodPenalty: sample(),
		Packing:    sample(),
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
		FreeCPU:    pick(a.FreeCPU, b.FreeCPU),
		FreeMem:    pick(a.FreeMem, b.FreeMem),
		PodPenalty: pick(a.PodPenalty, b.PodPenalty),
		Packing:    pick(a.Packing, b.Packing),
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
		out.PodPenalty = clampWeight(out.PodPenalty+rng.NormFloat64()*sigma, minWeight, maxWeight)
	}
	if rng.Float64() < rate {
		out.Packing = clampWeight(out.Packing+rng.NormFloat64()*sigma, minWeight, maxWeight)
	}
	return out
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
	betterOrEqualP95 := a.Objectives.AverageP95Pending <= b.Objectives.AverageP95Pending
	betterOrEqualNodes := a.Objectives.AverageMaxActiveNodes <= b.Objectives.AverageMaxActiveNodes
	strictlyBetterP95 := a.Objectives.AverageP95Pending < b.Objectives.AverageP95Pending
	strictlyBetterNodes := a.Objectives.AverageMaxActiveNodes < b.Objectives.AverageMaxActiveNodes
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
		func(c Candidate) float64 { return c.Objectives.AverageP95Pending },
		func(c Candidate) float64 { return c.Objectives.AverageMaxActiveNodes },
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
		if front[i].Objectives.AverageP95Pending == front[j].Objectives.AverageP95Pending {
			return front[i].Objectives.AverageMaxActiveNodes < front[j].Objectives.AverageMaxActiveNodes
		}
		return front[i].Objectives.AverageP95Pending < front[j].Objectives.AverageP95Pending
	})
	return front
}
