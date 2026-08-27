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

	"evok8s-scheduler/pkg/evaluator"
)

type options struct {
	workloadPath        string
	schedulerMode       string
	namespace           string
	schedulerNamespace  string
	schedulerDeployment string
	schedulerName       string
	image               string
	outputPath          string
	csvOutputPath       string
	weights             string
	weightsFile         string
	runName             string
	repetitions         int
	pollInterval        time.Duration
	timeout             time.Duration
	cleanup             bool
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.workloadPath, "workload", "", "Path to workload JSON file")
	flag.StringVar(&opts.schedulerMode, "scheduler-mode", string(evaluator.SchedulerModeCustomPolicy), "Scheduler execution mode: custom-policy or external-reference")
	flag.StringVar(&opts.namespace, "namespace", "evo-sched-eval", "Namespace for evaluator-created pods")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", "evo-sched", "Namespace of the scheduler deployment")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", "evok8s-scheduler", "Name of the scheduler deployment")
	flag.StringVar(&opts.schedulerName, "scheduler-name", "evok8s-scheduler", "schedulerName set on generated pods")
	flag.StringVar(&opts.image, "image", "busybox:1.36.1", "Container image for evaluator-created pods")
	flag.StringVar(&opts.outputPath, "output", "", "Optional output JSON path")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Optional CSV output path; defaults next to --output")
	flag.StringVar(&opts.weights, "weights", "", "Optional single run weights in FreeCPU,FreeMem,Balance,Consolidation order")
	flag.StringVar(&opts.weightsFile, "weights-file", "", "Optional JSON file describing named experiment runs")
	flag.StringVar(&opts.runName, "run-name", "", "Run name for external-reference mode; defaults to scheduler name")
	flag.IntVar(&opts.repetitions, "repetitions", 1, "Number of repeated runs; policy batches use round-robin repetition order")
	flag.DurationVar(&opts.pollInterval, "poll-interval", time.Second, "Polling interval for pod state collection")
	flag.DurationVar(&opts.timeout, "timeout", 0, "Per-run timeout; defaults from workload length")
	flag.BoolVar(&opts.cleanup, "cleanup", true, "Delete evaluator pods after each run")
	flag.Parse()
	return opts
}

func run(opts options) error {
	if opts.workloadPath == "" {
		return fmt.Errorf("--workload is required")
	}
	mode, err := parseSchedulerMode(opts.schedulerMode)
	if err != nil {
		return err
	}
	if opts.weights != "" && opts.weightsFile != "" {
		return fmt.Errorf("--weights and --weights-file cannot be used together")
	}
	if mode == evaluator.SchedulerModeExternalReference && (opts.weights != "" || opts.weightsFile != "") {
		return fmt.Errorf("--weights and --weights-file are not valid in external-reference mode")
	}
	if opts.repetitions < 1 {
		return fmt.Errorf("--repetitions must be at least 1")
	}

	clientset, err := evaluator.NewClientset()
	if err != nil {
		return fmt.Errorf("client init failed: %w", err)
	}

	runnerOpts := evaluator.DefaultRunnerOptions()
	runnerOpts.SchedulerMode = mode
	runnerOpts.Namespace = opts.namespace
	runnerOpts.SchedulerNamespace = opts.schedulerNamespace
	runnerOpts.SchedulerDeployment = opts.schedulerDeployment
	runnerOpts.SchedulerName = opts.schedulerName
	runnerOpts.Image = opts.image
	runnerOpts.PollInterval = opts.pollInterval
	runnerOpts.Timeout = opts.timeout
	runnerOpts.Cleanup = opts.cleanup

	var batch evaluator.BatchResult
	var runErr error
	if mode == evaluator.SchedulerModeExternalReference {
		runName := opts.runName
		if runName == "" {
			runName = opts.schedulerName
		}
		runNames, err := repeatRunNames(runName, opts.repetitions)
		if err != nil {
			return err
		}
		for i, name := range runNames {
			next, err := evaluator.RunExternalReference(context.Background(), clientset, runnerOpts, opts.workloadPath, name)
			if i == 0 {
				batch = next
			} else {
				batch, runErr = mergeBatchResults(batch, next)
				if runErr != nil {
					break
				}
			}
			if err != nil {
				runErr = err
				break
			}
		}
	} else {
		currentWeights, err := evaluator.CurrentDeploymentWeights(context.Background(), clientset, opts.schedulerNamespace, opts.schedulerDeployment)
		if err != nil {
			return err
		}
		runSpecs, err := loadRunSpecs(opts, currentWeights)
		if err != nil {
			return err
		}
		runSpecs, err = expandRunSpecs(runSpecs, opts.repetitions)
		if err != nil {
			return err
		}
		batch, runErr = evaluator.RunBatch(context.Background(), clientset, runnerOpts, opts.workloadPath, runSpecs)
	}
	if err := emitOutputs(batch, opts); err != nil {
		return err
	}
	return runErr
}

func expandRunSpecs(specs []evaluator.ExperimentRunSpec, repetitions int) ([]evaluator.ExperimentRunSpec, error) {
	if repetitions < 1 {
		return nil, fmt.Errorf("repetitions must be at least 1")
	}
	if repetitions == 1 {
		return append([]evaluator.ExperimentRunSpec(nil), specs...), nil
	}
	expanded := make([]evaluator.ExperimentRunSpec, 0, len(specs)*repetitions)
	for repetition := 1; repetition <= repetitions; repetition++ {
		for _, spec := range specs {
			spec.Name = fmt.Sprintf("%s-r%02d", spec.Name, repetition)
			expanded = append(expanded, spec)
		}
	}
	return expanded, nil
}

func repeatRunNames(base string, repetitions int) ([]string, error) {
	if repetitions < 1 {
		return nil, fmt.Errorf("repetitions must be at least 1")
	}
	if repetitions == 1 {
		return []string{base}, nil
	}
	names := make([]string, repetitions)
	for repetition := 1; repetition <= repetitions; repetition++ {
		names[repetition-1] = fmt.Sprintf("%s-r%02d", base, repetition)
	}
	return names, nil
}

func mergeBatchResults(base, next evaluator.BatchResult) (evaluator.BatchResult, error) {
	if base.PolicyVersion != next.PolicyVersion ||
		base.SchedulerMode != next.SchedulerMode ||
		base.MeasurementVersion != next.MeasurementVersion ||
		base.Workload != next.Workload ||
		base.WorkloadPath != next.WorkloadPath {
		return base, fmt.Errorf("cannot merge incompatible evaluator batches")
	}
	base.Results = append(base.Results, next.Results...)
	base.Runs = append(base.Runs, next.Runs...)
	return base, nil
}

func parseSchedulerMode(raw string) (evaluator.SchedulerMode, error) {
	mode := evaluator.SchedulerMode(raw)
	if mode != evaluator.SchedulerModeCustomPolicy && mode != evaluator.SchedulerModeExternalReference {
		return "", fmt.Errorf("unsupported --scheduler-mode %q", raw)
	}
	return mode, nil
}

func loadRunSpecs(opts options, current evaluator.WeightSet) ([]evaluator.ExperimentRunSpec, error) {
	if opts.weightsFile != "" {
		spec, err := evaluator.LoadExperiment(opts.weightsFile)
		if err != nil {
			return nil, err
		}
		return spec.Runs, nil
	}

	name := "current-deployment"
	weights := current
	if opts.weights != "" {
		name = "cli-weights"
		var err error
		weights, err = evaluator.ParseWeightSet(opts.weights)
		if err != nil {
			return nil, err
		}
	}

	return []evaluator.ExperimentRunSpec{
		{Name: name, Weights: weights},
	}, nil
}

func emitOutputs(batch evaluator.BatchResult, opts options) error {
	if err := emitJSON(batch, opts.outputPath); err != nil {
		return err
	}

	csvPath := deriveCSVOutputPath(opts)
	if csvPath == "" {
		return nil
	}
	if err := evaluator.WriteComparisonCSV(csvPath, batch.MeasurementProtocol, batch.Results); err != nil {
		return err
	}
	log.Printf("wrote evaluator csv to %s", csvPath)
	return nil
}

func emitJSON(batch evaluator.BatchResult, outputPath string) error {
	data, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if outputPath == "" {
		fmt.Println(string(data))
		return nil
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	log.Printf("wrote evaluator result to %s", outputPath)
	return nil
}

func deriveCSVOutputPath(opts options) string {
	if opts.csvOutputPath != "" {
		return opts.csvOutputPath
	}
	if opts.outputPath == "" {
		return ""
	}
	ext := filepath.Ext(opts.outputPath)
	if ext == "" {
		return opts.outputPath + ".csv"
	}
	return strings.TrimSuffix(opts.outputPath, ext) + ".csv"
}
