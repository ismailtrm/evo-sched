package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

func TestParseSeedsRejectsDuplicatesAndInvalidValues(t *testing.T) {
	got, err := parseSeeds("42, 123,999")
	if err != nil {
		t.Fatalf("parseSeeds() error = %v", err)
	}
	if want := []int64{42, 123, 999}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSeeds() = %v, want %v", got, want)
	}

	for _, input := range []string{"", "42,42", "42,nope"} {
		if _, err := parseSeeds(input); err == nil {
			t.Fatalf("parseSeeds(%q) error = nil", input)
		}
	}
}

func TestBuildOptimizerArgsIncludesExplicitMeasurementAndSearchFlags(t *testing.T) {
	opts := options{
		workloads:     "../workloads/v2/cpu-tight-train.json,../workloads/v2/mem-tight-train.json",
		baselineFile:  "../experiments/weights-v2.json",
		population:    12,
		generations:   5,
		mutationRate:  0.8,
		mutationSigma: 0.25,
		minWeight:     0,
		maxWeight:     2,
		pollInterval:  250 * time.Millisecond,
		timeout:       90 * time.Second,
		image:         "busybox:1.36.1",
	}

	got := buildOptimizerArgs(opts, 42, "/tmp/result.json")
	joined := strings.Join(got, " ")
	for _, required := range []string{
		"--seed 42",
		"--poll-interval 250ms",
		"--timeout 1m30s",
		"--population 12",
		"--generations 5",
		"--mutation-rate 0.8",
		"--mutation-sigma 0.25",
		"--min-weight 0",
		"--max-weight 2",
		"--image busybox:1.36.1",
		"--output /tmp/result.json",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("optimizer args missing %q:\n%s", required, joined)
		}
	}
	if strings.Contains(joined, "--search-mode") || strings.Contains(joined, "--optimizer-sample-budget") {
		t.Fatalf("legacy optimizer args unexpectedly contain V3 flags:\n%s", joined)
	}
}

func TestBuildOptimizerArgsIncludesBudgetedSearchContract(t *testing.T) {
	opts := options{
		workloads:                     "../workloads/v2/cpu-tight-train.json,../workloads/v2/mem-tight-train.json,../workloads/v2/fragmenting-mix-train.json",
		baselineFile:                  "../experiments/weights-v2.json",
		population:                    4,
		generations:                   1,
		mutationRate:                  0.8,
		mutationSigma:                 0.25,
		minWeight:                     0,
		maxWeight:                     2,
		pollInterval:                  250 * time.Millisecond,
		timeout:                       90 * time.Second,
		image:                         "busybox:1.36.1",
		searchMode:                    optimizer.SearchModeBudgetTwoStage,
		offspringPerGeneration:        2,
		promotionsPerGeneration:       1,
		additionalSamplesPerPromotion: 2,
		optimizerSampleBudget:         8,
	}

	joined := strings.Join(buildOptimizerArgs(opts, 20260831, "/tmp/result.json"), " ")
	for _, required := range []string{
		"--search-mode budget-two-stage",
		"--offspring-per-generation 2",
		"--promotions-per-generation 1",
		"--additional-samples-per-promotion 2",
		"--optimizer-sample-budget 8",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("budgeted optimizer args missing %q:\n%s", required, joined)
		}
	}
}

func TestBuildRunExpectationsSeparatesUniqueCandidatesFromFreshSamples(t *testing.T) {
	opts := options{
		population:                    4,
		generations:                   1,
		searchMode:                    optimizer.SearchModeBudgetTwoStage,
		offspringPerGeneration:        2,
		promotionsPerGeneration:       1,
		additionalSamplesPerPromotion: 2,
		optimizerSampleBudget:         8,
	}

	got, err := buildRunExpectations(opts, 3, 3)
	if err != nil {
		t.Fatalf("buildRunExpectations() error = %v", err)
	}
	if got.ExpectedCandidates != 9 || got.ExpectedOptimizerSamples != 8 || got.ExpectedBaselineSamples != 3 || got.ExpectedPromotionEvents != 1 || got.ExpectedWorkloadRuns != 33 || got.ExpectedPolicySamples != 11 {
		t.Fatalf("unexpected V3 expectations: %+v", got)
	}

	opts.optimizerSampleBudget = 7
	if _, err := buildRunExpectations(opts, 3, 3); err == nil {
		t.Fatal("buildRunExpectations() accepted a mismatched fresh-sample budget")
	}
}

func TestPaperEvidenceEligibilityStaysFalseForPilotAndBudgetedSearch(t *testing.T) {
	if paperEvidenceEligibleAtCompletion(options{class: "pilot"}) {
		t.Fatal("pilot run became paper evidence eligible")
	}
	if paperEvidenceEligibleAtCompletion(options{class: "official", searchMode: optimizer.SearchModeBudgetOneShot}) {
		t.Fatal("unvalidated V3 search became paper evidence eligible")
	}
	if !paperEvidenceEligibleAtCompletion(options{class: "official"}) {
		t.Fatal("legacy official V2 completion lost its evidence eligibility")
	}
}

func TestCountPodsPerPolicySampleResolvesAllWorkloads(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	if err := os.WriteFile(first, []byte(`[{"name":"a","arrivalSeconds":0,"cpuMilli":100,"memoryMiB":64,"durationSeconds":1}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`[
  {"name":"b","arrivalSeconds":0,"cpuFraction":0.1,"memoryFraction":0.1,"durationSeconds":1},
  {"name":"c","arrivalSeconds":1,"cpuFraction":0.1,"memoryFraction":0.1,"durationSeconds":1}
]`), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := clusterSnapshot{WorkerCount: 4, WorkerCPUMilli: 2000, WorkerMemoryMiB: 4096}
	got, err := countPodsPerPolicySample(dir, []string{"first.json", "second.json"}, snapshot)
	if err != nil {
		t.Fatalf("countPodsPerPolicySample() error = %v", err)
	}
	if got != 3 {
		t.Fatalf("Pods per policy sample = %d, want 3", got)
	}
}

func TestSplitPathsRejectsEmptyEntries(t *testing.T) {
	got, err := splitPaths("a.json, b.json")
	if err != nil {
		t.Fatalf("splitPaths() error = %v", err)
	}
	if want := []string{"a.json", "b.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("splitPaths() = %v, want %v", got, want)
	}
	for _, input := range []string{"", "a.json,,b.json"} {
		if _, err := splitPaths(input); err == nil {
			t.Fatalf("splitPaths(%q) error = nil", input)
		}
	}
}

func TestInspectSeedOutputsValidatesContractAndAllArtifacts(t *testing.T) {
	repoRoot := t.TempDir()
	seedDir := filepath.Join(repoRoot, "results", "run", "seed-42")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(seedDir, "result.json")
	result := optimizer.Result{
		PolicyVersion:      evaluator.PolicyVersion,
		MeasurementVersion: evaluator.MeasurementVersion,
		FinishedAt:         time.Now().UTC(),
		Config:             optimizer.Config{Seed: 42},
		Baselines:          make([]optimizer.Candidate, 1),
		Evaluated:          make([]optimizer.Candidate, 2),
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		resultPath: data,
		filepath.Join(seedDir, "result-pareto.csv"): []byte("pareto\n"),
		filepath.Join(seedDir, "result-all.csv"):    []byte("all\n"),
		filepath.Join(seedDir, "run.log"):           []byte("log\n"),
	}
	for path, content := range files {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	expectations := runExpectations{ExpectedCandidates: 3, ExpectedWorkloadRuns: 3}
	got, err := inspectSeedOutputs(repoRoot, resultPath, filepath.Join(seedDir, "run.log"), 42, expectations, "")
	if err != nil {
		t.Fatalf("inspectSeedOutputs() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("output artifacts = %d, want 4", len(got))
	}
	for _, artifact := range got {
		if strings.HasPrefix(artifact.Path, repoRoot) || len(artifact.SHA256) != 64 {
			t.Fatalf("invalid artifact provenance: %+v", artifact)
		}
	}

	if _, err := inspectSeedOutputs(repoRoot, resultPath, filepath.Join(seedDir, "run.log"), 123, expectations, ""); err == nil {
		t.Fatal("inspectSeedOutputs(seed mismatch) error = nil")
	}
}

func TestInspectSeedOutputsValidatesBudgetAndSampleArtifact(t *testing.T) {
	repoRoot := t.TempDir()
	seedDir := filepath.Join(repoRoot, "results", "pilot", "seed-20260831")
	if err := os.MkdirAll(seedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(seedDir, "result.json")
	result := optimizer.Result{
		PolicyVersion:      evaluator.PolicyVersion,
		MeasurementVersion: evaluator.MeasurementVersion,
		FinishedAt:         time.Now().UTC(),
		Config: optimizer.Config{
			Seed:       20260831,
			SearchMode: optimizer.SearchModeBudgetTwoStage,
		},
		Budget: &optimizer.BudgetSummary{
			ExpectedOptimizerSamples:    8,
			ExpectedBaselineSamples:     3,
			AttemptedOptimizerSamples:   8,
			ActualOptimizerSamples:      8,
			AttemptedBaselineSamples:    3,
			ActualBaselineSamples:       3,
			ExpectedWorkloadExecutions:  33,
			AttemptedWorkloadExecutions: 33,
			ActualWorkloadExecutions:    33,
			UniqueOptimizerCandidates:   6,
			PromotionEvents:             1,
		},
		Baselines: make([]optimizer.Candidate, 3),
		Evaluated: []optimizer.Candidate{
			{EvaluationCount: 3, EvaluationSamples: make([]optimizer.EvaluationSample, 3)},
			{EvaluationCount: 1, EvaluationSamples: make([]optimizer.EvaluationSample, 1)},
			{EvaluationCount: 1, EvaluationSamples: make([]optimizer.EvaluationSample, 1)},
			{EvaluationCount: 1, EvaluationSamples: make([]optimizer.EvaluationSample, 1)},
			{EvaluationCount: 1, EvaluationSamples: make([]optimizer.EvaluationSample, 1)},
			{EvaluationCount: 1, EvaluationSamples: make([]optimizer.EvaluationSample, 1)},
		},
	}
	for index := range result.Baselines {
		result.Baselines[index].EvaluationCount = 1
		result.Baselines[index].EvaluationSamples = []optimizer.EvaluationSample{{EvaluationID: "baseline-" + strconv.Itoa(index)}}
	}
	for candidateIndex := range result.Evaluated {
		for sampleIndex := range result.Evaluated[candidateIndex].EvaluationSamples {
			result.Evaluated[candidateIndex].EvaluationSamples[sampleIndex].EvaluationID = fmt.Sprintf("candidate-%d-sample-%d", candidateIndex, sampleIndex)
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		resultPath: data,
		filepath.Join(seedDir, "result-pareto.csv"): []byte("pareto\n"),
		filepath.Join(seedDir, "result-all.csv"):    []byte("all\n"),
		filepath.Join(seedDir, "run.log"):           []byte("log\n"),
	}
	for path, content := range files {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sampleCSV := "evaluation_id\n"
	for _, candidate := range append(append([]optimizer.Candidate(nil), result.Baselines...), result.Evaluated...) {
		for _, sample := range candidate.EvaluationSamples {
			sampleCSV += sample.EvaluationID + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(seedDir, "result-samples.csv"), []byte(sampleCSV), 0o644); err != nil {
		t.Fatal(err)
	}

	expectations := runExpectations{
		ExpectedCandidates:       9,
		ExpectedOptimizerSamples: 8,
		ExpectedBaselineSamples:  3,
		ExpectedPromotionEvents:  1,
		ExpectedWorkloadRuns:     33,
	}
	got, err := inspectSeedOutputs(
		repoRoot,
		resultPath,
		filepath.Join(seedDir, "run.log"),
		20260831,
		expectations,
		optimizer.SearchModeBudgetTwoStage,
	)
	if err != nil {
		t.Fatalf("inspectSeedOutputs() budgeted error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("budgeted output artifacts = %d, want 5", len(got))
	}
	expectations.ExpectedPods = 1
	if _, err := inspectSeedOutputs(
		repoRoot,
		resultPath,
		filepath.Join(seedDir, "run.log"),
		20260831,
		expectations,
		optimizer.SearchModeBudgetTwoStage,
	); err == nil || !strings.Contains(err.Error(), "Pod count") {
		t.Fatalf("inspectSeedOutputs() Pod budget error = %v", err)
	}
}

func TestInspectAvailableSeedOutputsPreservesPartialArtifacts(t *testing.T) {
	repoRoot := t.TempDir()
	dir := filepath.Join(repoRoot, "partial")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(dir, "result.json")
	logPath := filepath.Join(dir, "run.log")
	for path, content := range map[string]string{
		resultPath: "{}\n",
		strings.TrimSuffix(resultPath, ".json") + "-samples.csv": "evaluation_id\npartial-001\n",
		logPath: "interrupted\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	artifacts, err := inspectAvailableSeedOutputs(repoRoot, resultPath, logPath, optimizer.SearchModeBudgetTwoStage)
	if err != nil {
		t.Fatalf("inspectAvailableSeedOutputs() error = %v", err)
	}
	if len(artifacts) != 3 {
		t.Fatalf("partial artifacts = %d, want 3: %+v", len(artifacts), artifacts)
	}
	for _, artifact := range artifacts {
		if len(artifact.SHA256) != 64 {
			t.Fatalf("partial artifact has invalid checksum: %+v", artifact)
		}
	}
}
