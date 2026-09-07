package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/policy"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const defaultRepetitions = 10

const (
	executionOrderFixed          = "fixed"
	executionOrderCyclicRotation = "cyclic-rotation"
)

type options struct {
	runName                   string
	workloads                 string
	weightsFile               string
	protocolTag               string
	protocolVersion           string
	protocolFile              string
	executionOrder            string
	outputRoot                string
	provenanceFiles           string
	repetitions               int
	pollInterval              time.Duration
	timeout                   time.Duration
	expectedWorkers           int
	expectedSchedulerInterval time.Duration
	finalSchedulerInterval    time.Duration
	expectedWeights           string
	schedulerNamespace        string
	schedulerDeployment       string
	evaluatorNamespace        string
	schedulerName             string
	image                     string
}

type gitState struct {
	SHA          string `json:"sha"`
	Branch       string `json:"branch"`
	ProtocolTag  string `json:"protocolTag"`
	DirtyAtStart bool   `json:"dirtyAtStart"`
}

type artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type snapshot struct {
	CapturedAt               time.Time           `json:"capturedAt"`
	WorkerCount              int                 `json:"workerCount"`
	WorkerCPUMilli           int64               `json:"workerCpuMilli"`
	WorkerMemoryMiB          int64               `json:"workerMemoryMiB"`
	SchedulerRolloutReady    bool                `json:"schedulerRolloutReady"`
	SchedulerIntervalSeconds float64             `json:"schedulerIntervalSeconds"`
	SchedulerWeights         evaluator.WeightSet `json:"schedulerCanonicalWeights"`
	EvaluatorPodCount        int                 `json:"evaluatorPodCount"`
}

type workloadRun struct {
	Workload     string     `json:"workload"`
	Path         string     `json:"path"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
	ExpectedRows int        `json:"expectedRows"`
	ResultRows   int        `json:"resultRows,omitempty"`
	PodCount     int        `json:"podCount,omitempty"`
	Outputs      []artifact `json:"outputs,omitempty"`
	Error        string     `json:"error,omitempty"`
}

type manifest struct {
	SchemaVersion         string        `json:"schemaVersion"`
	ProtocolVersion       string        `json:"protocolVersion"`
	Class                 string        `json:"class"`
	PaperEvidenceEligible bool          `json:"paperEvidenceEligible"`
	RunName               string        `json:"runName"`
	Status                string        `json:"status"`
	StartedAt             time.Time     `json:"startedAt"`
	FinishedAt            *time.Time    `json:"finishedAt,omitempty"`
	Repository            string        `json:"repository"`
	WorkingDirectory      string        `json:"workingDirectory"`
	Git                   gitState      `json:"git"`
	Command               []string      `json:"command"`
	Inputs                []artifact    `json:"inputs"`
	ExecutionOrder        string        `json:"executionOrder"`
	Repetitions           int           `json:"repetitionsPerPolicyWorkload"`
	PolicyCount           int           `json:"policyCount"`
	ExpectedRows          int           `json:"expectedResultRows"`
	ExpectedWorkloadRuns  int           `json:"expectedWorkloadRuns"`
	ExpectedPods          int           `json:"expectedPods,omitempty"`
	ObservedPods          int           `json:"observedPods,omitempty"`
	Preflight             snapshot      `json:"preflight"`
	Runs                  []workloadRun `json:"runs"`
	Postflight            *snapshot     `json:"postflight,omitempty"`
	Failure               string        `json:"failure,omitempty"`
}

type validationProtocolDocument struct {
	SharedValidation struct {
		Official validationProtocolContract `json:"official"`
	} `json:"sharedValidation"`
}

type validationProtocolContract struct {
	Status                               string   `json:"status"`
	Class                                string   `json:"class"`
	PaperEvidenceEligibleAtRunCompletion bool     `json:"paperEvidenceEligibleAtRunCompletion"`
	ProtocolVersion                      string   `json:"protocolVersion"`
	ProtocolTag                          string   `json:"protocolTag"`
	RunName                              string   `json:"runName"`
	TemporaryOutputRoot                  string   `json:"temporaryOutputRoot"`
	WeightsFile                          string   `json:"weightsFile"`
	Workloads                            []string `json:"workloads"`
	ExecutionOrder                       string   `json:"executionOrder"`
	PolicyCount                          int      `json:"policyCount"`
	BaselinePolicyCount                  int      `json:"baselinePolicyCount"`
	OptimizerFinalistCount               int      `json:"optimizerFinalistCount"`
	RepetitionsPerPolicyWorkload         int      `json:"repetitionsPerPolicyWorkload"`
	ExpectedWorkloadExecutions           int      `json:"expectedWorkloadExecutions"`
	ExpectedPods                         int      `json:"expectedPods"`
	PollInterval                         string   `json:"pollInterval"`
	PerPolicyWorkloadTimeout             string   `json:"perPolicyWorkloadTimeout"`
	ExpectedWorkers                      int      `json:"expectedWorkers"`
	ExperimentSchedulerInterval          string   `json:"experimentSchedulerInterval"`
	RestoredIdleSchedulerInterval        string   `json:"restoredIdleSchedulerInterval"`
	CanonicalWeights                     string   `json:"canonicalWeights"`
	SchedulerName                        string   `json:"schedulerName"`
	Image                                string   `json:"image"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, parseFlags()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.runName, "run-name", "", "New run directory name below --output-root")
	flag.StringVar(&opts.workloads, "workloads", "", "Comma-separated workload paths")
	flag.StringVar(&opts.weightsFile, "weights-file", "", "Validation policy file")
	flag.StringVar(&opts.protocolTag, "protocol-tag", "", "Required annotated Git tag at HEAD")
	flag.StringVar(&opts.protocolVersion, "protocol-version", "v2-official-repeated-validation", "Manifest protocol version")
	flag.StringVar(&opts.protocolFile, "protocol-file", "", "Optional machine-readable protocol contract")
	flag.StringVar(&opts.executionOrder, "execution-order", executionOrderFixed, "Policy order across repetitions: fixed or cyclic-rotation")
	flag.StringVar(&opts.outputRoot, "output-root", "../results", "Output root relative to scheduler/")
	flag.StringVar(&opts.provenanceFiles, "provenance-files", "../kind-config-4workers.yaml,../deploy.yaml", "Additional input files")
	flag.IntVar(&opts.repetitions, "repetitions", defaultRepetitions, "Repetitions per policy and workload")
	flag.DurationVar(&opts.pollInterval, "poll-interval", 250*time.Millisecond, "Evaluator Pod polling interval")
	flag.DurationVar(&opts.timeout, "timeout", 90*time.Second, "Per policy-workload timeout")
	flag.IntVar(&opts.expectedWorkers, "expect-workers", 4, "Required ready worker count")
	flag.DurationVar(&opts.expectedSchedulerInterval, "expect-scheduler-interval", 250*time.Millisecond, "Required experiment scheduler interval")
	flag.DurationVar(&opts.finalSchedulerInterval, "final-scheduler-interval", 2*time.Second, "Scheduler interval restored after the run")
	flag.StringVar(&opts.expectedWeights, "expect-weights", "0.25,0.25,0.25,0.25", "Required canonical deployment weights")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", "evo-sched", "Scheduler namespace")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", "evok8s-scheduler", "Scheduler deployment")
	flag.StringVar(&opts.evaluatorNamespace, "evaluator-namespace", "evo-sched-eval", "Evaluator namespace")
	flag.StringVar(&opts.schedulerName, "scheduler-name", "evok8s-scheduler", "Scheduler name assigned to Pods")
	flag.StringVar(&opts.image, "image", "busybox:1.36.1", "Evaluator Pod image")
	flag.Parse()
	return opts
}

func run(ctx context.Context, opts options) (runErr error) {
	workloads, err := splitPaths(opts.workloads)
	if err != nil {
		return fmt.Errorf("--workloads: %w", err)
	}
	provenance, err := splitPaths(opts.provenanceFiles)
	if err != nil {
		return fmt.Errorf("--provenance-files: %w", err)
	}
	expectedWeights, err := parseWeights(opts.expectedWeights)
	if err != nil {
		return fmt.Errorf("--expect-weights: %w", err)
	}
	if err := validateOptions(opts); err != nil {
		return err
	}

	schedulerDir, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	repoRoot, err := gitOutput(schedulerDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	git, err := inspectGit(repoRoot, opts.protocolTag)
	if err != nil {
		return err
	}
	if git.DirtyAtStart {
		return errors.New("official run requires a clean Git tree")
	}

	runRoot := opts.outputRoot
	if !filepath.IsAbs(runRoot) {
		runRoot = filepath.Join(schedulerDir, runRoot)
	}
	runRoot = filepath.Clean(filepath.Join(runRoot, opts.runName))
	if _, err := os.Stat(runRoot); err == nil {
		return fmt.Errorf("run directory already exists: %s", runRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	inputPaths := append(append([]string(nil), workloads...), opts.weightsFile)
	if opts.protocolFile != "" {
		inputPaths = append(inputPaths, opts.protocolFile)
	}
	inputPaths = append(inputPaths, provenance...)
	inputs, err := inspectInputs(schedulerDir, repoRoot, inputPaths)
	if err != nil {
		return err
	}
	weightsPath := resolvePath(schedulerDir, opts.weightsFile)
	spec, err := evaluator.LoadExperiment(weightsPath)
	if err != nil {
		return err
	}
	expectedPods := 0
	if opts.protocolFile != "" {
		expectedPods, err = validateProtocolContract(resolvePath(schedulerDir, opts.protocolFile), schedulerDir, repoRoot, opts, workloads, spec.Runs)
		if err != nil {
			return fmt.Errorf("protocol contract: %w", err)
		}
	}
	expanded, err := expandRunSpecs(spec.Runs, opts.repetitions, opts.executionOrder)
	if err != nil {
		return err
	}

	clientset, err := evaluator.NewClientset()
	if err != nil {
		return fmt.Errorf("Kubernetes client init: %w", err)
	}
	preflight, err := captureSnapshot(ctx, clientset, opts)
	if err != nil {
		return err
	}
	if err := validateSnapshot(preflight, opts.expectedWorkers, opts.expectedSchedulerInterval, expectedWeights); err != nil {
		return fmt.Errorf("preflight failed: %w", err)
	}
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return err
	}
	manifestPath := filepath.Join(runRoot, "manifest.json")
	state := manifest{
		SchemaVersion:        "v1",
		ProtocolVersion:      opts.protocolVersion,
		Class:                "official",
		RunName:              opts.runName,
		Status:               "running",
		StartedAt:            time.Now().UTC(),
		Repository:           repoRoot,
		WorkingDirectory:     schedulerDir,
		Git:                  git,
		Command:              buildInvocation(opts),
		Inputs:               inputs,
		ExecutionOrder:       opts.executionOrder,
		Repetitions:          opts.repetitions,
		PolicyCount:          len(spec.Runs),
		ExpectedRows:         len(expanded) * len(workloads),
		ExpectedWorkloadRuns: len(expanded) * len(workloads),
		ExpectedPods:         expectedPods,
		Preflight:            preflight,
	}
	if err := writeManifest(manifestPath, state); err != nil {
		return err
	}

	defer func() {
		recoveryCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		recovery, recoveryErr := restoreAndSnapshot(recoveryCtx, clientset, opts, expectedWeights)
		finished := time.Now().UTC()
		state.FinishedAt = &finished
		if recoveryErr == nil {
			state.Postflight = &recovery
		}
		if runErr == nil && recoveryErr == nil {
			state.Status = "completed"
			state.PaperEvidenceEligible = true
		} else {
			state.Status = "failed"
			if errors.Is(ctx.Err(), context.Canceled) {
				state.Status = "interrupted"
			}
			if runErr != nil {
				state.Failure = runErr.Error()
			}
			if recoveryErr != nil {
				state.Failure = strings.TrimSpace(state.Failure + "; recovery: " + recoveryErr.Error())
			}
		}
		_ = writeManifest(manifestPath, state)
		if runErr == nil && recoveryErr != nil {
			runErr = recoveryErr
		}
	}()

	runnerOpts := evaluator.DefaultRunnerOptions()
	runnerOpts.Namespace = opts.evaluatorNamespace
	runnerOpts.SchedulerNamespace = opts.schedulerNamespace
	runnerOpts.SchedulerDeployment = opts.schedulerDeployment
	runnerOpts.SchedulerName = opts.schedulerName
	runnerOpts.Image = opts.image
	runnerOpts.PollInterval = opts.pollInterval
	runnerOpts.Timeout = opts.timeout
	runnerOpts.Cleanup = true

	for _, workloadPath := range workloads {
		absoluteWorkload := resolvePath(schedulerDir, workloadPath)
		entry := workloadRun{
			Workload:     strings.TrimSuffix(filepath.Base(workloadPath), filepath.Ext(workloadPath)),
			Path:         relativePath(repoRoot, absoluteWorkload),
			Status:       "running",
			StartedAt:    time.Now().UTC(),
			ExpectedRows: len(expanded),
		}
		state.Runs = append(state.Runs, entry)
		if err := writeManifest(manifestPath, state); err != nil {
			return err
		}

		batch, err := evaluator.RunBatch(ctx, clientset, runnerOpts, absoluteWorkload, expanded)
		current := &state.Runs[len(state.Runs)-1]
		finished := time.Now().UTC()
		current.FinishedAt = &finished
		if emitErr := emitBatch(runRoot, entry.Workload, batch); emitErr != nil && err == nil {
			err = emitErr
		}
		if err == nil {
			err = validateBatch(batch, len(expanded))
		}
		if err != nil {
			current.Status = "failed"
			current.Error = err.Error()
			_ = writeManifest(manifestPath, state)
			return fmt.Errorf("workload %s: %w", entry.Workload, err)
		}
		current.Status = "completed"
		current.ResultRows = len(batch.Results)
		for _, row := range batch.Results {
			current.PodCount += row.PodCount
		}
		state.ObservedPods += current.PodCount
		outputs, err := inspectOutputs(repoRoot, runRoot, entry.Workload)
		if err != nil {
			return err
		}
		current.Outputs = outputs
		if err := writeManifest(manifestPath, state); err != nil {
			return err
		}
	}
	if state.ExpectedPods > 0 && state.ObservedPods != state.ExpectedPods {
		return fmt.Errorf("observed Pods=%d, want %d", state.ObservedPods, state.ExpectedPods)
	}
	return nil
}

func validateOptions(opts options) error {
	if strings.TrimSpace(opts.runName) == "" || strings.Contains(opts.runName, string(filepath.Separator)) {
		return errors.New("--run-name must be a single non-empty path component")
	}
	if strings.TrimSpace(opts.weightsFile) == "" || strings.TrimSpace(opts.protocolTag) == "" {
		return errors.New("--weights-file and --protocol-tag are required")
	}
	if strings.TrimSpace(opts.protocolVersion) == "" {
		return errors.New("--protocol-version is required")
	}
	if opts.executionOrder != executionOrderFixed && opts.executionOrder != executionOrderCyclicRotation {
		return fmt.Errorf("--execution-order must be %q or %q", executionOrderFixed, executionOrderCyclicRotation)
	}
	if opts.repetitions < 2 || opts.expectedWorkers < 1 {
		return errors.New("repetitions must be at least 2 and expected workers must be positive")
	}
	if opts.pollInterval <= 0 || opts.timeout <= 0 || opts.expectedSchedulerInterval <= 0 || opts.finalSchedulerInterval <= 0 {
		return errors.New("poll, timeout, and scheduler intervals must be positive")
	}
	return nil
}

func validateProtocolContract(path, schedulerDir, repoRoot string, opts options, workloads []string, policies []evaluator.ExperimentRunSpec) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	var document validationProtocolDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	contract := document.SharedValidation.Official
	if contract.Status != "official-frozen-before-execution" || contract.Class != "official" || !contract.PaperEvidenceEligibleAtRunCompletion {
		return 0, fmt.Errorf("shared validation is not a frozen paper-eligible official protocol")
	}

	actualStrings := map[string]string{
		"protocolVersion":               opts.protocolVersion,
		"protocolTag":                   opts.protocolTag,
		"runName":                       opts.runName,
		"executionOrder":                opts.executionOrder,
		"pollInterval":                  opts.pollInterval.String(),
		"perPolicyWorkloadTimeout":      opts.timeout.String(),
		"experimentSchedulerInterval":   opts.expectedSchedulerInterval.String(),
		"restoredIdleSchedulerInterval": opts.finalSchedulerInterval.String(),
		"schedulerName":                 opts.schedulerName,
		"image":                         opts.image,
	}
	expectedStrings := map[string]string{
		"protocolVersion":               contract.ProtocolVersion,
		"protocolTag":                   contract.ProtocolTag,
		"runName":                       contract.RunName,
		"executionOrder":                contract.ExecutionOrder,
		"pollInterval":                  contract.PollInterval,
		"perPolicyWorkloadTimeout":      contract.PerPolicyWorkloadTimeout,
		"experimentSchedulerInterval":   contract.ExperimentSchedulerInterval,
		"restoredIdleSchedulerInterval": contract.RestoredIdleSchedulerInterval,
		"schedulerName":                 contract.SchedulerName,
		"image":                         contract.Image,
	}
	for field, actual := range actualStrings {
		if actual != expectedStrings[field] {
			return 0, fmt.Errorf("%s=%q, protocol requires %q", field, actual, expectedStrings[field])
		}
	}

	actualOutputRoot := resolvePath(schedulerDir, opts.outputRoot)
	if actualOutputRoot != filepath.Clean(contract.TemporaryOutputRoot) {
		return 0, fmt.Errorf("outputRoot=%q, protocol requires %q", actualOutputRoot, contract.TemporaryOutputRoot)
	}
	actualWeightsFile := relativePath(repoRoot, resolvePath(schedulerDir, opts.weightsFile))
	if actualWeightsFile != filepath.ToSlash(filepath.Clean(contract.WeightsFile)) {
		return 0, fmt.Errorf("weightsFile=%q, protocol requires %q", actualWeightsFile, contract.WeightsFile)
	}
	actualWorkloads := make([]string, len(workloads))
	for i, workload := range workloads {
		actualWorkloads[i] = relativePath(repoRoot, resolvePath(schedulerDir, workload))
	}
	if !slices.Equal(actualWorkloads, contract.Workloads) {
		return 0, fmt.Errorf("workloads=%v, protocol requires %v", actualWorkloads, contract.Workloads)
	}

	actualWeights, err := parseWeights(opts.expectedWeights)
	if err != nil {
		return 0, fmt.Errorf("parse expected weights: %w", err)
	}
	contractWeights, err := parseWeights(contract.CanonicalWeights)
	if err != nil {
		return 0, fmt.Errorf("parse protocol canonical weights: %w", err)
	}
	if actualWeights != contractWeights {
		return 0, fmt.Errorf("canonical weights=%+v, protocol requires %+v", actualWeights, contractWeights)
	}

	if len(policies) != contract.PolicyCount || contract.BaselinePolicyCount+contract.OptimizerFinalistCount != contract.PolicyCount {
		return 0, fmt.Errorf("policy count=%d, protocol requires %d (%d baseline + %d optimizer)", len(policies), contract.PolicyCount, contract.BaselinePolicyCount, contract.OptimizerFinalistCount)
	}
	if opts.repetitions != contract.RepetitionsPerPolicyWorkload || opts.expectedWorkers != contract.ExpectedWorkers {
		return 0, fmt.Errorf("repetitions/workers=%d/%d, protocol requires %d/%d", opts.repetitions, opts.expectedWorkers, contract.RepetitionsPerPolicyWorkload, contract.ExpectedWorkers)
	}
	expectedWorkloadExecutions := len(policies) * opts.repetitions * len(workloads)
	if expectedWorkloadExecutions != contract.ExpectedWorkloadExecutions {
		return 0, fmt.Errorf("workload executions=%d, protocol requires %d", expectedWorkloadExecutions, contract.ExpectedWorkloadExecutions)
	}

	podsPerRepetition := 0
	for _, workload := range workloads {
		count, err := countWorkloadPods(resolvePath(schedulerDir, workload))
		if err != nil {
			return 0, err
		}
		podsPerRepetition += count
	}
	expectedPods := len(policies) * opts.repetitions * podsPerRepetition
	if expectedPods != contract.ExpectedPods {
		return 0, fmt.Errorf("Pod executions=%d, protocol requires %d", expectedPods, contract.ExpectedPods)
	}
	return expectedPods, nil
}

func countWorkloadPods(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read workload %s: %w", path, err)
	}
	var direct []json.RawMessage
	if err := json.Unmarshal(data, &direct); err == nil && len(direct) > 0 {
		return len(direct), nil
	}
	var envelope struct {
		Pods []json.RawMessage `json:"pods"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0, fmt.Errorf("parse workload %s: %w", path, err)
	}
	if len(envelope.Pods) == 0 {
		return 0, fmt.Errorf("workload %s has no Pods", path)
	}
	return len(envelope.Pods), nil
}

func expandRunSpecs(specs []evaluator.ExperimentRunSpec, repetitions int, order string) ([]evaluator.ExperimentRunSpec, error) {
	if len(specs) == 0 || repetitions < 1 {
		return nil, errors.New("run specs and repetitions must be positive")
	}
	if order != executionOrderFixed && order != executionOrderCyclicRotation {
		return nil, fmt.Errorf("unsupported execution order %q", order)
	}
	expanded := make([]evaluator.ExperimentRunSpec, 0, len(specs)*repetitions)
	for repetition := 1; repetition <= repetitions; repetition++ {
		start := 0
		if order == executionOrderCyclicRotation {
			start = (repetition - 1) % len(specs)
		}
		for offset := range specs {
			spec := specs[(start+offset)%len(specs)]
			spec.Name = fmt.Sprintf("%s-r%02d", spec.Name, repetition)
			expanded = append(expanded, spec)
		}
	}
	return expanded, nil
}

func buildInvocation(opts options) []string {
	command := []string{
		"go", "run", "./cmd/repeated-validation-runner",
		"--run-name", opts.runName,
		"--workloads", opts.workloads,
		"--weights-file", opts.weightsFile,
		"--protocol-tag", opts.protocolTag,
		"--protocol-version", opts.protocolVersion,
	}
	if opts.protocolFile != "" {
		command = append(command, "--protocol-file", opts.protocolFile)
	}
	return append(command,
		"--execution-order", opts.executionOrder,
		"--output-root", opts.outputRoot,
		"--provenance-files", opts.provenanceFiles,
		"--repetitions", strconv.Itoa(opts.repetitions),
		"--poll-interval", opts.pollInterval.String(),
		"--timeout", opts.timeout.String(),
		"--expect-workers", strconv.Itoa(opts.expectedWorkers),
		"--expect-scheduler-interval", opts.expectedSchedulerInterval.String(),
		"--final-scheduler-interval", opts.finalSchedulerInterval.String(),
		"--expect-weights", opts.expectedWeights,
		"--scheduler-namespace", opts.schedulerNamespace,
		"--scheduler-deployment", opts.schedulerDeployment,
		"--evaluator-namespace", opts.evaluatorNamespace,
		"--scheduler-name", opts.schedulerName,
		"--image", opts.image,
	)
}

func validateBatch(batch evaluator.BatchResult, expectedRows int) error {
	if batch.PolicyVersion != evaluator.PolicyVersion || batch.MeasurementVersion != evaluator.MeasurementVersion {
		return fmt.Errorf("protocol mismatch: policy=%q measurement=%q", batch.PolicyVersion, batch.MeasurementVersion)
	}
	if len(batch.Results) != expectedRows {
		return fmt.Errorf("result rows=%d, want %d", len(batch.Results), expectedRows)
	}
	for _, row := range batch.Results {
		if row.PodCount == 0 || row.ScheduledCount != row.PodCount || row.CompletedCount != row.PodCount ||
			row.UnschedulableCount != 0 || row.DecisionTimestampCount != row.PodCount ||
			row.KubernetesScheduledTimestampCount != row.PodCount || row.EvaluatorObservationTimestampCount != row.PodCount {
			return fmt.Errorf("incomplete result row %q: pods=%d scheduled=%d completed=%d unschedulable=%d decision=%d", row.Name, row.PodCount, row.ScheduledCount, row.CompletedCount, row.UnschedulableCount, row.DecisionTimestampCount)
		}
	}
	return nil
}

func captureSnapshot(ctx context.Context, cs kubernetes.Interface, opts options) (snapshot, error) {
	base, err := evaluator.CurrentWorkloadBase(ctx, cs)
	if err != nil {
		return snapshot{}, err
	}
	weights, err := evaluator.CurrentDeploymentWeights(ctx, cs, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return snapshot{}, err
	}
	interval, err := evaluator.CurrentDeploymentSchedulerInterval(ctx, cs, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return snapshot{}, err
	}
	deployment, err := cs.AppsV1().Deployments(opts.schedulerNamespace).Get(ctx, opts.schedulerDeployment, metav1.GetOptions{})
	if err != nil {
		return snapshot{}, err
	}
	pods, err := cs.CoreV1().Pods(opts.evaluatorNamespace).List(ctx, metav1.ListOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return snapshot{}, err
	}
	evaluatorPodCount := 0
	if err == nil {
		evaluatorPodCount = len(pods.Items)
	}
	return snapshot{
		CapturedAt:               time.Now().UTC(),
		WorkerCount:              base.WorkerCount,
		WorkerCPUMilli:           base.WorkerCPUMilli,
		WorkerMemoryMiB:          base.WorkerMemoryMiB,
		SchedulerRolloutReady:    deploymentReady(deployment),
		SchedulerIntervalSeconds: interval.Seconds(),
		SchedulerWeights:         weights,
		EvaluatorPodCount:        evaluatorPodCount,
	}, nil
}

func validateSnapshot(got snapshot, expectedWorkers int, expectedInterval time.Duration, expectedWeights evaluator.WeightSet) error {
	if got.WorkerCount != expectedWorkers || !got.SchedulerRolloutReady || got.EvaluatorPodCount != 0 {
		return fmt.Errorf("cluster state workers=%d rollout=%t evaluatorPods=%d", got.WorkerCount, got.SchedulerRolloutReady, got.EvaluatorPodCount)
	}
	if got.SchedulerWeights != expectedWeights {
		return fmt.Errorf("scheduler weights=%+v, want %+v", got.SchedulerWeights, expectedWeights)
	}
	if time.Duration(got.SchedulerIntervalSeconds*float64(time.Second)) != expectedInterval {
		return fmt.Errorf("scheduler interval=%gs, want %s", got.SchedulerIntervalSeconds, expectedInterval)
	}
	return nil
}

func restoreAndSnapshot(ctx context.Context, cs kubernetes.Interface, opts options, weights evaluator.WeightSet) (snapshot, error) {
	if err := evaluator.CleanupManagedPods(ctx, cs, opts.evaluatorNamespace); err != nil {
		return snapshot{}, err
	}
	if err := evaluator.ApplyDeploymentWeights(ctx, cs, opts.schedulerNamespace, opts.schedulerDeployment, weights); err != nil {
		return snapshot{}, err
	}
	if err := evaluator.ApplyDeploymentSchedulerInterval(ctx, cs, opts.schedulerNamespace, opts.schedulerDeployment, opts.finalSchedulerInterval); err != nil {
		return snapshot{}, err
	}
	got, err := captureSnapshot(ctx, cs, opts)
	if err != nil {
		return snapshot{}, err
	}
	if err := validateSnapshot(got, opts.expectedWorkers, opts.finalSchedulerInterval, weights); err != nil {
		return snapshot{}, err
	}
	return got, nil
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

func emitBatch(runRoot, workload string, batch evaluator.BatchResult) error {
	jsonPath := filepath.Join(runRoot, workload+".json")
	data, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return err
	}
	return evaluator.WriteComparisonCSV(filepath.Join(runRoot, workload+".csv"), batch.MeasurementProtocol, batch.Results)
}

func inspectOutputs(repoRoot, runRoot, workload string) ([]artifact, error) {
	paths := []string{filepath.Join(runRoot, workload+".json"), filepath.Join(runRoot, workload+".csv")}
	outputs := make([]artifact, 0, len(paths))
	for _, path := range paths {
		sum, err := fileSHA256(path)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, artifact{Path: relativePath(repoRoot, path), SHA256: sum})
	}
	return outputs, nil
}

func inspectGit(repoRoot, tag string) (gitState, error) {
	sha, err := gitOutput(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return gitState{}, err
	}
	branch, err := gitOutput(repoRoot, "branch", "--show-current")
	if err != nil {
		return gitState{}, err
	}
	status, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		return gitState{}, err
	}
	tagType, err := gitOutput(repoRoot, "cat-file", "-t", tag)
	if err != nil || tagType != "tag" {
		return gitState{}, fmt.Errorf("protocol tag %q must be an annotated tag", tag)
	}
	tagSHA, err := gitOutput(repoRoot, "rev-list", "-n", "1", tag)
	if err != nil || tagSHA != sha {
		return gitState{}, fmt.Errorf("protocol tag %q does not point at HEAD", tag)
	}
	return gitState{SHA: sha, Branch: branch, ProtocolTag: tag, DirtyAtStart: status != ""}, nil
}

func inspectInputs(schedulerDir, repoRoot string, paths []string) ([]artifact, error) {
	seen := make(map[string]struct{})
	inputs := make([]artifact, 0, len(paths))
	for _, path := range paths {
		absolute := resolvePath(schedulerDir, path)
		relative := relativePath(repoRoot, absolute)
		if _, exists := seen[relative]; exists {
			continue
		}
		seen[relative] = struct{}{}
		sum, err := fileSHA256(absolute)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, artifact{Path: relative, SHA256: sum})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Path < inputs[j].Path })
	return inputs, nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writeManifest(path string, value manifest) error {
	data, err := json.MarshalIndent(value, "", "  ")
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

func gitOutput(dir string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func splitPaths(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if path := strings.TrimSpace(part); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("value is empty")
	}
	return paths, nil
}

func parseWeights(raw string) (evaluator.WeightSet, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return evaluator.WeightSet{}, fmt.Errorf("expected four comma-separated values")
	}
	values := make([]float64, 4)
	for i, part := range parts {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return evaluator.WeightSet{}, fmt.Errorf("parse weight %d: %w", i, err)
		}
		values[i] = value
	}
	return policy.Normalize(evaluator.WeightSet{
		FreeCPU:       values[0],
		FreeMem:       values[1],
		Balance:       values[2],
		Consolidation: values[3],
	})
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
