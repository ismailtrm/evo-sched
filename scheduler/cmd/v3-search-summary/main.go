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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
	"evok8s-scheduler/pkg/policy"
)

const expectedProtocolVersion = "v3-budget-matched-resampling-official"
const crowdingInfinity = 1e9

type options struct {
	oneShotInputs  string
	twoStageInputs string
	baselinesPath  string
	protocolPath   string
	manifestPath   string
	outputPath     string
	csvOutputPath  string
	weightsPath    string
}

type officialManifest struct {
	ProtocolVersion        string `json:"protocolVersion"`
	Class                  string `json:"class"`
	PaperEvidenceEligible  bool   `json:"paperEvidenceEligible"`
	Status                 string `json:"status"`
	ExpectedCombinedBudget struct {
		OptimizerSamples   int `json:"optimizerSamples"`
		BaselineSamples    int `json:"baselineSamples"`
		PolicySamples      int `json:"policySamples"`
		WorkloadExecutions int `json:"workloadExecutions"`
		Pods               int `json:"pods"`
	} `json:"expectedCombinedBudget"`
	Executions []struct {
		Status              string `json:"status"`
		ChildManifestPath   string `json:"childManifestPath"`
		ChildManifestSHA256 string `json:"childManifestSha256"`
		LauncherLogPath     string `json:"launcherLogPath"`
		LauncherLogSHA256   string `json:"launcherLogSha256"`
	} `json:"executions"`
	FinalRestoration struct {
		Status   string `json:"status"`
		Snapshot struct {
			WorkerCount              int                 `json:"workerCount"`
			SchedulerRolloutReady    bool                `json:"schedulerRolloutReady"`
			SchedulerIntervalSeconds float64             `json:"schedulerIntervalSeconds"`
			SchedulerWeights         evaluator.WeightSet `json:"schedulerCanonicalWeights"`
			EvaluatorPodCount        int                 `json:"evaluatorPodCount"`
		} `json:"snapshot"`
	} `json:"finalRestoration"`
}

type protocolSpec struct {
	ProtocolVersion        string   `json:"protocolVersion"`
	Status                 string   `json:"status"`
	TrainingWorkloads      []string `json:"trainingWorkloads"`
	CommonSearchParameters struct {
		PodsPerWorkload int `json:"podsPerWorkload"`
	} `json:"commonSearchParameters"`
	Official struct {
		Seeds []int64 `json:"seeds"`
	} `json:"official"`
	SharedValidation struct {
		MaximumFinalistsPerArm       int `json:"maximumFinalistsPerArm"`
		RepetitionsPerPolicyWorkload int `json:"repetitionsPerPolicyWorkload"`
	} `json:"sharedValidation"`
}

type inputArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type provenance struct {
	Seed            int64  `json:"seed"`
	Name            string `json:"name"`
	Source          string `json:"source"`
	Generation      int    `json:"generation"`
	CreationOrdinal int    `json:"creationOrdinal"`
	EvaluationCount int    `json:"evaluationCount"`
}

type finalist struct {
	ValidationName string               `json:"validationName"`
	Arm            string               `json:"arm"`
	Weights        evaluator.WeightSet  `json:"weights"`
	Objectives     optimizer.Objectives `json:"provisionalObjectives"`
	Crowding       float64              `json:"crowdingDistance"`
	Provenance     []provenance         `json:"provenance"`
	Selected       bool                 `json:"selectedForValidation"`
	SelectionRole  []string             `json:"selectionRole,omitempty"`
	phenotypeKey   string
}

type seedSummary struct {
	Seed                    int64    `json:"seed"`
	OptimizerCandidateCount int      `json:"optimizerCandidateCount"`
	OptimizerSampleCount    int      `json:"optimizerSampleCount"`
	BaselineSampleCount     int      `json:"baselineSampleCount"`
	WorkloadExecutionCount  int      `json:"workloadExecutionCount"`
	ProvisionalMembers      int      `json:"optimizerOnlyCombinedParetoMembers"`
	BaselinesKept           []string `json:"baselinesKept"`
}

type safetySummary struct {
	WorkloadRecords   int `json:"workloadRecords"`
	Pods              int `json:"pods"`
	Unschedulable     int `json:"unschedulable"`
	InvalidResultRows int `json:"invalidResultRows"`
}

type armSummary struct {
	Name                        string        `json:"name"`
	Seeds                       []seedSummary `json:"seeds"`
	OptimizerCandidateCount     int           `json:"optimizerCandidateCount"`
	OptimizerSampleCount        int           `json:"optimizerSampleCount"`
	BaselineSampleCount         int           `json:"baselineSampleCount"`
	WorkloadExecutionCount      int           `json:"workloadExecutionCount"`
	UniqueProvisionalPhenotypes int           `json:"uniqueProvisionalPhenotypes"`
	PooledParetoPointCount      int           `json:"pooledParetoPointCount"`
	SelectedFinalistCount       int           `json:"selectedFinalistCount"`
	Safety                      safetySummary `json:"safety"`
	PooledPareto                []finalist    `json:"pooledPointEstimateParetoFront"`
}

type summary struct {
	SchemaVersion    string          `json:"schemaVersion"`
	ProtocolVersion  string          `json:"protocolVersion"`
	GeneratedAt      time.Time       `json:"generatedAt"`
	Inputs           []inputArtifact `json:"inputs"`
	MaximumFinalists int             `json:"maximumFinalistsPerArm"`
	Arms             []armSummary    `json:"arms"`
	Validation       validationPlan  `json:"validation"`
	Notes            []string        `json:"notes"`
}

type validationPlan struct {
	BaselineCount              int `json:"baselineCount"`
	OptimizerFinalistCount     int `json:"optimizerFinalistCount"`
	PolicyCount                int `json:"policyCount"`
	RepetitionsPerPolicy       int `json:"repetitionsPerPolicyWorkload"`
	WorkloadCount              int `json:"workloadCount"`
	ExpectedWorkloadExecutions int `json:"expectedWorkloadExecutions"`
	ExpectedPods               int `json:"expectedPods"`
}

type resultInput struct {
	Path   string
	Result optimizer.Result
}

func main() {
	if err := run(parseFlags()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.oneShotInputs, "one-shot-inputs", "", "Comma-separated one-shot result.json paths")
	flag.StringVar(&opts.twoStageInputs, "two-stage-inputs", "", "Comma-separated two-stage result.json paths")
	flag.StringVar(&opts.baselinesPath, "baselines", "", "Baseline experiment JSON")
	flag.StringVar(&opts.protocolPath, "protocol", "", "Frozen V3 protocol JSON")
	flag.StringVar(&opts.manifestPath, "manifest", "", "Completed official V3 search manifest")
	flag.StringVar(&opts.outputPath, "output", "", "Summary JSON output")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Pooled Pareto CSV output")
	flag.StringVar(&opts.weightsPath, "weights-output", "", "Shared-validation weights JSON output")
	flag.Parse()
	return opts
}

func run(opts options) error {
	if strings.TrimSpace(opts.baselinesPath) == "" || strings.TrimSpace(opts.protocolPath) == "" || strings.TrimSpace(opts.manifestPath) == "" ||
		strings.TrimSpace(opts.outputPath) == "" || strings.TrimSpace(opts.weightsPath) == "" {
		return errors.New("--baselines, --protocol, --manifest, --output, and --weights-output are required")
	}
	if opts.csvOutputPath == "" {
		opts.csvOutputPath = strings.TrimSuffix(opts.outputPath, filepath.Ext(opts.outputPath)) + ".csv"
	}

	protocol, err := loadProtocol(opts.protocolPath)
	if err != nil {
		return err
	}
	official, err := loadOfficialManifest(opts.manifestPath, protocol)
	if err != nil {
		return err
	}
	baselines, err := evaluator.LoadExperiment(opts.baselinesPath)
	if err != nil {
		return err
	}
	armInputs := []struct {
		name string
		raw  string
		mode optimizer.SearchMode
	}{
		{name: "one-shot-control", raw: opts.oneShotInputs, mode: optimizer.SearchModeBudgetOneShot},
		{name: "two-stage-resampling", raw: opts.twoStageInputs, mode: optimizer.SearchModeBudgetTwoStage},
	}

	built := summary{
		SchemaVersion:    "v3-search-summary-v1",
		ProtocolVersion:  protocol.ProtocolVersion,
		GeneratedAt:      time.Now().UTC(),
		MaximumFinalists: protocol.SharedValidation.MaximumFinalistsPerArm,
		Validation: validationPlan{
			BaselineCount:        len(baselines.Runs),
			RepetitionsPerPolicy: protocol.SharedValidation.RepetitionsPerPolicyWorkload,
			WorkloadCount:        len(protocol.TrainingWorkloads),
		},
		Notes: []string{
			"All reported fronts are provisional point-estimate Pareto fronts from search.",
			"Selected optimizer policies require shared repeated validation before any improvement claim.",
			"Exact phenotype duplicates must have identical provisional objectives; otherwise selection fails rather than silently choosing an occurrence.",
		},
	}
	allPaths := []string{opts.baselinesPath, opts.protocolPath, opts.manifestPath}
	selected := make([]finalist, 0)
	for _, arm := range armInputs {
		paths, err := splitPaths(arm.raw)
		if err != nil {
			return fmt.Errorf("%s inputs: %w", arm.name, err)
		}
		allPaths = append(allPaths, paths...)
		inputs, err := readResults(paths, arm.mode)
		if err != nil {
			return fmt.Errorf("%s: %w", arm.name, err)
		}
		if err := validateSeeds(inputs, protocol.Official.Seeds); err != nil {
			return fmt.Errorf("%s: %w", arm.name, err)
		}
		if err := validateBaselineDefinitions(inputs, baselines); err != nil {
			return fmt.Errorf("%s: %w", arm.name, err)
		}
		summarized, err := summarizeArm(arm.name, inputs, protocol.SharedValidation.MaximumFinalistsPerArm)
		if err != nil {
			return err
		}
		built.Arms = append(built.Arms, summarized)
		for _, candidate := range summarized.PooledPareto {
			if candidate.Selected {
				selected = append(selected, candidate)
			}
		}
	}
	artifacts, err := inspectInputs(allPaths)
	if err != nil {
		return err
	}
	built.Inputs = artifacts
	built.Validation.OptimizerFinalistCount = len(selected)
	built.Validation.PolicyCount = len(baselines.Runs) + len(selected)
	built.Validation.ExpectedWorkloadExecutions = built.Validation.PolicyCount * built.Validation.RepetitionsPerPolicy * built.Validation.WorkloadCount
	built.Validation.ExpectedPods = built.Validation.ExpectedWorkloadExecutions * protocol.CommonSearchParameters.PodsPerWorkload
	if err := validateCombinedBudget(official, built); err != nil {
		return err
	}

	if err := writeJSON(opts.outputPath, built); err != nil {
		return err
	}
	if err := writeCSV(opts.csvOutputPath, built.Arms); err != nil {
		return err
	}
	return writeWeights(opts.weightsPath, baselines, selected)
}

func loadOfficialManifest(path string, protocol protocolSpec) (officialManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return officialManifest{}, fmt.Errorf("read official manifest: %w", err)
	}
	var manifest officialManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return officialManifest{}, fmt.Errorf("decode official manifest: %w", err)
	}
	if manifest.ProtocolVersion != protocol.ProtocolVersion || manifest.Class != "official" ||
		manifest.PaperEvidenceEligible || manifest.Status != "completed" || len(manifest.Executions) != len(protocol.Official.Seeds)*2 {
		return officialManifest{}, errors.New("official manifest identity or completion gate failed")
	}
	root := filepath.Dir(path)
	for _, execution := range manifest.Executions {
		if execution.Status != "completed" {
			return officialManifest{}, errors.New("official manifest contains an incomplete execution")
		}
		if err := verifyChecksum(filepath.Join(root, execution.ChildManifestPath), execution.ChildManifestSHA256); err != nil {
			return officialManifest{}, err
		}
		if err := verifyChecksum(filepath.Join(root, execution.LauncherLogPath), execution.LauncherLogSHA256); err != nil {
			return officialManifest{}, err
		}
	}
	restored := manifest.FinalRestoration.Snapshot
	expectedWeights := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	if manifest.FinalRestoration.Status != "completed" || restored.WorkerCount != 4 ||
		!restored.SchedulerRolloutReady || restored.SchedulerIntervalSeconds != 2 ||
		restored.SchedulerWeights != expectedWeights || restored.EvaluatorPodCount != 0 {
		return officialManifest{}, errors.New("official manifest final restoration gate failed")
	}
	return manifest, nil
}

func verifyChecksum(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read checksummed artifact %s: %w", path, err)
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if actual != expected {
		return fmt.Errorf("artifact checksum mismatch for %s", path)
	}
	return nil
}

func validateCombinedBudget(manifest officialManifest, built summary) error {
	optimizerSamples := 0
	baselineSamples := 0
	workloadExecutions := 0
	pods := 0
	for _, arm := range built.Arms {
		optimizerSamples += arm.OptimizerSampleCount
		baselineSamples += arm.BaselineSampleCount
		workloadExecutions += arm.WorkloadExecutionCount
		pods += arm.Safety.Pods
	}
	want := manifest.ExpectedCombinedBudget
	if optimizerSamples != want.OptimizerSamples || baselineSamples != want.BaselineSamples ||
		optimizerSamples+baselineSamples != want.PolicySamples || workloadExecutions != want.WorkloadExecutions || pods != want.Pods {
		return errors.New("official manifest combined budget does not match result data")
	}
	return nil
}

func loadProtocol(path string) (protocolSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return protocolSpec{}, fmt.Errorf("read protocol: %w", err)
	}
	var protocol protocolSpec
	if err := json.Unmarshal(data, &protocol); err != nil {
		return protocolSpec{}, fmt.Errorf("decode protocol: %w", err)
	}
	if protocol.ProtocolVersion != expectedProtocolVersion {
		return protocolSpec{}, fmt.Errorf("protocol version=%q, want %q", protocol.ProtocolVersion, expectedProtocolVersion)
	}
	if protocol.Status != "official-frozen-before-execution" {
		return protocolSpec{}, fmt.Errorf("protocol status=%q, want official-frozen-before-execution", protocol.Status)
	}
	if protocol.SharedValidation.MaximumFinalistsPerArm < 1 ||
		protocol.SharedValidation.RepetitionsPerPolicyWorkload < 2 ||
		len(protocol.TrainingWorkloads) == 0 || protocol.CommonSearchParameters.PodsPerWorkload < 1 ||
		len(protocol.Official.Seeds) == 0 {
		return protocolSpec{}, errors.New("protocol search and validation dimensions must be positive")
	}
	return protocol, nil
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
		return nil, errors.New("at least one input is required")
	}
	return paths, nil
}

func readResults(paths []string, expectedMode optimizer.SearchMode) ([]resultInput, error) {
	seenSeeds := make(map[int64]struct{}, len(paths))
	inputs := make([]resultInput, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var result optimizer.Result
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		if result.PolicyVersion != evaluator.PolicyVersion || result.MeasurementVersion != evaluator.MeasurementVersion {
			return nil, fmt.Errorf("%s: incompatible policy or measurement version", path)
		}
		if result.Config.SearchMode != expectedMode {
			return nil, fmt.Errorf("%s: search mode=%q, want %q", path, result.Config.SearchMode, expectedMode)
		}
		if _, exists := seenSeeds[result.Config.Seed]; exists {
			return nil, fmt.Errorf("duplicate seed %d", result.Config.Seed)
		}
		seenSeeds[result.Config.Seed] = struct{}{}
		inputs = append(inputs, resultInput{Path: path, Result: result})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Result.Config.Seed < inputs[j].Result.Config.Seed })
	return inputs, nil
}

func validateSeeds(inputs []resultInput, expected []int64) error {
	if len(inputs) != len(expected) {
		return fmt.Errorf("seed count=%d, want %d", len(inputs), len(expected))
	}
	want := append([]int64(nil), expected...)
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	for i := range inputs {
		if inputs[i].Result.Config.Seed != want[i] {
			return fmt.Errorf("seed[%d]=%d, want %d", i, inputs[i].Result.Config.Seed, want[i])
		}
	}
	return nil
}

func validateBaselineDefinitions(inputs []resultInput, expected evaluator.ExperimentSpec) error {
	want := make(map[string]evaluator.WeightSet, len(expected.Runs))
	for _, run := range expected.Runs {
		want[run.Name] = run.Weights
	}
	for _, input := range inputs {
		if len(input.Result.Baselines) != len(want) {
			return fmt.Errorf("seed %d baseline count=%d, want %d", input.Result.Config.Seed, len(input.Result.Baselines), len(want))
		}
		for _, baseline := range input.Result.Baselines {
			weights, exists := want[baseline.Name]
			if !exists || weights != baseline.Weights {
				return fmt.Errorf("seed %d baseline %q does not match committed definition", input.Result.Config.Seed, baseline.Name)
			}
		}
	}
	return nil
}

func summarizeArm(name string, inputs []resultInput, maximum int) (armSummary, error) {
	built := armSummary{Name: name}
	pool := make([]finalist, 0)
	for _, input := range inputs {
		result := input.Result
		if result.Budget == nil {
			return armSummary{}, fmt.Errorf("%s seed %d has no budget summary", name, result.Config.Seed)
		}
		if result.Budget.ActualOptimizerSamples != result.Budget.ExpectedOptimizerSamples ||
			result.Budget.ActualBaselineSamples != result.Budget.ExpectedBaselineSamples ||
			result.Budget.ActualWorkloadExecutions != result.Budget.ExpectedWorkloadExecutions {
			return armSummary{}, fmt.Errorf("%s seed %d failed exact budget gate", name, result.Config.Seed)
		}
		seedSafety := auditSafety(result)
		if seedSafety.Unschedulable != 0 || seedSafety.InvalidResultRows != 0 {
			return armSummary{}, fmt.Errorf("%s seed %d failed safety gate", name, result.Config.Seed)
		}
		kept := make([]string, 0)
		provisionalMembers := 0
		for _, candidate := range result.CombinedParetoFront {
			if candidate.Source == "baseline" {
				kept = append(kept, candidate.Name)
				continue
			}
			provisionalMembers++
			normalized, err := policy.Normalize(candidate.Weights)
			if err != nil || !weightsWithinTolerance(normalized, candidate.Weights, 1e-12) {
				return armSummary{}, fmt.Errorf("%s seed %d candidate %s has noncanonical weights", name, result.Config.Seed, candidate.Name)
			}
			pool = append(pool, finalist{
				Arm:        name,
				Weights:    candidate.Weights,
				Objectives: candidate.Objectives,
				Provenance: []provenance{{
					Seed: result.Config.Seed, Name: candidate.Name, Source: candidate.Source,
					Generation: candidate.Generation, CreationOrdinal: candidate.CreationOrdinal,
					EvaluationCount: candidate.EvaluationCount,
				}},
				phenotypeKey: weightKey(candidate.Weights),
			})
		}
		sort.Strings(kept)
		built.Seeds = append(built.Seeds, seedSummary{
			Seed: result.Config.Seed, OptimizerCandidateCount: result.Budget.UniqueOptimizerCandidates,
			OptimizerSampleCount:   result.Budget.ActualOptimizerSamples,
			BaselineSampleCount:    result.Budget.ActualBaselineSamples,
			WorkloadExecutionCount: result.Budget.ActualWorkloadExecutions,
			ProvisionalMembers:     provisionalMembers, BaselinesKept: kept,
		})
		built.OptimizerCandidateCount += result.Budget.UniqueOptimizerCandidates
		built.OptimizerSampleCount += result.Budget.ActualOptimizerSamples
		built.BaselineSampleCount += result.Budget.ActualBaselineSamples
		built.WorkloadExecutionCount += result.Budget.ActualWorkloadExecutions
		built.Safety.WorkloadRecords += seedSafety.WorkloadRecords
		built.Safety.Pods += seedSafety.Pods
		built.Safety.Unschedulable += seedSafety.Unschedulable
		built.Safety.InvalidResultRows += seedSafety.InvalidResultRows
	}

	unique, err := deduplicatePhenotypes(pool)
	if err != nil {
		return armSummary{}, fmt.Errorf("%s: %w", name, err)
	}
	built.UniqueProvisionalPhenotypes = len(unique)
	front := pooledPareto(unique)
	assignCrowding(front)
	selectFinalists(front, maximum)
	built.PooledPareto = front
	built.PooledParetoPointCount = len(front)
	for _, candidate := range front {
		if candidate.Selected {
			built.SelectedFinalistCount++
		}
	}
	return built, nil
}

func weightsWithinTolerance(a, b evaluator.WeightSet, tolerance float64) bool {
	return math.Abs(a.FreeCPU-b.FreeCPU) <= tolerance &&
		math.Abs(a.FreeMem-b.FreeMem) <= tolerance &&
		math.Abs(a.Balance-b.Balance) <= tolerance &&
		math.Abs(a.Consolidation-b.Consolidation) <= tolerance
}

func auditSafety(result optimizer.Result) safetySummary {
	built := safetySummary{}
	all := append(append([]optimizer.Candidate(nil), result.Baselines...), result.Evaluated...)
	for _, candidate := range all {
		for _, sample := range candidate.EvaluationSamples {
			for _, workload := range sample.Data.Workloads {
				built.WorkloadRecords++
				built.Pods += workload.PodCount
				built.Unschedulable += workload.UnschedulableCount
				if workload.PodCount == 0 || workload.ScheduledCount != workload.PodCount ||
					workload.CompletedCount != workload.PodCount || workload.DecisionTimestampCount != workload.PodCount ||
					workload.KubernetesScheduledTimestampCount != workload.PodCount ||
					workload.EvaluatorObservationTimestampCount != workload.PodCount {
					built.InvalidResultRows++
				}
			}
		}
	}
	return built
}

func deduplicatePhenotypes(candidates []finalist) ([]finalist, error) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].phenotypeKey != candidates[j].phenotypeKey {
			return candidates[i].phenotypeKey < candidates[j].phenotypeKey
		}
		if candidates[i].Provenance[0].Seed != candidates[j].Provenance[0].Seed {
			return candidates[i].Provenance[0].Seed < candidates[j].Provenance[0].Seed
		}
		return candidates[i].Provenance[0].Name < candidates[j].Provenance[0].Name
	})
	unique := make([]finalist, 0, len(candidates))
	byKey := make(map[string]int)
	for _, candidate := range candidates {
		idx, exists := byKey[candidate.phenotypeKey]
		if !exists {
			byKey[candidate.phenotypeKey] = len(unique)
			unique = append(unique, candidate)
			continue
		}
		if unique[idx].Objectives != candidate.Objectives {
			return nil, fmt.Errorf("phenotype %s has distinct provisional objectives", candidate.phenotypeKey)
		}
		unique[idx].Provenance = append(unique[idx].Provenance, candidate.Provenance...)
	}
	return unique, nil
}

func pooledPareto(candidates []finalist) []finalist {
	front := make([]finalist, 0, len(candidates))
	for i, candidate := range candidates {
		dominated := false
		for j := range candidates {
			if i != j && dominates(candidates[j].Objectives, candidate.Objectives) {
				dominated = true
				break
			}
		}
		if !dominated {
			front = append(front, candidate)
		}
	}
	sort.Slice(front, func(i, j int) bool {
		if front[i].Objectives.AverageP95SchedulingDecisionLatency == front[j].Objectives.AverageP95SchedulingDecisionLatency {
			return front[i].Objectives.AverageMaxWorkloadOccupiedWorkerNodes < front[j].Objectives.AverageMaxWorkloadOccupiedWorkerNodes
		}
		return front[i].Objectives.AverageP95SchedulingDecisionLatency < front[j].Objectives.AverageP95SchedulingDecisionLatency
	})
	return front
}

func dominates(a, b optimizer.Objectives) bool {
	return a.AverageP95SchedulingDecisionLatency <= b.AverageP95SchedulingDecisionLatency &&
		a.AverageMaxWorkloadOccupiedWorkerNodes <= b.AverageMaxWorkloadOccupiedWorkerNodes &&
		(a.AverageP95SchedulingDecisionLatency < b.AverageP95SchedulingDecisionLatency ||
			a.AverageMaxWorkloadOccupiedWorkerNodes < b.AverageMaxWorkloadOccupiedWorkerNodes)
}

func assignCrowding(front []finalist) {
	if len(front) == 0 {
		return
	}
	if len(front) <= 2 {
		for i := range front {
			front[i].Crowding = crowdingInfinity
		}
		return
	}
	for i := range front {
		front[i].Crowding = 0
	}
	objectives := []func(finalist) float64{
		func(c finalist) float64 { return c.Objectives.AverageP95SchedulingDecisionLatency },
		func(c finalist) float64 { return c.Objectives.AverageMaxWorkloadOccupiedWorkerNodes },
	}
	for _, objective := range objectives {
		indices := make([]int, len(front))
		for i := range indices {
			indices[i] = i
		}
		sort.Slice(indices, func(i, j int) bool {
			a, b := objective(front[indices[i]]), objective(front[indices[j]])
			if a == b {
				return front[indices[i]].phenotypeKey < front[indices[j]].phenotypeKey
			}
			return a < b
		})
		front[indices[0]].Crowding = crowdingInfinity
		front[indices[len(indices)-1]].Crowding = crowdingInfinity
		minimum, maximum := objective(front[indices[0]]), objective(front[indices[len(indices)-1]])
		if minimum == maximum {
			continue
		}
		for i := 1; i < len(indices)-1; i++ {
			if front[indices[i]].Crowding != crowdingInfinity {
				front[indices[i]].Crowding += (objective(front[indices[i+1]]) - objective(front[indices[i-1]])) / (maximum - minimum)
			}
		}
	}
}

func selectFinalists(front []finalist, maximum int) {
	if len(front) == 0 || maximum < 1 {
		return
	}
	pendingBest := 0
	nodeBest := 0
	for i := range front {
		if front[i].Objectives.AverageP95SchedulingDecisionLatency < front[pendingBest].Objectives.AverageP95SchedulingDecisionLatency ||
			(front[i].Objectives.AverageP95SchedulingDecisionLatency == front[pendingBest].Objectives.AverageP95SchedulingDecisionLatency &&
				front[i].Objectives.AverageMaxWorkloadOccupiedWorkerNodes < front[pendingBest].Objectives.AverageMaxWorkloadOccupiedWorkerNodes) {
			pendingBest = i
		}
		if front[i].Objectives.AverageMaxWorkloadOccupiedWorkerNodes < front[nodeBest].Objectives.AverageMaxWorkloadOccupiedWorkerNodes ||
			(front[i].Objectives.AverageMaxWorkloadOccupiedWorkerNodes == front[nodeBest].Objectives.AverageMaxWorkloadOccupiedWorkerNodes &&
				front[i].Objectives.AverageP95SchedulingDecisionLatency < front[nodeBest].Objectives.AverageP95SchedulingDecisionLatency) {
			nodeBest = i
		}
	}
	markSelected(front, pendingBest, "pending-best")
	if maximum > 1 {
		markSelected(front, nodeBest, "node-best")
	}
	remaining := make([]int, 0)
	for i := range front {
		if !front[i].Selected {
			remaining = append(remaining, i)
		}
	}
	sort.Slice(remaining, func(i, j int) bool {
		a, b := front[remaining[i]], front[remaining[j]]
		if a.Crowding == b.Crowding {
			return a.phenotypeKey < b.phenotypeKey
		}
		return a.Crowding > b.Crowding
	})
	selectedCount := 0
	for i := range front {
		if front[i].Selected {
			selectedCount++
		}
	}
	for _, idx := range remaining {
		if selectedCount >= maximum {
			break
		}
		markSelected(front, idx, "crowding")
		selectedCount++
	}
	for i := range front {
		if front[i].Selected {
			p := front[i].Provenance[0]
			front[i].ValidationName = fmt.Sprintf("v3-%s-s%d-%s", validationArmPrefix(front[i].Arm), p.Seed, p.Name)
		}
	}
}

func markSelected(front []finalist, index int, role string) {
	front[index].Selected = true
	for _, existing := range front[index].SelectionRole {
		if existing == role {
			return
		}
	}
	front[index].SelectionRole = append(front[index].SelectionRole, role)
}

func validationArmPrefix(arm string) string {
	if arm == "one-shot-control" {
		return "one-shot"
	}
	return "two-stage"
}

func weightKey(weights evaluator.WeightSet) string {
	return fmt.Sprintf("%016x:%016x:%016x:%016x",
		math.Float64bits(weights.FreeCPU), math.Float64bits(weights.FreeMem),
		math.Float64bits(weights.Balance), math.Float64bits(weights.Consolidation))
}

func inspectInputs(paths []string) ([]inputArtifact, error) {
	artifacts := make([]inputArtifact, 0, len(paths))
	seen := make(map[string]struct{})
	for _, path := range paths {
		clean := filepath.Clean(path)
		if _, exists := seen[clean]; exists {
			continue
		}
		seen[clean] = struct{}{}
		data, err := os.ReadFile(clean)
		if err != nil {
			return nil, fmt.Errorf("read input %s: %w", clean, err)
		}
		digest := sha256.Sum256(data)
		artifacts = append(artifacts, inputArtifact{Path: clean, SHA256: hex.EncodeToString(digest[:])})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
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

func writeCSV(path string, arms []armSummary) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"arm", "validation_name", "seed", "candidate_name", "generation", "evaluation_count", "free_cpu", "free_mem", "balance", "consolidation", "average_p95_scheduling_decision_latency_seconds", "average_max_workload_occupied_worker_nodes", "crowding_distance", "selected", "selection_roles"}); err != nil {
		_ = file.Close()
		return err
	}
	for _, arm := range arms {
		for _, candidate := range arm.PooledPareto {
			p := candidate.Provenance[0]
			record := []string{
				arm.Name, candidate.ValidationName, strconv.FormatInt(p.Seed, 10), p.Name,
				strconv.Itoa(p.Generation), strconv.Itoa(p.EvaluationCount),
				strconv.FormatFloat(candidate.Weights.FreeCPU, 'g', -1, 64),
				strconv.FormatFloat(candidate.Weights.FreeMem, 'g', -1, 64),
				strconv.FormatFloat(candidate.Weights.Balance, 'g', -1, 64),
				strconv.FormatFloat(candidate.Weights.Consolidation, 'g', -1, 64),
				strconv.FormatFloat(candidate.Objectives.AverageP95SchedulingDecisionLatency, 'g', -1, 64),
				strconv.FormatFloat(candidate.Objectives.AverageMaxWorkloadOccupiedWorkerNodes, 'g', -1, 64),
				strconv.FormatFloat(candidate.Crowding, 'g', -1, 64), strconv.FormatBool(candidate.Selected),
				strings.Join(candidate.SelectionRole, ";"),
			}
			if err := writer.Write(record); err != nil {
				_ = file.Close()
				return err
			}
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeWeights(path string, baselines evaluator.ExperimentSpec, selected []finalist) error {
	runs := append([]evaluator.ExperimentRunSpec(nil), baselines.Runs...)
	for _, candidate := range selected {
		runs = append(runs, evaluator.ExperimentRunSpec{Name: candidate.ValidationName, Weights: candidate.Weights})
	}
	return writeJSON(path, evaluator.ExperimentSpec{PolicyVersion: evaluator.PolicyVersion, Runs: runs})
}
