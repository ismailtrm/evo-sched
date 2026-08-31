package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

type options struct {
	workloads                     string
	baselineFile                  string
	namespace                     string
	schedulerNamespace            string
	schedulerDeployment           string
	schedulerName                 string
	image                         string
	outputPath                    string
	csvOutputPath                 string
	allCSVOutputPath              string
	samplesCSVOutputPath          string
	pollInterval                  time.Duration
	timeout                       time.Duration
	cleanup                       bool
	populationSize                int
	generations                   int
	mutationRate                  float64
	mutationSigma                 float64
	minWeight                     float64
	maxWeight                     float64
	seed                          int64
	searchMode                    string
	offspringPerGeneration        int
	promotionsPerGeneration       int
	additionalSamplesPerPromotion int
	optimizerSampleBudget         int
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
	flag.StringVar(&opts.samplesCSVOutputPath, "samples-csv-output", "", "Optional fresh evaluation samples CSV output path")
	flag.DurationVar(&opts.pollInterval, "poll-interval", time.Second, "Polling interval for pod state collection")
	flag.DurationVar(&opts.timeout, "timeout", 0, "Per-workload timeout; defaults from workload length")
	flag.BoolVar(&opts.cleanup, "cleanup", true, "Delete evaluator pods after each run")
	flag.IntVar(&opts.populationSize, "population", 12, "Population size")
	flag.IntVar(&opts.generations, "generations", 5, "Number of generations")
	flag.Float64Var(&opts.mutationRate, "mutation-rate", 0.8, "Per-gene mutation probability")
	flag.Float64Var(&opts.mutationSigma, "mutation-sigma", 0.25, "Gaussian mutation sigma")
	flag.Float64Var(&opts.minWeight, "min-weight", 0.0, "Lower raw-gene clamp before L1 normalization")
	flag.Float64Var(&opts.maxWeight, "max-weight", 2.0, "Upper raw-gene clamp before L1 normalization")
	flag.Int64Var(&opts.seed, "seed", defaultSeed, "Random seed")
	flag.StringVar(&opts.searchMode, "search-mode", "", "Optional V3 search mode: budget-one-shot or budget-two-stage")
	flag.IntVar(&opts.offspringPerGeneration, "offspring-per-generation", 0, "Fresh offspring candidates per generation in a budgeted search")
	flag.IntVar(&opts.promotionsPerGeneration, "promotions-per-generation", 0, "Candidates promoted for resampling per generation")
	flag.IntVar(&opts.additionalSamplesPerPromotion, "additional-samples-per-promotion", 0, "Additional fresh samples collected per promoted candidate")
	flag.IntVar(&opts.optimizerSampleBudget, "optimizer-sample-budget", 0, "Exact fresh optimizer sample budget")
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
	schedulerInterval, err := evaluator.CurrentDeploymentSchedulerInterval(
		context.Background(),
		clientset,
		opts.schedulerNamespace,
		opts.schedulerDeployment,
	)
	if err != nil {
		return err
	}
	measurementProtocol := evaluator.MeasurementProtocolFor(runnerOpts, schedulerInterval)

	cfg := optimizer.Config{
		PopulationSize:                opts.populationSize,
		Generations:                   opts.generations,
		MutationRate:                  opts.mutationRate,
		MutationSigma:                 opts.mutationSigma,
		MinWeight:                     opts.minWeight,
		MaxWeight:                     opts.maxWeight,
		Seed:                          opts.seed,
		Workloads:                     append([]string(nil), workloadPaths...),
		SearchMode:                    optimizer.SearchMode(opts.searchMode),
		OffspringPerGeneration:        opts.offspringPerGeneration,
		PromotionsPerGeneration:       opts.promotionsPerGeneration,
		AdditionalSamplesPerPromotion: opts.additionalSamplesPerPromotion,
		OptimizerSampleBudget:         opts.optimizerSampleBudget,
	}

	clusterEval := newClusterEvaluator(clientset, runnerOpts, workloadPaths)
	var result optimizer.Result
	var runErr error
	if cfg.SearchMode == "" {
		result, runErr = optimizer.Run(cfg, baselines, clusterEval.Evaluate)
	} else {
		result, runErr = optimizer.RunBudgeted(cfg, baselines, clusterEval.EvaluateFresh)
	}
	result.MeasurementProtocol = measurementProtocol
	if err := emitOutputs(result, opts); err != nil {
		return err
	}
	return runErr
}

type evaluateWorkloadsFunc func(
	context.Context,
	kubernetes.Interface,
	evaluator.RunnerOptions,
	[]string,
	evaluator.ExperimentRunSpec,
) ([]evaluator.BatchResult, error)

type clusterEvaluator struct {
	clientset         kubernetes.Interface
	runnerOpts        evaluator.RunnerOptions
	workloadPaths     []string
	cache             map[string]optimizer.EvaluationData
	evaluateWorkloads evaluateWorkloadsFunc
}

func newClusterEvaluator(clientset kubernetes.Interface, runnerOpts evaluator.RunnerOptions, workloadPaths []string) *clusterEvaluator {
	return &clusterEvaluator{
		clientset:         clientset,
		runnerOpts:        runnerOpts,
		workloadPaths:     append([]string(nil), workloadPaths...),
		cache:             map[string]optimizer.EvaluationData{},
		evaluateWorkloads: evaluator.EvaluateWorkloads,
	}
}

func (e *clusterEvaluator) Evaluate(weights evaluator.WeightSet) (optimizer.EvaluationData, error) {
	key := weightKey(weights)
	if cached, ok := e.cache[key]; ok {
		return cloneEvaluationData(cached), nil
	}

	data, err := e.evaluate(key, weights)
	if err != nil {
		return optimizer.EvaluationData{}, err
	}

	e.cache[key] = cloneEvaluationData(data)
	return cloneEvaluationData(data), nil
}

func (e *clusterEvaluator) EvaluateFresh(request optimizer.EvaluationRequest) (optimizer.EvaluationData, error) {
	return e.evaluate(request.EvaluationID, request.Weights)
}

func (e *clusterEvaluator) evaluate(name string, weights evaluator.WeightSet) (optimizer.EvaluationData, error) {
	spec := evaluator.ExperimentRunSpec{Name: name, Weights: weights}
	batches, err := e.evaluateWorkloads(context.Background(), e.clientset, e.runnerOpts, e.workloadPaths, spec)
	if err != nil {
		return optimizer.EvaluationData{}, err
	}
	return aggregateEvaluationBatches(batches)
}

func aggregateEvaluationBatches(batches []evaluator.BatchResult) (optimizer.EvaluationData, error) {
	if len(batches) == 0 {
		return optimizer.EvaluationData{}, fmt.Errorf("evaluation returned no workload batches")
	}

	protocol := batches[0].MeasurementProtocol
	if protocol.SchedulerIntervalSeconds <= 0 || protocol.PodPollIntervalSeconds <= 0 {
		return optimizer.EvaluationData{}, fmt.Errorf(
			"measurement protocol has invalid intervals: scheduler=%v poll=%v",
			protocol.SchedulerIntervalSeconds,
			protocol.PodPollIntervalSeconds,
		)
	}
	workloads := make([]optimizer.WorkloadMetrics, 0, len(batches))
	var totalP95DecisionLatency float64
	var totalMaxOccupiedWorkers float64
	notes := make([]string, 0)
	for _, batch := range batches {
		if batch.MeasurementVersion != evaluator.MeasurementVersion || batch.MeasurementProtocol.Version != evaluator.MeasurementVersion {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s measurement contract mismatch: result=%q protocol=%q want=%q",
				batch.Workload,
				batch.MeasurementVersion,
				batch.MeasurementProtocol.Version,
				evaluator.MeasurementVersion,
			)
		}
		if batch.MeasurementProtocol != protocol {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s uses a different measurement protocol", batch.Workload)
		}
		if batch.MeasurementProtocol.WorkloadResourceMetricSource == "" {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s has no workload resource metric source", batch.Workload)
		}
		if batch.WorkloadSamplingProfile.MinimumDurationToPollIntervalRatio < evaluator.MinimumDurationToPollIntervalRatio {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s is undersampled: minimum duration to poll interval ratio=%0.3f, require at least %0.1f",
				batch.Workload,
				batch.WorkloadSamplingProfile.MinimumDurationToPollIntervalRatio,
				evaluator.MinimumDurationToPollIntervalRatio,
			)
		}
		if len(batch.Results) != 1 {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s returned %d results, want 1", batch.WorkloadPath, len(batch.Results))
		}
		row := batch.Results[0]
		if row.WorkloadSamplingProfile != batch.WorkloadSamplingProfile {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s sampling profile does not match its result row", batch.Workload)
		}
		if row.PodCount <= 0 {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s has no pods", batch.Workload)
		}
		if row.ScheduledCount+row.UnschedulableCount != row.PodCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s count mismatch: scheduled=%d unschedulable=%d pods=%d",
				batch.Workload,
				row.ScheduledCount,
				row.UnschedulableCount,
				row.PodCount,
			)
		}
		if row.UnschedulableCount != 0 || row.ScheduledCount != row.PodCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s is not fully schedulable: scheduled=%d unschedulable=%d pods=%d",
				batch.Workload,
				row.ScheduledCount,
				row.UnschedulableCount,
				row.PodCount,
			)
		}
		if row.CompletedCount != row.PodCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s is incomplete: completed=%d pods=%d",
				batch.Workload,
				row.CompletedCount,
				row.PodCount,
			)
		}
		if row.DecisionTimestampCount != row.ScheduledCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s has %d scheduler decision timestamps for %d scheduled pods",
				batch.Workload,
				row.DecisionTimestampCount,
				row.ScheduledCount,
			)
		}
		if row.KubernetesScheduledTimestampCount != row.ScheduledCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s has %d Kubernetes scheduled timestamps for %d scheduled pods",
				batch.Workload,
				row.KubernetesScheduledTimestampCount,
				row.ScheduledCount,
			)
		}
		if row.EvaluatorObservationTimestampCount != row.ScheduledCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s has %d evaluator observations for %d scheduled pods",
				batch.Workload,
				row.EvaluatorObservationTimestampCount,
				row.ScheduledCount,
			)
		}
		if row.P95SchedulingDecisionLatency < 0 {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s has negative decision latency", batch.Workload)
		}
		if row.MaxWorkloadOccupiedWorkerNodes <= 0 {
			return optimizer.EvaluationData{}, fmt.Errorf("workload %s has no observed occupied worker nodes", batch.Workload)
		}
		if batch.WorkloadBase != nil && row.MaxWorkloadOccupiedWorkerNodes > batch.WorkloadBase.WorkerCount {
			return optimizer.EvaluationData{}, fmt.Errorf(
				"workload %s occupied worker count %d exceeds worker snapshot size %d",
				batch.Workload,
				row.MaxWorkloadOccupiedWorkerNodes,
				batch.WorkloadBase.WorkerCount,
			)
		}
		workloads = append(workloads, optimizer.WorkloadMetrics{
			Workload:                           batch.Workload,
			AverageSchedulingDecisionLatency:   row.AverageSchedulingDecisionLatency,
			P95SchedulingDecisionLatency:       row.P95SchedulingDecisionLatency,
			AverageKubernetesPending:           row.AverageKubernetesPending,
			P95KubernetesPending:               row.P95KubernetesPending,
			AverageEvaluatorObservationLag:     row.AverageEvaluatorObservationLag,
			P95EvaluatorObservationLag:         row.P95EvaluatorObservationLag,
			MaxWorkloadOccupiedWorkerNodes:     row.MaxWorkloadOccupiedWorkerNodes,
			PodCount:                           row.PodCount,
			ScheduledCount:                     row.ScheduledCount,
			CompletedCount:                     row.CompletedCount,
			UnschedulableCount:                 row.UnschedulableCount,
			DecisionTimestampCount:             row.DecisionTimestampCount,
			KubernetesScheduledTimestampCount:  row.KubernetesScheduledTimestampCount,
			EvaluatorObservationTimestampCount: row.EvaluatorObservationTimestampCount,
			ResourceSummary:                    row.ResourceSummary,
			WorkloadSamplingProfile:            batch.WorkloadSamplingProfile,
		})
		totalP95DecisionLatency += row.P95SchedulingDecisionLatency
		totalMaxOccupiedWorkers += float64(row.MaxWorkloadOccupiedWorkerNodes)
		if len(row.Notes) > 0 {
			notes = append(notes, fmt.Sprintf("%s: %s", batch.Workload, strings.Join(row.Notes, " | ")))
		}
	}

	data := optimizer.EvaluationData{
		Objectives: optimizer.Objectives{
			AverageP95SchedulingDecisionLatency:   totalP95DecisionLatency / float64(len(workloads)),
			AverageMaxWorkloadOccupiedWorkerNodes: totalMaxOccupiedWorkers / float64(len(workloads)),
		},
		Workloads: workloads,
		Notes:     notes,
	}
	return data, nil
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
	return fmt.Sprintf(
		"%016x|%016x|%016x|%016x",
		math.Float64bits(weights.FreeCPU),
		math.Float64bits(weights.FreeMem),
		math.Float64bits(weights.Balance),
		math.Float64bits(weights.Consolidation),
	)
}

func emitOutputs(result optimizer.Result, opts options) error {
	if err := emitJSON(result, opts.outputPath); err != nil {
		return err
	}

	paretoCSVPath := deriveParetoCSVOutputPath(opts)
	if paretoCSVPath != "" {
		if err := optimizer.WriteCandidatesCSV(paretoCSVPath, result.MeasurementProtocol, result.CombinedParetoFront); err != nil {
			return err
		}
		log.Printf("wrote optimizer pareto csv to %s", paretoCSVPath)
	}

	allCSVPath := deriveAllCSVOutputPath(opts)
	if allCSVPath != "" {
		allCandidates := make([]optimizer.Candidate, 0, len(result.Baselines)+len(result.Evaluated))
		allCandidates = append(allCandidates, result.Baselines...)
		allCandidates = append(allCandidates, result.Evaluated...)
		if err := optimizer.WriteCandidatesCSV(allCSVPath, result.MeasurementProtocol, allCandidates); err != nil {
			return err
		}
		log.Printf("wrote optimizer all-candidates csv to %s", allCSVPath)
	}

	samplesCSVPath := deriveSamplesCSVOutputPath(opts, result)
	if samplesCSVPath != "" {
		allCandidates := make([]optimizer.Candidate, 0, len(result.Baselines)+len(result.Evaluated))
		allCandidates = append(allCandidates, result.Baselines...)
		allCandidates = append(allCandidates, result.Evaluated...)
		if err := optimizer.WriteSamplesCSV(samplesCSVPath, result.MeasurementProtocol, result.Config, allCandidates); err != nil {
			return err
		}
		log.Printf("wrote optimizer fresh samples csv to %s", samplesCSVPath)
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

func deriveSamplesCSVOutputPath(opts options, result optimizer.Result) string {
	if opts.samplesCSVOutputPath != "" {
		return opts.samplesCSVOutputPath
	}
	if result.Budget == nil || opts.outputPath == "" {
		return ""
	}
	ext := filepath.Ext(opts.outputPath)
	base := opts.outputPath
	if ext != "" {
		base = strings.TrimSuffix(opts.outputPath, ext)
	}
	return base + "-samples.csv"
}
