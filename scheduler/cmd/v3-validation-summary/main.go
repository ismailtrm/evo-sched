package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

const (
	expectedSearchProtocol     = "v3-budget-matched-resampling-official"
	expectedValidationProtocol = "v3-budget-matched-resampling-shared-validation-official"
)

var repetitionPattern = regexp.MustCompile(`^(.*)-r([0-9]{2})$`)

type options struct {
	inputs        string
	protocolPath  string
	manifestPath  string
	searchPath    string
	weightsPath   string
	outputPath    string
	csvOutputPath string
}

type protocolDocument struct {
	ProtocolVersion   string   `json:"protocolVersion"`
	Status            string   `json:"status"`
	TrainingWorkloads []string `json:"trainingWorkloads"`
	Common            struct {
		PodsPerWorkload int `json:"podsPerWorkload"`
	} `json:"commonSearchParameters"`
	SharedValidation struct {
		Official validationContract `json:"official"`
	} `json:"sharedValidation"`
}

type validationContract struct {
	Status                               string   `json:"status"`
	Class                                string   `json:"class"`
	PaperEvidenceEligibleAtRunCompletion bool     `json:"paperEvidenceEligibleAtRunCompletion"`
	ProtocolVersion                      string   `json:"protocolVersion"`
	ProtocolTag                          string   `json:"protocolTag"`
	RunName                              string   `json:"runName"`
	Policies                             []string `json:"policies"`
	ExecutionOrder                       string   `json:"executionOrder"`
	PolicyCount                          int      `json:"policyCount"`
	BaselinePolicyCount                  int      `json:"baselinePolicyCount"`
	OptimizerFinalistCount               int      `json:"optimizerFinalistCount"`
	RepetitionsPerPolicyWorkload         int      `json:"repetitionsPerPolicyWorkload"`
	ExpectedWorkloadExecutions           int      `json:"expectedWorkloadExecutions"`
	ExpectedPods                         int      `json:"expectedPods"`
	ExpectedWorkers                      int      `json:"expectedWorkers"`
	ExperimentSchedulerInterval          string   `json:"experimentSchedulerInterval"`
	RestoredIdleSchedulerInterval        string   `json:"restoredIdleSchedulerInterval"`
}

type validationManifest struct {
	ProtocolVersion       string `json:"protocolVersion"`
	Class                 string `json:"class"`
	PaperEvidenceEligible bool   `json:"paperEvidenceEligible"`
	RunName               string `json:"runName"`
	Status                string `json:"status"`
	Repository            string `json:"repository"`
	Git                   struct {
		SHA          string `json:"sha"`
		Branch       string `json:"branch"`
		ProtocolTag  string `json:"protocolTag"`
		DirtyAtStart bool   `json:"dirtyAtStart"`
	} `json:"git"`
	Inputs         []inputArtifact `json:"inputs"`
	ExecutionOrder string          `json:"executionOrder"`
	Repetitions    int             `json:"repetitionsPerPolicyWorkload"`
	PolicyCount    int             `json:"policyCount"`
	ExpectedRows   int             `json:"expectedResultRows"`
	ExpectedRuns   int             `json:"expectedWorkloadRuns"`
	ExpectedPods   int             `json:"expectedPods"`
	ObservedPods   int             `json:"observedPods"`
	Preflight      snapshot        `json:"preflight"`
	Runs           []workloadRun   `json:"runs"`
	Postflight     *snapshot       `json:"postflight"`
	Failure        string          `json:"failure"`
}

type snapshot struct {
	WorkerCount              int                 `json:"workerCount"`
	SchedulerRolloutReady    bool                `json:"schedulerRolloutReady"`
	SchedulerIntervalSeconds float64             `json:"schedulerIntervalSeconds"`
	SchedulerWeights         evaluator.WeightSet `json:"schedulerCanonicalWeights"`
	EvaluatorPodCount        int                 `json:"evaluatorPodCount"`
}

type workloadRun struct {
	Workload     string          `json:"workload"`
	Path         string          `json:"path"`
	Status       string          `json:"status"`
	ExpectedRows int             `json:"expectedRows"`
	ResultRows   int             `json:"resultRows"`
	PodCount     int             `json:"podCount"`
	Outputs      []inputArtifact `json:"outputs"`
}

type searchSummary struct {
	ProtocolVersion string `json:"protocolVersion"`
	Arms            []struct {
		Name                  string `json:"name"`
		SelectedFinalistCount int    `json:"selectedFinalistCount"`
		PooledPareto          []struct {
			ValidationName string               `json:"validationName"`
			Arm            string               `json:"arm"`
			Weights        evaluator.WeightSet  `json:"weights"`
			Objectives     optimizer.Objectives `json:"provisionalObjectives"`
			Selected       bool                 `json:"selectedForValidation"`
		} `json:"pooledPointEstimateParetoFront"`
	} `json:"arms"`
	Validation struct {
		BaselineCount          int `json:"baselineCount"`
		OptimizerFinalistCount int `json:"optimizerFinalistCount"`
		PolicyCount            int `json:"policyCount"`
		RepetitionsPerPolicy   int `json:"repetitionsPerPolicyWorkload"`
		WorkloadCount          int `json:"workloadCount"`
		ExpectedRuns           int `json:"expectedWorkloadExecutions"`
		ExpectedPods           int `json:"expectedPods"`
	} `json:"validation"`
}

type inputArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type statistics struct {
	Mean              float64 `json:"mean"`
	SampleStdDev      float64 `json:"sampleStdDev"`
	Minimum           float64 `json:"minimum"`
	Maximum           float64 `json:"maximum"`
	Confidence95Lower float64 `json:"confidence95Lower"`
	Confidence95Upper float64 `json:"confidence95Upper"`
}

type objectiveStatistics struct {
	Repetitions        int        `json:"repetitions"`
	P95DecisionLatency statistics `json:"averageP95SchedulingDecisionLatencySeconds"`
	MaxOccupiedWorkers statistics `json:"averageMaxWorkloadOccupiedWorkerNodes"`
}

type workloadStatistics struct {
	Workload           string     `json:"workload"`
	Repetitions        int        `json:"repetitions"`
	P95DecisionLatency statistics `json:"p95SchedulingDecisionLatencySeconds"`
	MaxOccupiedWorkers statistics `json:"maxWorkloadOccupiedWorkerNodes"`
	LoadVariance       statistics `json:"meanWorkloadDominantRequestedLoadVariance"`
}

type policySummary struct {
	Name                  string                `json:"name"`
	Arm                   string                `json:"arm"`
	Baseline              bool                  `json:"baseline"`
	Weights               evaluator.WeightSet   `json:"weights"`
	ProvisionalObjectives *optimizer.Objectives `json:"provisionalObjectives,omitempty"`
	Objectives            objectiveStatistics   `json:"repeatedValidationObjectives"`
	ByWorkload            []workloadStatistics  `json:"byWorkload"`
	UnschedulableTotal    int                   `json:"unschedulableTotal"`
	OnPareto              bool                  `json:"onRepeatedMeanPointEstimateParetoFront"`
	FalseParetoDiscovery  *bool                 `json:"falseParetoDiscovery,omitempty"`
	DominatedBy           []string              `json:"dominatedBy,omitempty"`
}

type paretoPoint struct {
	MeanP95DecisionLatency float64  `json:"meanP95SchedulingDecisionLatencySeconds"`
	MeanMaxOccupiedWorkers float64  `json:"meanMaxWorkloadOccupiedWorkerNodes"`
	Members                []string `json:"members"`
}

type armOutcome struct {
	Name                           string   `json:"name"`
	ValidatedFinalists             int      `json:"validatedFinalists"`
	RemainedNondominated           int      `json:"remainedNondominated"`
	FalseParetoDiscoveries         int      `json:"falseParetoDiscoveries"`
	FalseParetoDiscoveryProportion float64  `json:"falseParetoDiscoveryProportion"`
	NondominatedPolicies           []string `json:"nondominatedPolicies"`
	FalseDiscoveryPolicies         []string `json:"falseDiscoveryPolicies"`
}

type comparison struct {
	OneShotFalseDiscoveryProportion  float64 `json:"oneShotFalseParetoDiscoveryProportion"`
	TwoStageFalseDiscoveryProportion float64 `json:"twoStageFalseParetoDiscoveryProportion"`
	TwoStageMinusOneShot             float64 `json:"twoStageMinusOneShot"`
	TwoStageLower                    bool    `json:"twoStageLower"`
	DescriptiveMitigationSupported   bool    `json:"descriptiveMitigationSupported"`
}

type acceptance struct {
	Workloads                    int `json:"workloads"`
	Policies                     int `json:"policies"`
	RepetitionsPerPolicyWorkload int `json:"repetitionsPerPolicyWorkload"`
	ResultRows                   int `json:"resultRows"`
	Pods                         int `json:"pods"`
	Scheduled                    int `json:"scheduled"`
	Completed                    int `json:"completed"`
	DecisionTimestamps           int `json:"decisionTimestamps"`
	KubernetesTimestamps         int `json:"kubernetesScheduledTimestamps"`
	ObservationTimestamps        int `json:"evaluatorObservationTimestamps"`
	Unschedulable                int `json:"unschedulable"`
}

type generator struct {
	Command string `json:"command"`
	GitSHA  string `json:"gitSha"`
	Branch  string `json:"branch"`
}

type output struct {
	SchemaVersion    string          `json:"schemaVersion"`
	ProtocolVersion  string          `json:"protocolVersion"`
	GeneratedAt      time.Time       `json:"generatedAt"`
	Generator        generator       `json:"generator"`
	Inputs           []inputArtifact `json:"inputs"`
	Acceptance       acceptance      `json:"acceptance"`
	Policies         []policySummary `json:"policies"`
	ParetoFront      []paretoPoint   `json:"repeatedMeanPointEstimateParetoFront"`
	BaselinesOnFront []string        `json:"baselinesOnRepeatedMeanFront"`
	Arms             []armOutcome    `json:"arms"`
	Comparison       comparison      `json:"comparison"`
	Notes            []string        `json:"notes"`
}

type policyObservations struct {
	weights       evaluator.WeightSet
	byWorkloadRep map[string]map[int]evaluator.ComparisonRow
}

func main() {
	if err := run(parseFlags()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.inputs, "inputs", "", "Comma-separated shared-validation JSON files")
	flag.StringVar(&opts.protocolPath, "protocol", "", "Frozen V3 protocol JSON")
	flag.StringVar(&opts.manifestPath, "manifest", "", "Completed shared-validation manifest")
	flag.StringVar(&opts.searchPath, "search-summary", "", "Committed V3 search summary")
	flag.StringVar(&opts.weightsPath, "weights", "", "Frozen shared-validation policy file")
	flag.StringVar(&opts.outputPath, "output", "", "Analysis JSON output")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Policy-level analysis CSV output")
	flag.Parse()
	return opts
}

func run(opts options) error {
	paths := splitPaths(opts.inputs)
	if len(paths) == 0 || opts.protocolPath == "" || opts.manifestPath == "" || opts.searchPath == "" || opts.weightsPath == "" || opts.outputPath == "" {
		return errors.New("--inputs, --protocol, --manifest, --search-summary, --weights, and --output are required")
	}
	if opts.csvOutputPath == "" {
		opts.csvOutputPath = strings.TrimSuffix(opts.outputPath, filepath.Ext(opts.outputPath)) + ".csv"
	}

	generatedBy, err := inspectGenerator()
	if err != nil {
		return err
	}
	protocol, err := loadJSON[protocolDocument](opts.protocolPath)
	if err != nil {
		return err
	}
	manifest, err := loadJSON[validationManifest](opts.manifestPath)
	if err != nil {
		return err
	}
	search, err := loadJSON[searchSummary](opts.searchPath)
	if err != nil {
		return err
	}
	weights, err := evaluator.LoadExperiment(opts.weightsPath)
	if err != nil {
		return err
	}
	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if err := validateProvenance(protocol, manifest, search, weights, repoRoot, opts.manifestPath, paths); err != nil {
		return err
	}

	batches := make([]evaluator.BatchResult, 0, len(paths))
	for _, path := range paths {
		batch, err := loadJSON[evaluator.BatchResult](path)
		if err != nil {
			return err
		}
		batches = append(batches, batch)
	}
	built, err := buildOutput(protocol, manifest, search, weights, batches)
	if err != nil {
		return err
	}
	built.GeneratedAt = time.Now().UTC()
	built.Generator = generatedBy
	analysisInputs := append([]string{opts.protocolPath, opts.manifestPath, opts.searchPath, opts.weightsPath}, paths...)
	for _, path := range paths {
		analysisInputs = append(analysisInputs, strings.TrimSuffix(path, filepath.Ext(path))+".csv")
	}
	built.Inputs, err = inspectInputs(analysisInputs)
	if err != nil {
		return err
	}
	if err := writeJSON(opts.outputPath, built); err != nil {
		return err
	}
	return writeCSV(opts.csvOutputPath, built.Policies)
}

func validateProvenance(protocol protocolDocument, manifest validationManifest, search searchSummary, weights evaluator.ExperimentSpec, repoRoot, manifestPath string, resultPaths []string) error {
	contract := protocol.SharedValidation.Official
	if protocol.ProtocolVersion != expectedSearchProtocol || protocol.Status != "official-frozen-before-execution" || contract.ProtocolVersion != expectedValidationProtocol || contract.Status != "official-frozen-before-execution" || contract.Class != "official" || !contract.PaperEvidenceEligibleAtRunCompletion {
		return errors.New("frozen V3 protocol identity gate failed")
	}
	if manifest.ProtocolVersion != contract.ProtocolVersion || manifest.Class != "official" || !manifest.PaperEvidenceEligible || manifest.RunName != contract.RunName || manifest.Status != "completed" || manifest.Git.ProtocolTag != contract.ProtocolTag || manifest.Git.DirtyAtStart || manifest.Failure != "" {
		return errors.New("shared-validation manifest identity or completion gate failed")
	}
	tagSHA, err := gitOutput("rev-parse", contract.ProtocolTag+"^{}")
	if err != nil || tagSHA != manifest.Git.SHA {
		return errors.New("shared-validation protocol tag does not match the run Git SHA")
	}
	if manifest.ExecutionOrder != contract.ExecutionOrder || manifest.PolicyCount != contract.PolicyCount || manifest.Repetitions != contract.RepetitionsPerPolicyWorkload || manifest.ExpectedRows != contract.ExpectedWorkloadExecutions || manifest.ExpectedRuns != contract.ExpectedWorkloadExecutions || manifest.ExpectedPods != contract.ExpectedPods || manifest.ObservedPods != contract.ExpectedPods {
		return errors.New("shared-validation manifest budget gate failed")
	}
	experimentInterval, err := time.ParseDuration(contract.ExperimentSchedulerInterval)
	if err != nil {
		return fmt.Errorf("parse experiment scheduler interval: %w", err)
	}
	idleInterval, err := time.ParseDuration(contract.RestoredIdleSchedulerInterval)
	if err != nil {
		return fmt.Errorf("parse restored scheduler interval: %w", err)
	}
	if !validSnapshot(manifest.Preflight, contract.ExpectedWorkers, experimentInterval.Seconds()) || manifest.Postflight == nil || !validSnapshot(*manifest.Postflight, contract.ExpectedWorkers, idleInterval.Seconds()) {
		return errors.New("shared-validation preflight or postflight gate failed")
	}
	if len(manifest.Runs) != len(protocol.TrainingWorkloads) || len(resultPaths) != len(protocol.TrainingWorkloads) {
		return errors.New("shared-validation workload count gate failed")
	}
	manifestRoot := filepath.Dir(manifestPath)
	for _, item := range manifest.Runs {
		if item.Status != "completed" || item.ExpectedRows != contract.PolicyCount*contract.RepetitionsPerPolicyWorkload || item.ResultRows != item.ExpectedRows || item.PodCount != item.ResultRows*protocol.Common.PodsPerWorkload {
			return fmt.Errorf("incomplete workload manifest entry %q", item.Workload)
		}
		for _, artifact := range item.Outputs {
			if err := verifyChecksum(filepath.Join(manifestRoot, filepath.Base(artifact.Path)), artifact.SHA256); err != nil {
				return err
			}
		}
	}
	for _, artifact := range manifest.Inputs {
		if err := verifyChecksum(filepath.Join(repoRoot, filepath.FromSlash(artifact.Path)), artifact.SHA256); err != nil {
			return err
		}
	}
	if search.ProtocolVersion != protocol.ProtocolVersion || search.Validation.BaselineCount != contract.BaselinePolicyCount || search.Validation.OptimizerFinalistCount != contract.OptimizerFinalistCount || search.Validation.PolicyCount != contract.PolicyCount || search.Validation.RepetitionsPerPolicy != contract.RepetitionsPerPolicyWorkload || search.Validation.WorkloadCount != len(protocol.TrainingWorkloads) || search.Validation.ExpectedRuns != contract.ExpectedWorkloadExecutions || search.Validation.ExpectedPods != contract.ExpectedPods {
		return errors.New("search-summary validation plan gate failed")
	}
	if len(weights.Runs) != contract.PolicyCount {
		return errors.New("validation weights policy count gate failed")
	}
	for i, run := range weights.Runs {
		if i >= len(contract.Policies) || run.Name != contract.Policies[i] {
			return errors.New("validation policy order differs from frozen protocol")
		}
	}
	return validateSelectedFinalists(search, weights, contract.BaselinePolicyCount, contract.OptimizerFinalistCount)
}

func validateSelectedFinalists(search searchSummary, weights evaluator.ExperimentSpec, baselineCount, expectedFinalists int) error {
	expected := make(map[string]evaluator.WeightSet, expectedFinalists)
	for _, arm := range search.Arms {
		selected := 0
		for _, candidate := range arm.PooledPareto {
			if !candidate.Selected {
				continue
			}
			selected++
			if candidate.ValidationName == "" || candidate.Arm != arm.Name {
				return errors.New("selected finalist identity gate failed")
			}
			expected[candidate.ValidationName] = candidate.Weights
		}
		if selected != arm.SelectedFinalistCount {
			return fmt.Errorf("selected finalist count mismatch for arm %q", arm.Name)
		}
	}
	if len(expected) != expectedFinalists {
		return fmt.Errorf("selected finalist count = %d, want %d", len(expected), expectedFinalists)
	}
	for _, run := range weights.Runs[baselineCount:] {
		want, ok := expected[run.Name]
		if !ok || !equalWeights(run.Weights, want) {
			return fmt.Errorf("validation finalist %q differs from search summary", run.Name)
		}
	}
	return nil
}

func buildOutput(protocol protocolDocument, manifest validationManifest, search searchSummary, weights evaluator.ExperimentSpec, batches []evaluator.BatchResult) (output, error) {
	contract := protocol.SharedValidation.Official
	observations := make(map[string]*policyObservations, len(weights.Runs))
	for _, run := range weights.Runs {
		observations[run.Name] = &policyObservations{weights: run.Weights, byWorkloadRep: make(map[string]map[int]evaluator.ComparisonRow)}
	}
	accepted := acceptance{Workloads: len(batches), Policies: len(weights.Runs), RepetitionsPerPolicyWorkload: contract.RepetitionsPerPolicyWorkload}
	seenWorkloads := make(map[string]struct{}, len(batches))
	for _, batch := range batches {
		if batch.PolicyVersion != evaluator.PolicyVersion || batch.MeasurementVersion != evaluator.MeasurementVersion || batch.SchedulerMode != evaluator.SchedulerModeCustomPolicy || !batch.MeasurementProtocol.SchedulerDecisionTimestampAvailable || !batch.MeasurementProtocol.SchedulerIntervalApplicable || math.Abs(batch.MeasurementProtocol.SchedulerIntervalSeconds-0.25) > 1e-12 || math.Abs(batch.MeasurementProtocol.PodPollIntervalSeconds-0.25) > 1e-12 {
			return output{}, fmt.Errorf("measurement contract gate failed for workload %q", batch.Workload)
		}
		workload := filepath.Base(batch.WorkloadPath)
		if _, duplicate := seenWorkloads[workload]; duplicate {
			return output{}, fmt.Errorf("duplicate workload %q", workload)
		}
		seenWorkloads[workload] = struct{}{}
		if len(batch.Results) != contract.PolicyCount*contract.RepetitionsPerPolicyWorkload || len(batch.Runs) != len(batch.Results) {
			return output{}, fmt.Errorf("result row count gate failed for workload %q", workload)
		}
		runsByName := make(map[string]evaluator.RunResult, len(batch.Runs))
		for _, run := range batch.Runs {
			runsByName[run.Name] = run
		}
		for _, row := range batch.Results {
			match := repetitionPattern.FindStringSubmatch(row.Name)
			if len(match) != 3 {
				return output{}, fmt.Errorf("invalid repeated run name %q", row.Name)
			}
			policyName := match[1]
			repetition, _ := strconv.Atoi(match[2])
			policy, ok := observations[policyName]
			if !ok || repetition < 1 || repetition > contract.RepetitionsPerPolicyWorkload || policy.byWorkloadRep[workload] != nil && policy.byWorkloadRep[workload][repetition].Name != "" {
				return output{}, fmt.Errorf("unexpected or duplicate result %q", row.Name)
			}
			if !row.WeightsApplicable || row.Weights == nil || !equalWeights(*row.Weights, policy.weights) || !validRow(row, protocol.Common.PodsPerWorkload) {
				return output{}, fmt.Errorf("safety or weight gate failed for result %q", row.Name)
			}
			run, ok := runsByName[row.Name]
			if !ok || !validRun(run, protocol.Common.PodsPerWorkload) || !reflect.DeepEqual(evaluator.ComparisonRowFromRun(run), row) {
				return output{}, fmt.Errorf("raw run gate failed for result %q", row.Name)
			}
			if policy.byWorkloadRep[workload] == nil {
				policy.byWorkloadRep[workload] = make(map[int]evaluator.ComparisonRow)
			}
			policy.byWorkloadRep[workload][repetition] = row
			accepted.ResultRows++
			accepted.Pods += row.PodCount
			accepted.Scheduled += row.ScheduledCount
			accepted.Completed += row.CompletedCount
			accepted.DecisionTimestamps += row.DecisionTimestampCount
			accepted.KubernetesTimestamps += row.KubernetesScheduledTimestampCount
			accepted.ObservationTimestamps += row.EvaluatorObservationTimestampCount
			accepted.Unschedulable += row.UnschedulableCount
		}
	}
	if accepted.ResultRows != manifest.ExpectedRows || accepted.Pods != manifest.ExpectedPods || accepted.Scheduled != accepted.Pods || accepted.Completed != accepted.Pods || accepted.DecisionTimestamps != accepted.Pods || accepted.KubernetesTimestamps != accepted.Pods || accepted.ObservationTimestamps != accepted.Pods || accepted.Unschedulable != 0 {
		return output{}, errors.New("aggregate safety gate failed")
	}

	armByPolicy, provisional, err := finalistMetadata(search)
	if err != nil {
		return output{}, err
	}
	policies := make([]policySummary, 0, len(weights.Runs))
	for i, run := range weights.Runs {
		arm := "baseline"
		if i >= contract.BaselinePolicyCount {
			arm = armByPolicy[run.Name]
		}
		item, err := summarizePolicy(run.Name, arm, i < contract.BaselinePolicyCount, observations[run.Name], protocol.TrainingWorkloads, contract.RepetitionsPerPolicyWorkload)
		if err != nil {
			return output{}, err
		}
		if objective, ok := provisional[run.Name]; ok {
			copy := objective
			item.ProvisionalObjectives = &copy
		}
		policies = append(policies, item)
	}
	front := markPareto(policies)
	armOutcomes, compared, err := summarizeArms(policies)
	if err != nil {
		return output{}, err
	}
	baselines := make([]string, 0)
	for _, policy := range policies {
		if policy.Baseline && policy.OnPareto {
			baselines = append(baselines, policy.Name)
		}
	}
	return output{
		SchemaVersion: "v3-validation-summary-v1", ProtocolVersion: manifest.ProtocolVersion,
		Acceptance: accepted, Policies: policies, ParetoFront: front, BaselinesOnFront: baselines,
		Arms: armOutcomes, Comparison: compared,
		Notes: []string{
			"The Pareto front uses repeated-mean point estimates and is not a statistical significance test.",
			"Confidence intervals are marginal 95 percent Student-t intervals over ten repetition-level objectives, each averaged across the three workloads.",
			"Two-stage mitigation is descriptively supported only when its false Pareto discovery proportion is lower than the one-shot control under the frozen shared-validation protocol.",
			"Each arm contributes only three selected finalists, so false-discovery proportions are descriptive small-denominator outcomes rather than inferential estimates.",
			"No V3 holdout is part of this protocol; surviving policies require a separately committed holdout before a generalization claim.",
		},
	}, nil
}

func validSnapshot(value snapshot, workers int, interval float64) bool {
	canonical := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	return value.WorkerCount == workers && value.SchedulerRolloutReady && math.Abs(value.SchedulerIntervalSeconds-interval) < 1e-12 && equalWeights(value.SchedulerWeights, canonical) && value.EvaluatorPodCount == 0
}

func validRow(row evaluator.ComparisonRow, pods int) bool {
	return row.PolicyVersion == evaluator.PolicyVersion && row.SchedulerMode == evaluator.SchedulerModeCustomPolicy && row.PodCount == pods && row.ScheduledCount == pods && row.CompletedCount == pods && row.UnschedulableCount == 0 && row.DecisionTimestampCount == pods && row.KubernetesScheduledTimestampCount == pods && row.EvaluatorObservationTimestampCount == pods
}

func validRun(run evaluator.RunResult, pods int) bool {
	if run.Summary.PodCount != pods || run.Summary.ScheduledCount != pods || run.Summary.CompletedCount != pods || run.Summary.UnschedulableCount != 0 || run.Summary.DecisionTimestampCount != pods || run.Summary.KubernetesScheduledTimestampCount != pods || run.Summary.EvaluatorObservationTimestampCount != pods || len(run.Pods) != pods {
		return false
	}
	for _, pod := range run.Pods {
		if pod.SubmittedAt == nil || pod.SchedulerDecisionAt == nil || pod.KubernetesScheduledAt == nil || pod.EvaluatorObservedScheduledAt == nil || pod.NodeName == "" || pod.Phase != "Succeeded" {
			return false
		}
	}
	return true
}

func finalistMetadata(search searchSummary) (map[string]string, map[string]optimizer.Objectives, error) {
	arms := make(map[string]string)
	objectives := make(map[string]optimizer.Objectives)
	for _, arm := range search.Arms {
		for _, finalist := range arm.PooledPareto {
			if !finalist.Selected {
				continue
			}
			if _, duplicate := arms[finalist.ValidationName]; duplicate {
				return nil, nil, fmt.Errorf("duplicate selected finalist %q", finalist.ValidationName)
			}
			arms[finalist.ValidationName] = arm.Name
			objectives[finalist.ValidationName] = finalist.Objectives
		}
	}
	return arms, objectives, nil
}

func summarizePolicy(name, arm string, baseline bool, observed *policyObservations, workloads []string, repetitions int) (policySummary, error) {
	item := policySummary{Name: name, Arm: arm, Baseline: baseline, Weights: observed.weights}
	latencyByRep := make([]float64, repetitions)
	nodesByRep := make([]float64, repetitions)
	for _, path := range workloads {
		workload := filepath.Base(path)
		rows := observed.byWorkloadRep[workload]
		if len(rows) != repetitions {
			return policySummary{}, fmt.Errorf("policy %q workload %q has %d repetitions, want %d", name, workload, len(rows), repetitions)
		}
		latencies := make([]float64, 0, repetitions)
		nodes := make([]float64, 0, repetitions)
		variance := make([]float64, 0, repetitions)
		for repetition := 1; repetition <= repetitions; repetition++ {
			row, ok := rows[repetition]
			if !ok {
				return policySummary{}, fmt.Errorf("policy %q workload %q missing repetition %d", name, workload, repetition)
			}
			latencies = append(latencies, row.P95SchedulingDecisionLatency)
			nodes = append(nodes, float64(row.MaxWorkloadOccupiedWorkerNodes))
			variance = append(variance, row.MeanWorkloadDominantRequestedLoadVariance)
			latencyByRep[repetition-1] += row.P95SchedulingDecisionLatency
			nodesByRep[repetition-1] += float64(row.MaxWorkloadOccupiedWorkerNodes)
			item.UnschedulableTotal += row.UnschedulableCount
		}
		item.ByWorkload = append(item.ByWorkload, workloadStatistics{Workload: strings.TrimSuffix(workload, filepath.Ext(workload)), Repetitions: repetitions, P95DecisionLatency: calculateStatistics(latencies), MaxOccupiedWorkers: calculateStatistics(nodes), LoadVariance: calculateStatistics(variance)})
	}
	for i := range latencyByRep {
		latencyByRep[i] /= float64(len(workloads))
		nodesByRep[i] /= float64(len(workloads))
	}
	item.Objectives = objectiveStatistics{Repetitions: repetitions, P95DecisionLatency: calculateStatistics(latencyByRep), MaxOccupiedWorkers: calculateStatistics(nodesByRep)}
	return item, nil
}

func markPareto(policies []policySummary) []paretoPoint {
	for i := range policies {
		for j := range policies {
			if i == j {
				continue
			}
			if dominates(policies[j], policies[i]) {
				policies[i].DominatedBy = append(policies[i].DominatedBy, policies[j].Name)
			}
		}
		sort.Strings(policies[i].DominatedBy)
		policies[i].OnPareto = len(policies[i].DominatedBy) == 0
		if !policies[i].Baseline {
			value := !policies[i].OnPareto
			policies[i].FalseParetoDiscovery = &value
		}
	}
	type key struct{ latency, nodes float64 }
	groups := make(map[key][]string)
	for _, policy := range policies {
		if !policy.OnPareto {
			continue
		}
		k := key{policy.Objectives.P95DecisionLatency.Mean, policy.Objectives.MaxOccupiedWorkers.Mean}
		groups[k] = append(groups[k], policy.Name)
	}
	front := make([]paretoPoint, 0, len(groups))
	for point, members := range groups {
		sort.Strings(members)
		front = append(front, paretoPoint{MeanP95DecisionLatency: point.latency, MeanMaxOccupiedWorkers: point.nodes, Members: members})
	}
	sort.Slice(front, func(i, j int) bool {
		if front[i].MeanP95DecisionLatency == front[j].MeanP95DecisionLatency {
			return front[i].MeanMaxOccupiedWorkers < front[j].MeanMaxOccupiedWorkers
		}
		return front[i].MeanP95DecisionLatency < front[j].MeanP95DecisionLatency
	})
	return front
}

func dominates(left, right policySummary) bool {
	leftLatency := left.Objectives.P95DecisionLatency.Mean
	leftNodes := left.Objectives.MaxOccupiedWorkers.Mean
	rightLatency := right.Objectives.P95DecisionLatency.Mean
	rightNodes := right.Objectives.MaxOccupiedWorkers.Mean
	return leftLatency <= rightLatency && leftNodes <= rightNodes && (leftLatency < rightLatency || leftNodes < rightNodes)
}

func summarizeArms(policies []policySummary) ([]armOutcome, comparison, error) {
	byArm := make(map[string]*armOutcome)
	for _, policy := range policies {
		if policy.Baseline {
			continue
		}
		arm := byArm[policy.Arm]
		if arm == nil {
			arm = &armOutcome{Name: policy.Arm}
			byArm[policy.Arm] = arm
		}
		arm.ValidatedFinalists++
		if policy.OnPareto {
			arm.RemainedNondominated++
			arm.NondominatedPolicies = append(arm.NondominatedPolicies, policy.Name)
		} else {
			arm.FalseParetoDiscoveries++
			arm.FalseDiscoveryPolicies = append(arm.FalseDiscoveryPolicies, policy.Name)
		}
	}
	orderedNames := []string{"one-shot-control", "two-stage-resampling"}
	result := make([]armOutcome, 0, len(orderedNames))
	for _, name := range orderedNames {
		arm := byArm[name]
		if arm == nil || arm.ValidatedFinalists == 0 {
			return nil, comparison{}, fmt.Errorf("missing validated finalists for arm %q", name)
		}
		arm.FalseParetoDiscoveryProportion = float64(arm.FalseParetoDiscoveries) / float64(arm.ValidatedFinalists)
		sort.Strings(arm.NondominatedPolicies)
		sort.Strings(arm.FalseDiscoveryPolicies)
		result = append(result, *arm)
	}
	delta := result[1].FalseParetoDiscoveryProportion - result[0].FalseParetoDiscoveryProportion
	return result, comparison{OneShotFalseDiscoveryProportion: result[0].FalseParetoDiscoveryProportion, TwoStageFalseDiscoveryProportion: result[1].FalseParetoDiscoveryProportion, TwoStageMinusOneShot: delta, TwoStageLower: delta < 0, DescriptiveMitigationSupported: delta < 0}, nil
}

func calculateStatistics(values []float64) statistics {
	mean, minimum, maximum := 0.0, values[0], values[0]
	for _, value := range values {
		mean += value
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	mean /= float64(len(values))
	variance := 0.0
	if len(values) > 1 {
		for _, value := range values {
			variance += math.Pow(value-mean, 2)
		}
		variance /= float64(len(values) - 1)
	}
	stdDev := math.Sqrt(variance)
	margin := 0.0
	if len(values) > 1 {
		margin = studentTCritical95(len(values)-1) * stdDev / math.Sqrt(float64(len(values)))
	}
	return statistics{Mean: mean, SampleStdDev: stdDev, Minimum: minimum, Maximum: maximum, Confidence95Lower: mean - margin, Confidence95Upper: mean + margin}
}

func studentTCritical95(df int) float64 {
	critical := [...]float64{0, 12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228, 2.201, 2.179, 2.160, 2.145, 2.131, 2.120, 2.110, 2.101, 2.093, 2.086, 2.080, 2.074, 2.069, 2.064, 2.060, 2.056, 2.052, 2.048, 2.045, 2.042}
	if df < 1 {
		return 0
	}
	if df < len(critical) {
		return critical[df]
	}
	return 1.96
}

func writeCSV(path string, policies []policySummary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	header := []string{"policy", "arm", "baseline", "free_cpu", "free_mem", "balance", "consolidation", "repetitions", "p95_mean_seconds", "p95_sample_stddev_seconds", "p95_ci95_lower_seconds", "p95_ci95_upper_seconds", "max_workers_mean", "max_workers_sample_stddev", "max_workers_ci95_lower", "max_workers_ci95_upper", "on_repeated_mean_point_estimate_pareto", "false_pareto_discovery", "dominated_by"}
	if err := writer.Write(header); err != nil {
		_ = file.Close()
		return err
	}
	for _, policy := range policies {
		falseDiscovery := ""
		if policy.FalseParetoDiscovery != nil {
			falseDiscovery = strconv.FormatBool(*policy.FalseParetoDiscovery)
		}
		record := []string{policy.Name, policy.Arm, strconv.FormatBool(policy.Baseline), formatFloat(policy.Weights.FreeCPU), formatFloat(policy.Weights.FreeMem), formatFloat(policy.Weights.Balance), formatFloat(policy.Weights.Consolidation), strconv.Itoa(policy.Objectives.Repetitions), formatFloat(policy.Objectives.P95DecisionLatency.Mean), formatFloat(policy.Objectives.P95DecisionLatency.SampleStdDev), formatFloat(policy.Objectives.P95DecisionLatency.Confidence95Lower), formatFloat(policy.Objectives.P95DecisionLatency.Confidence95Upper), formatFloat(policy.Objectives.MaxOccupiedWorkers.Mean), formatFloat(policy.Objectives.MaxOccupiedWorkers.SampleStdDev), formatFloat(policy.Objectives.MaxOccupiedWorkers.Confidence95Lower), formatFloat(policy.Objectives.MaxOccupiedWorkers.Confidence95Upper), strconv.FormatBool(policy.OnPareto), falseDiscovery, strings.Join(policy.DominatedBy, ";")}
		if err := writer.Write(record); err != nil {
			_ = file.Close()
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }

func loadJSON[T any](path string) (T, error) {
	var value T
	data, err := os.ReadFile(path)
	if err != nil {
		return value, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", path, err)
	}
	return value, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func inspectInputs(paths []string) ([]inputArtifact, error) {
	artifacts := make([]inputArtifact, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		artifacts = append(artifacts, inputArtifact{Path: filepath.Clean(path), SHA256: hex.EncodeToString(digest[:])})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

func verifyChecksum(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read checksummed input %s: %w", path, err)
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", path, actual, expected)
	}
	return nil
}

func inspectGenerator() (generator, error) {
	sha, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return generator{}, err
	}
	branch, err := gitOutput("branch", "--show-current")
	if err != nil {
		return generator{}, err
	}
	status, err := gitOutput("status", "--porcelain")
	if err != nil {
		return generator{}, err
	}
	if status != "" {
		return generator{}, errors.New("V3 validation summary generation requires a clean Git tree")
	}
	return generator{Command: "go run ./cmd/v3-validation-summary", GitSHA: sha, Branch: branch}, nil
}

func gitOutput(args ...string) (string, error) {
	command := exec.Command("git", args...)
	value, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(value)))
	}
	return strings.TrimSpace(string(value)), nil
}

func splitPaths(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func equalWeights(left, right evaluator.WeightSet) bool {
	return math.Abs(left.FreeCPU-right.FreeCPU) < 1e-12 && math.Abs(left.FreeMem-right.FreeMem) < 1e-12 && math.Abs(left.Balance-right.Balance) < 1e-12 && math.Abs(left.Consolidation-right.Consolidation) < 1e-12
}
