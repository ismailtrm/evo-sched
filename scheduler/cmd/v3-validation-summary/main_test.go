package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

func TestFalseParetoDiscoveryComparisonByArm(t *testing.T) {
	policies := []policySummary{
		policyWithObjectives("baseline", "baseline", true, 1.2, 4),
		policyWithObjectives("one-a", "one-shot-control", false, 1.0, 4),
		policyWithObjectives("one-b", "one-shot-control", false, 1.3, 4),
		policyWithObjectives("one-c", "one-shot-control", false, 1.5, 3),
		policyWithObjectives("two-a", "two-stage-resampling", false, 1.1, 3.5),
		policyWithObjectives("two-b", "two-stage-resampling", false, 1.3, 3),
		policyWithObjectives("two-c", "two-stage-resampling", false, 1.4, 4),
	}

	front := markPareto(policies)
	if len(front) != 3 {
		t.Fatalf("Pareto points = %d, want 3: %+v", len(front), front)
	}
	arms, comparison, err := summarizeArms(policies)
	if err != nil {
		t.Fatal(err)
	}
	if arms[0].FalseParetoDiscoveries != 2 || arms[0].RemainedNondominated != 1 || arms[0].FalseParetoDiscoveryProportion != 2.0/3.0 {
		t.Fatalf("one-shot outcome = %+v", arms[0])
	}
	if arms[1].FalseParetoDiscoveries != 1 || arms[1].RemainedNondominated != 2 || arms[1].FalseParetoDiscoveryProportion != 1.0/3.0 {
		t.Fatalf("two-stage outcome = %+v", arms[1])
	}
	if !comparison.TwoStageLower || !comparison.DescriptiveMitigationSupported || math.Abs(comparison.TwoStageMinusOneShot+1.0/3.0) > 1e-12 {
		t.Fatalf("comparison = %+v", comparison)
	}
}

func TestSummarizePolicyUsesRepetitionLevelWorkloadAverages(t *testing.T) {
	weights := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	observed := &policyObservations{
		weights: weights,
		byWorkloadRep: map[string]map[int]evaluator.ComparisonRow{
			"a.json": {
				1: {P95SchedulingDecisionLatency: 1, MaxWorkloadOccupiedWorkerNodes: 4, ResourceSummary: evaluator.ResourceSummary{MeanWorkloadDominantRequestedLoadVariance: 0.1}},
				2: {P95SchedulingDecisionLatency: 3, MaxWorkloadOccupiedWorkerNodes: 2, ResourceSummary: evaluator.ResourceSummary{MeanWorkloadDominantRequestedLoadVariance: 0.2}},
			},
			"b.json": {
				1: {P95SchedulingDecisionLatency: 3, MaxWorkloadOccupiedWorkerNodes: 2, ResourceSummary: evaluator.ResourceSummary{MeanWorkloadDominantRequestedLoadVariance: 0.3}},
				2: {P95SchedulingDecisionLatency: 5, MaxWorkloadOccupiedWorkerNodes: 4, ResourceSummary: evaluator.ResourceSummary{MeanWorkloadDominantRequestedLoadVariance: 0.4}},
			},
		},
	}

	got, err := summarizePolicy("policy", "baseline", true, observed, []string{"workloads/a.json", "workloads/b.json"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Objectives.Repetitions != 2 || got.Objectives.P95DecisionLatency.Mean != 3 || got.Objectives.MaxOccupiedWorkers.Mean != 3 {
		t.Fatalf("objectives = %+v", got.Objectives)
	}
	if math.Abs(got.Objectives.P95DecisionLatency.SampleStdDev-math.Sqrt(2)) > 1e-12 {
		t.Fatalf("latency SD = %v, want %v", got.Objectives.P95DecisionLatency.SampleStdDev, math.Sqrt(2))
	}
	if len(got.ByWorkload) != 2 || got.ByWorkload[0].Repetitions != 2 || got.ByWorkload[1].Repetitions != 2 {
		t.Fatalf("workload summaries = %+v", got.ByWorkload)
	}
}

func TestValidRunRejectsMissingDecisionTimestamp(t *testing.T) {
	now := time.Now()
	run := evaluator.RunResult{
		Summary: evaluator.Summary{
			PodCount: 1, ScheduledCount: 1, CompletedCount: 1,
			DecisionTimestampCount: 1, KubernetesScheduledTimestampCount: 1,
			EvaluatorObservationTimestampCount: 1,
		},
		Pods: []evaluator.PodTiming{{
			SubmittedAt: &now, KubernetesScheduledAt: &now, EvaluatorObservedScheduledAt: &now,
			NodeName: "worker", Phase: "Succeeded",
		}},
	}
	if validRun(run, 1) {
		t.Fatal("validRun accepted a Pod without schedulerDecisionAt")
	}
}

func TestExactCommandRecordsAllAnalysisInputsAndOutputs(t *testing.T) {
	opts := options{
		inputs: "a.json,b.json", protocolPath: "protocol.json", manifestPath: "manifest.json",
		searchPath: "search.json", weightsPath: "weights.json", outputPath: "summary.json", csvOutputPath: "summary.csv",
	}
	want := "go run ./cmd/v3-validation-summary --inputs a.json,b.json --protocol protocol.json --manifest manifest.json --search-summary search.json --weights weights.json --output summary.json --csv-output summary.csv"
	if got := exactCommand(opts); got != want {
		t.Fatalf("exactCommand() = %q, want %q", got, want)
	}
}

func TestOfficialSharedValidationArtifactsPassProvenanceGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	protocolPath := filepath.Join(repoRoot, "experiments", "protocol-v3-budget-matched-resampling.json")
	manifestPath := filepath.Join(repoRoot, "results", "2026-09-07-v3-shared-validation", "manifest.json")
	searchPath := filepath.Join(repoRoot, "results", "2026-09-07-v3-budget-matched-official-search", "analysis", "search-summary.json")
	weightsPath := filepath.Join(repoRoot, "experiments", "weights-v3-validation-finalists.json")
	resultRoot := filepath.Dir(manifestPath)
	resultPaths := []string{
		filepath.Join(resultRoot, "cpu-tight-train.json"),
		filepath.Join(resultRoot, "mem-tight-train.json"),
		filepath.Join(resultRoot, "fragmenting-mix-train.json"),
	}

	protocol, err := loadJSON[protocolDocument](protocolPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadJSON[validationManifest](manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	search, err := loadJSON[searchSummary](searchPath)
	if err != nil {
		t.Fatal(err)
	}
	weights, err := evaluator.LoadExperiment(weightsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProvenance(protocol, manifest, search, weights, repoRoot, manifestPath, resultPaths); err != nil {
		t.Fatal(err)
	}
	batches := make([]evaluator.BatchResult, 0, len(resultPaths))
	for _, resultPath := range resultPaths {
		batch, err := loadJSON[evaluator.BatchResult](resultPath)
		if err != nil {
			t.Fatal(err)
		}
		batches = append(batches, batch)
	}
	built, err := buildOutput(protocol, manifest, search, weights, batches)
	if err != nil {
		t.Fatal(err)
	}
	if built.Acceptance.ResultRows != 270 || built.Acceptance.Pods != 6480 || built.Acceptance.Unschedulable != 0 {
		t.Fatalf("acceptance = %+v", built.Acceptance)
	}
	if len(built.Policies) != 9 || len(built.Arms) != 2 || built.Arms[0].ValidatedFinalists != 3 || built.Arms[1].ValidatedFinalists != 3 || len(built.ParetoFront) == 0 {
		t.Fatalf("unexpected official summary shape: policies=%d arms=%+v front=%+v", len(built.Policies), built.Arms, built.ParetoFront)
	}

	analysisRoot := filepath.Join(resultRoot, "analysis")
	committed, err := loadJSON[output](filepath.Join(analysisRoot, "validation-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	built.GeneratedAt = committed.GeneratedAt
	built.Generator = committed.Generator
	built.Inputs = committed.Inputs
	if !reflect.DeepEqual(built, committed) {
		t.Fatal("committed V3 validation JSON differs from regenerated analysis body")
	}

	regeneratedCSV := filepath.Join(t.TempDir(), "validation-summary.csv")
	if err := writeCSV(regeneratedCSV, built.Policies); err != nil {
		t.Fatal(err)
	}
	regeneratedData, err := os.ReadFile(regeneratedCSV)
	if err != nil {
		t.Fatal(err)
	}
	committedData, err := os.ReadFile(filepath.Join(analysisRoot, "validation-summary.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(regeneratedData, committedData) {
		t.Fatal("committed V3 validation CSV differs from regenerated output")
	}
}

func policyWithObjectives(name, arm string, baseline bool, latency, nodes float64) policySummary {
	return policySummary{
		Name: name, Arm: arm, Baseline: baseline,
		Objectives: objectiveStatistics{
			P95DecisionLatency: statistics{Mean: latency},
			MaxOccupiedWorkers: statistics{Mean: nodes},
		},
	}
}
