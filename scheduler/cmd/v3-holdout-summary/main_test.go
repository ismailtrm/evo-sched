package main

import (
	"math"
	"path/filepath"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

func TestMarkParetoAndSurvivorOutcome(t *testing.T) {
	policies := []policySummary{
		policyWithObjectives("baseline", "baseline", true, 1.2, 4),
		policyWithObjectives("survivor-a", "one-shot-control", false, 1.0, 4),
		policyWithObjectives("survivor-b", "two-stage-resampling", false, 1.3, 3),
		policyWithObjectives("survivor-c", "two-stage-resampling", false, 1.5, 4),
	}
	front := markPareto(policies)
	if len(front) != 2 {
		t.Fatalf("Pareto points = %d, want 2: %+v", len(front), front)
	}
	outcome := summarizeSurvivors(policies)
	if outcome.FrozenSurvivors != 3 || outcome.RemainedNondominated != 2 || outcome.DidNotRemain != 1 || math.Abs(outcome.SurvivalProportion-2.0/3.0) > 1e-12 || !outcome.DescriptiveSupport {
		t.Fatalf("survivor outcome = %+v", outcome)
	}
}

func TestOfficialHoldoutArtifactsPassProvenanceAndSafetyGates(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	resultRoot := filepath.Join(repoRoot, "results", "2026-09-08-v3-survivor-holdout")
	protocol, err := loadJSON[protocolDocument](filepath.Join(repoRoot, "experiments", "protocol-v3-survivor-holdout.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadJSON[validationManifest](filepath.Join(resultRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	shared, err := loadJSON[sharedSummary](filepath.Join(repoRoot, "results", "2026-09-07-v3-shared-validation", "analysis", "validation-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	weights, err := evaluator.LoadExperiment(filepath.Join(repoRoot, "experiments", "weights-v3-holdout-survivors.json"))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := loadJSON[evaluator.BatchResult](filepath.Join(resultRoot, "mixed-burst-holdout.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProvenance(protocol, manifest, shared, weights, batch, repoRoot, filepath.Join(resultRoot, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	built, err := buildOutput(protocol, weights, shared, batch)
	if err != nil {
		t.Fatal(err)
	}
	if built.Acceptance.ResultRows != 60 || built.Acceptance.Pods != 1440 || built.Acceptance.Unschedulable != 0 || len(built.Policies) != 6 || built.Survivors.FrozenSurvivors != 3 {
		t.Fatalf("unexpected official holdout shape: acceptance=%+v policies=%d survivors=%+v", built.Acceptance, len(built.Policies), built.Survivors)
	}
}

func TestExactCommandRecordsAllInputsAndOutputs(t *testing.T) {
	opts := options{inputPath: "input.json", protocolPath: "protocol.json", manifestPath: "manifest.json", sharedPath: "shared.json", weightsPath: "weights.json", outputPath: "summary.json", csvOutputPath: "summary.csv"}
	want := "go run ./cmd/v3-holdout-summary --input input.json --protocol protocol.json --manifest manifest.json --shared-summary shared.json --weights weights.json --output summary.json --csv-output summary.csv"
	if got := exactCommand(opts); got != want {
		t.Fatalf("exactCommand() = %q, want %q", got, want)
	}
}

func policyWithObjectives(name, arm string, baseline bool, latency, nodes float64) policySummary {
	return policySummary{Name: name, Arm: arm, Baseline: baseline, FrozenSurvivor: !baseline, P95DecisionLatency: statistics{Mean: latency}, MaxOccupiedWorkers: statistics{Mean: nodes}}
}
