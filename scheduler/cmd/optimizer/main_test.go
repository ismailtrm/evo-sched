package main

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

func TestWeightKeyPreservesDistinctFloatValues(t *testing.T) {
	a := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	b := a
	b.FreeCPU = math.Nextafter(a.FreeCPU, 1)

	if weightKey(a) == weightKey(b) {
		t.Fatalf("distinct weights collapsed to one cache key: a=%+v b=%+v", a, b)
	}
}

func TestClusterEvaluatorKeepsCachedAndFreshEvaluationPathsDistinct(t *testing.T) {
	runnerOpts := evaluator.DefaultRunnerOptions()
	clusterEval := newClusterEvaluator(nil, runnerOpts, []string{"cpu-tight.json"})
	calls := 0
	runNames := make([]string, 0)
	clusterEval.evaluateWorkloads = func(
		_ context.Context,
		_ kubernetes.Interface,
		_ evaluator.RunnerOptions,
		_ []string,
		spec evaluator.ExperimentRunSpec,
	) ([]evaluator.BatchResult, error) {
		calls++
		runNames = append(runNames, spec.Name)
		return []evaluator.BatchResult{validEvaluationBatch()}, nil
	}

	weights := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	if _, err := clusterEval.Evaluate(weights); err != nil {
		t.Fatalf("cached Evaluate() first call error = %v", err)
	}
	if _, err := clusterEval.Evaluate(weights); err != nil {
		t.Fatalf("cached Evaluate() second call error = %v", err)
	}
	for _, evaluationID := range []string{"fresh-001", "fresh-002"} {
		_, err := clusterEval.EvaluateFresh(optimizer.EvaluationRequest{
			EvaluationID: evaluationID,
			Weights:      weights,
		})
		if err != nil {
			t.Fatalf("EvaluateFresh(%s) error = %v", evaluationID, err)
		}
	}

	if calls != 3 {
		t.Fatalf("live workload evaluations = %d, want 3 (one cached miss and two fresh calls)", calls)
	}
	if got, want := runNames[1:], []string{"fresh-001", "fresh-002"}; !slices.Equal(got, want) {
		t.Fatalf("fresh run names = %v, want %v", got, want)
	}
}

func TestDeriveSamplesCSVOutputPathOnlyDefaultsForBudgetedResults(t *testing.T) {
	opts := options{outputPath: "/tmp/result.json"}
	if got := deriveSamplesCSVOutputPath(opts, optimizer.Result{}); got != "" {
		t.Fatalf("legacy samples path = %q, want empty", got)
	}
	result := optimizer.Result{Budget: &optimizer.BudgetSummary{}}
	if got, want := deriveSamplesCSVOutputPath(opts, result), "/tmp/result-samples.csv"; got != want {
		t.Fatalf("budgeted samples path = %q, want %q", got, want)
	}

	opts.samplesCSVOutputPath = "/tmp/explicit.csv"
	if got, want := deriveSamplesCSVOutputPath(opts, optimizer.Result{}), opts.samplesCSVOutputPath; got != want {
		t.Fatalf("explicit samples path = %q, want %q", got, want)
	}
}

func TestAggregateEvaluationBatches(t *testing.T) {
	data, err := aggregateEvaluationBatches([]evaluator.BatchResult{validEvaluationBatch()})
	if err != nil {
		t.Fatalf("aggregateEvaluationBatches() error = %v", err)
	}
	if got, want := data.Objectives.AverageP95SchedulingDecisionLatency, 0.75; got != want {
		t.Fatalf("average p95 decision latency = %v, want %v", got, want)
	}
	if got, want := data.Objectives.AverageMaxWorkloadOccupiedWorkerNodes, 2.0; got != want {
		t.Fatalf("average max occupied workers = %v, want %v", got, want)
	}
	if len(data.Workloads) != 1 || data.Workloads[0].PodCount != 4 {
		t.Fatalf("unexpected workload metrics: %+v", data.Workloads)
	}
	if got, want := data.Workloads[0].MeanWorkloadDominantRequestedLoadVariance, 0.04; got != want {
		t.Fatalf("mean workload load variance = %v, want %v", got, want)
	}
	if got, want := data.Workloads[0].MinimumDurationToPollIntervalRatio, 5.0; got != want {
		t.Fatalf("duration to poll ratio = %v, want %v", got, want)
	}
}

func TestAggregateEvaluationBatchesRejectsInvalidMeasurementEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*evaluator.BatchResult)
		want   string
	}{
		{
			name: "measurement version mismatch",
			mutate: func(batch *evaluator.BatchResult) {
				batch.MeasurementVersion = "v1"
			},
			want: "measurement contract mismatch",
		},
		{
			name: "invalid measurement interval",
			mutate: func(batch *evaluator.BatchResult) {
				batch.MeasurementProtocol.PodPollIntervalSeconds = 0
			},
			want: "invalid intervals",
		},
		{
			name: "missing resource metric source",
			mutate: func(batch *evaluator.BatchResult) {
				batch.MeasurementProtocol.WorkloadResourceMetricSource = ""
			},
			want: "no workload resource metric source",
		},
		{
			name: "undersampled workload",
			mutate: func(batch *evaluator.BatchResult) {
				batch.WorkloadSamplingProfile.MinimumDurationToPollIntervalRatio = 1
			},
			want: "undersampled",
		},
		{
			name: "sampling profile mismatch",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].MinimumDurationToPollIntervalRatio = 4
			},
			want: "sampling profile does not match",
		},
		{
			name: "unschedulable pod",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].ScheduledCount = 3
				batch.Results[0].UnschedulableCount = 1
			},
			want: "not fully schedulable",
		},
		{
			name: "incomplete workload",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].CompletedCount = 3
			},
			want: "is incomplete",
		},
		{
			name: "missing decision timestamp",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].DecisionTimestampCount = 3
			},
			want: "scheduler decision timestamps",
		},
		{
			name: "missing Kubernetes timestamp",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].KubernetesScheduledTimestampCount = 3
			},
			want: "Kubernetes scheduled timestamps",
		},
		{
			name: "missing evaluator observation",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].EvaluatorObservationTimestampCount = 3
			},
			want: "evaluator observations",
		},
		{
			name: "missing occupancy observation",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].MaxWorkloadOccupiedWorkerNodes = 0
			},
			want: "no observed occupied worker nodes",
		},
		{
			name: "occupancy exceeds snapshot",
			mutate: func(batch *evaluator.BatchResult) {
				batch.Results[0].MaxWorkloadOccupiedWorkerNodes = 5
			},
			want: "exceeds worker snapshot size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := validEvaluationBatch()
			tt.mutate(&batch)
			_, err := aggregateEvaluationBatches([]evaluator.BatchResult{batch})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("aggregateEvaluationBatches() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestAggregateEvaluationBatchesRejectsEmptyInput(t *testing.T) {
	if _, err := aggregateEvaluationBatches(nil); err == nil {
		t.Fatal("aggregateEvaluationBatches() error = nil, want empty input rejection")
	}
}

func TestAggregateEvaluationBatchesRejectsProtocolDrift(t *testing.T) {
	first := validEvaluationBatch()
	second := validEvaluationBatch()
	second.Workload = "mem-tight"
	second.MeasurementProtocol.PodPollIntervalSeconds = 0.5

	_, err := aggregateEvaluationBatches([]evaluator.BatchResult{first, second})
	if err == nil || !strings.Contains(err.Error(), "different measurement protocol") {
		t.Fatalf("aggregateEvaluationBatches() error = %v, want protocol drift rejection", err)
	}
}

func validEvaluationBatch() evaluator.BatchResult {
	return evaluator.BatchResult{
		MeasurementVersion: evaluator.MeasurementVersion,
		MeasurementProtocol: evaluator.MeasurementProtocol{
			Version:                      evaluator.MeasurementVersion,
			WorkloadResourceMetricSource: evaluator.WorkloadRequestedResourceMetricSource,
			SchedulerIntervalSeconds:     2,
			PodPollIntervalSeconds:       1,
		},
		Workload:     "cpu-tight",
		WorkloadPath: "../workloads/cpu-tight.json",
		WorkloadBase: &evaluator.WorkloadBase{WorkerCount: 4},
		WorkloadSamplingProfile: evaluator.WorkloadSamplingProfile{
			MinimumPodDurationSeconds:          5,
			MaximumPodDurationSeconds:          10,
			MinimumDurationToPollIntervalRatio: 5,
		},
		Results: []evaluator.ComparisonRow{{
			PodCount:                           4,
			ScheduledCount:                     4,
			CompletedCount:                     4,
			DecisionTimestampCount:             4,
			KubernetesScheduledTimestampCount:  4,
			EvaluatorObservationTimestampCount: 4,
			AverageSchedulingDecisionLatency:   0.5,
			P95SchedulingDecisionLatency:       0.75,
			AverageKubernetesPending:           1.0,
			P95KubernetesPending:               1.0,
			MaxWorkloadOccupiedWorkerNodes:     2,
			ResourceSummary: evaluator.ResourceSummary{
				MeanWorkloadCPURequestedUtilization:       0.3,
				MeanWorkloadMemoryRequestedUtilization:    0.2,
				MeanWorkloadDominantRequestedLoadVariance: 0.04,
				MeanWorkloadCPUToMemoryImbalance:          0.1,
			},
			WorkloadSamplingProfile: evaluator.WorkloadSamplingProfile{
				MinimumPodDurationSeconds:          5,
				MaximumPodDurationSeconds:          10,
				MinimumDurationToPollIntervalRatio: 5,
			},
		}},
	}
}
