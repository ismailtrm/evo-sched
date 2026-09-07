package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

const (
	officialManifestSchemaVersion = "v1"
	officialProtocolVersion       = "v3-budget-matched-resampling-official"
	officialProtocolStatus        = "official-frozen-before-execution"
	officialProtocolTag           = "protocol-v3-official-search-2026-09-07"
	officialRunName               = "2026-09-07-v3-budget-matched-official-search"
	officialWorkloads             = "../workloads/v2/cpu-tight-train.json,../workloads/v2/mem-tight-train.json,../workloads/v2/fragmenting-mix-train.json"
	officialBaselineFile          = "../experiments/weights-v2.json"
	canonicalWeights              = "0.25,0.25,0.25,0.25"
)

type options struct {
	runName             string
	outputRoot          string
	protocolTag         string
	protocolFile        string
	provenanceFiles     string
	schedulerNamespace  string
	schedulerDeployment string
	evaluatorNamespace  string
	image               string
	restoreTimeout      time.Duration
	expectedWorkers     int
	experimentInterval  time.Duration
	idleInterval        time.Duration
}

type armSpec struct {
	Name                       string               `json:"name"`
	SearchMode                 optimizer.SearchMode `json:"searchMode"`
	Population                 int                  `json:"population"`
	Generations                int                  `json:"generations"`
	OffspringPerGeneration     int                  `json:"offspringPerGeneration"`
	PromotionsPerGeneration    int                  `json:"promotionsPerGeneration"`
	AdditionalSamples          int                  `json:"additionalSamplesPerPromotion"`
	OptimizerSampleBudget      int                  `json:"optimizerSampleBudget"`
	ExpectedOptimizerSamples   int                  `json:"expectedOptimizerSamples"`
	ExpectedBaselineSamples    int                  `json:"expectedBaselineSamples"`
	ExpectedPolicySamples      int                  `json:"expectedPolicySamples"`
	ExpectedUniqueCandidates   int                  `json:"expectedUniqueCandidates"`
	ExpectedPromotions         int                  `json:"expectedPromotions"`
	ExpectedWorkloadExecutions int                  `json:"expectedWorkloadExecutions"`
	ExpectedPods               int                  `json:"expectedPods"`
}

type executionSpec struct {
	Seed int64   `json:"seed"`
	Arm  armSpec `json:"arm"`
}

type executionKey struct {
	Seed int64  `json:"seed"`
	Arm  string `json:"arm"`
}

type gitState struct {
	SHA          string `json:"sha"`
	Branch       string `json:"branch"`
	ProtocolTag  string `json:"protocolTag"`
	TagAnnotated bool   `json:"tagAnnotated"`
	DirtyAtStart bool   `json:"dirtyAtStart"`
}

type inputArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type executionArtifacts struct {
	ChildManifestPath   string
	ChildManifestSHA256 string
	LauncherLogPath     string
	LauncherLogSHA256   string
}

type executionRun struct {
	Seed                int64      `json:"seed"`
	Arm                 armSpec    `json:"arm"`
	Status              string     `json:"status"`
	StartedAt           time.Time  `json:"startedAt"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
	ChildManifestPath   string     `json:"childManifestPath,omitempty"`
	ChildManifestSHA256 string     `json:"childManifestSha256,omitempty"`
	LauncherLogPath     string     `json:"launcherLogPath,omitempty"`
	LauncherLogSHA256   string     `json:"launcherLogSha256,omitempty"`
	Error               string     `json:"error,omitempty"`
}

type finalSnapshot struct {
	CapturedAt                time.Time           `json:"capturedAt"`
	WorkerCount               int                 `json:"workerCount"`
	SchedulerRolloutReady     bool                `json:"schedulerRolloutReady"`
	SchedulerIntervalSeconds  float64             `json:"schedulerIntervalSeconds"`
	SchedulerCanonicalWeights evaluator.WeightSet `json:"schedulerCanonicalWeights"`
	EvaluatorPodCount         int                 `json:"evaluatorPodCount"`
}

type restorationRecord struct {
	Status     string        `json:"status"`
	StartedAt  time.Time     `json:"startedAt"`
	FinishedAt *time.Time    `json:"finishedAt,omitempty"`
	Snapshot   finalSnapshot `json:"snapshot"`
	Error      string        `json:"error,omitempty"`
}

type combinedBudget struct {
	OptimizerSamples   int `json:"optimizerSamples"`
	BaselineSamples    int `json:"baselineSamples"`
	PolicySamples      int `json:"policySamples"`
	WorkloadExecutions int `json:"workloadExecutions"`
	Pods               int `json:"pods"`
}

type officialManifest struct {
	SchemaVersion          string            `json:"schemaVersion"`
	ProtocolVersion        string            `json:"protocolVersion"`
	Class                  string            `json:"class"`
	PaperEvidenceEligible  bool              `json:"paperEvidenceEligible"`
	RunName                string            `json:"runName"`
	Status                 string            `json:"status"`
	StartedAt              time.Time         `json:"startedAt"`
	FinishedAt             *time.Time        `json:"finishedAt,omitempty"`
	Repository             string            `json:"repository"`
	WorkingDirectory       string            `json:"workingDirectory"`
	Command                []string          `json:"command"`
	Git                    gitState          `json:"git"`
	Inputs                 []inputArtifact   `json:"inputs"`
	ExecutionOrder         []executionKey    `json:"executionOrder"`
	ExpectedCombinedBudget combinedBudget    `json:"expectedCombinedBudget"`
	Executions             []executionRun    `json:"executions"`
	FinalRestoration       restorationRecord `json:"finalRestoration"`
	Failure                string            `json:"failure,omitempty"`
}

type childRun struct {
	Seed                     int64  `json:"seed"`
	Status                   string `json:"status"`
	ExpectedCandidates       int    `json:"expectedCandidates"`
	ExpectedWorkloadRuns     int    `json:"expectedWorkloadRuns"`
	ExpectedOptimizerSamples int    `json:"expectedOptimizerSamples"`
	ExpectedBaselineSamples  int    `json:"expectedBaselineSamples"`
	ExpectedPromotionEvents  int    `json:"expectedPromotionEvents"`
	ExpectedPolicySamples    int    `json:"expectedPolicySamples"`
	ExpectedPods             int    `json:"expectedPods"`
}

type childManifest struct {
	ProtocolVersion       string `json:"protocolVersion"`
	Class                 string `json:"class"`
	PaperEvidenceEligible bool   `json:"paperEvidenceEligible"`
	Status                string `json:"status"`
	Git                   struct {
		SHA          string `json:"sha"`
		ProtocolTag  string `json:"protocolTag"`
		TagAnnotated bool   `json:"tagAnnotated"`
		DirtyAtStart bool   `json:"dirtyAtStart"`
	} `json:"git"`
	Search struct {
		Seeds                      []int64              `json:"seeds"`
		SearchMode                 optimizer.SearchMode `json:"searchMode"`
		ExpectedCandidates         int                  `json:"expectedCandidatesPerSeed"`
		ExpectedOptimizerSamples   int                  `json:"expectedOptimizerSamplesPerSeed"`
		ExpectedBaselineSamples    int                  `json:"expectedBaselineSamplesPerSeed"`
		ExpectedPromotionEvents    int                  `json:"expectedPromotionEventsPerSeed"`
		ExpectedPolicySamples      int                  `json:"expectedPolicySamplesPerSeed"`
		ExpectedWorkloadExecutions int                  `json:"expectedWorkloadRunsPerSeed"`
		ExpectedPods               int                  `json:"expectedPodsPerSeed"`
	} `json:"search"`
	Runs []childRun `json:"runs"`
}

type protocolContract struct {
	ProtocolVersion string `json:"protocolVersion"`
	Status          string `json:"status"`
	Budget          struct {
		OptimizerSamplesPerArmSeed   int `json:"optimizerSampleBudgetPerArmSeed"`
		BaselineSamplesPerArmSeed    int `json:"baselineSampleBudgetPerArmSeed"`
		PolicySamplesPerArmSeed      int `json:"totalPolicySamplesPerArmSeed"`
		WorkloadExecutionsPerArmSeed int `json:"totalWorkloadExecutionsPerArmSeed"`
		PodsPerArmSeed               int `json:"totalPodsPerArmSeed"`
		ArmSeedCount                 int `json:"armSeedCount"`
		OfficialWorkloadExecutions   int `json:"officialSearchWorkloadExecutions"`
		OfficialPods                 int `json:"officialSearchPods"`
	} `json:"budgetDefinition"`
	Official struct {
		Class                                string  `json:"class"`
		PaperEvidenceEligibleAtRunCompletion bool    `json:"paperEvidenceEligibleAtRunCompletion"`
		ProtocolTag                          string  `json:"protocolTag"`
		RunName                              string  `json:"runName"`
		Seeds                                []int64 `json:"seeds"`
		ArmExecutionOrder                    []struct {
			Seed  int64    `json:"seed"`
			Order []string `json:"order"`
		} `json:"armExecutionOrder"`
	} `json:"official"`
}

func main() {
	opts, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultOptions() options {
	return options{
		runName:             officialRunName,
		outputRoot:          "/tmp/evo-sched-v3-official",
		protocolTag:         officialProtocolTag,
		protocolFile:        "../experiments/protocol-v3-budget-matched-resampling.json",
		provenanceFiles:     "../kind-config-4workers.yaml,../deploy.yaml,../experiments/protocol-v3-budget-matched-resampling.json",
		schedulerNamespace:  "evo-sched",
		schedulerDeployment: "evok8s-scheduler",
		evaluatorNamespace:  "evo-sched-eval",
		image:               "busybox:1.36.1",
		restoreTimeout:      3 * time.Minute,
		expectedWorkers:     4,
		experimentInterval:  250 * time.Millisecond,
		idleInterval:        2 * time.Second,
	}
}

func parseFlags(args []string) (options, error) {
	opts := defaultOptions()
	flags := flag.NewFlagSet("v3-official-runner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.runName, "run-name", opts.runName, "New V3 official directory name below --output-root")
	flags.StringVar(&opts.outputRoot, "output-root", opts.outputRoot, "Official output root outside the Git repository")
	flags.StringVar(&opts.protocolTag, "protocol-tag", opts.protocolTag, "Required annotated official protocol tag at HEAD")
	flags.StringVar(&opts.protocolFile, "protocol-file", opts.protocolFile, "Committed V3 protocol path relative to scheduler/")
	flags.StringVar(&opts.provenanceFiles, "provenance-files", opts.provenanceFiles, "Child-runner provenance paths")
	flags.StringVar(&opts.schedulerNamespace, "scheduler-namespace", opts.schedulerNamespace, "Scheduler deployment namespace")
	flags.StringVar(&opts.schedulerDeployment, "scheduler-deployment", opts.schedulerDeployment, "Scheduler deployment name")
	flags.StringVar(&opts.evaluatorNamespace, "evaluator-namespace", opts.evaluatorNamespace, "Evaluator workload namespace")
	flags.StringVar(&opts.image, "image", opts.image, "Evaluator Pod image")
	flags.DurationVar(&opts.restoreTimeout, "restore-timeout", opts.restoreTimeout, "Final cleanup and idle-cadence timeout")
	flags.IntVar(&opts.expectedWorkers, "expect-workers", opts.expectedWorkers, "Required ready worker count")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	return opts, nil
}

func validateOptions(opts options) error {
	if opts.runName != officialRunName {
		return fmt.Errorf("--run-name must be frozen official name %q", officialRunName)
	}
	if opts.protocolTag != officialProtocolTag {
		return fmt.Errorf("--protocol-tag must be frozen official tag %q", officialProtocolTag)
	}
	if strings.TrimSpace(opts.outputRoot) == "" || strings.TrimSpace(opts.protocolFile) == "" {
		return errors.New("--output-root and --protocol-file are required")
	}
	if strings.TrimSpace(opts.provenanceFiles) == "" || strings.TrimSpace(opts.schedulerNamespace) == "" ||
		strings.TrimSpace(opts.schedulerDeployment) == "" || strings.TrimSpace(opts.evaluatorNamespace) == "" ||
		strings.TrimSpace(opts.image) == "" {
		return errors.New("provenance, Kubernetes object names, and image are required")
	}
	if opts.restoreTimeout <= 0 || opts.expectedWorkers <= 0 || opts.experimentInterval <= 0 || opts.idleInterval <= 0 {
		return errors.New("timeouts, worker count, and scheduler intervals must be positive")
	}
	return nil
}

func officialArmSpecs() map[string]armSpec {
	return map[string]armSpec{
		"one-shot-control": {
			Name:                       "one-shot-control",
			SearchMode:                 optimizer.SearchModeBudgetOneShot,
			Population:                 12,
			Generations:                5,
			OffspringPerGeneration:     12,
			OptimizerSampleBudget:      72,
			ExpectedOptimizerSamples:   72,
			ExpectedBaselineSamples:    3,
			ExpectedPolicySamples:      75,
			ExpectedUniqueCandidates:   75,
			ExpectedWorkloadExecutions: 225,
			ExpectedPods:               5400,
		},
		"two-stage-resampling": {
			Name:                       "two-stage-resampling",
			SearchMode:                 optimizer.SearchModeBudgetTwoStage,
			Population:                 12,
			Generations:                5,
			OffspringPerGeneration:     6,
			PromotionsPerGeneration:    2,
			AdditionalSamples:          3,
			OptimizerSampleBudget:      72,
			ExpectedOptimizerSamples:   72,
			ExpectedBaselineSamples:    3,
			ExpectedPolicySamples:      75,
			ExpectedUniqueCandidates:   45,
			ExpectedPromotions:         10,
			ExpectedWorkloadExecutions: 225,
			ExpectedPods:               5400,
		},
	}
}

func officialExecutionPlan() []executionSpec {
	arms := officialArmSpecs()
	return []executionSpec{
		{Seed: 42, Arm: arms["one-shot-control"]},
		{Seed: 42, Arm: arms["two-stage-resampling"]},
		{Seed: 123, Arm: arms["two-stage-resampling"]},
		{Seed: 123, Arm: arms["one-shot-control"]},
		{Seed: 999, Arm: arms["one-shot-control"]},
		{Seed: 999, Arm: arms["two-stage-resampling"]},
	}
}

func run(ctx context.Context, opts options) error {
	if err := validateOptions(opts); err != nil {
		return err
	}
	schedulerDir, repoRoot, git, err := inspectGit(opts.protocolTag)
	if err != nil {
		return err
	}
	topRoot := opts.outputRoot
	if !filepath.IsAbs(topRoot) {
		topRoot = filepath.Join(schedulerDir, topRoot)
	}
	topRoot = filepath.Clean(filepath.Join(topRoot, opts.runName))
	if pathWithin(repoRoot, topRoot) {
		return fmt.Errorf("official output must be outside the Git repository: %s", topRoot)
	}
	if _, err := os.Stat(topRoot); err == nil {
		return fmt.Errorf("official directory already exists: %s", topRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect official directory: %w", err)
	}

	plan := officialExecutionPlan()
	protocolPath := resolvePath(schedulerDir, opts.protocolFile)
	if err := validateProtocolContract(protocolPath, plan, opts.protocolTag, opts.runName); err != nil {
		return fmt.Errorf("official protocol gate: %w", err)
	}
	inputs, err := inspectInputs(schedulerDir, repoRoot, opts)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(topRoot, "logs"), 0o755); err != nil {
		return fmt.Errorf("create official directory: %w", err)
	}

	order := make([]executionKey, 0, len(plan))
	for _, execution := range plan {
		order = append(order, executionKey{Seed: execution.Seed, Arm: execution.Arm.Name})
	}
	manifest := officialManifest{
		SchemaVersion:          officialManifestSchemaVersion,
		ProtocolVersion:        officialProtocolVersion,
		Class:                  "official",
		PaperEvidenceEligible:  false,
		RunName:                opts.runName,
		Status:                 "running",
		StartedAt:              time.Now().UTC(),
		Repository:             repoRoot,
		WorkingDirectory:       schedulerDir,
		Command:                buildInvocation(opts),
		Git:                    git,
		Inputs:                 inputs,
		ExecutionOrder:         order,
		ExpectedCombinedBudget: combinedExpectedBudget(plan),
	}
	manifestPath := filepath.Join(topRoot, "manifest.json")
	if err := writeManifest(manifestPath, manifest); err != nil {
		return err
	}

	execute := func(runCtx context.Context, execution executionSpec) (executionArtifacts, error) {
		return executeOfficial(runCtx, schedulerDir, git, opts, execution, topRoot)
	}
	restore := func() (finalSnapshot, error) {
		return restoreFinalState(opts)
	}
	if err := runOfficialSequence(ctx, manifestPath, &manifest, plan, execute, restore); err != nil {
		return err
	}
	fmt.Printf("completed V3 counterbalanced official search: %s\n", topRoot)
	return nil
}

func buildInvocation(opts options) []string {
	return []string{
		"go", "run", "./cmd/v3-official-runner",
		"--run-name", opts.runName,
		"--output-root", opts.outputRoot,
		"--protocol-tag", opts.protocolTag,
		"--protocol-file", opts.protocolFile,
		"--provenance-files", opts.provenanceFiles,
		"--scheduler-namespace", opts.schedulerNamespace,
		"--scheduler-deployment", opts.schedulerDeployment,
		"--evaluator-namespace", opts.evaluatorNamespace,
		"--image", opts.image,
		"--restore-timeout", opts.restoreTimeout.String(),
		"--expect-workers", strconv.Itoa(opts.expectedWorkers),
	}
}

func runOfficialSequence(
	ctx context.Context,
	manifestPath string,
	manifest *officialManifest,
	plan []executionSpec,
	execute func(context.Context, executionSpec) (executionArtifacts, error),
	restore func() (finalSnapshot, error),
) error {
	var runErr error
	for _, execution := range plan {
		startedAt := time.Now().UTC()
		manifest.Executions = append(manifest.Executions, executionRun{
			Seed: execution.Seed, Arm: execution.Arm, Status: "running", StartedAt: startedAt,
		})
		if err := writeManifest(manifestPath, *manifest); err != nil {
			runErr = err
			break
		}

		artifacts, err := execute(ctx, execution)
		finishedAt := time.Now().UTC()
		current := &manifest.Executions[len(manifest.Executions)-1]
		current.FinishedAt = &finishedAt
		current.ChildManifestPath = artifacts.ChildManifestPath
		current.ChildManifestSHA256 = artifacts.ChildManifestSHA256
		current.LauncherLogPath = artifacts.LauncherLogPath
		current.LauncherLogSHA256 = artifacts.LauncherLogSHA256
		if err != nil {
			if ctx.Err() != nil {
				current.Status = "interrupted"
				manifest.Status = "interrupted"
			} else {
				current.Status = "failed"
				manifest.Status = "failed"
			}
			current.Error = err.Error()
			manifest.Failure = fmt.Sprintf("seed %d arm %s: %v", execution.Seed, execution.Arm.Name, err)
			runErr = err
			_ = writeManifest(manifestPath, *manifest)
			break
		}
		current.Status = "completed"
		if err := writeManifest(manifestPath, *manifest); err != nil {
			runErr = err
			break
		}
	}
	if runErr != nil && manifest.Status == "running" {
		manifest.Status = "failed"
		manifest.Failure = runErr.Error()
	}

	manifest.FinalRestoration = restorationRecord{Status: "running", StartedAt: time.Now().UTC()}
	_ = writeManifest(manifestPath, *manifest)
	snapshot, restoreErr := restore()
	restoredAt := time.Now().UTC()
	manifest.FinalRestoration.FinishedAt = &restoredAt
	manifest.FinalRestoration.Snapshot = snapshot
	if restoreErr != nil {
		manifest.FinalRestoration.Status = "failed"
		manifest.FinalRestoration.Error = restoreErr.Error()
		if runErr == nil {
			manifest.Status = "failed"
			manifest.Failure = fmt.Sprintf("final restoration: %v", restoreErr)
		}
	} else {
		manifest.FinalRestoration.Status = "completed"
	}

	finishedAt := time.Now().UTC()
	manifest.FinishedAt = &finishedAt
	if runErr == nil && restoreErr == nil {
		manifest.Status = "completed"
	}
	if err := writeManifest(manifestPath, *manifest); err != nil {
		return errors.Join(runErr, restoreErr, err)
	}
	return errors.Join(runErr, restoreErr)
}

func buildExecutionArgs(opts options, execution executionSpec, topRoot string) []string {
	outputRoot := filepath.Join(topRoot, fmt.Sprintf("seed-%d", execution.Seed))
	arm := execution.Arm
	return []string{
		"run", "./cmd/multi-seed-runner",
		"--run-name", arm.Name,
		"--seeds", strconv.FormatInt(execution.Seed, 10),
		"--workloads", officialWorkloads,
		"--baseline-file", officialBaselineFile,
		"--provenance-files", opts.provenanceFiles,
		"--output-root", outputRoot,
		"--protocol-tag", opts.protocolTag,
		"--class", "official",
		"--protocol-version", officialProtocolVersion,
		"--scheduler-namespace", opts.schedulerNamespace,
		"--scheduler-deployment", opts.schedulerDeployment,
		"--evaluator-namespace", opts.evaluatorNamespace,
		"--image", opts.image,
		"--population", strconv.Itoa(arm.Population),
		"--generations", strconv.Itoa(arm.Generations),
		"--mutation-rate", "0.8",
		"--mutation-sigma", "0.25",
		"--min-weight", "0",
		"--max-weight", "2",
		"--poll-interval", "250ms",
		"--timeout", "90s",
		"--expect-workers", strconv.Itoa(opts.expectedWorkers),
		"--expect-scheduler-interval", opts.experimentInterval.String(),
		"--expect-weights", canonicalWeights,
		"--search-mode", string(arm.SearchMode),
		"--offspring-per-generation", strconv.Itoa(arm.OffspringPerGeneration),
		"--promotions-per-generation", strconv.Itoa(arm.PromotionsPerGeneration),
		"--additional-samples-per-promotion", strconv.Itoa(arm.AdditionalSamples),
		"--optimizer-sample-budget", strconv.Itoa(arm.OptimizerSampleBudget),
	}
}

func combinedExpectedBudget(plan []executionSpec) combinedBudget {
	var total combinedBudget
	for _, execution := range plan {
		arm := execution.Arm
		total.OptimizerSamples += arm.ExpectedOptimizerSamples
		total.BaselineSamples += arm.ExpectedBaselineSamples
		total.PolicySamples += arm.ExpectedPolicySamples
		total.WorkloadExecutions += arm.ExpectedWorkloadExecutions
		total.Pods += arm.ExpectedPods
	}
	return total
}

func executeOfficial(
	ctx context.Context,
	schedulerDir string,
	git gitState,
	opts options,
	execution executionSpec,
	topRoot string,
) (executionArtifacts, error) {
	args := buildExecutionArgs(opts, execution, topRoot)
	logPath := filepath.Join(topRoot, "logs", fmt.Sprintf("seed-%d-%s.log", execution.Seed, execution.Arm.Name))
	logFile, err := os.Create(logPath)
	if err != nil {
		return executionArtifacts{}, fmt.Errorf("create execution launcher log: %w", err)
	}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = schedulerDir
	cmd.Stdout = io.MultiWriter(os.Stdout, logFile)
	cmd.Stderr = io.MultiWriter(os.Stderr, logFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 15 * time.Second
	runErr := cmd.Run()
	closeErr := logFile.Close()
	childPath := filepath.Join(topRoot, fmt.Sprintf("seed-%d", execution.Seed), execution.Arm.Name, "manifest.json")
	artifacts := executionArtifacts{
		ChildManifestPath: relativePath(topRoot, childPath),
		LauncherLogPath:   relativePath(topRoot, logPath),
	}
	if digest, err := sha256File(logPath); err == nil {
		artifacts.LauncherLogSHA256 = digest
	}
	if digest, err := sha256File(childPath); err == nil {
		artifacts.ChildManifestSHA256 = digest
	}
	if runErr != nil {
		return artifacts, fmt.Errorf("child runner failed: %w", runErr)
	}
	if closeErr != nil {
		return artifacts, fmt.Errorf("close execution launcher log: %w", closeErr)
	}
	if err := validateChildManifest(childPath, git, opts.protocolTag, execution); err != nil {
		return artifacts, err
	}
	digest, err := sha256File(childPath)
	if err != nil {
		return artifacts, err
	}
	artifacts.ChildManifestSHA256 = digest
	return artifacts, nil
}

func validateChildManifest(path string, git gitState, protocolTag string, execution executionSpec) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read child manifest: %w", err)
	}
	var child childManifest
	if err := json.Unmarshal(data, &child); err != nil {
		return fmt.Errorf("decode child manifest: %w", err)
	}
	if child.Status != "completed" || child.Class != "official" || child.PaperEvidenceEligible {
		return fmt.Errorf("child evidence state status=%q class=%q eligible=%t", child.Status, child.Class, child.PaperEvidenceEligible)
	}
	if child.ProtocolVersion != officialProtocolVersion {
		return fmt.Errorf("child protocol=%q, want %q", child.ProtocolVersion, officialProtocolVersion)
	}
	if child.Git.SHA != git.SHA || child.Git.ProtocolTag != protocolTag || !child.Git.TagAnnotated || child.Git.DirtyAtStart {
		return errors.New("child Git provenance does not match clean tagged official commit")
	}
	arm := execution.Arm
	if len(child.Search.Seeds) != 1 || child.Search.Seeds[0] != execution.Seed || child.Search.SearchMode != arm.SearchMode {
		return fmt.Errorf("child seed/search mode does not match seed %d arm %q", execution.Seed, arm.Name)
	}
	if child.Search.ExpectedCandidates != arm.ExpectedUniqueCandidates ||
		child.Search.ExpectedOptimizerSamples != arm.ExpectedOptimizerSamples ||
		child.Search.ExpectedBaselineSamples != arm.ExpectedBaselineSamples ||
		child.Search.ExpectedPromotionEvents != arm.ExpectedPromotions ||
		child.Search.ExpectedPolicySamples != arm.ExpectedPolicySamples ||
		child.Search.ExpectedWorkloadExecutions != arm.ExpectedWorkloadExecutions ||
		child.Search.ExpectedPods != arm.ExpectedPods {
		return fmt.Errorf("child search budget does not match seed %d arm %q", execution.Seed, arm.Name)
	}
	if len(child.Runs) != 1 {
		return fmt.Errorf("child run count=%d, want 1", len(child.Runs))
	}
	run := child.Runs[0]
	if run.Seed != execution.Seed || run.Status != "completed" ||
		run.ExpectedCandidates != arm.ExpectedUniqueCandidates ||
		run.ExpectedOptimizerSamples != arm.ExpectedOptimizerSamples ||
		run.ExpectedBaselineSamples != arm.ExpectedBaselineSamples ||
		run.ExpectedPromotionEvents != arm.ExpectedPromotions ||
		run.ExpectedPolicySamples != arm.ExpectedPolicySamples ||
		run.ExpectedWorkloadRuns != arm.ExpectedWorkloadExecutions ||
		run.ExpectedPods != arm.ExpectedPods {
		return fmt.Errorf("child run budget/status does not match seed %d arm %q", execution.Seed, arm.Name)
	}
	return nil
}

func validateProtocolContract(path string, plan []executionSpec, protocolTag, runName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read protocol: %w", err)
	}
	var protocol protocolContract
	if err := json.Unmarshal(data, &protocol); err != nil {
		return fmt.Errorf("decode protocol: %w", err)
	}
	if protocol.ProtocolVersion != officialProtocolVersion {
		return fmt.Errorf("protocol version=%q, want %q", protocol.ProtocolVersion, officialProtocolVersion)
	}
	if protocol.Status != officialProtocolStatus {
		return fmt.Errorf("protocol status=%q, want %q", protocol.Status, officialProtocolStatus)
	}
	if protocol.Official.Class != "official" || protocol.Official.PaperEvidenceEligibleAtRunCompletion {
		return fmt.Errorf(
			"protocol official evidence state class=%q eligible=%t",
			protocol.Official.Class,
			protocol.Official.PaperEvidenceEligibleAtRunCompletion,
		)
	}
	if protocol.Official.ProtocolTag != protocolTag || protocol.Official.RunName != runName {
		return fmt.Errorf(
			"protocol identity tag=%q run=%q, want tag=%q run=%q",
			protocol.Official.ProtocolTag,
			protocol.Official.RunName,
			protocolTag,
			runName,
		)
	}
	if len(plan) == 0 {
		return errors.New("official execution plan is empty")
	}
	first := plan[0].Arm
	for _, execution := range plan[1:] {
		arm := execution.Arm
		if arm.ExpectedOptimizerSamples != first.ExpectedOptimizerSamples ||
			arm.ExpectedBaselineSamples != first.ExpectedBaselineSamples ||
			arm.ExpectedPolicySamples != first.ExpectedPolicySamples ||
			arm.ExpectedWorkloadExecutions != first.ExpectedWorkloadExecutions ||
			arm.ExpectedPods != first.ExpectedPods {
			return errors.New("official plan arm budgets are not equal")
		}
	}
	combined := combinedExpectedBudget(plan)
	budget := protocol.Budget
	if budget.OptimizerSamplesPerArmSeed != first.ExpectedOptimizerSamples ||
		budget.BaselineSamplesPerArmSeed != first.ExpectedBaselineSamples ||
		budget.PolicySamplesPerArmSeed != first.ExpectedPolicySamples ||
		budget.WorkloadExecutionsPerArmSeed != first.ExpectedWorkloadExecutions ||
		budget.PodsPerArmSeed != first.ExpectedPods ||
		budget.ArmSeedCount != len(plan) ||
		budget.OfficialWorkloadExecutions != combined.WorkloadExecutions ||
		budget.OfficialPods != combined.Pods {
		return fmt.Errorf("protocol budget does not match launcher plan")
	}

	wantSeeds := make([]int64, 0, len(protocol.Official.Seeds))
	wantOrder := make([]executionKey, 0, len(plan))
	for _, execution := range plan {
		if len(wantSeeds) == 0 || wantSeeds[len(wantSeeds)-1] != execution.Seed {
			wantSeeds = append(wantSeeds, execution.Seed)
		}
		wantOrder = append(wantOrder, executionKey{Seed: execution.Seed, Arm: execution.Arm.Name})
	}
	if !equalInt64s(protocol.Official.Seeds, wantSeeds) {
		return fmt.Errorf("protocol seeds=%v, want %v", protocol.Official.Seeds, wantSeeds)
	}
	gotOrder := make([]executionKey, 0, len(plan))
	for _, seedOrder := range protocol.Official.ArmExecutionOrder {
		for _, arm := range seedOrder.Order {
			gotOrder = append(gotOrder, executionKey{Seed: seedOrder.Seed, Arm: arm})
		}
	}
	if !equalExecutionKeys(gotOrder, wantOrder) {
		return fmt.Errorf("protocol execution order=%v, want %v", gotOrder, wantOrder)
	}
	return nil
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalExecutionKeys(left, right []executionKey) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func restoreFinalState(opts options) (finalSnapshot, error) {
	clientset, err := evaluator.NewClientset()
	if err != nil {
		return finalSnapshot{}, fmt.Errorf("Kubernetes client init: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.restoreTimeout)
	defer cancel()

	weights, err := evaluator.ParseWeightSet(canonicalWeights)
	if err != nil {
		return finalSnapshot{}, err
	}
	var restoreErr error
	if err := evaluator.CleanupManagedPods(ctx, clientset, opts.evaluatorNamespace); err != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("cleanup evaluator pods: %w", err))
	}
	if err := evaluator.ApplyDeploymentWeights(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment, weights); err != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("restore scheduler weights: %w", err))
	}
	if err := evaluator.ApplyDeploymentSchedulerInterval(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment, opts.idleInterval); err != nil {
		restoreErr = errors.Join(restoreErr, fmt.Errorf("restore scheduler interval: %w", err))
	}
	snapshot, err := captureFinalSnapshot(ctx, clientset, opts)
	if err != nil {
		return finalSnapshot{}, errors.Join(restoreErr, fmt.Errorf("capture final snapshot: %w", err))
	}
	if err := validateFinalSnapshot(snapshot, opts, weights); err != nil {
		restoreErr = errors.Join(restoreErr, err)
	}
	return snapshot, restoreErr
}

func captureFinalSnapshot(ctx context.Context, clientset kubernetes.Interface, opts options) (finalSnapshot, error) {
	base, err := evaluator.CurrentWorkloadBase(ctx, clientset)
	if err != nil {
		return finalSnapshot{}, err
	}
	weights, err := evaluator.CurrentDeploymentWeights(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return finalSnapshot{}, err
	}
	interval, err := evaluator.CurrentDeploymentSchedulerInterval(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return finalSnapshot{}, err
	}
	deployment, err := clientset.AppsV1().Deployments(opts.schedulerNamespace).Get(ctx, opts.schedulerDeployment, metav1.GetOptions{})
	if err != nil {
		return finalSnapshot{}, err
	}
	pods, err := clientset.CoreV1().Pods(opts.evaluatorNamespace).List(ctx, metav1.ListOptions{})
	if apierrors.IsNotFound(err) {
		pods = nil
		err = nil
	}
	if err != nil {
		return finalSnapshot{}, err
	}
	podCount := 0
	if pods != nil {
		podCount = len(pods.Items)
	}
	return finalSnapshot{
		CapturedAt:                time.Now().UTC(),
		WorkerCount:               base.WorkerCount,
		SchedulerRolloutReady:     deploymentReady(deployment),
		SchedulerIntervalSeconds:  interval.Seconds(),
		SchedulerCanonicalWeights: weights,
		EvaluatorPodCount:         podCount,
	}, nil
}

func validateFinalSnapshot(snapshot finalSnapshot, opts options, weights evaluator.WeightSet) error {
	if snapshot.WorkerCount != opts.expectedWorkers || !snapshot.SchedulerRolloutReady || snapshot.EvaluatorPodCount != 0 {
		return fmt.Errorf("final cluster state workers=%d rollout=%t evaluatorPods=%d", snapshot.WorkerCount, snapshot.SchedulerRolloutReady, snapshot.EvaluatorPodCount)
	}
	if math.Abs(snapshot.SchedulerIntervalSeconds-opts.idleInterval.Seconds()) > 1e-9 {
		return fmt.Errorf("final scheduler interval=%gs, want %s", snapshot.SchedulerIntervalSeconds, opts.idleInterval)
	}
	if !weightSetsClose(snapshot.SchedulerCanonicalWeights, weights) {
		return fmt.Errorf("final scheduler weights=%+v, want %+v", snapshot.SchedulerCanonicalWeights, weights)
	}
	return nil
}

func deploymentReady(deployment *appsv1.Deployment) bool {
	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}
	return deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.Replicas == replicas && deployment.Status.UpdatedReplicas == replicas &&
		deployment.Status.ReadyReplicas == replicas && deployment.Status.AvailableReplicas == replicas &&
		deployment.Status.UnavailableReplicas == 0
}

func inspectGit(protocolTag string) (string, string, gitState, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", gitState{}, err
	}
	repoRoot, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", gitState{}, err
	}
	status, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		return "", "", gitState{}, err
	}
	if status != "" {
		return "", "", gitState{}, errors.New("official run requires a clean Git tree")
	}
	sha, err := gitOutput(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", "", gitState{}, err
	}
	branch, err := gitOutput(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", gitState{}, err
	}
	tagSHA, err := gitOutput(repoRoot, "rev-list", "-n", "1", protocolTag)
	if err != nil || tagSHA != sha {
		return "", "", gitState{}, fmt.Errorf("protocol tag %q must point at HEAD", protocolTag)
	}
	tagType, err := gitOutput(repoRoot, "cat-file", "-t", "refs/tags/"+protocolTag)
	if err != nil || tagType != "tag" {
		return "", "", gitState{}, fmt.Errorf("protocol tag %q must be annotated", protocolTag)
	}
	return filepath.Join(repoRoot, "scheduler"), repoRoot, gitState{
		SHA: sha, Branch: branch, ProtocolTag: protocolTag, TagAnnotated: true, DirtyAtStart: false,
	}, nil
}

func inspectInputs(schedulerDir, repoRoot string, opts options) ([]inputArtifact, error) {
	paths := []string{opts.protocolFile, officialBaselineFile}
	paths = append(paths, splitCSV(officialWorkloads)...)
	paths = append(paths, splitCSV(opts.provenanceFiles)...)
	seen := make(map[string]struct{}, len(paths))
	artifacts := make([]inputArtifact, 0, len(paths))
	for _, path := range paths {
		absolute := resolvePath(schedulerDir, path)
		if _, exists := seen[absolute]; exists {
			continue
		}
		seen[absolute] = struct{}{}
		digest, err := sha256File(absolute)
		if err != nil {
			return nil, fmt.Errorf("input %s: %w", path, err)
		}
		artifacts = append(artifacts, inputArtifact{Path: relativePath(repoRoot, absolute), SHA256: digest})
	}
	return artifacts, nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func writeManifest(path string, manifest officialManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}

func relativePath(base, path string) string {
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}

func pathWithin(base, path string) bool {
	relative, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func weightSetsClose(left, right evaluator.WeightSet) bool {
	const epsilon = 1e-12
	return math.Abs(left.FreeCPU-right.FreeCPU) < epsilon &&
		math.Abs(left.FreeMem-right.FreeMem) < epsilon &&
		math.Abs(left.Balance-right.Balance) < epsilon &&
		math.Abs(left.Consolidation-right.Consolidation) < epsilon
}
