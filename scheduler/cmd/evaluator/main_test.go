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
	})
	if err == nil || !strings.Contains(err.Error(), "not valid in external-reference mode") {
		t.Fatalf("run() error = %v, want external weights rejection", err)
	}
}
