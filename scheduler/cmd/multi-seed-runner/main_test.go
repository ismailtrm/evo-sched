package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

	got, err := inspectSeedOutputs(repoRoot, resultPath, filepath.Join(seedDir, "run.log"), 42, 3)
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

	if _, err := inspectSeedOutputs(repoRoot, resultPath, filepath.Join(seedDir, "run.log"), 123, 3); err == nil {
		t.Fatal("inspectSeedOutputs(seed mismatch) error = nil")
	}
}
