package main

import (
	"strings"
	"testing"
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

func TestExpandRunSpecsUsesRepetitionMajorRoundRobinOrder(t *testing.T) {
	specs := []evaluator.ExperimentRunSpec{{Name: "a"}, {Name: "b"}}
	got, err := expandRunSpecs(specs, 2, executionOrderFixed)
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

func TestExpandRunSpecsUsesCyclicRotationAcrossRepetitions(t *testing.T) {
	specs := []evaluator.ExperimentRunSpec{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got, err := expandRunSpecs(specs, 3, executionOrderCyclicRotation)
	if err != nil {
		t.Fatalf("expandRunSpecs() error = %v", err)
	}
	want := []string{"a-r01", "b-r01", "c-r01", "b-r02", "c-r02", "a-r02", "c-r03", "a-r03", "b-r03"}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("run %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestBuildInvocationRecordsEffectiveProtocolAndOrder(t *testing.T) {
	opts := options{
		runName: "v3-validation", workloads: "a.json,b.json", weightsFile: "weights.json",
		protocolTag: "protocol-v3-validation", protocolVersion: "v3-validation-official",
		outputRoot: "/tmp/results", provenanceFiles: "protocol.json", repetitions: 10,
		pollInterval: 250 * time.Millisecond, timeout: 90 * time.Second, expectedWorkers: 4,
		expectedSchedulerInterval: 250 * time.Millisecond, finalSchedulerInterval: 2 * time.Second,
		expectedWeights: "0.25,0.25,0.25,0.25", executionOrder: executionOrderCyclicRotation,
		schedulerNamespace: "evo-sched", schedulerDeployment: "evok8s-scheduler",
		evaluatorNamespace: "evo-sched-eval", schedulerName: "evok8s-scheduler", image: "busybox:1.36.1",
	}
	got := buildInvocation(opts)
	joined := strings.Join(got, " ")
	for _, expected := range []string{"go run ./cmd/repeated-validation-runner", "--protocol-version v3-validation-official", "--execution-order cyclic-rotation", "--repetitions 10"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("invocation %q does not contain %q", joined, expected)
		}
	}
}

func TestExpandRunSpecsRejectsUnknownExecutionOrder(t *testing.T) {
	_, err := expandRunSpecs([]evaluator.ExperimentRunSpec{{Name: "a"}}, 2, "random")
	if err == nil {
		t.Fatal("expandRunSpecs() error = nil, want unsupported order rejection")
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
