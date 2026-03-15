package evaluator

import (
	"encoding/json"
	"fmt"
	"os"
)

func LoadExperiment(path string) (ExperimentSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ExperimentSpec{}, fmt.Errorf("read experiment: %w", err)
	}

	var spec ExperimentSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return ExperimentSpec{}, fmt.Errorf("parse experiment: %w", err)
	}
	if len(spec.Runs) == 0 {
		return ExperimentSpec{}, fmt.Errorf("experiment has no runs")
	}

	seen := make(map[string]struct{}, len(spec.Runs))
	for i := range spec.Runs {
		run := spec.Runs[i]
		if run.Name == "" {
			return ExperimentSpec{}, fmt.Errorf("experiment run %d has empty name", i)
		}
		if _, ok := seen[run.Name]; ok {
			return ExperimentSpec{}, fmt.Errorf("duplicate experiment run name %q", run.Name)
		}
		seen[run.Name] = struct{}{}
	}

	return spec, nil
}

func ComparisonRowFromRun(run RunResult) ComparisonRow {
	return ComparisonRow{
		Name:               run.Name,
		Weights:            run.Weights,
		ScheduledCount:     run.Summary.ScheduledCount,
		CompletedCount:     run.Summary.CompletedCount,
		UnschedulableCount: run.Summary.UnschedulableCount,
		AveragePending:     run.Summary.AveragePending,
		P95Pending:         run.Summary.P95Pending,
		MaxActiveNodes:     run.Summary.MaxActiveNodes,
		Notes:              append([]string(nil), run.Notes...),
	}
}
