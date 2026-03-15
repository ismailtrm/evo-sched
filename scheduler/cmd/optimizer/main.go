package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

type options struct {
	workloads           string
	baselineFile        string
	namespace           string
	schedulerNamespace  string
	schedulerDeployment string
	schedulerName       string
	image               string
	outputPath          string
	csvOutputPath       string
	allCSVOutputPath    string
	pollInterval        time.Duration
	timeout             time.Duration
	cleanup             bool
	populationSize      int
	generations         int
	mutationRate        float64
	mutationSigma       float64
	minWeight           float64
	maxWeight           float64
	seed                int64
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() options {
	defaultSeed := time.Now().UnixNano()
	opts := options{}
	flag.StringVar(&opts.workloads, "workloads", "", "Comma-separated workload JSON paths")
	flag.StringVar(&opts.baselineFile, "baseline-file", "", "Optional baseline experiment JSON file")
	flag.StringVar(&opts.namespace, "namespace", "evo-sched-eval", "Namespace for evaluator-created pods")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", "evo-sched", "Namespace of the scheduler deployment")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", "evok8s-scheduler", "Name of the scheduler deployment")
	flag.StringVar(&opts.schedulerName, "scheduler-name", "evok8s-scheduler", "schedulerName set on generated pods")
	flag.StringVar(&opts.image, "image", "busybox:1.36.1", "Container image for evaluator-created pods")
	flag.StringVar(&opts.outputPath, "output", "", "Optional optimizer JSON output path")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Optional combined Pareto CSV output path")
	flag.StringVar(&opts.allCSVOutputPath, "all-csv-output", "", "Optional all-candidates CSV output path")
	flag.DurationVar(&opts.pollInterval, "poll-interval", time.Second, "Polling interval for pod state collection")
	flag.DurationVar(&opts.timeout, "timeout", 0, "Per-workload timeout; defaults from workload length")
	flag.BoolVar(&opts.cleanup, "cleanup", true, "Delete evaluator pods after each run")
	flag.IntVar(&opts.populationSize, "population", 12, "Population size")
	flag.IntVar(&opts.generations, "generations", 5, "Number of generations")
	flag.Float64Var(&opts.mutationRate, "mutation-rate", 0.8, "Per-gene mutation probability")
	flag.Float64Var(&opts.mutationSigma, "mutation-sigma", 0.25, "Gaussian mutation sigma")
	flag.Float64Var(&opts.minWeight, "min-weight", 0.0, "Lower clamp for weight values")
	flag.Float64Var(&opts.maxWeight, "max-weight", 2.0, "Upper clamp for weight values")
	flag.Int64Var(&opts.seed, "seed", defaultSeed, "Random seed")
	flag.Parse()
	return opts
}

func run(opts options) error {
	workloadPaths := splitPaths(opts.workloads)
	if len(workloadPaths) == 0 {
		return fmt.Errorf("--workloads is required")
	}

	clientset, err := evaluator.NewClientset()
	if err != nil {
		return fmt.Errorf("client init failed: %w", err)
	}

	baselines, err := loadBaselines(opts.baselineFile)
	if err != nil {
		return err
	}

	runnerOpts := evaluator.DefaultRunnerOptions()
	runnerOpts.Namespace = opts.namespace
	runnerOpts.SchedulerNamespace = opts.schedulerNamespace
	runnerOpts.SchedulerDeployment = opts.schedulerDeployment
	runnerOpts.SchedulerName = opts.schedulerName
	runnerOpts.Image = opts.image
	runnerOpts.PollInterval = opts.pollInterval
	runnerOpts.Timeout = opts.timeout
	runnerOpts.Cleanup = opts.cleanup

	cfg := optimizer.Config{
		PopulationSize: opts.populationSize,
		Generations:    opts.generations,
		MutationRate:   opts.mutationRate,
		MutationSigma:  opts.mutationSigma,
		MinWeight:      opts.minWeight,
		MaxWeight:      opts.maxWeight,
		Seed:           opts.seed,
		Workloads:      append([]string(nil), workloadPaths...),
	}

	clusterEval := newClusterEvaluator(clientset, runnerOpts, workloadPaths)
	result, runErr := optimizer.Run(cfg, baselines, clusterEval.Evaluate)
	if err := emitOutputs(result, opts); err != nil {
		return err
	}
	return runErr
}

type clusterEvaluator struct {
	clientset     kubernetes.Interface
	runnerOpts    evaluator.RunnerOptions
	workloadPaths []string
	cache         map[string]optimizer.EvaluationData
}

func newClusterEvaluator(clientset kubernetes.Interface, runnerOpts evaluator.RunnerOptions, workloadPaths []string) *clusterEvaluator {
	return &clusterEvaluator{
		clientset:     clientset,
		runnerOpts:    runnerOpts,
		workloadPaths: append([]string(nil), workloadPaths...),
		cache:         map[string]optimizer.EvaluationData{},
	}
}

func (e *clusterEvaluator) Evaluate(weights evaluator.WeightSet) (optimizer.EvaluationData, error) {
	key := weightKey(weights)
	if cached, ok := e.cache[key]; ok {
		return cloneEvaluationData(cached), nil
	}

	spec := evaluator.ExperimentRunSpec{Name: key, Weights: weights}
	batches, err := evaluator.EvaluateWorkloads(context.Background(), e.clientset, e.runnerOpts, e.workloadPaths, spec)
	if err != nil {
		return optimizer.EvaluationData{}, err
	}

	workloads := make([]optimizer.WorkloadMetrics, 0, len(batches))
	var totalP95 float64
	var totalMaxActive float64
	notes := make([]string, 0)
	for _, batch := range batches {
		if len(batch.Results) == 0 {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s returned no results", batch.WorkloadPath)
		}
		row := batch.Results[0]
		workloads = append(workloads, optimizer.WorkloadMetrics{
			Workload:           batch.Workload,
			AveragePending:     row.AveragePending,
			P95Pending:         row.P95Pending,
			MaxActiveNodes:     row.MaxActiveNodes,
			ScheduledCount:     row.ScheduledCount,
			CompletedCount:     row.CompletedCount,
			UnschedulableCount: row.UnschedulableCount,
		})
		totalP95 += row.P95Pending
		totalMaxActive += float64(row.MaxActiveNodes)
		if len(row.Notes) > 0 {
			notes = append(notes, fmt.Sprintf("%s: %s", batch.Workload, strings.Join(row.Notes, " | ")))
		}
	}

	data := optimizer.EvaluationData{
		Objectives: optimizer.Objectives{
			AverageP95Pending:     totalP95 / float64(len(workloads)),
			AverageMaxActiveNodes: totalMaxActive / float64(len(workloads)),
		},
		Workloads: workloads,
		Notes:     notes,
	}
	e.cache[key] = cloneEvaluationData(data)
	return cloneEvaluationData(data), nil
}

func cloneEvaluationData(data optimizer.EvaluationData) optimizer.EvaluationData {
	out := data
	out.Workloads = append([]optimizer.WorkloadMetrics(nil), data.Workloads...)
	out.Notes = append([]string(nil), data.Notes...)
	return out
}

func loadBaselines(path string) ([]evaluator.ExperimentRunSpec, error) {
	if path == "" {
		return nil, nil
	}
	spec, err := evaluator.LoadExperiment(path)
	if err != nil {
		return nil, err
	}
	return spec.Runs, nil
}

func splitPaths(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func weightKey(weights evaluator.WeightSet) string {
	return fmt.Sprintf("%.6f|%.6f|%.6f|%.6f", weights.FreeCPU, weights.FreeMem, weights.PodPenalty, weights.Packing)
}

func emitOutputs(result optimizer.Result, opts options) error {
	if err := emitJSON(result, opts.outputPath); err != nil {
		return err
	}

	paretoCSVPath := deriveParetoCSVOutputPath(opts)
	if paretoCSVPath != "" {
		if err := optimizer.WriteCandidatesCSV(paretoCSVPath, result.CombinedParetoFront); err != nil {
			return err
		}
		log.Printf("wrote optimizer pareto csv to %s", paretoCSVPath)
	}

	allCSVPath := deriveAllCSVOutputPath(opts)
	if allCSVPath != "" {
		allCandidates := make([]optimizer.Candidate, 0, len(result.Baselines)+len(result.Evaluated))
		allCandidates = append(allCandidates, result.Baselines...)
		allCandidates = append(allCandidates, result.Evaluated...)
		if err := optimizer.WriteCandidatesCSV(allCSVPath, allCandidates); err != nil {
			return err
		}
		log.Printf("wrote optimizer all-candidates csv to %s", allCSVPath)
	}
	return nil
}

func emitJSON(result optimizer.Result, outputPath string) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal optimizer result: %w", err)
	}
	if outputPath == "" {
		fmt.Println(string(data))
		return nil
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write optimizer result: %w", err)
	}
	log.Printf("wrote optimizer result to %s", outputPath)
	return nil
}

func deriveParetoCSVOutputPath(opts options) string {
	if opts.csvOutputPath != "" {
		return opts.csvOutputPath
	}
	if opts.outputPath == "" {
		return ""
	}
	ext := filepath.Ext(opts.outputPath)
	base := opts.outputPath
	if ext != "" {
		base = strings.TrimSuffix(opts.outputPath, ext)
	}
	return base + "-pareto.csv"
}

func deriveAllCSVOutputPath(opts options) string {
	if opts.allCSVOutputPath != "" {
		return opts.allCSVOutputPath
	}
	if opts.outputPath == "" {
		return ""
	}
	ext := filepath.Ext(opts.outputPath)
	base := opts.outputPath
	if ext != "" {
		base = strings.TrimSuffix(opts.outputPath, ext)
	}
	return base + "-all.csv"
}
