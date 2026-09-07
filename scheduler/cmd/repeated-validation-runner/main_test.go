package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

func TestExpandRunSpecsUsesRepetitionMajorRoundRobinOrder(t *testing.T) {
	specs := []evaluator.ExperimentRunSpec{{Name: "a"}, {Name: "b"}}
	got, err := expandRunSpecs(specs, 2, executionOrderFixed)
	if err != nil {
		t.Fatalf("expandRunSpecs() error = %v", err)
	}
	want := []string{"a-r01", "b-r01", "a-r02", "b-r02"}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("run %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestExpandRunSpecsUsesCyclicRotationAcrossRepetitions(t *testing.T) {
	specs := []evaluator.ExperimentRunSpec{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got, err := expandRunSpecs(specs, 3, executionOrderCyclicRotation)
	if err != nil {
		t.Fatalf("expandRunSpecs() error = %v", err)
	}
	want := []string{"a-r01", "b-r01", "c-r01", "b-r02", "c-r02", "a-r02", "c-r03", "a-r03", "b-r03"}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("run %d = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestBuildInvocationRecordsEffectiveProtocolAndOrder(t *testing.T) {
	opts := options{
		runName: "v3-validation", workloads: "a.json,b.json", weightsFile: "weights.json",
		protocolTag: "protocol-v3-validation", protocolVersion: "v3-validation-official",
		protocolFile: "protocol.json", outputRoot: "/tmp/results", provenanceFiles: "protocol.json", repetitions: 10,
		pollInterval: 250 * time.Millisecond, timeout: 90 * time.Second, expectedWorkers: 4,
		expectedSchedulerInterval: 250 * time.Millisecond, finalSchedulerInterval: 2 * time.Second,
		expectedWeights: "0.25,0.25,0.25,0.25", executionOrder: executionOrderCyclicRotation,
		schedulerNamespace: "evo-sched", schedulerDeployment: "evok8s-scheduler",
		evaluatorNamespace: "evo-sched-eval", schedulerName: "evok8s-scheduler", image: "busybox:1.36.1",
	}
	got := buildInvocation(opts)
	joined := strings.Join(got, " ")
	for _, expected := range []string{"go run ./cmd/repeated-validation-runner", "--protocol-version v3-validation-official", "--protocol-file protocol.json", "--execution-order cyclic-rotation", "--repetitions 10"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("invocation %q does not contain %q", joined, expected)
		}
	}
}

func TestBuildInvocationOmitsOptionalProtocolFileForV2Compatibility(t *testing.T) {
	opts := options{protocolVersion: "v2-official-repeated-validation"}
	if got := strings.Join(buildInvocation(opts), " "); strings.Contains(got, "--protocol-file") || strings.Contains(got, "--protocol-contract") {
		t.Fatalf("V2-compatible invocation unexpectedly contains an optional protocol selector: %s", got)
	}
}

func TestBuildInvocationIncludesExplicitHoldoutContract(t *testing.T) {
	opts := options{
		protocolFile:     "protocol.json",
		protocolContract: protocolContractHoldout,
	}
	got := strings.Join(buildInvocation(opts), " ")
	if !strings.Contains(got, "--protocol-file protocol.json --protocol-contract holdout") {
		t.Fatalf("holdout invocation does not contain the explicit contract selector: %s", got)
	}
}

func TestSelectProtocolContract(t *testing.T) {
	document := validationProtocolDocument{}
	document.SharedValidation.Official.RunName = "shared"
	document.Holdout.Official.RunName = "holdout"

	shared, err := selectProtocolContract(document, protocolContractSharedValidation)
	if err != nil || shared.RunName != "shared" {
		t.Fatalf("select shared-validation = %+v, %v", shared, err)
	}
	holdout, err := selectProtocolContract(document, protocolContractHoldout)
	if err != nil || holdout.RunName != "holdout" {
		t.Fatalf("select holdout = %+v, %v", holdout, err)
	}
	if _, err := selectProtocolContract(document, "unknown"); err == nil {
		t.Fatal("select unknown contract error = nil")
	}
}

func TestValidateOptionsRejectsHoldoutWithoutProtocolFile(t *testing.T) {
	opts := options{
		runName: "holdout", weightsFile: "weights.json", protocolTag: "protocol-holdout",
		protocolVersion: "holdout-v1", protocolContract: protocolContractHoldout,
		executionOrder: executionOrderFixed, repetitions: 10, expectedWorkers: 4,
		pollInterval: time.Second, timeout: time.Second,
		expectedSchedulerInterval: time.Second, finalSchedulerInterval: time.Second,
	}
	if err := validateOptions(opts); err == nil || !strings.Contains(err.Error(), "requires --protocol-file") {
		t.Fatalf("validateOptions() error = %v, want missing protocol file rejection", err)
	}
}

func TestValidateOptionsRejectsUnknownProtocolContract(t *testing.T) {
	opts := options{
		runName: "validation", weightsFile: "weights.json", protocolTag: "protocol-validation",
		protocolVersion: "validation-v1", protocolContract: "unknown",
		executionOrder: executionOrderFixed, repetitions: 10, expectedWorkers: 4,
		pollInterval: time.Second, timeout: time.Second,
		expectedSchedulerInterval: time.Second, finalSchedulerInterval: time.Second,
	}
	if err := validateOptions(opts); err == nil || !strings.Contains(err.Error(), "must be") {
		t.Fatalf("validateOptions() error = %v, want unknown contract rejection", err)
	}
}

func TestValidateProtocolContractAcceptsExactFrozenBudget(t *testing.T) {
	repoRoot := t.TempDir()
	schedulerDir := filepath.Join(repoRoot, "scheduler")
	workloadDir := filepath.Join(repoRoot, "workloads")
	protocolDir := filepath.Join(repoRoot, "experiments")
	for _, dir := range []string{schedulerDir, workloadDir, protocolDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	workloadPath := filepath.Join(workloadDir, "training.json")
	if err := os.WriteFile(workloadPath, []byte(`[{"name":"a"},{"name":"b"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := options{
		runName: "v3-validation", workloads: "../workloads/training.json", weightsFile: "../experiments/weights.json",
		protocolTag: "protocol-v3-validation", protocolVersion: "v3-validation-official",
		protocolFile: "../experiments/protocol.json", executionOrder: executionOrderCyclicRotation,
		outputRoot: filepath.Join(repoRoot, "results"), provenanceFiles: "../kind.yaml,../deploy.yaml", repetitions: 2,
		pollInterval: 250 * time.Millisecond, timeout: 90 * time.Second, expectedWorkers: 4,
		expectedSchedulerInterval: 250 * time.Millisecond, finalSchedulerInterval: 2 * time.Second,
		expectedWeights: "0.25,0.25,0.25,0.25", schedulerNamespace: "evo-sched",
		schedulerDeployment: "evok8s-scheduler", evaluatorNamespace: "evo-sched-eval",
		schedulerName: "evok8s-scheduler", image: "busybox:1.36.1",
	}
	document := validationProtocolDocument{}
	contract := &document.SharedValidation.Official
	contract.Status = "official-frozen-before-execution"
	contract.Class = "official"
	contract.PaperEvidenceEligibleAtRunCompletion = true
	contract.ProtocolVersion = opts.protocolVersion
	contract.ProtocolTag = opts.protocolTag
	contract.RunName = opts.runName
	contract.Command = buildInvocation(opts)
	contract.TemporaryOutputRoot = opts.outputRoot
	contract.WeightsFile = "experiments/weights.json"
	contract.Workloads = []string{"workloads/training.json"}
	contract.Policies = []string{"baseline", "one-shot", "two-stage"}
	contract.ProvenanceFiles = []string{"kind.yaml", "deploy.yaml"}
	contract.ExecutionOrder = opts.executionOrder
	contract.PolicyCount = 3
	contract.BaselinePolicyCount = 1
	contract.OptimizerFinalistCount = 2
	contract.RepetitionsPerPolicyWorkload = opts.repetitions
	contract.ExpectedWorkloadExecutions = 6
	contract.ExpectedPods = 12
	contract.PollInterval = opts.pollInterval.String()
	contract.PerPolicyWorkloadTimeout = opts.timeout.String()
	contract.ExpectedWorkers = opts.expectedWorkers
	contract.ExperimentSchedulerInterval = opts.expectedSchedulerInterval.String()
	contract.RestoredIdleSchedulerInterval = opts.finalSchedulerInterval.String()
	contract.CanonicalWeights = opts.expectedWeights
	contract.SchedulerNamespace = opts.schedulerNamespace
	contract.SchedulerDeployment = opts.schedulerDeployment
	contract.EvaluatorNamespace = opts.evaluatorNamespace
	contract.SchedulerName = opts.schedulerName
	contract.Image = opts.image
	protocolPath := filepath.Join(protocolDir, "protocol.json")
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protocolPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	policies := []evaluator.ExperimentRunSpec{{Name: "baseline"}, {Name: "one-shot"}, {Name: "two-stage"}}
	gotPods, err := validateProtocolContract(protocolPath, schedulerDir, repoRoot, opts, []string{"../workloads/training.json"}, policies)
	if err != nil {
		t.Fatalf("validateProtocolContract() error = %v", err)
	}
	if gotPods != 12 {
		t.Fatalf("validateProtocolContract() Pods = %d, want 12", gotPods)
	}

	document.SharedValidation.Official.ExpectedPods = 11
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protocolPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateProtocolContract(protocolPath, schedulerDir, repoRoot, opts, []string{"../workloads/training.json"}, policies); err == nil || !strings.Contains(err.Error(), "Pod executions") {
		t.Fatalf("validateProtocolContract(Pod drift) error = %v", err)
	}
}

func TestFrozenV3ValidationProtocolMatchesRepositoryArtifacts(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	schedulerDir := filepath.Join(repoRoot, "scheduler")
	workloads := []string{
		"../workloads/v2/cpu-tight-train.json",
		"../workloads/v2/mem-tight-train.json",
		"../workloads/v2/fragmenting-mix-train.json",
	}
	opts := options{
		runName:         "2026-09-07-v3-shared-validation",
		workloads:       strings.Join(workloads, ","),
		weightsFile:     "../experiments/weights-v3-validation-finalists.json",
		protocolTag:     "protocol-v3-shared-validation-2026-09-07",
		protocolVersion: "v3-budget-matched-resampling-shared-validation-official",
		protocolFile:    "../experiments/protocol-v3-budget-matched-resampling.json",
		executionOrder:  executionOrderCyclicRotation,
		outputRoot:      "/tmp/evo-sched-v3-validation",
		provenanceFiles: strings.Join([]string{
			"../kind-config-4workers.yaml",
			"../deploy.yaml",
			"../results/2026-09-07-v3-budget-matched-official-search/manifest.json",
			"../results/2026-09-07-v3-budget-matched-official-search/analysis/search-summary.json",
		}, ","),
		repetitions: 10, pollInterval: 250 * time.Millisecond, timeout: 90 * time.Second,
		expectedWorkers: 4, expectedSchedulerInterval: 250 * time.Millisecond,
		finalSchedulerInterval: 2 * time.Second, expectedWeights: "0.25,0.25,0.25,0.25",
		schedulerNamespace: "evo-sched", schedulerDeployment: "evok8s-scheduler",
		evaluatorNamespace: "evo-sched-eval", schedulerName: "evok8s-scheduler", image: "busybox:1.36.1",
	}
	spec, err := evaluator.LoadExperiment(resolvePath(schedulerDir, opts.weightsFile))
	if err != nil {
		t.Fatal(err)
	}
	gotPods, err := validateProtocolContract(resolvePath(schedulerDir, opts.protocolFile), schedulerDir, repoRoot, opts, workloads, spec.Runs)
	if err != nil {
		t.Fatalf("validateProtocolContract(frozen V3) error = %v", err)
	}
	if gotPods != 6480 {
		t.Fatalf("frozen V3 validation Pods = %d, want 6480", gotPods)
	}
}

func TestExpandRunSpecsRejectsUnknownExecutionOrder(t *testing.T) {
	_, err := expandRunSpecs([]evaluator.ExperimentRunSpec{{Name: "a"}}, 2, "random")
	if err == nil {
		t.Fatal("expandRunSpecs() error = nil, want unsupported order rejection")
	}
}

func TestValidateBatchRequiresCompleteRows(t *testing.T) {
	batch := evaluator.BatchResult{
		PolicyVersion:      evaluator.PolicyVersion,
		MeasurementVersion: evaluator.MeasurementVersion,
		Workload:           "cpu-tight-train",
		Results: []evaluator.ComparisonRow{
			{
				PodCount:                           24,
				ScheduledCount:                     24,
				CompletedCount:                     24,
				DecisionTimestampCount:             24,
				KubernetesScheduledTimestampCount:  24,
				EvaluatorObservationTimestampCount: 24,
			},
		},
	}
	if err := validateBatch(batch, 1); err != nil {
		t.Fatalf("validateBatch() error = %v", err)
	}

	batch.Results[0].CompletedCount = 23
	if err := validateBatch(batch, 1); err == nil {
		t.Fatal("validateBatch() error = nil, want incomplete row rejection")
	}
}

func TestParseSeedsAreNotPartOfRepeatedValidation(t *testing.T) {
	if defaultRepetitions < 10 {
		t.Fatalf("defaultRepetitions = %d, want at least 10", defaultRepetitions)
	}
}
