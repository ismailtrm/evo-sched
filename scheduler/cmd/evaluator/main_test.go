package main

import (
	"strings"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

func TestParseSchedulerMode(t *testing.T) {
	for _, want := range []evaluator.SchedulerMode{
		evaluator.SchedulerModeCustomPolicy,
		evaluator.SchedulerModeExternalReference,
	} {
		got, err := parseSchedulerMode(string(want))
		if err != nil {
			t.Fatalf("parseSchedulerMode(%q) error = %v", want, err)
		}
		if got != want {
			t.Fatalf("parseSchedulerMode(%q) = %q, want %q", want, got, want)
		}
	}
	if _, err := parseSchedulerMode("unknown"); err == nil {
		t.Fatal("parseSchedulerMode(unknown) error = nil")
	}
}

func TestRunRejectsWeightsForExternalReferenceBeforeClientInit(t *testing.T) {
	err := run(options{
		workloadPath:  "workload.json",
		schedulerMode: string(evaluator.SchedulerModeExternalReference),
		weights:       "1,1,1,1",
		repetitions:   1,
	})
	if err == nil || !strings.Contains(err.Error(), "not valid in external-reference mode") {
		t.Fatalf("run() error = %v, want external weights rejection", err)
	}
}

func TestExpandRunSpecsUsesRoundRobinRepetitionOrder(t *testing.T) {
	input := []evaluator.ExperimentRunSpec{
		{Name: "balanced", Weights: evaluator.WeightSet{FreeCPU: 1}},
		{Name: "spread", Weights: evaluator.WeightSet{FreeMem: 1}},
	}
	got, err := expandRunSpecs(input, 2)
	if err != nil {
		t.Fatalf("expandRunSpecs() error = %v", err)
	}
	wantNames := []string{"balanced-r01", "spread-r01", "balanced-r02", "spread-r02"}
	if len(got) != len(wantNames) {
		t.Fatalf("expanded count = %d, want %d", len(got), len(wantNames))
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Fatalf("expanded[%d].Name = %q, want %q", i, got[i].Name, want)
		}
	}
	if got[0].Weights != input[0].Weights || got[1].Weights != input[1].Weights {
		t.Fatalf("expanded weights changed: %+v", got)
	}

	single, err := expandRunSpecs(input, 1)
	if err != nil {
		t.Fatalf("expandRunSpecs(single) error = %v", err)
	}
	if single[0].Name != "balanced" || single[1].Name != "spread" {
		t.Fatalf("single repetition must preserve names: %+v", single)
	}
	if _, err := expandRunSpecs(input, 0); err == nil {
		t.Fatal("expandRunSpecs(repetitions=0) error = nil")
	}
}

func TestRepeatRunNames(t *testing.T) {
	got, err := repeatRunNames("kube-scheduler-default", 3)
	if err != nil {
		t.Fatalf("repeatRunNames() error = %v", err)
	}
	want := []string{
		"kube-scheduler-default-r01",
		"kube-scheduler-default-r02",
		"kube-scheduler-default-r03",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("repeatRunNames()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	single, err := repeatRunNames("kube-scheduler-default", 1)
	if err != nil || len(single) != 1 || single[0] != "kube-scheduler-default" {
		t.Fatalf("single repeat names = %v, err=%v", single, err)
	}
}

func TestMergeBatchResultsAppendsRunsAndRows(t *testing.T) {
	base := evaluator.BatchResult{
		PolicyVersion: "not-applicable",
		SchedulerMode: evaluator.SchedulerModeExternalReference,
		Workload:      "cpu-tight-train",
		Results:       []evaluator.ComparisonRow{{Name: "run-r01"}},
		Runs:          []evaluator.RunResult{{Name: "run-r01"}},
	}
	next := evaluator.BatchResult{
		PolicyVersion: "not-applicable",
		SchedulerMode: evaluator.SchedulerModeExternalReference,
		Workload:      "cpu-tight-train",
		Results:       []evaluator.ComparisonRow{{Name: "run-r02"}},
		Runs:          []evaluator.RunResult{{Name: "run-r02"}},
	}
	got, err := mergeBatchResults(base, next)
	if err != nil {
		t.Fatalf("mergeBatchResults() error = %v", err)
	}
	if len(got.Results) != 2 || len(got.Runs) != 2 || got.Results[1].Name != "run-r02" {
		t.Fatalf("unexpected merged batch: %+v", got)
	}

	next.Workload = "mem-tight-train"
	if _, err := mergeBatchResults(base, next); err == nil {
		t.Fatal("mergeBatchResults(workload mismatch) error = nil")
	}
}
