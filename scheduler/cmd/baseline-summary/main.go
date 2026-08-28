package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"evok8s-scheduler/pkg/evaluator"
)

var repetitionSuffix = regexp.MustCompile(`-r[0-9]{2}$`)

type options struct {
	inputs        string
	outputPath    string
	csvOutputPath string
}

type statistics struct {
	Mean              float64 `json:"mean"`
	SampleStdDev      float64 `json:"sampleStdDev"`
	Minimum           float64 `json:"minimum"`
	Maximum           float64 `json:"maximum"`
	Confidence95Lower float64 `json:"confidence95Lower"`
	Confidence95Upper float64 `json:"confidence95Upper"`
}

type summaryRow struct {
	Scope                                     string                  `json:"scope"`
	Workload                                  string                  `json:"workload"`
	WorkloadCount                             int                     `json:"workloadCount"`
	SchedulerMode                             evaluator.SchedulerMode `json:"schedulerMode"`
	SchedulerName                             string                  `json:"schedulerName"`
	PolicyName                                string                  `json:"policyName"`
	Weights                                   *evaluator.WeightSet    `json:"weights,omitempty"`
	WeightsApplicable                         bool                    `json:"weightsApplicable"`
	Repetitions                               int                     `json:"repetitions"`
	UnschedulableTotal                        int                     `json:"unschedulableTotal"`
	DecisionLatencyApplicable                 bool                    `json:"decisionLatencyApplicable"`
	P95SchedulingDecisionLatency              *statistics             `json:"p95SchedulingDecisionLatencySeconds,omitempty"`
	MaxWorkloadOccupiedWorkerNodes            statistics              `json:"maxWorkloadOccupiedWorkerNodes"`
	MeanWorkloadDominantRequestedLoadVariance statistics              `json:"meanWorkloadDominantRequestedLoadVariance"`
	MeanWorkloadCPUToMemoryImbalance          statistics              `json:"meanWorkloadCpuToMemoryImbalance"`
}

type summaryOutput struct {
	SchemaVersion      string       `json:"schemaVersion"`
	MeasurementVersion string       `json:"measurementVersion"`
	SourceFiles        []string     `json:"sourceFiles"`
	ResultRows         int          `json:"resultRows"`
	ByWorkload         []summaryRow `json:"byWorkload"`
	Aggregates         []summaryRow `json:"aggregates"`
}

type observation struct {
	workload          string
	schedulerMode     evaluator.SchedulerMode
	schedulerName     string
	policyName        string
	weights           *evaluator.WeightSet
	weightsApplicable bool
	row               evaluator.ComparisonRow
}

type groupKey struct {
	workload      string
	schedulerMode evaluator.SchedulerMode
	schedulerName string
	policyName    string
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.inputs, "inputs", "", "Comma-separated evaluator batch JSON files")
	flag.StringVar(&opts.outputPath, "output", "", "Summary JSON output path")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Summary CSV output path; defaults next to --output")
	flag.Parse()
	return opts
}

func run(opts options) error {
	paths := splitPaths(opts.inputs)
	if len(paths) == 0 {
		return fmt.Errorf("--inputs is required")
	}
	if opts.outputPath == "" {
		return fmt.Errorf("--output is required")
	}
	if opts.csvOutputPath == "" {
		ext := filepath.Ext(opts.outputPath)
		opts.csvOutputPath = strings.TrimSuffix(opts.outputPath, ext) + ".csv"
	}

	batches := make([]evaluator.BatchResult, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var batch evaluator.BatchResult
		if err := json.Unmarshal(data, &batch); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		batches = append(batches, batch)
	}

	output, err := summarizeBatches(batches, paths)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("encode summary: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(opts.outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", opts.outputPath, err)
	}

	csvFile, err := os.Create(opts.csvOutputPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", opts.csvOutputPath, err)
	}
	if err := writeSummaryCSV(csvFile, output); err != nil {
		_ = csvFile.Close()
		return fmt.Errorf("write %s: %w", opts.csvOutputPath, err)
	}
	if err := csvFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", opts.csvOutputPath, err)
	}
	return nil
}

func summarizeBatches(batches []evaluator.BatchResult, sourceFiles []string) (summaryOutput, error) {
	if len(batches) == 0 {
		return summaryOutput{}, fmt.Errorf("no evaluator batches provided")
	}
	measurementVersion := batches[0].MeasurementVersion
	observations := make([]observation, 0)
	for _, batch := range batches {
		if batch.MeasurementVersion != measurementVersion {
			return summaryOutput{}, fmt.Errorf("measurement version mismatch: %q != %q", batch.MeasurementVersion, measurementVersion)
		}
		if batch.SchedulerMode != evaluator.SchedulerModeCustomPolicy && batch.SchedulerMode != evaluator.SchedulerModeExternalReference {
			return summaryOutput{}, fmt.Errorf("unsupported scheduler mode %q", batch.SchedulerMode)
		}
		for _, row := range batch.Results {
			observations = append(observations, observation{
				workload:          batch.Workload,
				schedulerMode:     batch.SchedulerMode,
				schedulerName:     row.SchedulerName,
				policyName:        repetitionSuffix.ReplaceAllString(row.Name, ""),
				weights:           cloneWeights(row.Weights),
				weightsApplicable: row.WeightsApplicable,
				row:               row,
			})
		}
	}
	if len(observations) == 0 {
		return summaryOutput{}, fmt.Errorf("evaluator batches contain no result rows")
	}

	byWorkload, err := summarizeGroups(observations, true)
	if err != nil {
		return summaryOutput{}, err
	}
	aggregates, err := summarizeGroups(observations, false)
	if err != nil {
		return summaryOutput{}, err
	}
	return summaryOutput{
		SchemaVersion:      "v1",
		MeasurementVersion: measurementVersion,
		SourceFiles:        append([]string(nil), sourceFiles...),
		ResultRows:         len(observations),
		ByWorkload:         byWorkload,
		Aggregates:         aggregates,
	}, nil
}

func summarizeGroups(observations []observation, byWorkload bool) ([]summaryRow, error) {
	groups := make(map[groupKey][]observation)
	for _, item := range observations {
		workload := "all-workloads"
		if byWorkload {
			workload = item.workload
		}
		key := groupKey{
			workload:      workload,
			schedulerMode: item.schedulerMode,
			schedulerName: item.schedulerName,
			policyName:    item.policyName,
		}
		groups[key] = append(groups[key], item)
	}

	rows := make([]summaryRow, 0, len(groups))
	for key, items := range groups {
		row, err := summarizeGroup(key, items, byWorkload)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left := fmt.Sprintf("%s\x00%s\x00%s\x00%s", rows[i].Workload, rows[i].SchedulerMode, rows[i].SchedulerName, rows[i].PolicyName)
		right := fmt.Sprintf("%s\x00%s\x00%s\x00%s", rows[j].Workload, rows[j].SchedulerMode, rows[j].SchedulerName, rows[j].PolicyName)
		return left < right
	})
	return rows, nil
}

func summarizeGroup(key groupKey, items []observation, byWorkload bool) (summaryRow, error) {
	first := items[0]
	workloads := map[string]struct{}{}
	unschedulable := 0
	p95Decision := make([]float64, 0, len(items))
	maxNodes := make([]float64, 0, len(items))
	loadVariance := make([]float64, 0, len(items))
	imbalance := make([]float64, 0, len(items))
	for _, item := range items {
		if item.weightsApplicable != first.weightsApplicable || !equalWeights(item.weights, first.weights) {
			return summaryRow{}, fmt.Errorf("inconsistent weights in policy group %s", key.policyName)
		}
		workloads[item.workload] = struct{}{}
		unschedulable += item.row.UnschedulableCount
		p95Decision = append(p95Decision, item.row.P95SchedulingDecisionLatency)
		maxNodes = append(maxNodes, float64(item.row.MaxWorkloadOccupiedWorkerNodes))
		loadVariance = append(loadVariance, item.row.MeanWorkloadDominantRequestedLoadVariance)
		imbalance = append(imbalance, item.row.MeanWorkloadCPUToMemoryImbalance)
	}

	decisionApplicable := key.schedulerMode == evaluator.SchedulerModeCustomPolicy
	var decisionStats *statistics
	if decisionApplicable {
		value := calculateStatistics(p95Decision)
		decisionStats = &value
	}
	scope := "aggregate"
	if byWorkload {
		scope = "workload"
	}
	return summaryRow{
		Scope:                          scope,
		Workload:                       key.workload,
		WorkloadCount:                  len(workloads),
		SchedulerMode:                  key.schedulerMode,
		SchedulerName:                  key.schedulerName,
		PolicyName:                     key.policyName,
		Weights:                        cloneWeights(first.weights),
		WeightsApplicable:              first.weightsApplicable,
		Repetitions:                    len(items),
		UnschedulableTotal:             unschedulable,
		DecisionLatencyApplicable:      decisionApplicable,
		P95SchedulingDecisionLatency:   decisionStats,
		MaxWorkloadOccupiedWorkerNodes: calculateStatistics(maxNodes),
		MeanWorkloadDominantRequestedLoadVariance: calculateStatistics(loadVariance),
		MeanWorkloadCPUToMemoryImbalance:          calculateStatistics(imbalance),
	}, nil
}

func calculateStatistics(values []float64) statistics {
	mean := 0.0
	minimum := values[0]
	maximum := values[0]
	for _, value := range values {
		mean += value
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	mean /= float64(len(values))

	variance := 0.0
	if len(values) > 1 {
		for _, value := range values {
			delta := value - mean
			variance += delta * delta
		}
		variance /= float64(len(values) - 1)
	}
	stdDev := math.Sqrt(variance)
	margin := 0.0
	if len(values) > 1 {
		margin = studentTCritical95(len(values)-1) * stdDev / math.Sqrt(float64(len(values)))
	}
	return statistics{
		Mean:              mean,
		SampleStdDev:      stdDev,
		Minimum:           minimum,
		Maximum:           maximum,
		Confidence95Lower: mean - margin,
		Confidence95Upper: mean + margin,
	}
}

func studentTCritical95(degreesOfFreedom int) float64 {
	critical := [...]float64{
		0,
		12.706, 4.303, 3.182, 2.776, 2.571,
		2.447, 2.365, 2.306, 2.262, 2.228,
		2.201, 2.179, 2.160, 2.145, 2.131,
		2.120, 2.110, 2.101, 2.093, 2.086,
		2.080, 2.074, 2.069, 2.064, 2.060,
		2.056, 2.052, 2.048, 2.045, 2.042,
	}
	if degreesOfFreedom < 1 {
		return 0
	}
	if degreesOfFreedom < len(critical) {
		return critical[degreesOfFreedom]
	}
	return 1.96
}

func writeSummaryCSV(writer io.Writer, output summaryOutput) error {
	csvWriter := csv.NewWriter(writer)
	header := []string{
		"scope", "workload", "workload_count", "scheduler_mode", "scheduler_name", "policy_name",
		"free_cpu", "free_mem", "balance", "consolidation", "repetitions", "unschedulable_total",
		"decision_latency_applicable", "p95_decision_mean_seconds", "p95_decision_sample_stddev_seconds",
		"p95_decision_min_seconds", "p95_decision_max_seconds", "p95_decision_ci95_lower_seconds", "p95_decision_ci95_upper_seconds",
		"max_nodes_mean", "max_nodes_sample_stddev", "max_nodes_min", "max_nodes_max", "max_nodes_ci95_lower", "max_nodes_ci95_upper",
		"load_variance_mean", "load_variance_sample_stddev", "load_variance_min", "load_variance_max",
		"load_variance_ci95_lower", "load_variance_ci95_upper", "cpu_memory_imbalance_mean",
		"cpu_memory_imbalance_sample_stddev", "cpu_memory_imbalance_min", "cpu_memory_imbalance_max",
		"cpu_memory_imbalance_ci95_lower", "cpu_memory_imbalance_ci95_upper",
	}
	if err := csvWriter.Write(header); err != nil {
		return err
	}
	rows := append(append([]summaryRow(nil), output.ByWorkload...), output.Aggregates...)
	for _, row := range rows {
		weights := []string{"", "", "", ""}
		if row.Weights != nil {
			weights = []string{
				formatFloat(row.Weights.FreeCPU),
				formatFloat(row.Weights.FreeMem),
				formatFloat(row.Weights.Balance),
				formatFloat(row.Weights.Consolidation),
			}
		}
		decision := []string{"", "", "", "", "", ""}
		if row.P95SchedulingDecisionLatency != nil {
			decision = statisticsFields(*row.P95SchedulingDecisionLatency)
		}
		record := []string{
			row.Scope, row.Workload, strconv.Itoa(row.WorkloadCount), string(row.SchedulerMode), row.SchedulerName, row.PolicyName,
			weights[0], weights[1], weights[2], weights[3], strconv.Itoa(row.Repetitions), strconv.Itoa(row.UnschedulableTotal),
			strconv.FormatBool(row.DecisionLatencyApplicable), decision[0], decision[1], decision[2], decision[3],
		}
		record = append(record, statisticsFields(row.MaxWorkloadOccupiedWorkerNodes)...)
		record = append(record, statisticsFields(row.MeanWorkloadDominantRequestedLoadVariance)...)
		record = append(record, statisticsFields(row.MeanWorkloadCPUToMemoryImbalance)...)
		if err := csvWriter.Write(record); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	return csvWriter.Error()
}

func statisticsFields(value statistics) []string {
	return []string{
		formatFloat(value.Mean),
		formatFloat(value.SampleStdDev),
		formatFloat(value.Minimum),
		formatFloat(value.Maximum),
		formatFloat(value.Confidence95Lower),
		formatFloat(value.Confidence95Upper),
	}
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func splitPaths(raw string) []string {
	parts := strings.Split(raw, ",")
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if path := strings.TrimSpace(part); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func cloneWeights(weights *evaluator.WeightSet) *evaluator.WeightSet {
	if weights == nil {
		return nil
	}
	clone := *weights
	return &clone
}

func equalWeights(left, right *evaluator.WeightSet) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
