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
)

const expectedProtocolVersion = "v3-survivor-holdout-official"

const expectedProtocolPath = "experiments/protocol-v3-survivor-holdout.json"

var repetitionPattern = regexp.MustCompile(`^(.*)-r([0-9]{2})$`)

type options struct {
	inputPath     string
	protocolPath  string
	manifestPath  string
	sharedPath    string
	weightsPath   string
	outputPath    string
	csvOutputPath string
}

type protocolDocument struct {
	ProtocolVersion string `json:"protocolVersion"`
	Status          string `json:"status"`
	SourceEvidence  struct {
		Survivors []string `json:"survivors"`
	} `json:"sourceEvidence"`
	HoldoutWorkload struct {
		Path     string `json:"path"`
		PodCount int    `json:"podCount"`
	} `json:"holdoutWorkload"`
	Execution struct {
		PolicyCount                  int `json:"policyCount"`
		BaselinePolicyCount          int `json:"baselinePolicyCount"`
		OptimizerSurvivorCount       int `json:"optimizerSurvivorCount"`
		RepetitionsPerPolicyWorkload int `json:"repetitionsPerPolicyWorkload"`
		ExpectedWorkloadExecutions   int `json:"expectedWorkloadExecutions"`
		ExpectedPods                 int `json:"expectedPods"`
	} `json:"execution"`
	Holdout struct {
		Official validationContract `json:"official"`
	} `json:"holdout"`
}

type validationContract struct {
	Status                               string   `json:"status"`
	Class                                string   `json:"class"`
	PaperEvidenceEligibleAtRunCompletion bool     `json:"paperEvidenceEligibleAtRunCompletion"`
	ProtocolVersion                      string   `json:"protocolVersion"`
	ProtocolTag                          string   `json:"protocolTag"`
	RunName                              string   `json:"runName"`
	WeightsFile                          string   `json:"weightsFile"`
	Workloads                            []string `json:"workloads"`
	Policies                             []string `json:"policies"`
	ProvenanceFiles                      []string `json:"provenanceFiles"`
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
	Git                   struct {
		SHA          string `json:"sha"`
		ProtocolTag  string `json:"protocolTag"`
		DirtyAtStart bool   `json:"dirtyAtStart"`
	} `json:"git"`
	Inputs         []artifact    `json:"inputs"`
	ExecutionOrder string        `json:"executionOrder"`
	Repetitions    int           `json:"repetitionsPerPolicyWorkload"`
	PolicyCount    int           `json:"policyCount"`
	ExpectedRows   int           `json:"expectedResultRows"`
	ExpectedRuns   int           `json:"expectedWorkloadRuns"`
	ExpectedPods   int           `json:"expectedPods"`
	ObservedPods   int           `json:"observedPods"`
	Preflight      snapshot      `json:"preflight"`
	Runs           []workloadRun `json:"runs"`
	Postflight     *snapshot     `json:"postflight"`
	Failure        string        `json:"failure"`
}

type snapshot struct {
	WorkerCount              int                 `json:"workerCount"`
	SchedulerRolloutReady    bool                `json:"schedulerRolloutReady"`
	SchedulerIntervalSeconds float64             `json:"schedulerIntervalSeconds"`
	SchedulerWeights         evaluator.WeightSet `json:"schedulerCanonicalWeights"`
	EvaluatorPodCount        int                 `json:"evaluatorPodCount"`
}

type workloadRun struct {
	Workload     string     `json:"workload"`
	Path         string     `json:"path"`
	Status       string     `json:"status"`
	ExpectedRows int        `json:"expectedRows"`
	ResultRows   int        `json:"resultRows"`
	PodCount     int        `json:"podCount"`
	Outputs      []artifact `json:"outputs"`
}

type sharedSummary struct {
	Policies []struct {
		Name     string              `json:"name"`
		Arm      string              `json:"arm"`
		Baseline bool                `json:"baseline"`
		Weights  evaluator.WeightSet `json:"weights"`
		OnPareto bool                `json:"onRepeatedMeanPointEstimateParetoFront"`
	} `json:"policies"`
}

type artifact struct {
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

type policySummary struct {
	Name                 string              `json:"name"`
	Arm                  string              `json:"arm"`
	Baseline             bool                `json:"baseline"`
	FrozenSurvivor       bool                `json:"frozenSharedValidationSurvivor"`
	Weights              evaluator.WeightSet `json:"weights"`
	Repetitions          int                 `json:"repetitions"`
	P95DecisionLatency   statistics          `json:"p95SchedulingDecisionLatencySeconds"`
	MaxOccupiedWorkers   statistics          `json:"maxWorkloadOccupiedWorkerNodes"`
	LoadVariance         statistics          `json:"meanWorkloadDominantRequestedLoadVariance"`
	CPUToMemoryImbalance statistics          `json:"meanWorkloadCpuToMemoryImbalance"`
	UnschedulableTotal   int                 `json:"unschedulableTotal"`
	OnPareto             bool                `json:"onRepeatedMeanPointEstimateParetoFront"`
	DominatedBy          []string            `json:"dominatedBy,omitempty"`
}

type paretoPoint struct {
	MeanP95DecisionLatency float64  `json:"meanP95SchedulingDecisionLatencySeconds"`
	MeanMaxOccupiedWorkers float64  `json:"meanMaxWorkloadOccupiedWorkerNodes"`
	Members                []string `json:"members"`
}

type survivorOutcome struct {
	FrozenSurvivors      int      `json:"frozenSurvivors"`
	RemainedNondominated int      `json:"remainedNondominated"`
	DidNotRemain         int      `json:"didNotRemainNondominated"`
	SurvivalProportion   float64  `json:"survivalProportion"`
	NondominatedPolicies []string `json:"nondominatedPolicies"`
	DominatedPolicies    []string `json:"dominatedPolicies"`
	DescriptiveSupport   bool     `json:"descriptiveHoldoutSupport"`
}

type acceptance struct {
	Policies              int `json:"policies"`
	RepetitionsPerPolicy  int `json:"repetitionsPerPolicy"`
	ResultRows            int `json:"resultRows"`
	Pods                  int `json:"pods"`
	Scheduled             int `json:"scheduled"`
	Completed             int `json:"completed"`
	DecisionTimestamps    int `json:"decisionTimestamps"`
	KubernetesTimestamps  int `json:"kubernetesScheduledTimestamps"`
	ObservationTimestamps int `json:"evaluatorObservationTimestamps"`
	Unschedulable         int `json:"unschedulable"`
}

type generator struct {
	Command          string `json:"command"`
	WorkingDirectory string `json:"workingDirectory"`
	GitSHA           string `json:"gitSha"`
	Branch           string `json:"branch"`
}

type output struct {
	SchemaVersion    string          `json:"schemaVersion"`
	ProtocolVersion  string          `json:"protocolVersion"`
	GeneratedAt      time.Time       `json:"generatedAt"`
	Generator        generator       `json:"generator"`
	Inputs           []artifact      `json:"inputs"`
	Acceptance       acceptance      `json:"acceptance"`
	Policies         []policySummary `json:"policies"`
	ParetoFront      []paretoPoint   `json:"repeatedMeanPointEstimateParetoFront"`
	BaselinesOnFront []string        `json:"baselinesOnRepeatedMeanFront"`
	Survivors        survivorOutcome `json:"survivorOutcome"`
	Notes            []string        `json:"notes"`
}

func main() {
	if err := run(parseFlags()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.inputPath, "input", "", "Official holdout evaluator JSON")
	flag.StringVar(&opts.protocolPath, "protocol", "", "Frozen V3 holdout protocol JSON")
	flag.StringVar(&opts.manifestPath, "manifest", "", "Completed official holdout manifest")
	flag.StringVar(&opts.sharedPath, "shared-summary", "", "Committed V3 shared-validation summary")
	flag.StringVar(&opts.weightsPath, "weights", "", "Frozen holdout policy file")
	flag.StringVar(&opts.outputPath, "output", "", "Analysis JSON output")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Policy-level analysis CSV output")
	flag.Parse()
	return opts
}

func run(opts options) error {
	if opts.inputPath == "" || opts.protocolPath == "" || opts.manifestPath == "" || opts.sharedPath == "" || opts.weightsPath == "" || opts.outputPath == "" {
		return errors.New("--input, --protocol, --manifest, --shared-summary, --weights, and --output are required")
	}
	if opts.csvOutputPath == "" {
		opts.csvOutputPath = strings.TrimSuffix(opts.outputPath, filepath.Ext(opts.outputPath)) + ".csv"
	}
	generatedBy, err := inspectGenerator(exactCommand(opts))
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
	shared, err := loadJSON[sharedSummary](opts.sharedPath)
	if err != nil {
		return err
	}
	weights, err := evaluator.LoadExperiment(opts.weightsPath)
	if err != nil {
		return err
	}
	batch, err := loadJSON[evaluator.BatchResult](opts.inputPath)
	if err != nil {
		return err
	}
	repoRoot, err := gitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if err := validateProvenance(protocol, manifest, shared, weights, batch, repoRoot, opts.manifestPath); err != nil {
		return err
	}
	built, err := buildOutput(protocol, weights, shared, batch)
	if err != nil {
		return err
	}
	built.GeneratedAt = time.Now().UTC()
	built.Generator = generatedBy
	built.Inputs, err = inspectInputs([]string{opts.protocolPath, opts.manifestPath, opts.sharedPath, opts.weightsPath, opts.inputPath, strings.TrimSuffix(opts.inputPath, filepath.Ext(opts.inputPath)) + ".csv"})
	if err != nil {
		return err
	}
	if err := writeJSON(opts.outputPath, built); err != nil {
		return err
	}
	return writeCSV(opts.csvOutputPath, built.Policies)
}

func validateProvenance(protocol protocolDocument, manifest validationManifest, shared sharedSummary, weights evaluator.ExperimentSpec, batch evaluator.BatchResult, repoRoot, manifestPath string) error {
	contract := protocol.Holdout.Official
	if protocol.ProtocolVersion != expectedProtocolVersion || protocol.Status != "official-frozen-before-execution" || contract.ProtocolVersion != expectedProtocolVersion || contract.Status != "official-frozen-before-execution" || contract.Class != "official" || !contract.PaperEvidenceEligibleAtRunCompletion {
		return errors.New("frozen V3 holdout protocol identity gate failed")
	}
	if manifest.ProtocolVersion != contract.ProtocolVersion || manifest.Class != "official" || !manifest.PaperEvidenceEligible || manifest.RunName != contract.RunName || manifest.Status != "completed" || manifest.Git.ProtocolTag != contract.ProtocolTag || manifest.Git.DirtyAtStart || manifest.Failure != "" {
		return errors.New("holdout manifest identity or completion gate failed")
	}
	tagSHA, err := gitOutput("rev-parse", contract.ProtocolTag+"^{}")
	if err != nil || tagSHA != manifest.Git.SHA {
		return errors.New("holdout protocol tag does not match the run Git SHA")
	}
	if manifest.ExecutionOrder != contract.ExecutionOrder || manifest.PolicyCount != contract.PolicyCount || manifest.Repetitions != contract.RepetitionsPerPolicyWorkload || manifest.ExpectedRows != contract.ExpectedWorkloadExecutions || manifest.ExpectedRuns != contract.ExpectedWorkloadExecutions || manifest.ExpectedPods != contract.ExpectedPods || manifest.ObservedPods != contract.ExpectedPods {
		return errors.New("holdout manifest budget gate failed")
	}
	if protocol.Execution.PolicyCount != contract.PolicyCount || protocol.Execution.BaselinePolicyCount != contract.BaselinePolicyCount || protocol.Execution.OptimizerSurvivorCount != contract.OptimizerFinalistCount || protocol.Execution.RepetitionsPerPolicyWorkload != contract.RepetitionsPerPolicyWorkload || protocol.Execution.ExpectedWorkloadExecutions != contract.ExpectedWorkloadExecutions || protocol.Execution.ExpectedPods != contract.ExpectedPods {
		return errors.New("holdout protocol budget fields disagree")
	}
	if len(contract.Workloads) != 1 || contract.Workloads[0] != protocol.HoldoutWorkload.Path {
		return errors.New("holdout workload path differs between protocol sections")
	}
	experimentInterval, err := time.ParseDuration(contract.ExperimentSchedulerInterval)
	if err != nil {
		return err
	}
	idleInterval, err := time.ParseDuration(contract.RestoredIdleSchedulerInterval)
	if err != nil {
		return err
	}
	if !validSnapshot(manifest.Preflight, contract.ExpectedWorkers, experimentInterval.Seconds()) || manifest.Postflight == nil || !validSnapshot(*manifest.Postflight, contract.ExpectedWorkers, idleInterval.Seconds()) {
		return errors.New("holdout preflight or postflight gate failed")
	}
	if len(manifest.Runs) != 1 || manifest.Runs[0].Status != "completed" || manifest.Runs[0].Path != protocol.HoldoutWorkload.Path || manifest.Runs[0].ExpectedRows != contract.ExpectedWorkloadExecutions || manifest.Runs[0].ResultRows != contract.ExpectedWorkloadExecutions || manifest.Runs[0].PodCount != contract.ExpectedPods {
		return errors.New("holdout workload manifest gate failed")
	}
	manifestRoot := filepath.Dir(manifestPath)
	for _, item := range manifest.Runs[0].Outputs {
		if err := verifyChecksum(filepath.Join(manifestRoot, filepath.Base(item.Path)), item.SHA256); err != nil {
			return err
		}
	}
	expectedInputPaths := append([]string{}, contract.Workloads...)
	expectedInputPaths = append(expectedInputPaths, contract.WeightsFile, expectedProtocolPath)
	expectedInputPaths = append(expectedInputPaths, contract.ProvenanceFiles...)
	actualInputPaths := make([]string, 0, len(manifest.Inputs))
	for _, item := range manifest.Inputs {
		actualInputPaths = append(actualInputPaths, item.Path)
		if err := verifyChecksum(filepath.Join(repoRoot, filepath.FromSlash(item.Path)), item.SHA256); err != nil {
			return err
		}
	}
	sort.Strings(expectedInputPaths)
	sort.Strings(actualInputPaths)
	if !reflect.DeepEqual(actualInputPaths, expectedInputPaths) {
		return fmt.Errorf("holdout manifest inputs=%v, protocol requires %v", actualInputPaths, expectedInputPaths)
	}
	if len(weights.Runs) != contract.PolicyCount || len(contract.Policies) != contract.PolicyCount {
		return errors.New("holdout policy count gate failed")
	}
	for i, run := range weights.Runs {
		if run.Name != contract.Policies[i] {
			return errors.New("holdout policy order differs from frozen protocol")
		}
	}
	if err := validateFrozenSurvivors(protocol.SourceEvidence.Survivors, shared, weights, contract.BaselinePolicyCount); err != nil {
		return err
	}
	if batch.WorkloadPath != filepath.Join(repoRoot, filepath.FromSlash(protocol.HoldoutWorkload.Path)) || len(batch.ResolvedWorkload) != protocol.HoldoutWorkload.PodCount {
		return errors.New("holdout batch workload identity gate failed")
	}
	return nil
}

func validateFrozenSurvivors(expected []string, shared sharedSummary, weights evaluator.ExperimentSpec, baselineCount int) error {
	actual := make([]string, 0)
	sharedByName := make(map[string]struct {
		arm     string
		weights evaluator.WeightSet
	})
	for _, item := range shared.Policies {
		if item.Baseline || !item.OnPareto {
			continue
		}
		actual = append(actual, item.Name)
		sharedByName[item.Name] = struct {
			arm     string
			weights evaluator.WeightSet
		}{item.Arm, item.Weights}
	}
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) || len(weights.Runs)-baselineCount != len(expected) {
		return errors.New("frozen holdout survivors differ from shared-validation front")
	}
	for _, run := range weights.Runs[baselineCount:] {
		source, ok := sharedByName[run.Name]
		if !ok || source.arm == "" || !equalWeights(source.weights, run.Weights) {
			return fmt.Errorf("holdout survivor %q differs from shared-validation summary", run.Name)
		}
	}
	return nil
}

func buildOutput(protocol protocolDocument, weights evaluator.ExperimentSpec, shared sharedSummary, batch evaluator.BatchResult) (output, error) {
	contract := protocol.Holdout.Official
	if batch.PolicyVersion != evaluator.PolicyVersion || batch.MeasurementVersion != evaluator.MeasurementVersion || batch.SchedulerMode != evaluator.SchedulerModeCustomPolicy || !batch.MeasurementProtocol.SchedulerDecisionTimestampAvailable || !batch.MeasurementProtocol.SchedulerIntervalApplicable || math.Abs(batch.MeasurementProtocol.SchedulerIntervalSeconds-0.25) > 1e-12 || math.Abs(batch.MeasurementProtocol.PodPollIntervalSeconds-0.25) > 1e-12 {
		return output{}, errors.New("holdout measurement contract gate failed")
	}
	if len(batch.Results) != contract.ExpectedWorkloadExecutions || len(batch.Runs) != len(batch.Results) {
		return output{}, errors.New("holdout result row count gate failed")
	}
	runsByName := make(map[string]evaluator.RunResult, len(batch.Runs))
	for _, run := range batch.Runs {
		runsByName[run.Name] = run
	}
	rowsByPolicy := make(map[string]map[int]evaluator.ComparisonRow, len(weights.Runs))
	weightByPolicy := make(map[string]evaluator.WeightSet, len(weights.Runs))
	for _, run := range weights.Runs {
		rowsByPolicy[run.Name] = make(map[int]evaluator.ComparisonRow)
		weightByPolicy[run.Name] = run.Weights
	}
	accepted := acceptance{Policies: len(weights.Runs), RepetitionsPerPolicy: contract.RepetitionsPerPolicyWorkload}
	for _, row := range batch.Results {
		match := repetitionPattern.FindStringSubmatch(row.Name)
		if len(match) != 3 {
			return output{}, fmt.Errorf("invalid repeated run name %q", row.Name)
		}
		name := match[1]
		repetition, _ := strconv.Atoi(match[2])
		wantWeights, ok := weightByPolicy[name]
		if !ok || repetition < 1 || repetition > contract.RepetitionsPerPolicyWorkload || rowsByPolicy[name][repetition].Name != "" {
			return output{}, fmt.Errorf("unexpected or duplicate result %q", row.Name)
		}
		if !row.WeightsApplicable || row.Weights == nil || !equalWeights(*row.Weights, wantWeights) || !validRow(row, protocol.HoldoutWorkload.PodCount) {
			return output{}, fmt.Errorf("safety or weight gate failed for %q", row.Name)
		}
		run, ok := runsByName[row.Name]
		if !ok || !validRun(run, protocol.HoldoutWorkload.PodCount) || !reflect.DeepEqual(evaluator.ComparisonRowFromRun(run), row) {
			return output{}, fmt.Errorf("raw run gate failed for %q", row.Name)
		}
		rowsByPolicy[name][repetition] = row
		accepted.ResultRows++
		accepted.Pods += row.PodCount
		accepted.Scheduled += row.ScheduledCount
		accepted.Completed += row.CompletedCount
		accepted.DecisionTimestamps += row.DecisionTimestampCount
		accepted.KubernetesTimestamps += row.KubernetesScheduledTimestampCount
		accepted.ObservationTimestamps += row.EvaluatorObservationTimestampCount
		accepted.Unschedulable += row.UnschedulableCount
	}
	if accepted.ResultRows != contract.ExpectedWorkloadExecutions || accepted.Pods != contract.ExpectedPods || accepted.Scheduled != accepted.Pods || accepted.Completed != accepted.Pods || accepted.DecisionTimestamps != accepted.Pods || accepted.KubernetesTimestamps != accepted.Pods || accepted.ObservationTimestamps != accepted.Pods || accepted.Unschedulable != 0 {
		return output{}, errors.New("holdout aggregate safety gate failed")
	}
	armByPolicy := make(map[string]string)
	for _, item := range shared.Policies {
		armByPolicy[item.Name] = item.Arm
	}
	policies := make([]policySummary, 0, len(weights.Runs))
	for i, run := range weights.Runs {
		rows := rowsByPolicy[run.Name]
		if len(rows) != contract.RepetitionsPerPolicyWorkload {
			return output{}, fmt.Errorf("policy %q has %d repetitions", run.Name, len(rows))
		}
		latencies := make([]float64, 0, len(rows))
		nodes := make([]float64, 0, len(rows))
		variance := make([]float64, 0, len(rows))
		imbalance := make([]float64, 0, len(rows))
		unschedulable := 0
		for repetition := 1; repetition <= contract.RepetitionsPerPolicyWorkload; repetition++ {
			row, ok := rows[repetition]
			if !ok {
				return output{}, fmt.Errorf("policy %q missing repetition %d", run.Name, repetition)
			}
			latencies = append(latencies, row.P95SchedulingDecisionLatency)
			nodes = append(nodes, float64(row.MaxWorkloadOccupiedWorkerNodes))
			variance = append(variance, row.MeanWorkloadDominantRequestedLoadVariance)
			imbalance = append(imbalance, row.MeanWorkloadCPUToMemoryImbalance)
			unschedulable += row.UnschedulableCount
		}
		arm := "baseline"
		if i >= contract.BaselinePolicyCount {
			arm = armByPolicy[run.Name]
		}
		policies = append(policies, policySummary{
			Name: run.Name, Arm: arm, Baseline: i < contract.BaselinePolicyCount,
			FrozenSurvivor: i >= contract.BaselinePolicyCount, Weights: run.Weights,
			Repetitions: len(rows), P95DecisionLatency: calculateStatistics(latencies),
			MaxOccupiedWorkers: calculateStatistics(nodes), LoadVariance: calculateStatistics(variance),
			CPUToMemoryImbalance: calculateStatistics(imbalance), UnschedulableTotal: unschedulable,
		})
	}
	front := markPareto(policies)
	outcome := summarizeSurvivors(policies)
	baselines := make([]string, 0)
	for _, item := range policies {
		if item.Baseline && item.OnPareto {
			baselines = append(baselines, item.Name)
		}
	}
	return output{
		SchemaVersion: "v3-holdout-summary-v1", ProtocolVersion: protocol.ProtocolVersion,
		Acceptance: accepted, Policies: policies, ParetoFront: front, BaselinesOnFront: baselines, Survivors: outcome,
		Notes: []string{
			"This pre-existing workload was unseen to V3 search and policy selection, but its baseline-only V2 outcomes were known before the V3 holdout protocol; this is not a fully blinded holdout.",
			"The Pareto front uses repeated-mean point estimates and is not a statistical significance test.",
			"Confidence intervals are marginal 95 percent Student-t intervals over ten independent holdout repetitions.",
			"Each replay contains 24 Pods, so interpolated p95 remains sensitive to a few upper-tail observations.",
			"A survivor on this front supports only descriptive generalization to the fixed mixed-burst workload.",
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

func markPareto(policies []policySummary) []paretoPoint {
	for i := range policies {
		for j := range policies {
			if i != j && dominates(policies[j], policies[i]) {
				policies[i].DominatedBy = append(policies[i].DominatedBy, policies[j].Name)
			}
		}
		sort.Strings(policies[i].DominatedBy)
		policies[i].OnPareto = len(policies[i].DominatedBy) == 0
	}
	type key struct{ latency, nodes float64 }
	groups := make(map[key][]string)
	for _, item := range policies {
		if item.OnPareto {
			point := key{item.P95DecisionLatency.Mean, item.MaxOccupiedWorkers.Mean}
			groups[point] = append(groups[point], item.Name)
		}
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
	return left.P95DecisionLatency.Mean <= right.P95DecisionLatency.Mean && left.MaxOccupiedWorkers.Mean <= right.MaxOccupiedWorkers.Mean && (left.P95DecisionLatency.Mean < right.P95DecisionLatency.Mean || left.MaxOccupiedWorkers.Mean < right.MaxOccupiedWorkers.Mean)
}

func summarizeSurvivors(policies []policySummary) survivorOutcome {
	result := survivorOutcome{}
	for _, item := range policies {
		if !item.FrozenSurvivor {
			continue
		}
		result.FrozenSurvivors++
		if item.OnPareto {
			result.RemainedNondominated++
			result.NondominatedPolicies = append(result.NondominatedPolicies, item.Name)
		} else {
			result.DidNotRemain++
			result.DominatedPolicies = append(result.DominatedPolicies, item.Name)
		}
	}
	if result.FrozenSurvivors > 0 {
		result.SurvivalProportion = float64(result.RemainedNondominated) / float64(result.FrozenSurvivors)
	}
	result.DescriptiveSupport = result.RemainedNondominated > 0
	sort.Strings(result.NondominatedPolicies)
	sort.Strings(result.DominatedPolicies)
	return result
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
	header := []string{"policy", "arm", "baseline", "frozen_shared_validation_survivor", "free_cpu", "free_mem", "balance", "consolidation", "repetitions", "p95_mean_seconds", "p95_sample_stddev_seconds", "p95_ci95_lower_seconds", "p95_ci95_upper_seconds", "max_workers_mean", "max_workers_sample_stddev", "max_workers_ci95_lower", "max_workers_ci95_upper", "load_variance_mean", "load_variance_sample_stddev", "load_variance_ci95_lower", "load_variance_ci95_upper", "unschedulable_total", "on_repeated_mean_point_estimate_pareto", "dominated_by"}
	if err := writer.Write(header); err != nil {
		_ = file.Close()
		return err
	}
	for _, item := range policies {
		record := []string{item.Name, item.Arm, strconv.FormatBool(item.Baseline), strconv.FormatBool(item.FrozenSurvivor), formatFloat(item.Weights.FreeCPU), formatFloat(item.Weights.FreeMem), formatFloat(item.Weights.Balance), formatFloat(item.Weights.Consolidation), strconv.Itoa(item.Repetitions), formatFloat(item.P95DecisionLatency.Mean), formatFloat(item.P95DecisionLatency.SampleStdDev), formatFloat(item.P95DecisionLatency.Confidence95Lower), formatFloat(item.P95DecisionLatency.Confidence95Upper), formatFloat(item.MaxOccupiedWorkers.Mean), formatFloat(item.MaxOccupiedWorkers.SampleStdDev), formatFloat(item.MaxOccupiedWorkers.Confidence95Lower), formatFloat(item.MaxOccupiedWorkers.Confidence95Upper), formatFloat(item.LoadVariance.Mean), formatFloat(item.LoadVariance.SampleStdDev), formatFloat(item.LoadVariance.Confidence95Lower), formatFloat(item.LoadVariance.Confidence95Upper), strconv.Itoa(item.UnschedulableTotal), strconv.FormatBool(item.OnPareto), strings.Join(item.DominatedBy, ";")}
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

func inspectInputs(paths []string) ([]artifact, error) {
	items := make([]artifact, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(data)
		items = append(items, artifact{Path: filepath.Clean(path), SHA256: hex.EncodeToString(digest[:])})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func verifyChecksum(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if actual := hex.EncodeToString(digest[:]); actual != expected {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", path, actual, expected)
	}
	return nil
}

func inspectGenerator(command string) (generator, error) {
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
		return generator{}, errors.New("V3 holdout summary generation requires a clean Git tree")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return generator{}, err
	}
	return generator{Command: command, WorkingDirectory: workingDirectory, GitSHA: sha, Branch: branch}, nil
}

func exactCommand(opts options) string {
	return strings.Join([]string{
		"go run ./cmd/v3-holdout-summary",
		"--input " + opts.inputPath,
		"--protocol " + opts.protocolPath,
		"--manifest " + opts.manifestPath,
		"--shared-summary " + opts.sharedPath,
		"--weights " + opts.weightsPath,
		"--output " + opts.outputPath,
		"--csv-output " + opts.csvOutputPath,
	}, " ")
}

func gitOutput(args ...string) (string, error) {
	command := exec.Command("git", args...)
	value, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(value)))
	}
	return strings.TrimSpace(string(value)), nil
}

func equalWeights(left, right evaluator.WeightSet) bool {
	return math.Abs(left.FreeCPU-right.FreeCPU) < 1e-12 && math.Abs(left.FreeMem-right.FreeMem) < 1e-12 && math.Abs(left.Balance-right.Balance) < 1e-12 && math.Abs(left.Consolidation-right.Consolidation) < 1e-12
}
