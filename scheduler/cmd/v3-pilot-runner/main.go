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
	pilotManifestSchemaVersion = "v1"
	pilotProtocolVersion       = "v3-budget-matched-resampling-pilot"
	pilotSeed                  = int64(20260831)
	pilotWorkloads             = "../workloads/v2/cpu-tight-train.json,../workloads/v2/mem-tight-train.json,../workloads/v2/fragmenting-mix-train.json"
	pilotBaselineFile          = "../experiments/weights-v2.json"
	canonicalWeights           = "0.25,0.25,0.25,0.25"
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

type armExecution struct {
	ChildManifestPath   string
	ChildManifestSHA256 string
	LauncherLogPath     string
	LauncherLogSHA256   string
}

type armRun struct {
	Spec                armSpec    `json:"spec"`
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

type pilotManifest struct {
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
	ArmOrder               []string          `json:"armOrder"`
	ExpectedCombinedBudget combinedBudget    `json:"expectedCombinedBudget"`
	Arms                   []armRun          `json:"arms"`
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

func main() {
	opts := parseFlags()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func defaultOptions() options {
	return options{
		outputRoot:          "/tmp/evo-sched-v3-pilot",
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

func parseFlags() options {
	opts := defaultOptions()
	flag.StringVar(&opts.runName, "run-name", "", "New V3 pilot directory name below --output-root")
	flag.StringVar(&opts.outputRoot, "output-root", opts.outputRoot, "Pilot output root outside the Git repository")
	flag.StringVar(&opts.protocolTag, "protocol-tag", "", "Required annotated pilot protocol tag at HEAD")
	flag.StringVar(&opts.protocolFile, "protocol-file", opts.protocolFile, "Committed V3 protocol path relative to scheduler/")
	flag.StringVar(&opts.provenanceFiles, "provenance-files", opts.provenanceFiles, "Child-runner provenance paths")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", opts.schedulerNamespace, "Scheduler deployment namespace")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", opts.schedulerDeployment, "Scheduler deployment name")
	flag.StringVar(&opts.evaluatorNamespace, "evaluator-namespace", opts.evaluatorNamespace, "Evaluator workload namespace")
	flag.StringVar(&opts.image, "image", opts.image, "Evaluator Pod image")
	flag.DurationVar(&opts.restoreTimeout, "restore-timeout", opts.restoreTimeout, "Final cleanup and idle-cadence timeout")
	flag.IntVar(&opts.expectedWorkers, "expect-workers", opts.expectedWorkers, "Required ready worker count")
	flag.Parse()
	return opts
}

func validateOptions(opts options) error {
	if strings.TrimSpace(opts.runName) == "" || strings.Contains(opts.runName, string(filepath.Separator)) {
		return errors.New("--run-name must be a single non-empty path component")
	}
	if strings.TrimSpace(opts.protocolTag) == "" {
		return errors.New("--protocol-tag is required")
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

func pilotArmSpecs() []armSpec {
	return []armSpec{
		{
			Name:                       "one-shot-control",
			SearchMode:                 optimizer.SearchModeBudgetOneShot,
			Population:                 4,
			Generations:                1,
			OffspringPerGeneration:     4,
			OptimizerSampleBudget:      8,
			ExpectedOptimizerSamples:   8,
			ExpectedBaselineSamples:    3,
			ExpectedPolicySamples:      11,
			ExpectedUniqueCandidates:   11,
			ExpectedWorkloadExecutions: 33,
			ExpectedPods:               792,
		},
		{
			Name:                       "two-stage-resampling",
			SearchMode:                 optimizer.SearchModeBudgetTwoStage,
			Population:                 4,
			Generations:                1,
			OffspringPerGeneration:     2,
			PromotionsPerGeneration:    1,
			AdditionalSamples:          2,
			OptimizerSampleBudget:      8,
			ExpectedOptimizerSamples:   8,
			ExpectedBaselineSamples:    3,
			ExpectedPolicySamples:      11,
			ExpectedUniqueCandidates:   9,
			ExpectedPromotions:         1,
			ExpectedWorkloadExecutions: 33,
			ExpectedPods:               792,
		},
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
		return fmt.Errorf("pilot output must be outside the Git repository: %s", topRoot)
	}
	if _, err := os.Stat(topRoot); err == nil {
		return fmt.Errorf("pilot directory already exists: %s", topRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect pilot directory: %w", err)
	}

	protocolPath := resolvePath(schedulerDir, opts.protocolFile)
	protocolDigest, err := sha256File(protocolPath)
	if err != nil {
		return fmt.Errorf("protocol input: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(topRoot, "logs"), 0o755); err != nil {
		return fmt.Errorf("create pilot directory: %w", err)
	}

	arms := pilotArmSpecs()
	manifest := pilotManifest{
		SchemaVersion:         pilotManifestSchemaVersion,
		ProtocolVersion:       pilotProtocolVersion,
		Class:                 "pilot",
		PaperEvidenceEligible: false,
		RunName:               opts.runName,
		Status:                "running",
		StartedAt:             time.Now().UTC(),
		Repository:            repoRoot,
		WorkingDirectory:      schedulerDir,
		Command:               append([]string(nil), os.Args...),
		Git:                   git,
		Inputs: []inputArtifact{{
			Path:   relativePath(repoRoot, protocolPath),
			SHA256: protocolDigest,
		}},
		ArmOrder:               []string{arms[0].Name, arms[1].Name},
		ExpectedCombinedBudget: combinedExpectedBudget(arms),
	}
	manifestPath := filepath.Join(topRoot, "manifest.json")
	if err := writeManifest(manifestPath, manifest); err != nil {
		return err
	}

	execute := func(runCtx context.Context, arm armSpec) (armExecution, error) {
		return executeArm(runCtx, schedulerDir, git, opts, arm, topRoot)
	}
	restore := func() (finalSnapshot, error) {
		return restoreFinalState(opts)
	}
	if err := runPilotSequence(ctx, manifestPath, &manifest, arms, execute, restore); err != nil {
		return err
	}
	fmt.Printf("completed V3 two-arm pilot: %s\n", topRoot)
	return nil
}

func runPilotSequence(
	ctx context.Context,
	manifestPath string,
	manifest *pilotManifest,
	arms []armSpec,
	execute func(context.Context, armSpec) (armExecution, error),
	restore func() (finalSnapshot, error),
) error {
	var runErr error
	for _, arm := range arms {
		startedAt := time.Now().UTC()
		manifest.Arms = append(manifest.Arms, armRun{Spec: arm, Status: "running", StartedAt: startedAt})
		if err := writeManifest(manifestPath, *manifest); err != nil {
			runErr = err
			break
		}

		execution, err := execute(ctx, arm)
		finishedAt := time.Now().UTC()
		current := &manifest.Arms[len(manifest.Arms)-1]
		current.FinishedAt = &finishedAt
		current.ChildManifestPath = execution.ChildManifestPath
		current.ChildManifestSHA256 = execution.ChildManifestSHA256
		current.LauncherLogPath = execution.LauncherLogPath
		current.LauncherLogSHA256 = execution.LauncherLogSHA256
		if err != nil {
			if ctx.Err() != nil {
				current.Status = "interrupted"
				manifest.Status = "interrupted"
			} else {
				current.Status = "failed"
				manifest.Status = "failed"
			}
			current.Error = err.Error()
			manifest.Failure = fmt.Sprintf("arm %s: %v", arm.Name, err)
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

func buildArmArgs(opts options, arm armSpec, topRoot string) []string {
	return []string{
		"run", "./cmd/multi-seed-runner",
		"--run-name", arm.Name,
		"--seeds", strconv.FormatInt(pilotSeed, 10),
		"--workloads", pilotWorkloads,
		"--baseline-file", pilotBaselineFile,
		"--provenance-files", opts.provenanceFiles,
		"--output-root", topRoot,
		"--protocol-tag", opts.protocolTag,
		"--class", "pilot",
		"--protocol-version", pilotProtocolVersion,
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

func combinedExpectedBudget(arms []armSpec) combinedBudget {
	var total combinedBudget
	for _, arm := range arms {
		total.OptimizerSamples += arm.ExpectedOptimizerSamples
		total.BaselineSamples += arm.ExpectedBaselineSamples
		total.PolicySamples += arm.ExpectedPolicySamples
		total.WorkloadExecutions += arm.ExpectedWorkloadExecutions
		total.Pods += arm.ExpectedPods
	}
	return total
}

func executeArm(
	ctx context.Context,
	schedulerDir string,
	git gitState,
	opts options,
	arm armSpec,
	topRoot string,
) (armExecution, error) {
	args := buildArmArgs(opts, arm, topRoot)
	logPath := filepath.Join(topRoot, "logs", arm.Name+".log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return armExecution{}, fmt.Errorf("create arm launcher log: %w", err)
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
	childPath := filepath.Join(topRoot, arm.Name, "manifest.json")
	execution := armExecution{
		ChildManifestPath: relativePath(topRoot, childPath),
		LauncherLogPath:   relativePath(topRoot, logPath),
	}
	if digest, err := sha256File(logPath); err == nil {
		execution.LauncherLogSHA256 = digest
	}
	if digest, err := sha256File(childPath); err == nil {
		execution.ChildManifestSHA256 = digest
	}
	if runErr != nil {
		return execution, fmt.Errorf("child runner failed: %w", runErr)
	}
	if closeErr != nil {
		return execution, fmt.Errorf("close arm launcher log: %w", closeErr)
	}
	if err := validateChildManifest(childPath, git, opts.protocolTag, arm); err != nil {
		return execution, err
	}
	digest, err := sha256File(childPath)
	if err != nil {
		return execution, err
	}
	execution.ChildManifestSHA256 = digest
	return execution, nil
}

func validateChildManifest(path string, git gitState, protocolTag string, arm armSpec) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read child manifest: %w", err)
	}
	var child childManifest
	if err := json.Unmarshal(data, &child); err != nil {
		return fmt.Errorf("decode child manifest: %w", err)
	}
	if child.Status != "completed" || child.Class != "pilot" || child.PaperEvidenceEligible {
		return fmt.Errorf("child evidence state status=%q class=%q eligible=%t", child.Status, child.Class, child.PaperEvidenceEligible)
	}
	if child.ProtocolVersion != pilotProtocolVersion {
		return fmt.Errorf("child protocol=%q, want %q", child.ProtocolVersion, pilotProtocolVersion)
	}
	if child.Git.SHA != git.SHA || child.Git.ProtocolTag != protocolTag || !child.Git.TagAnnotated || child.Git.DirtyAtStart {
		return fmt.Errorf("child Git provenance does not match clean tagged pilot commit")
	}
	if len(child.Search.Seeds) != 1 || child.Search.Seeds[0] != pilotSeed || child.Search.SearchMode != arm.SearchMode {
		return fmt.Errorf("child seed/search mode does not match arm %q", arm.Name)
	}
	if child.Search.ExpectedCandidates != arm.ExpectedUniqueCandidates ||
		child.Search.ExpectedOptimizerSamples != arm.ExpectedOptimizerSamples ||
		child.Search.ExpectedBaselineSamples != arm.ExpectedBaselineSamples ||
		child.Search.ExpectedPromotionEvents != arm.ExpectedPromotions ||
		child.Search.ExpectedPolicySamples != arm.ExpectedPolicySamples ||
		child.Search.ExpectedWorkloadExecutions != arm.ExpectedWorkloadExecutions ||
		child.Search.ExpectedPods != arm.ExpectedPods {
		return fmt.Errorf("child search budget does not match arm %q", arm.Name)
	}
	if len(child.Runs) != 1 {
		return fmt.Errorf("child run count=%d, want 1", len(child.Runs))
	}
	run := child.Runs[0]
	if run.Seed != pilotSeed || run.Status != "completed" ||
		run.ExpectedCandidates != arm.ExpectedUniqueCandidates ||
		run.ExpectedOptimizerSamples != arm.ExpectedOptimizerSamples ||
		run.ExpectedBaselineSamples != arm.ExpectedBaselineSamples ||
		run.ExpectedPromotionEvents != arm.ExpectedPromotions ||
		run.ExpectedPolicySamples != arm.ExpectedPolicySamples ||
		run.ExpectedWorkloadRuns != arm.ExpectedWorkloadExecutions ||
		run.ExpectedPods != arm.ExpectedPods {
		return fmt.Errorf("child run budget/status does not match arm %q", arm.Name)
	}
	return nil
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
		return "", "", gitState{}, errors.New("pilot requires a clean Git tree")
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
		SHA:          sha,
		Branch:       branch,
		ProtocolTag:  protocolTag,
		TagAnnotated: true,
		DirtyAtStart: false,
	}, nil
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

func writeManifest(path string, manifest pilotManifest) error {
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
