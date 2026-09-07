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

func TestOfficialExecutionPlanFreezesCounterbalancedOrderAndBudget(t *testing.T) {
	plan := officialExecutionPlan()
	wantOrder := []executionKey{
		{Seed: 42, Arm: "one-shot-control"},
		{Seed: 42, Arm: "two-stage-resampling"},
		{Seed: 123, Arm: "two-stage-resampling"},
		{Seed: 123, Arm: "one-shot-control"},
		{Seed: 999, Arm: "one-shot-control"},
		{Seed: 999, Arm: "two-stage-resampling"},
	}
	gotOrder := make([]executionKey, 0, len(plan))
	for _, execution := range plan {
		gotOrder = append(gotOrder, executionKey{Seed: execution.Seed, Arm: execution.Arm.Name})
		arm := execution.Arm
		if arm.ExpectedOptimizerSamples != 72 || arm.ExpectedBaselineSamples != 3 ||
			arm.ExpectedPolicySamples != 75 || arm.ExpectedWorkloadExecutions != 225 ||
			arm.ExpectedPods != 5400 {
			t.Fatalf("execution %+v has unexpected fixed budget: %+v", executionKey{Seed: execution.Seed, Arm: arm.Name}, arm)
		}
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("official execution order = %+v, want %+v", gotOrder, wantOrder)
	}

	wantBudget := combinedBudget{
		OptimizerSamples:   432,
		BaselineSamples:    18,
		PolicySamples:      450,
		WorkloadExecutions: 1350,
		Pods:               32400,
	}
	if got := combinedExpectedBudget(plan); got != wantBudget {
		t.Fatalf("combined official budget = %+v, want %+v", got, wantBudget)
	}
}

func TestBuildExecutionArgsCarriesFrozenOfficialContract(t *testing.T) {
	opts := defaultOptions()
	topRoot := filepath.Join(opts.outputRoot, "official-test")
	execution := officialExecutionPlan()[2]
	args := strings.Join(buildExecutionArgs(opts, execution, topRoot), " ")
	for _, required := range []string{
		"run ./cmd/multi-seed-runner",
		"--run-name two-stage-resampling",
		"--seeds 123",
		"--class official",
		"--protocol-version v3-budget-matched-resampling-official",
		"--protocol-tag " + officialProtocolTag,
		"--search-mode budget-two-stage",
		"--population 12",
		"--generations 5",
		"--offspring-per-generation 6",
		"--promotions-per-generation 2",
		"--additional-samples-per-promotion 3",
		"--optimizer-sample-budget 72",
		"--poll-interval 250ms",
		"--timeout 90s",
		"--expect-workers 4",
		"--expect-scheduler-interval 250ms",
		"--expect-weights 0.25,0.25,0.25,0.25",
		"--output-root " + filepath.Join(topRoot, "seed-123"),
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("official execution args missing %q:\n%s", required, args)
		}
	}
}

func TestValidateOptionsRejectsOfficialIdentityDrift(t *testing.T) {
	valid := defaultOptions()
	if err := validateOptions(valid); err != nil {
		t.Fatalf("validateOptions(valid) error = %v", err)
	}

	wrongTag := valid
	wrongTag.protocolTag = "protocol-v3-other"
	if err := validateOptions(wrongTag); err == nil || !strings.Contains(err.Error(), "protocol-tag") {
		t.Fatalf("validateOptions(wrong tag) error = %v", err)
	}

	wrongRun := valid
	wrongRun.runName = "other-run"
	if err := validateOptions(wrongRun); err == nil || !strings.Contains(err.Error(), "run-name") {
		t.Fatalf("validateOptions(wrong run) error = %v", err)
	}
}

func TestRunOfficialSequenceRestoresIdleStateAfterExecutionFailure(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	manifest := officialManifest{
		SchemaVersion:         officialManifestSchemaVersion,
		ProtocolVersion:       officialProtocolVersion,
		Class:                 "official",
		PaperEvidenceEligible: false,
		Status:                "running",
		StartedAt:             time.Now().UTC(),
	}
	plan := officialExecutionPlan()
	var order []executionKey
	restored := false
	execute := func(_ context.Context, item executionSpec) (executionArtifacts, error) {
		key := executionKey{Seed: item.Seed, Arm: item.Arm.Name}
		order = append(order, key)
		if key == (executionKey{Seed: 123, Arm: "two-stage-resampling"}) {
			return executionArtifacts{}, errors.New("synthetic execution failure")
		}
		return executionArtifacts{
			ChildManifestPath:   filepath.Join("seed", item.Arm.Name, "manifest.json"),
			ChildManifestSHA256: strings.Repeat("a", 64),
			LauncherLogPath:     filepath.Join("logs", item.Arm.Name+".log"),
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

	err := runOfficialSequence(context.Background(), manifestPath, &manifest, plan, execute, restore)
	if err == nil || !strings.Contains(err.Error(), "synthetic execution failure") {
		t.Fatalf("runOfficialSequence() error = %v", err)
	}
	if !restored {
		t.Fatal("final restoration was not attempted")
	}
	wantOrder := []executionKey{
		{Seed: 42, Arm: "one-shot-control"},
		{Seed: 42, Arm: "two-stage-resampling"},
		{Seed: 123, Arm: "two-stage-resampling"},
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("executed order = %+v, want %+v", order, wantOrder)
	}
	if manifest.Status != "failed" || len(manifest.Executions) != 3 {
		t.Fatalf("unexpected failed manifest: %+v", manifest)
	}
	if manifest.Executions[0].Status != "completed" || manifest.Executions[2].Status != "failed" {
		t.Fatalf("unexpected execution statuses: %+v", manifest.Executions)
	}
	if manifest.FinalRestoration.Status != "completed" || manifest.FinalRestoration.Snapshot.SchedulerIntervalSeconds != 2 {
		t.Fatalf("unexpected restoration record: %+v", manifest.FinalRestoration)
	}
}

func TestValidateChildManifestEnforcesOfficialEvidenceAndBudgetContract(t *testing.T) {
	execution := officialExecutionPlan()[2]
	git := gitState{SHA: strings.Repeat("c", 40), ProtocolTag: "protocol-v3-official-test", TagAnnotated: true}
	child := childManifest{
		ProtocolVersion:       officialProtocolVersion,
		Class:                 "official",
		PaperEvidenceEligible: false,
		Status:                "completed",
	}
	child.Git.SHA = git.SHA
	child.Git.ProtocolTag = git.ProtocolTag
	child.Git.TagAnnotated = true
	child.Search.Seeds = []int64{execution.Seed}
	child.Search.SearchMode = execution.Arm.SearchMode
	child.Search.ExpectedCandidates = execution.Arm.ExpectedUniqueCandidates
	child.Search.ExpectedOptimizerSamples = execution.Arm.ExpectedOptimizerSamples
	child.Search.ExpectedBaselineSamples = execution.Arm.ExpectedBaselineSamples
	child.Search.ExpectedPromotionEvents = execution.Arm.ExpectedPromotions
	child.Search.ExpectedPolicySamples = execution.Arm.ExpectedPolicySamples
	child.Search.ExpectedWorkloadExecutions = execution.Arm.ExpectedWorkloadExecutions
	child.Search.ExpectedPods = execution.Arm.ExpectedPods
	child.Runs = []childRun{{
		Seed:                     execution.Seed,
		Status:                   "completed",
		ExpectedCandidates:       execution.Arm.ExpectedUniqueCandidates,
		ExpectedWorkloadRuns:     execution.Arm.ExpectedWorkloadExecutions,
		ExpectedOptimizerSamples: execution.Arm.ExpectedOptimizerSamples,
		ExpectedBaselineSamples:  execution.Arm.ExpectedBaselineSamples,
		ExpectedPromotionEvents:  execution.Arm.ExpectedPromotions,
		ExpectedPolicySamples:    execution.Arm.ExpectedPolicySamples,
		ExpectedPods:             execution.Arm.ExpectedPods,
	}}

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
	if err := validateChildManifest(path, git, git.ProtocolTag, execution); err != nil {
		t.Fatalf("validateChildManifest(valid) error = %v", err)
	}

	child.Search.ExpectedPods--
	writeChild()
	if err := validateChildManifest(path, git, git.ProtocolTag, execution); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("validateChildManifest(budget drift) error = %v", err)
	}
}

func TestValidateProtocolContractRejectsDraftAndExecutionOrderDrift(t *testing.T) {
	plan := officialExecutionPlan()
	document := map[string]any{
		"protocolVersion": officialProtocolVersion,
		"status":          officialProtocolStatus,
		"budgetDefinition": map[string]any{
			"optimizerSampleBudgetPerArmSeed":   72,
			"baselineSampleBudgetPerArmSeed":    3,
			"totalPolicySamplesPerArmSeed":      75,
			"totalWorkloadExecutionsPerArmSeed": 225,
			"totalPodsPerArmSeed":               5400,
			"armSeedCount":                      6,
			"officialSearchWorkloadExecutions":  1350,
			"officialSearchPods":                32400,
		},
		"official": map[string]any{
			"class":                                "official",
			"paperEvidenceEligibleAtRunCompletion": false,
			"protocolTag":                          officialProtocolTag,
			"runName":                              officialRunName,
			"seeds":                                []int64{42, 123, 999},
			"armExecutionOrder": []map[string]any{
				{"seed": int64(42), "order": []string{"one-shot-control", "two-stage-resampling"}},
				{"seed": int64(123), "order": []string{"two-stage-resampling", "one-shot-control"}},
				{"seed": int64(999), "order": []string{"one-shot-control", "two-stage-resampling"}},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "protocol.json")
	writeDocument := func() {
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeDocument()
	if err := validateProtocolContract(path, plan, officialProtocolTag, officialRunName); err != nil {
		t.Fatalf("validateProtocolContract(valid) error = %v", err)
	}

	document["status"] = "design-committed-before-implementation"
	writeDocument()
	if err := validateProtocolContract(path, plan, officialProtocolTag, officialRunName); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("validateProtocolContract(draft) error = %v", err)
	}

	document["status"] = officialProtocolStatus
	official := document["official"].(map[string]any)
	official["armExecutionOrder"] = []map[string]any{
		{"seed": int64(42), "order": []string{"two-stage-resampling", "one-shot-control"}},
		{"seed": int64(123), "order": []string{"two-stage-resampling", "one-shot-control"}},
		{"seed": int64(999), "order": []string{"one-shot-control", "two-stage-resampling"}},
	}
	writeDocument()
	if err := validateProtocolContract(path, plan, officialProtocolTag, officialRunName); err == nil || !strings.Contains(err.Error(), "execution order") {
		t.Fatalf("validateProtocolContract(order drift) error = %v", err)
	}

	official["armExecutionOrder"] = []map[string]any{
		{"seed": int64(42), "order": []string{"one-shot-control", "two-stage-resampling"}},
		{"seed": int64(123), "order": []string{"two-stage-resampling", "one-shot-control"}},
		{"seed": int64(999), "order": []string{"one-shot-control", "two-stage-resampling"}},
	}
	budget := document["budgetDefinition"].(map[string]any)
	budget["officialSearchPods"] = 32399
	writeDocument()
	if err := validateProtocolContract(path, plan, officialProtocolTag, officialRunName); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("validateProtocolContract(budget drift) error = %v", err)
	}

	budget["officialSearchPods"] = 32400
	official["runName"] = "wrong-run"
	writeDocument()
	if err := validateProtocolContract(path, plan, officialProtocolTag, officialRunName); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("validateProtocolContract(identity drift) error = %v", err)
	}
}

func TestRepositoryProtocolMatchesOfficialLauncherContract(t *testing.T) {
	path := filepath.Join("..", "..", "..", "experiments", "protocol-v3-budget-matched-resampling.json")
	if err := validateProtocolContract(path, officialExecutionPlan(), officialProtocolTag, officialRunName); err != nil {
		t.Fatalf("repository protocol does not match official launcher: %v", err)
	}
}
