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
	namespace           string
	schedulerNamespace  string
	schedulerDeployment string
	schedulerName       string
	image               string
	outputPath          string
	csvOutputPath       string
	weights             string
	weightsFile         string
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
	flag.StringVar(&opts.namespace, "namespace", "evo-sched-eval", "Namespace for evaluator-created pods")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", "evo-sched", "Namespace of the scheduler deployment")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", "evok8s-scheduler", "Name of the scheduler deployment")
	flag.StringVar(&opts.schedulerName, "scheduler-name", "evok8s-scheduler", "schedulerName set on generated pods")
	flag.StringVar(&opts.image, "image", "busybox:1.36.1", "Container image for evaluator-created pods")
	flag.StringVar(&opts.outputPath, "output", "", "Optional output JSON path")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Optional CSV output path; defaults next to --output")
	flag.StringVar(&opts.weights, "weights", "", "Optional single run weights in FreeCPU,FreeMem,Balance,Consolidation order")
	flag.StringVar(&opts.weightsFile, "weights-file", "", "Optional JSON file describing named experiment runs")
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
	if opts.weights != "" && opts.weightsFile != "" {
		return fmt.Errorf("--weights and --weights-file cannot be used together")
	}

	clientset, err := evaluator.NewClientset()
	if err != nil {
		return fmt.Errorf("client init failed: %w", err)
	}

	currentWeights, err := evaluator.CurrentDeploymentWeights(context.Background(), clientset, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return err
	}

	runSpecs, err := loadRunSpecs(opts, currentWeights)
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

	batch, runErr := evaluator.RunBatch(context.Background(), clientset, runnerOpts, opts.workloadPath, runSpecs)
	if err := emitOutputs(batch, opts); err != nil {
		return err
	}
	return runErr
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
	if err := evaluator.WriteComparisonCSV(csvPath, batch.Results); err != nil {
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
