package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

type options struct {
	inputs        string
	outputPath    string
	csvOutputPath string
}

type seedResult struct {
	Path   string
	Result optimizer.Result
}

type summary struct {
	SchemaVersion string           `json:"schemaVersion"`
	GeneratedAt   time.Time        `json:"generatedAt"`
	Inputs        []string         `json:"inputs"`
	Seeds         []seedSummary    `json:"seeds"`
	Aggregate     aggregateSummary `json:"aggregate"`
	Notes         []string         `json:"notes"`
}

type seedSummary struct {
	Seed                           int64         `json:"seed"`
	DurationSeconds                float64       `json:"durationSeconds"`
	BaselineCount                  int           `json:"baselineCount"`
	OptimizerCandidateCount        int           `json:"optimizerCandidateCount"`
	TotalSolutionCount             int           `json:"totalSolutionCount"`
	WorkloadExecutionCount         int           `json:"workloadExecutionCount"`
	RawCombinedParetoCount         int           `json:"rawCombinedParetoCount"`
	UniqueCombinedParetoPointCount int           `json:"uniqueCombinedParetoPointCount"`
	BaselinesKept                  []string      `json:"baselinesKept"`
	BaselinesDropped               []string      `json:"baselinesDropped"`
	Points                         []paretoPoint `json:"points"`
	PendingBest                    paretoPoint   `json:"pendingBest"`
	NodeBest                       paretoPoint   `json:"nodeBest"`
	StrictInteriorKneeAvailable    bool          `json:"strictInteriorKneeAvailable"`
}

type paretoPoint struct {
	AverageP95SchedulingDecisionLatencySeconds float64        `json:"averageP95SchedulingDecisionLatencySeconds"`
	AverageMaxWorkloadOccupiedWorkerNodes      float64        `json:"averageMaxWorkloadOccupiedWorkerNodes"`
	NovelOptimizerPoint                        bool           `json:"novelOptimizerPoint"`
	RediscoveredBaselinePoint                  bool           `json:"rediscoveredBaselinePoint"`
	Members                                    []paretoMember `json:"members"`
}

type paretoMember struct {
	Seed       int64               `json:"seed"`
	Name       string              `json:"name"`
	Source     string              `json:"source"`
	Generation int                 `json:"generation"`
	Weights    evaluator.WeightSet `json:"weights"`
}

type aggregateSummary struct {
	SeedCount                    int            `json:"seedCount"`
	OptimizerCandidateCount      int            `json:"optimizerCandidateCount"`
	TotalSolutionCount           int            `json:"totalSolutionCount"`
	WorkloadExecutionCount       int            `json:"workloadExecutionCount"`
	RawCombinedParetoMemberCount int            `json:"rawCombinedParetoMemberCount"`
	ObservedParetoPointCount     int            `json:"observedParetoPointCountAcrossSeeds"`
	SeedsWithNovelOptimizerPoint int            `json:"seedsWithNovelOptimizerPoint"`
	BaselineSurvivalBySeed       map[string]int `json:"baselineSurvivalBySeed"`
	Finalists                    []paretoMember `json:"optimizerParetoFinalists"`
}

type objectiveKey struct {
	latencyBits uint64
	nodeBits    uint64
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.inputs, "inputs", "", "Comma-separated optimizer result.json paths")
	flag.StringVar(&opts.outputPath, "output", "", "Summary JSON output path")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Pareto member CSV output path")
	flag.Parse()
	return opts
}

func run(opts options) error {
	paths, err := splitInputs(opts.inputs)
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.outputPath) == "" {
		return fmt.Errorf("--output is required")
	}
	if strings.TrimSpace(opts.csvOutputPath) == "" {
		opts.csvOutputPath = strings.TrimSuffix(opts.outputPath, filepath.Ext(opts.outputPath)) + "-pareto.csv"
	}

	results := make([]seedResult, 0, len(paths))
	for _, path := range paths {
		result, err := readResult(path)
		if err != nil {
			return err
		}
		results = append(results, seedResult{Path: path, Result: result})
	}
	built, err := buildSummary(results)
	if err != nil {
		return err
	}
	if err := writeJSON(opts.outputPath, built); err != nil {
		return err
	}
	if err := writeCSV(opts.csvOutputPath, built); err != nil {
		return err
	}
	return nil
}

func readResult(path string) (optimizer.Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return optimizer.Result{}, fmt.Errorf("read %s: %w", path, err)
	}
	var result optimizer.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return optimizer.Result{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return result, nil
}

func splitInputs(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		if path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("--inputs is required")
	}
	return paths, nil
}

func buildSummary(results []seedResult) (summary, error) {
	if len(results) == 0 {
		return summary{}, errors.New("at least one optimizer result is required")
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Result.Config.Seed < results[j].Result.Config.Seed
	})

	built := summary{
		SchemaVersion: "v2-multi-seed-summary",
		GeneratedAt:   time.Now().UTC(),
		Seeds:         make([]seedSummary, 0, len(results)),
		Aggregate: aggregateSummary{
			SeedCount:              len(results),
			BaselineSurvivalBySeed: make(map[string]int),
		},
		Notes: []string{
			"Pareto points are deduplicated within each seed using exact objective values.",
			"Objective values from different seeds are retained separately because live-cluster timing noise makes exact cross-seed point equality an invalid stability criterion.",
			"Optimizer Pareto finalists are search outputs, not confirmed improvements; baselines and finalists require repeated validation under one shared protocol.",
		},
	}
	seenSeeds := make(map[int64]struct{}, len(results))
	observedPoints := make(map[objectiveKey]struct{})

	for _, input := range results {
		result := input.Result
		seed := result.Config.Seed
		if _, exists := seenSeeds[seed]; exists {
			return summary{}, fmt.Errorf("duplicate seed %d", seed)
		}
		seenSeeds[seed] = struct{}{}
		built.Inputs = append(built.Inputs, input.Path)

		seedSummary, err := summarizeSeed(result)
		if err != nil {
			return summary{}, fmt.Errorf("seed %d: %w", seed, err)
		}
		built.Seeds = append(built.Seeds, seedSummary)
		built.Aggregate.OptimizerCandidateCount += seedSummary.OptimizerCandidateCount
		built.Aggregate.TotalSolutionCount += seedSummary.TotalSolutionCount
		built.Aggregate.WorkloadExecutionCount += seedSummary.WorkloadExecutionCount
		built.Aggregate.RawCombinedParetoMemberCount += seedSummary.RawCombinedParetoCount
		for _, name := range seedSummary.BaselinesKept {
			built.Aggregate.BaselineSurvivalBySeed[name]++
		}
		hasNovel := false
		for _, point := range seedSummary.Points {
			observedPoints[keyForObjectives(point.AverageP95SchedulingDecisionLatencySeconds, point.AverageMaxWorkloadOccupiedWorkerNodes)] = struct{}{}
			hasNovel = hasNovel || point.NovelOptimizerPoint
			for _, member := range point.Members {
				if member.Source != "baseline" {
					built.Aggregate.Finalists = append(built.Aggregate.Finalists, member)
				}
			}
		}
		if hasNovel {
			built.Aggregate.SeedsWithNovelOptimizerPoint++
		}
	}
	built.Aggregate.ObservedParetoPointCount = len(observedPoints)
	return built, nil
}

func summarizeSeed(result optimizer.Result) (seedSummary, error) {
	if len(result.Config.Workloads) == 0 {
		return seedSummary{}, errors.New("result has no workloads")
	}
	if len(result.CombinedParetoFront) == 0 {
		return seedSummary{}, errors.New("combined Pareto front is empty")
	}

	baselinePoints := make(map[objectiveKey]struct{}, len(result.Baselines))
	baselineNames := make([]string, 0, len(result.Baselines))
	for _, baseline := range result.Baselines {
		baselinePoints[keyForCandidate(baseline)] = struct{}{}
		baselineNames = append(baselineNames, baseline.Name)
	}
	sort.Strings(baselineNames)

	groups := make(map[objectiveKey][]optimizer.Candidate)
	for _, candidate := range result.CombinedParetoFront {
		groups[keyForCandidate(candidate)] = append(groups[keyForCandidate(candidate)], candidate)
	}
	points := make([]paretoPoint, 0, len(groups))
	keptSet := make(map[string]struct{})
	for key, candidates := range groups {
		point := paretoPoint{
			AverageP95SchedulingDecisionLatencySeconds: math.Float64frombits(key.latencyBits),
			AverageMaxWorkloadOccupiedWorkerNodes:      math.Float64frombits(key.nodeBits),
			Members:                                    make([]paretoMember, 0, len(candidates)),
		}
		hasOptimizer := false
		for _, candidate := range candidates {
			member := paretoMember{
				Seed:       result.Config.Seed,
				Name:       candidate.Name,
				Source:     candidate.Source,
				Generation: candidate.Generation,
				Weights:    candidate.Weights,
			}
			point.Members = append(point.Members, member)
			if candidate.Source == "baseline" {
				keptSet[candidate.Name] = struct{}{}
			} else {
				hasOptimizer = true
			}
		}
		_, matchesBaseline := baselinePoints[key]
		point.NovelOptimizerPoint = hasOptimizer && !matchesBaseline
		point.RediscoveredBaselinePoint = hasOptimizer && matchesBaseline
		sort.Slice(point.Members, func(i, j int) bool {
			if point.Members[i].Source == point.Members[j].Source {
				return point.Members[i].Name < point.Members[j].Name
			}
			return point.Members[i].Source < point.Members[j].Source
		})
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].AverageP95SchedulingDecisionLatencySeconds == points[j].AverageP95SchedulingDecisionLatencySeconds {
			return points[i].AverageMaxWorkloadOccupiedWorkerNodes < points[j].AverageMaxWorkloadOccupiedWorkerNodes
		}
		return points[i].AverageP95SchedulingDecisionLatencySeconds < points[j].AverageP95SchedulingDecisionLatencySeconds
	})

	kept := make([]string, 0, len(keptSet))
	dropped := make([]string, 0, len(baselineNames))
	for _, name := range baselineNames {
		if _, exists := keptSet[name]; exists {
			kept = append(kept, name)
		} else {
			dropped = append(dropped, name)
		}
	}
	nodeBest := points[0]
	for _, point := range points[1:] {
		if point.AverageMaxWorkloadOccupiedWorkerNodes < nodeBest.AverageMaxWorkloadOccupiedWorkerNodes ||
			(point.AverageMaxWorkloadOccupiedWorkerNodes == nodeBest.AverageMaxWorkloadOccupiedWorkerNodes &&
				point.AverageP95SchedulingDecisionLatencySeconds < nodeBest.AverageP95SchedulingDecisionLatencySeconds) {
			nodeBest = point
		}
	}

	total := len(result.Baselines) + len(result.Evaluated)
	return seedSummary{
		Seed:                           result.Config.Seed,
		DurationSeconds:                result.FinishedAt.Sub(result.StartedAt).Seconds(),
		BaselineCount:                  len(result.Baselines),
		OptimizerCandidateCount:        len(result.Evaluated),
		TotalSolutionCount:             total,
		WorkloadExecutionCount:         total * len(result.Config.Workloads),
		RawCombinedParetoCount:         len(result.CombinedParetoFront),
		UniqueCombinedParetoPointCount: len(points),
		BaselinesKept:                  kept,
		BaselinesDropped:               dropped,
		Points:                         points,
		PendingBest:                    points[0],
		NodeBest:                       nodeBest,
		StrictInteriorKneeAvailable:    len(points) >= 3,
	}, nil
}

func keyForCandidate(candidate optimizer.Candidate) objectiveKey {
	return keyForObjectives(
		candidate.Objectives.AverageP95SchedulingDecisionLatency,
		candidate.Objectives.AverageMaxWorkloadOccupiedWorkerNodes,
	)
}

func keyForObjectives(latency, nodes float64) objectiveKey {
	return objectiveKey{latencyBits: math.Float64bits(latency), nodeBits: math.Float64bits(nodes)}
}

func writeJSON(path string, built summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create summary directory: %w", err)
	}
	data, err := json.MarshalIndent(built, "", "  ")
	if err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeCSV(path string, built summary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create CSV directory: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	writer := csv.NewWriter(file)
	header := []string{
		"seed", "name", "source", "generation",
		"free_cpu", "free_mem", "balance", "consolidation",
		"average_p95_scheduling_decision_latency_seconds",
		"average_max_workload_occupied_worker_nodes",
		"novel_optimizer_point", "rediscovered_baseline_point",
	}
	if err := writer.Write(header); err != nil {
		_ = file.Close()
		return fmt.Errorf("write CSV header: %w", err)
	}
	for _, seed := range built.Seeds {
		for _, point := range seed.Points {
			for _, member := range point.Members {
				record := []string{
					strconv.FormatInt(member.Seed, 10),
					member.Name,
					member.Source,
					strconv.Itoa(member.Generation),
					strconv.FormatFloat(member.Weights.FreeCPU, 'g', -1, 64),
					strconv.FormatFloat(member.Weights.FreeMem, 'g', -1, 64),
					strconv.FormatFloat(member.Weights.Balance, 'g', -1, 64),
					strconv.FormatFloat(member.Weights.Consolidation, 'g', -1, 64),
					strconv.FormatFloat(point.AverageP95SchedulingDecisionLatencySeconds, 'g', -1, 64),
					strconv.FormatFloat(point.AverageMaxWorkloadOccupiedWorkerNodes, 'g', -1, 64),
					strconv.FormatBool(point.NovelOptimizerPoint),
					strconv.FormatBool(point.RediscoveredBaselinePoint),
				}
				if err := writer.Write(record); err != nil {
					_ = file.Close()
					return fmt.Errorf("write CSV row: %w", err)
				}
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		return fmt.Errorf("flush CSV: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close CSV: %w", err)
	}
	return nil
}
