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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

type options struct {
	runName                   string
	seeds                     string
	workloads                 string
	baselineFile              string
	provenanceFiles           string
	outputRoot                string
	protocolTag               string
	schedulerNamespace        string
	schedulerDeployment       string
	evaluatorNamespace        string
	image                     string
	population                int
	generations               int
	mutationRate              float64
	mutationSigma             float64
	minWeight                 float64
	maxWeight                 float64
	pollInterval              time.Duration
	timeout                   time.Duration
	expectedWorkers           int
	expectedSchedulerInterval time.Duration
	expectedWeights           string
}

type gitMetadata struct {
	SHA          string   `json:"sha"`
	Branch       string   `json:"branch"`
	TagsAtHead   []string `json:"tagsAtHead"`
	ProtocolTag  string   `json:"protocolTag"`
	TagAnnotated bool     `json:"tagAnnotated"`
	DirtyAtStart bool     `json:"dirtyAtStart"`
}

type inputArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type clusterSnapshot struct {
	CapturedAt                time.Time           `json:"capturedAt"`
	Context                   string              `json:"context"`
	KubernetesServerVersion   string              `json:"kubernetesServerVersion"`
	WorkerCount               int                 `json:"workerCount"`
	WorkerCPUMilli            int64               `json:"workerCpuMilli"`
	WorkerMemoryMiB           int64               `json:"workerMemoryMiB"`
	SchedulerDeployment       string              `json:"schedulerDeployment"`
	SchedulerImage            string              `json:"schedulerImage"`
	SchedulerRuntimeImageIDs  []string            `json:"schedulerRuntimeImageIds"`
	SchedulerRolloutReady     bool                `json:"schedulerRolloutReady"`
	SchedulerIntervalSeconds  float64             `json:"schedulerIntervalSeconds"`
	SchedulerCanonicalWeights evaluator.WeightSet `json:"schedulerCanonicalWeights"`
	EvaluatorPodCount         int                 `json:"evaluatorPodCount"`
}

type outputArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type seedRun struct {
	Seed                 int64            `json:"seed"`
	Status               string           `json:"status"`
	StartedAt            time.Time        `json:"startedAt"`
	FinishedAt           *time.Time       `json:"finishedAt,omitempty"`
	Command              []string         `json:"command"`
	ExitCode             *int             `json:"exitCode,omitempty"`
	ExpectedCandidates   int              `json:"expectedCandidates"`
	ExpectedWorkloadRuns int              `json:"expectedWorkloadRuns"`
	LogPath              string           `json:"logPath"`
	Outputs              []outputArtifact `json:"outputs,omitempty"`
	Postflight           *clusterSnapshot `json:"postflight,omitempty"`
	Error                string           `json:"error,omitempty"`
}

type searchConfig struct {
	Seeds                     []int64       `json:"seeds"`
	Workloads                 []string      `json:"workloads"`
	BaselineFile              string        `json:"baselineFile"`
	Population                int           `json:"population"`
	Generations               int           `json:"generations"`
	MutationRate              float64       `json:"mutationRate"`
	MutationSigma             float64       `json:"mutationSigma"`
	MinWeight                 float64       `json:"minWeight"`
	MaxWeight                 float64       `json:"maxWeight"`
	PollInterval              time.Duration `json:"pollIntervalNanoseconds"`
	Timeout                   time.Duration `json:"timeoutNanoseconds"`
	Image                     string        `json:"image"`
	ExpectedCandidatesPerSeed int           `json:"expectedCandidatesPerSeed"`
	ExpectedWorkloadRuns      int           `json:"expectedWorkloadRunsPerSeed"`
}

type experimentManifest struct {
	SchemaVersion         string          `json:"schemaVersion"`
	ProtocolVersion       string          `json:"protocolVersion"`
	Class                 string          `json:"class"`
	PaperEvidenceEligible bool            `json:"paperEvidenceEligible"`
	RunName               string          `json:"runName"`
	Status                string          `json:"status"`
	StartedAt             time.Time       `json:"startedAt"`
	FinishedAt            *time.Time      `json:"finishedAt,omitempty"`
	Repository            string          `json:"repository"`
	WorkingDirectory      string          `json:"workingDirectory"`
	Git                   gitMetadata     `json:"git"`
	Inputs                []inputArtifact `json:"inputs"`
	Search                searchConfig    `json:"search"`
	Preflight             clusterSnapshot `json:"preflight"`
	Runs                  []seedRun       `json:"runs"`
	Failure               string          `json:"failure,omitempty"`
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

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.runName, "run-name", "", "New run directory name below --output-root")
	flag.StringVar(&opts.seeds, "seeds", "", "Comma-separated unique random seeds")
	flag.StringVar(&opts.workloads, "workloads", "", "Comma-separated workload paths relative to scheduler/")
	flag.StringVar(&opts.baselineFile, "baseline-file", "", "Baseline file path relative to scheduler/")
	flag.StringVar(&opts.provenanceFiles, "provenance-files", "../kind-config-4workers.yaml,../deploy.yaml", "Additional comma-separated input files")
	flag.StringVar(&opts.outputRoot, "output-root", "../results", "Output root relative to scheduler/")
	flag.StringVar(&opts.protocolTag, "protocol-tag", "", "Required annotated Git tag pointing at HEAD")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", "evo-sched", "Scheduler deployment namespace")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", "evok8s-scheduler", "Scheduler deployment name")
	flag.StringVar(&opts.evaluatorNamespace, "evaluator-namespace", "evo-sched-eval", "Evaluator workload namespace")
	flag.StringVar(&opts.image, "image", "busybox:1.36.1", "Evaluator Pod image")
	flag.IntVar(&opts.population, "population", 12, "Optimizer population size")
	flag.IntVar(&opts.generations, "generations", 5, "Optimizer generation count")
	flag.Float64Var(&opts.mutationRate, "mutation-rate", 0.8, "Per-gene mutation probability")
	flag.Float64Var(&opts.mutationSigma, "mutation-sigma", 0.25, "Gaussian mutation sigma")
	flag.Float64Var(&opts.minWeight, "min-weight", 0, "Minimum raw gene value")
	flag.Float64Var(&opts.maxWeight, "max-weight", 2, "Maximum raw gene value")
	flag.DurationVar(&opts.pollInterval, "poll-interval", 250*time.Millisecond, "Evaluator Pod polling interval")
	flag.DurationVar(&opts.timeout, "timeout", 90*time.Second, "Per-workload timeout")
	flag.IntVar(&opts.expectedWorkers, "expect-workers", 4, "Required ready worker count")
	flag.DurationVar(&opts.expectedSchedulerInterval, "expect-scheduler-interval", 250*time.Millisecond, "Required custom scheduler interval")
	flag.StringVar(&opts.expectedWeights, "expect-weights", "0.25,0.25,0.25,0.25", "Required canonical deployment weights")
	flag.Parse()
	return opts
}

func run(ctx context.Context, opts options) error {
	seeds, err := parseSeeds(opts.seeds)
	if err != nil {
		return err
	}
	workloads, err := splitPaths(opts.workloads)
	if err != nil {
		return fmt.Errorf("--workloads: %w", err)
	}
	provenanceFiles, err := splitPaths(opts.provenanceFiles)
	if err != nil {
		return fmt.Errorf("--provenance-files: %w", err)
	}
	if opts.runName == "" {
		return fmt.Errorf("--run-name is required")
	}
	if opts.baselineFile == "" {
		return fmt.Errorf("--baseline-file is required")
	}
	if opts.protocolTag == "" {
		return fmt.Errorf("--protocol-tag is required")
	}
	if opts.population <= 0 || opts.generations < 0 {
		return fmt.Errorf("population must be positive and generations non-negative")
	}
	if opts.pollInterval <= 0 || opts.timeout <= 0 || opts.expectedSchedulerInterval <= 0 {
		return fmt.Errorf("poll, timeout, and expected scheduler intervals must be positive")
	}
	expectedWeights, err := evaluator.ParseWeightSet(opts.expectedWeights)
	if err != nil {
		return fmt.Errorf("--expect-weights: %w", err)
	}

	schedulerDir, repoRoot, git, err := inspectGit(opts.protocolTag)
	if err != nil {
		return err
	}
	runRoot := opts.outputRoot
	if !filepath.IsAbs(runRoot) {
		runRoot = filepath.Join(schedulerDir, runRoot)
	}
	runRoot = filepath.Clean(filepath.Join(runRoot, opts.runName))
	if _, err := os.Stat(runRoot); err == nil {
		return fmt.Errorf("run directory already exists: %s", runRoot)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect run directory: %w", err)
	}

	inputPaths := append(append([]string(nil), workloads...), opts.baselineFile)
	inputPaths = append(inputPaths, provenanceFiles...)
	inputs, err := inspectInputs(schedulerDir, repoRoot, inputPaths)
	if err != nil {
		return err
	}
	baselinePath := opts.baselineFile
	if !filepath.IsAbs(baselinePath) {
		baselinePath = filepath.Join(schedulerDir, baselinePath)
	}
	baselineSpec, err := evaluator.LoadExperiment(filepath.Clean(baselinePath))
	if err != nil {
		return fmt.Errorf("load baseline file: %w", err)
	}

	clientset, err := evaluator.NewClientset()
	if err != nil {
		return fmt.Errorf("Kubernetes client init: %w", err)
	}
	preflight, err := captureClusterSnapshot(ctx, clientset, opts)
	if err != nil {
		return fmt.Errorf("capture preflight: %w", err)
	}
	if err := validateClusterSnapshot(preflight, opts, expectedWeights); err != nil {
		return fmt.Errorf("preflight failed: %w", err)
	}

	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		return fmt.Errorf("create run directory: %w", err)
	}
	manifestPath := filepath.Join(runRoot, "manifest.json")
	expectedCandidates := opts.population*(opts.generations+1) + len(baselineSpec.Runs)
	manifest := experimentManifest{
		SchemaVersion:         "v1",
		ProtocolVersion:       "v2-official-optimizer",
		Class:                 "official",
		PaperEvidenceEligible: false,
		RunName:               opts.runName,
		Status:                "running",
		StartedAt:             time.Now().UTC(),
		Repository:            repoRoot,
		WorkingDirectory:      schedulerDir,
		Git:                   git,
		Inputs:                inputs,
		Search: searchConfig{
			Seeds:                     seeds,
			Workloads:                 workloads,
			BaselineFile:              opts.baselineFile,
			Population:                opts.population,
			Generations:               opts.generations,
			MutationRate:              opts.mutationRate,
			MutationSigma:             opts.mutationSigma,
			MinWeight:                 opts.minWeight,
			MaxWeight:                 opts.maxWeight,
			PollInterval:              opts.pollInterval,
			Timeout:                   opts.timeout,
			Image:                     opts.image,
			ExpectedCandidatesPerSeed: expectedCandidates,
			ExpectedWorkloadRuns:      expectedCandidates * len(workloads),
		},
		Preflight: preflight,
		Runs:      make([]seedRun, 0, len(seeds)),
	}
	if err := writeManifest(manifestPath, manifest); err != nil {
		return err
	}

	for _, seed := range seeds {
		seedDir := filepath.Join(runRoot, fmt.Sprintf("seed-%d", seed))
		if err := os.MkdirAll(seedDir, 0o755); err != nil {
			return finishManifestWithFailure(manifestPath, &manifest, fmt.Errorf("create seed directory: %w", err))
		}
		outputPath := filepath.Join(seedDir, "result.json")
		logPath := filepath.Join(seedDir, "run.log")
		args := buildOptimizerArgs(opts, seed, outputPath)
		runInfo := seedRun{
			Seed:                 seed,
			Status:               "running",
			StartedAt:            time.Now().UTC(),
			Command:              append([]string{"go"}, args...),
			ExpectedCandidates:   expectedCandidates,
			ExpectedWorkloadRuns: expectedCandidates * len(workloads),
			LogPath:              relativePath(repoRoot, logPath),
		}
		manifest.Runs = append(manifest.Runs, runInfo)
		if err := writeManifest(manifestPath, manifest); err != nil {
			return err
		}

		logFile, err := os.Create(logPath)
		if err != nil {
			return finishManifestWithFailure(manifestPath, &manifest, fmt.Errorf("create seed log: %w", err))
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
		finishedAt := time.Now().UTC()
		current := &manifest.Runs[len(manifest.Runs)-1]
		current.FinishedAt = &finishedAt
		exitCode := commandExitCode(runErr)
		current.ExitCode = &exitCode

		if ctx.Err() != nil {
			recovery, recoveryErr := recoverClusterState(clientset, opts, expectedWeights)
			if recoveryErr == nil {
				current.Postflight = &recovery
			}
			current.Status = "interrupted"
			current.Error = ctx.Err().Error()
			if recoveryErr != nil {
				current.Error += fmt.Sprintf("; recovery failed: %v", recoveryErr)
			}
			manifest.Status = "interrupted"
			manifest.Failure = fmt.Sprintf("seed %d interrupted: %v", seed, ctx.Err())
			manifest.FinishedAt = &finishedAt
			_ = writeManifest(manifestPath, manifest)
			return ctx.Err()
		}
		if runErr != nil {
			recovery, recoveryErr := recoverClusterState(clientset, opts, expectedWeights)
			if recoveryErr == nil {
				current.Postflight = &recovery
			}
			current.Status = "failed"
			current.Error = runErr.Error()
			if recoveryErr != nil {
				current.Error += fmt.Sprintf("; recovery failed: %v", recoveryErr)
			}
			manifest.Status = "failed"
			manifest.Failure = fmt.Sprintf("seed %d failed: %v", seed, runErr)
			manifest.FinishedAt = &finishedAt
			_ = writeManifest(manifestPath, manifest)
			return fmt.Errorf("seed %d optimizer failed: %w", seed, runErr)
		}
		if closeErr != nil {
			current.Status = "failed"
			current.Error = closeErr.Error()
			manifest.Status = "failed"
			manifest.Failure = fmt.Sprintf("seed %d log close failed: %v", seed, closeErr)
			manifest.FinishedAt = &finishedAt
			_ = writeManifest(manifestPath, manifest)
			return fmt.Errorf("seed %d log close failed: %w", seed, closeErr)
		}

		outputs, err := inspectSeedOutputs(repoRoot, outputPath, logPath, seed, expectedCandidates)
		if err != nil {
			recovery, recoveryErr := recoverClusterState(clientset, opts, expectedWeights)
			if recoveryErr == nil {
				current.Postflight = &recovery
			}
			current.Status = "failed"
			current.Error = err.Error()
			if recoveryErr != nil {
				current.Error += fmt.Sprintf("; recovery failed: %v", recoveryErr)
			}
			manifest.Status = "failed"
			manifest.Failure = fmt.Sprintf("seed %d output validation failed: %v", seed, err)
			manifest.FinishedAt = &finishedAt
			_ = writeManifest(manifestPath, manifest)
			return err
		}
		postflight, err := captureClusterSnapshot(ctx, clientset, opts)
		if err == nil {
			err = validateClusterSnapshot(postflight, opts, expectedWeights)
		}
		if err != nil {
			current.Status = "failed"
			current.Error = fmt.Sprintf("postflight: %v", err)
			manifest.Status = "failed"
			manifest.Failure = fmt.Sprintf("seed %d postflight failed: %v", seed, err)
			manifest.FinishedAt = &finishedAt
			_ = writeManifest(manifestPath, manifest)
			return fmt.Errorf("seed %d postflight failed: %w", seed, err)
		}
		current.Status = "completed"
		current.Outputs = outputs
		current.Postflight = &postflight
		if err := writeManifest(manifestPath, manifest); err != nil {
			return err
		}
	}

	finishedAt := time.Now().UTC()
	manifest.Status = "completed"
	manifest.PaperEvidenceEligible = true
	manifest.FinishedAt = &finishedAt
	if err := writeManifest(manifestPath, manifest); err != nil {
		return err
	}
	fmt.Printf("completed multi-seed experiment: %s\n", runRoot)
	return nil
}

func parseSeeds(raw string) ([]int64, error) {
	parts, err := splitPaths(raw)
	if err != nil {
		return nil, fmt.Errorf("--seeds: %w", err)
	}
	seeds := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		seed, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid seed %q: %w", part, err)
		}
		if _, exists := seen[seed]; exists {
			return nil, fmt.Errorf("duplicate seed %d", seed)
		}
		seen[seed] = struct{}{}
		seeds = append(seeds, seed)
	}
	return seeds, nil
}

func splitPaths(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("value is empty")
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, fmt.Errorf("value contains an empty entry")
		}
		result = append(result, value)
	}
	return result, nil
}

func buildOptimizerArgs(opts options, seed int64, outputPath string) []string {
	return []string{
		"run", "./cmd/optimizer",
		"--workloads", opts.workloads,
		"--baseline-file", opts.baselineFile,
		"--population", strconv.Itoa(opts.population),
		"--generations", strconv.Itoa(opts.generations),
		"--mutation-rate", strconv.FormatFloat(opts.mutationRate, 'g', -1, 64),
		"--mutation-sigma", strconv.FormatFloat(opts.mutationSigma, 'g', -1, 64),
		"--min-weight", strconv.FormatFloat(opts.minWeight, 'g', -1, 64),
		"--max-weight", strconv.FormatFloat(opts.maxWeight, 'g', -1, 64),
		"--seed", strconv.FormatInt(seed, 10),
		"--poll-interval", opts.pollInterval.String(),
		"--timeout", opts.timeout.String(),
		"--image", opts.image,
		"--output", outputPath,
	}
}

func inspectGit(protocolTag string) (string, string, gitMetadata, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	repoRoot, err := gitOutput(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	schedulerDir := filepath.Join(repoRoot, "scheduler")
	status, err := gitOutput(repoRoot, "status", "--porcelain")
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	if status != "" {
		return "", "", gitMetadata{}, fmt.Errorf("official run requires a clean Git tree")
	}
	sha, err := gitOutput(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	branch, err := gitOutput(repoRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	tagOutput, err := gitOutput(repoRoot, "tag", "--points-at", "HEAD")
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	tags := strings.Fields(tagOutput)
	sort.Strings(tags)
	if !contains(tags, protocolTag) {
		return "", "", gitMetadata{}, fmt.Errorf("required protocol tag %q does not point at HEAD", protocolTag)
	}
	tagType, err := gitOutput(repoRoot, "cat-file", "-t", "refs/tags/"+protocolTag)
	if err != nil {
		return "", "", gitMetadata{}, err
	}
	if tagType != "tag" {
		return "", "", gitMetadata{}, fmt.Errorf("protocol tag %q must be annotated", protocolTag)
	}
	return schedulerDir, repoRoot, gitMetadata{
		SHA:          sha,
		Branch:       branch,
		TagsAtHead:   tags,
		ProtocolTag:  protocolTag,
		TagAnnotated: true,
		DirtyAtStart: false,
	}, nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	data, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(data)), nil
}

func inspectInputs(schedulerDir, repoRoot string, paths []string) ([]inputArtifact, error) {
	artifacts := make([]inputArtifact, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		absolute := path
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(schedulerDir, path)
		}
		absolute = filepath.Clean(absolute)
		if _, exists := seen[absolute]; exists {
			continue
		}
		seen[absolute] = struct{}{}
		digest, err := sha256File(absolute)
		if err != nil {
			return nil, fmt.Errorf("input %s: %w", path, err)
		}
		relative, err := filepath.Rel(repoRoot, absolute)
		if err != nil {
			relative = absolute
		}
		artifacts = append(artifacts, inputArtifact{Path: relative, SHA256: digest})
	}
	return artifacts, nil
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func captureClusterSnapshot(ctx context.Context, clientset kubernetes.Interface, opts options) (clusterSnapshot, error) {
	base, err := evaluator.CurrentWorkloadBase(ctx, clientset)
	if err != nil {
		return clusterSnapshot{}, err
	}
	weights, err := evaluator.CurrentDeploymentWeights(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return clusterSnapshot{}, err
	}
	interval, err := evaluator.CurrentDeploymentSchedulerInterval(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return clusterSnapshot{}, err
	}
	deployment, err := clientset.AppsV1().Deployments(opts.schedulerNamespace).Get(ctx, opts.schedulerDeployment, metav1.GetOptions{})
	if err != nil {
		return clusterSnapshot{}, err
	}
	evaluatorPods, err := clientset.CoreV1().Pods(opts.evaluatorNamespace).List(ctx, metav1.ListOptions{})
	if apierrors.IsNotFound(err) {
		evaluatorPods = nil
		err = nil
	}
	if err != nil {
		return clusterSnapshot{}, err
	}
	schedulerPods, err := clientset.CoreV1().Pods(opts.schedulerNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=evok8s-scheduler"})
	if err != nil {
		return clusterSnapshot{}, err
	}
	runtimeImageIDs := make([]string, 0)
	for _, pod := range schedulerPods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			if status.ImageID != "" {
				runtimeImageIDs = append(runtimeImageIDs, status.ImageID)
			}
		}
	}
	sort.Strings(runtimeImageIDs)
	config, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return clusterSnapshot{}, err
	}
	serverVersion, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return clusterSnapshot{}, err
	}
	schedulerImage := ""
	if len(deployment.Spec.Template.Spec.Containers) > 0 {
		schedulerImage = deployment.Spec.Template.Spec.Containers[0].Image
	}
	evaluatorPodCount := 0
	if evaluatorPods != nil {
		evaluatorPodCount = len(evaluatorPods.Items)
	}
	rolloutReady := deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.Replicas == 1 && deployment.Status.UpdatedReplicas == 1 &&
		deployment.Status.AvailableReplicas == 1 && deployment.Status.UnavailableReplicas == 0
	return clusterSnapshot{
		CapturedAt:                time.Now().UTC(),
		Context:                   config.CurrentContext,
		KubernetesServerVersion:   serverVersion.GitVersion,
		WorkerCount:               base.WorkerCount,
		WorkerCPUMilli:            base.WorkerCPUMilli,
		WorkerMemoryMiB:           base.WorkerMemoryMiB,
		SchedulerDeployment:       opts.schedulerDeployment,
		SchedulerImage:            schedulerImage,
		SchedulerRuntimeImageIDs:  runtimeImageIDs,
		SchedulerRolloutReady:     rolloutReady,
		SchedulerIntervalSeconds:  interval.Seconds(),
		SchedulerCanonicalWeights: weights,
		EvaluatorPodCount:         evaluatorPodCount,
	}, nil
}

func validateClusterSnapshot(snapshot clusterSnapshot, opts options, expectedWeights evaluator.WeightSet) error {
	if snapshot.WorkerCount != opts.expectedWorkers {
		return fmt.Errorf("worker count=%d, want %d", snapshot.WorkerCount, opts.expectedWorkers)
	}
	if !snapshot.SchedulerRolloutReady {
		return fmt.Errorf("scheduler rollout is not converged")
	}
	if snapshot.EvaluatorPodCount != 0 {
		return fmt.Errorf("evaluator namespace has %d pods", snapshot.EvaluatorPodCount)
	}
	if math.Abs(snapshot.SchedulerIntervalSeconds-opts.expectedSchedulerInterval.Seconds()) > 1e-9 {
		return fmt.Errorf("scheduler interval=%gs, want %s", snapshot.SchedulerIntervalSeconds, opts.expectedSchedulerInterval)
	}
	if !weightSetsClose(snapshot.SchedulerCanonicalWeights, expectedWeights) {
		return fmt.Errorf("scheduler weights=%+v, want %+v", snapshot.SchedulerCanonicalWeights, expectedWeights)
	}
	return nil
}

func recoverClusterState(clientset kubernetes.Interface, opts options, expectedWeights evaluator.WeightSet) (clusterSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var recoveryErr error
	if err := evaluator.CleanupManagedPods(ctx, clientset, opts.evaluatorNamespace); err != nil {
		recoveryErr = errors.Join(recoveryErr, fmt.Errorf("cleanup evaluator pods: %w", err))
	}
	if err := evaluator.ApplyDeploymentWeights(ctx, clientset, opts.schedulerNamespace, opts.schedulerDeployment, expectedWeights); err != nil {
		recoveryErr = errors.Join(recoveryErr, fmt.Errorf("restore scheduler weights: %w", err))
	}
	snapshot, err := captureClusterSnapshot(ctx, clientset, opts)
	if err != nil {
		return clusterSnapshot{}, errors.Join(recoveryErr, fmt.Errorf("capture recovery snapshot: %w", err))
	}
	if err := validateClusterSnapshot(snapshot, opts, expectedWeights); err != nil {
		recoveryErr = errors.Join(recoveryErr, fmt.Errorf("validate recovery snapshot: %w", err))
	}
	return snapshot, recoveryErr
}

func weightSetsClose(left, right evaluator.WeightSet) bool {
	const epsilon = 1e-12
	return math.Abs(left.FreeCPU-right.FreeCPU) < epsilon &&
		math.Abs(left.FreeMem-right.FreeMem) < epsilon &&
		math.Abs(left.Balance-right.Balance) < epsilon &&
		math.Abs(left.Consolidation-right.Consolidation) < epsilon
}

func inspectSeedOutputs(repoRoot, resultPath, logPath string, seed int64, expectedCandidates int) ([]outputArtifact, error) {
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return nil, fmt.Errorf("read optimizer result: %w", err)
	}
	var result optimizer.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode optimizer result: %w", err)
	}
	if result.PolicyVersion != evaluator.PolicyVersion || result.MeasurementVersion != evaluator.MeasurementVersion {
		return nil, fmt.Errorf("result contract mismatch: policy=%q measurement=%q", result.PolicyVersion, result.MeasurementVersion)
	}
	if result.Config.Seed != seed {
		return nil, fmt.Errorf("result seed=%d, want %d", result.Config.Seed, seed)
	}
	if len(result.Baselines)+len(result.Evaluated) != expectedCandidates {
		return nil, fmt.Errorf("candidate count=%d, want %d", len(result.Baselines)+len(result.Evaluated), expectedCandidates)
	}
	if result.FinishedAt.IsZero() {
		return nil, fmt.Errorf("optimizer result has no finish timestamp")
	}

	base := strings.TrimSuffix(resultPath, filepath.Ext(resultPath))
	paths := []string{resultPath, base + "-pareto.csv", base + "-all.csv", logPath}
	outputs := make([]outputArtifact, 0, len(paths))
	for _, path := range paths {
		digest, err := sha256File(path)
		if err != nil {
			return nil, fmt.Errorf("output %s: %w", path, err)
		}
		outputs = append(outputs, outputArtifact{Path: relativePath(repoRoot, path), SHA256: digest})
	}
	return outputs, nil
}

func writeManifest(path string, manifest experimentManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return fmt.Errorf("write manifest temporary file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}

func finishManifestWithFailure(path string, manifest *experimentManifest, failure error) error {
	finishedAt := time.Now().UTC()
	manifest.Status = "failed"
	manifest.Failure = failure.Error()
	manifest.FinishedAt = &finishedAt
	if err := writeManifest(path, *manifest); err != nil {
		return fmt.Errorf("%v; additionally failed to update manifest: %w", failure, err)
	}
	return failure
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return relative
}
