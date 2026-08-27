package evaluator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"evok8s-scheduler/pkg/policy"
)

func LoadExperiment(path string) (ExperimentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ExperimentSpec{}, fmt.Errorf("read experiment: %w", err)
	}

	var spec ExperimentSpec
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return ExperimentSpec{}, fmt.Errorf("parse experiment: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ExperimentSpec{}, fmt.Errorf("parse experiment: unexpected trailing JSON content")
	}
	if spec.PolicyVersion != PolicyVersion {
		return ExperimentSpec{}, fmt.Errorf("experiment policyVersion must be %q, got %q", PolicyVersion, spec.PolicyVersion)
	}
	if len(spec.Runs) == 0 {
		return ExperimentSpec{}, fmt.Errorf("experiment has no runs")
	}

	seen := make(map[string]struct{}, len(spec.Runs))
	for i := range spec.Runs {
		run := &spec.Runs[i]
		if run.Name == "" {
			return ExperimentSpec{}, fmt.Errorf("experiment run %d has empty name", i)
		}
		if _, ok := seen[run.Name]; ok {
			return ExperimentSpec{}, fmt.Errorf("duplicate experiment run name %q", run.Name)
		}
		seen[run.Name] = struct{}{}

		normalized, err := policy.Normalize(run.Weights)
		if err != nil {
			return ExperimentSpec{}, fmt.Errorf("experiment run %q weights: %w", run.Name, err)
		}
		run.Weights = normalized
	}

	return spec, nil
}

func ComparisonRowFromRun(run RunResult) ComparisonRow {
	return ComparisonRow{
		Name:                               run.Name,
		Weights:                            run.Weights,
		PodCount:                           run.Summary.PodCount,
		ScheduledCount:                     run.Summary.ScheduledCount,
		CompletedCount:                     run.Summary.CompletedCount,
		UnschedulableCount:                 run.Summary.UnschedulableCount,
		DecisionTimestampCount:             run.Summary.DecisionTimestampCount,
		KubernetesScheduledTimestampCount:  run.Summary.KubernetesScheduledTimestampCount,
		EvaluatorObservationTimestampCount: run.Summary.EvaluatorObservationTimestampCount,
		AverageSchedulingDecisionLatency:   run.Summary.AverageSchedulingDecisionLatency,
		P95SchedulingDecisionLatency:       run.Summary.P95SchedulingDecisionLatency,
		AverageKubernetesPending:           run.Summary.AverageKubernetesPending,
		P95KubernetesPending:               run.Summary.P95KubernetesPending,
		AverageEvaluatorObservationLag:     run.Summary.AverageEvaluatorObservationLag,
		P95EvaluatorObservationLag:         run.Summary.P95EvaluatorObservationLag,
		MaxWorkloadOccupiedWorkerNodes:     run.Summary.MaxWorkloadOccupiedWorkerNodes,
		ResourceSummary:                    run.Summary.ResourceSummary,
		WorkloadSamplingProfile:            run.WorkloadSamplingProfile,
		Notes:                              append([]string(nil), run.Notes...),
	}
}
