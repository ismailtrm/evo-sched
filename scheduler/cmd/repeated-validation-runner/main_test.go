package main

import (
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

func TestExpandRunSpecsUsesRepetitionMajorRoundRobinOrder(t *testing.T) {
	specs := []evaluator.ExperimentRunSpec{{Name: "a"}, {Name: "b"}}
	got, err := expandRunSpecs(specs, 2)
	if err != nil {
		t.Fatalf("expandRunSpecs() error = %v", err)
	}
	want := []string{"a-r01", "b-r01", "a-r02", "b-r02"}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("run %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestValidateBatchRequiresCompleteRows(t *testing.T) {
	batch := evaluator.BatchResult{
		PolicyVersion:      evaluator.PolicyVersion,
		MeasurementVersion: evaluator.MeasurementVersion,
		Workload:           "cpu-tight-train",
		Results: []evaluator.ComparisonRow{
			{
				PodCount:                           24,
				ScheduledCount:                     24,
				CompletedCount:                     24,
				DecisionTimestampCount:             24,
				KubernetesScheduledTimestampCount:  24,
				EvaluatorObservationTimestampCount: 24,
			},
		},
	}
	if err := validateBatch(batch, 1); err != nil {
		t.Fatalf("validateBatch() error = %v", err)
	}

	batch.Results[0].CompletedCount = 23
	if err := validateBatch(batch, 1); err == nil {
		t.Fatal("validateBatch() error = nil, want incomplete row rejection")
	}
}

func TestParseSeedsAreNotPartOfRepeatedValidation(t *testing.T) {
	if defaultRepetitions < 10 {
		t.Fatalf("defaultRepetitions = %d, want at least 10", defaultRepetitions)
	}
}
