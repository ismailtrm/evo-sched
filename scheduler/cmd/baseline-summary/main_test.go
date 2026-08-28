package main

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

func TestSummarizeBatchesGroupsRepetitionsAndKeepsExternalLatencyUnavailable(t *testing.T) {
	weights := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	batches := []evaluator.BatchResult{
		{
			MeasurementVersion: evaluator.MeasurementVersion,
			SchedulerMode:      evaluator.SchedulerModeCustomPolicy,
			Workload:           "cpu-tight-train",
			Results: []evaluator.ComparisonRow{
				row("balanced-r01", "evok8s-scheduler", &weights, true, 1, 4, 0.01, 0.2),
				row("balanced-r02", "evok8s-scheduler", &weights, true, 3, 3, 0.03, 0.4),
			},
		},
		{
			MeasurementVersion: evaluator.MeasurementVersion,
			SchedulerMode:      evaluator.SchedulerModeExternalReference,
			Workload:           "cpu-tight-train",
			Results: []evaluator.ComparisonRow{
				row("kube-scheduler-default-r01", "default-scheduler", nil, false, 0, 4, 0.02, 0.3),
				row("kube-scheduler-default-r02", "default-scheduler", nil, false, 0, 4, 0.04, 0.5),
			},
		},
	}

	got, err := summarizeBatches(batches, []string{"custom.json", "default.json"})
	if err != nil {
		t.Fatalf("summarizeBatches() error = %v", err)
	}
	if len(got.ByWorkload) != 2 || len(got.Aggregates) != 2 {
		t.Fatalf("summary row counts = byWorkload:%d aggregates:%d", len(got.ByWorkload), len(got.Aggregates))
	}

	custom := got.ByWorkload[0]
	if custom.PolicyName != "balanced" || custom.Repetitions != 2 {
		t.Fatalf("custom group = %+v", custom)
	}
	if custom.P95SchedulingDecisionLatency == nil {
		t.Fatal("custom decision latency stats = nil")
	}
	assertClose(t, custom.P95SchedulingDecisionLatency.Mean, 2)
	assertClose(t, custom.P95SchedulingDecisionLatency.SampleStdDev, math.Sqrt(2))
	assertClose(t, custom.MaxWorkloadOccupiedWorkerNodes.Mean, 3.5)
	assertClose(t, custom.MaxWorkloadOccupiedWorkerNodes.SampleStdDev, math.Sqrt(0.5))

	external := got.ByWorkload[1]
	if external.PolicyName != "kube-scheduler-default" || external.DecisionLatencyApplicable {
		t.Fatalf("external group = %+v", external)
	}
	if external.P95SchedulingDecisionLatency != nil {
		t.Fatalf("external decision latency stats = %+v, want nil", external.P95SchedulingDecisionLatency)
	}
}

func TestWriteSummaryCSVLeavesExternalDecisionStatsBlank(t *testing.T) {
	output, err := summarizeBatches([]evaluator.BatchResult{
		{
			MeasurementVersion: evaluator.MeasurementVersion,
			SchedulerMode:      evaluator.SchedulerModeExternalReference,
			Workload:           "cpu-tight-train",
			Results: []evaluator.ComparisonRow{
				row("kube-scheduler-default-r01", "default-scheduler", nil, false, 0, 4, 0.02, 0.3),
			},
		},
	}, []string{"default.json"})
	if err != nil {
		t.Fatalf("summarizeBatches() error = %v", err)
	}

	var buf bytes.Buffer
	if err := writeSummaryCSV(&buf, output); err != nil {
		t.Fatalf("writeSummaryCSV() error = %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("CSV lines = %d, want header + workload + aggregate\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], "p95_decision_mean_seconds") {
		t.Fatalf("CSV header missing decision field: %s", lines[0])
	}
	if strings.Contains(lines[1], ",0.000000,0.000000,0.000000,0.000000,") {
		t.Fatalf("external decision fields were serialized as zero: %s", lines[1])
	}
}

func TestCalculateStatisticsIncludesStudentTConfidenceInterval(t *testing.T) {
	got := calculateStatistics([]float64{1, 2, 3, 4, 5})

	assertClose(t, got.Mean, 3)
	assertClose(t, got.SampleStdDev, math.Sqrt(2.5))
	margin := 2.776 * got.SampleStdDev / math.Sqrt(5)
	assertClose(t, got.Confidence95Lower, got.Mean-margin)
	assertClose(t, got.Confidence95Upper, got.Mean+margin)
}

func TestCalculateStatisticsUsesDegenerateIntervalForSingleObservation(t *testing.T) {
	got := calculateStatistics([]float64{7})
	if got.Confidence95Lower != 7 || got.Confidence95Upper != 7 {
		t.Fatalf("single-observation confidence interval = [%v, %v], want [7, 7]", got.Confidence95Lower, got.Confidence95Upper)
	}
}

func row(name, scheduler string, weights *evaluator.WeightSet, weightsApplicable bool, p95 float64, nodes int, variance, imbalance float64) evaluator.ComparisonRow {
	return evaluator.ComparisonRow{
		SchedulerMode:                  evaluator.SchedulerModeCustomPolicy,
		SchedulerName:                  scheduler,
		Name:                           name,
		Weights:                        weights,
		WeightsApplicable:              weightsApplicable,
		PodCount:                       24,
		ScheduledCount:                 24,
		CompletedCount:                 24,
		P95SchedulingDecisionLatency:   p95,
		MaxWorkloadOccupiedWorkerNodes: nodes,
		ResourceSummary: evaluator.ResourceSummary{
			MeanWorkloadDominantRequestedLoadVariance: variance,
			MeanWorkloadCPUToMemoryImbalance:          imbalance,
		},
	}
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v, want %v", got, want)
	}
}
