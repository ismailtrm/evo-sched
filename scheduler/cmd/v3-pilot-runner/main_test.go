package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPilotArmSpecsFreezeOrderAndEqualFreshSampleBudgets(t *testing.T) {
	arms := pilotArmSpecs()
	if len(arms) != 2 {
		t.Fatalf("pilot arm count = %d, want 2", len(arms))
	}
	if got, want := []string{arms[0].Name, arms[1].Name}, []string{"one-shot-control", "two-stage-resampling"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pilot arm order = %v, want %v", got, want)
	}
	for _, arm := range arms {
		if arm.ExpectedOptimizerSamples != 8 || arm.ExpectedBaselineSamples != 3 ||
			arm.ExpectedPolicySamples != 11 || arm.ExpectedWorkloadExecutions != 33 ||
			arm.ExpectedPods != 792 {
			t.Fatalf("arm %q has unexpected fixed budget: %+v", arm.Name, arm)
		}
	}
	if arms[0].ExpectedUniqueCandidates != 11 || arms[0].ExpectedPromotions != 0 {
		t.Fatalf("unexpected one-shot contract: %+v", arms[0])
	}
	if arms[1].ExpectedUniqueCandidates != 9 || arms[1].ExpectedPromotions != 1 {
		t.Fatalf("unexpected two-stage contract: %+v", arms[1])
	}
	if got, want := combinedExpectedBudget(arms), (combinedBudget{
		OptimizerSamples: 16, BaselineSamples: 6, PolicySamples: 22,
		WorkloadExecutions: 66, Pods: 1584,
	}); got != want {
		t.Fatalf("combined pilot budget = %+v, want %+v", got, want)
	}
}

func TestBuildArmArgsCarriesFrozenPilotContract(t *testing.T) {
	opts := defaultOptions()
	opts.protocolTag = "protocol-v3-pilot-test"
	topRoot := filepath.Join(opts.outputRoot, "pilot-test")
	args := strings.Join(buildArmArgs(opts, pilotArmSpecs()[1], topRoot), " ")
	for _, required := range []string{
		"run ./cmd/multi-seed-runner",
		"--run-name two-stage-resampling",
		"--seeds 20260831",
		"--class pilot",
		"--protocol-version v3-budget-matched-resampling-pilot",
		"--protocol-tag protocol-v3-pilot-test",
		"--search-mode budget-two-stage",
		"--population 4",
		"--generations 1",
		"--offspring-per-generation 2",
		"--promotions-per-generation 1",
		"--additional-samples-per-promotion 2",
		"--optimizer-sample-budget 8",
		"--poll-interval 250ms",
		"--timeout 90s",
		"--expect-workers 4",
		"--expect-scheduler-interval 250ms",
		"--expect-weights 0.25,0.25,0.25,0.25",
		"--output-root " + topRoot,
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("pilot arm args missing %q:\n%s", required, args)
		}
	}
}

func TestRunPilotSequenceRestoresIdleStateAfterArmFailure(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	manifest := pilotManifest{
		SchemaVersion:         pilotManifestSchemaVersion,
		ProtocolVersion:       pilotProtocolVersion,
		Class:                 "pilot",
		PaperEvidenceEligible: false,
		Status:                "running",
		StartedAt:             time.Now().UTC(),
	}
	var order []string
	restored := false
	execute := func(_ context.Context, arm armSpec) (armExecution, error) {
		order = append(order, arm.Name)
		if arm.Name == "two-stage-resampling" {
			return armExecution{}, errors.New("synthetic arm failure")
		}
		return armExecution{
			ChildManifestPath:   arm.Name + "/manifest.json",
			ChildManifestSHA256: strings.Repeat("a", 64),
			LauncherLogPath:     "logs/" + arm.Name + ".log",
			LauncherLogSHA256:   strings.Repeat("b", 64),
		}, nil
	}
	restore := func() (finalSnapshot, error) {
		restored = true
		return finalSnapshot{
			WorkerCount:              4,
			SchedulerRolloutReady:    true,
			SchedulerIntervalSeconds: 2,
			EvaluatorPodCount:        0,
		}, nil
	}

	err := runPilotSequence(context.Background(), manifestPath, &manifest, pilotArmSpecs(), execute, restore)
	if err == nil || !strings.Contains(err.Error(), "synthetic arm failure") {
		t.Fatalf("runPilotSequence() error = %v", err)
	}
	if !restored {
		t.Fatal("final restoration was not attempted")
	}
	if got, want := order, []string{"one-shot-control", "two-stage-resampling"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("executed arm order = %v, want %v", got, want)
	}
	if manifest.Status != "failed" || len(manifest.Arms) != 2 {
		t.Fatalf("unexpected failed manifest: %+v", manifest)
	}
	if manifest.Arms[0].Status != "completed" || manifest.Arms[1].Status != "failed" {
		t.Fatalf("unexpected arm statuses: %+v", manifest.Arms)
	}
	if manifest.Arms[0].LauncherLogSHA256 != strings.Repeat("b", 64) {
		t.Fatalf("launcher log checksum was not preserved: %+v", manifest.Arms[0])
	}
	if manifest.FinalRestoration.Status != "completed" || manifest.FinalRestoration.Snapshot.SchedulerIntervalSeconds != 2 {
		t.Fatalf("unexpected restoration record: %+v", manifest.FinalRestoration)
	}
}

func TestValidateChildManifestEnforcesEvidenceAndBudgetContract(t *testing.T) {
	arm := pilotArmSpecs()[1]
	git := gitState{SHA: strings.Repeat("c", 40), ProtocolTag: "protocol-v3-pilot-test", TagAnnotated: true}
	child := childManifest{
		ProtocolVersion:       pilotProtocolVersion,
		Class:                 "pilot",
		PaperEvidenceEligible: false,
		Status:                "completed",
	}
	child.Git.SHA = git.SHA
	child.Git.ProtocolTag = git.ProtocolTag
	child.Git.TagAnnotated = true
	child.Search.Seeds = []int64{pilotSeed}
	child.Search.SearchMode = arm.SearchMode
	child.Search.ExpectedCandidates = arm.ExpectedUniqueCandidates
	child.Search.ExpectedOptimizerSamples = arm.ExpectedOptimizerSamples
	child.Search.ExpectedBaselineSamples = arm.ExpectedBaselineSamples
	child.Search.ExpectedPromotionEvents = arm.ExpectedPromotions
	child.Search.ExpectedPolicySamples = arm.ExpectedPolicySamples
	child.Search.ExpectedWorkloadExecutions = arm.ExpectedWorkloadExecutions
	child.Search.ExpectedPods = arm.ExpectedPods
	child.Runs = make([]childRun, 1)
	child.Runs[0].Seed = pilotSeed
	child.Runs[0].Status = "completed"
	child.Runs[0].ExpectedCandidates = arm.ExpectedUniqueCandidates
	child.Runs[0].ExpectedOptimizerSamples = arm.ExpectedOptimizerSamples
	child.Runs[0].ExpectedBaselineSamples = arm.ExpectedBaselineSamples
	child.Runs[0].ExpectedPromotionEvents = arm.ExpectedPromotions
	child.Runs[0].ExpectedPolicySamples = arm.ExpectedPolicySamples
	child.Runs[0].ExpectedWorkloadRuns = arm.ExpectedWorkloadExecutions
	child.Runs[0].ExpectedPods = arm.ExpectedPods

	path := filepath.Join(t.TempDir(), "manifest.json")
	writeChild := func() {
		data, err := json.Marshal(child)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeChild()
	if err := validateChildManifest(path, git, git.ProtocolTag, arm); err != nil {
		t.Fatalf("validateChildManifest(valid) error = %v", err)
	}

	child.Search.ExpectedPods--
	writeChild()
	if err := validateChildManifest(path, git, git.ProtocolTag, arm); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("validateChildManifest(budget drift) error = %v", err)
	}
}

func TestValidateOptionsRejectsUnsafePilotOverrides(t *testing.T) {
	valid := defaultOptions()
	valid.runName = "v3-pilot-test"
	valid.protocolTag = "protocol-v3-pilot-test"
	if err := validateOptions(valid); err != nil {
		t.Fatalf("validateOptions(valid) error = %v", err)
	}

	checks := []struct {
		name string
		edit func(*options)
	}{
		{name: "missing run name", edit: func(opts *options) { opts.runName = "" }},
		{name: "nested run name", edit: func(opts *options) { opts.runName = "nested/name" }},
		{name: "missing tag", edit: func(opts *options) { opts.protocolTag = "" }},
		{name: "non-positive restore timeout", edit: func(opts *options) { opts.restoreTimeout = 0 }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			got := valid
			check.edit(&got)
			if err := validateOptions(got); err == nil {
				t.Fatal("validateOptions() error = nil")
			}
		})
	}
}
