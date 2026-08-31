package optimizer

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/policy"
)

func WriteCandidatesCSV(path string, protocol evaluator.MeasurementProtocol, candidates []Candidate) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"policy_version",
		"measurement_version",
		"scheduler_interval_seconds",
		"pod_poll_interval_seconds",
		"workload_resource_metric_source",
		"candidate_id",
		"name",
		"source",
		"generation",
		"free_cpu",
		"free_mem",
		"balance",
		"consolidation",
		"avg_p95_scheduling_decision_latency_seconds",
		"avg_max_workload_occupied_worker_nodes",
		"rank",
		"crowding_distance",
		"workloads",
		"notes",
	}); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	for _, candidate := range candidates {
		record := []string{
			policy.Version,
			protocol.Version,
			strconv.FormatFloat(protocol.SchedulerIntervalSeconds, 'f', 6, 64),
			strconv.FormatFloat(protocol.PodPollIntervalSeconds, 'f', 6, 64),
			protocol.WorkloadResourceMetricSource,
			candidate.ID,
			candidate.Name,
			candidate.Source,
			strconv.Itoa(candidate.Generation),
			strconv.FormatFloat(candidate.Weights.FreeCPU, 'g', -1, 64),
			strconv.FormatFloat(candidate.Weights.FreeMem, 'g', -1, 64),
			strconv.FormatFloat(candidate.Weights.Balance, 'g', -1, 64),
			strconv.FormatFloat(candidate.Weights.Consolidation, 'g', -1, 64),
			strconv.FormatFloat(candidate.Objectives.AverageP95SchedulingDecisionLatency, 'f', 6, 64),
			strconv.FormatFloat(candidate.Objectives.AverageMaxWorkloadOccupiedWorkerNodes, 'f', 6, 64),
			strconv.Itoa(candidate.Rank),
			strconv.FormatFloat(candidate.CrowdingDistance, 'f', 6, 64),
			formatWorkloadMetrics(candidate.Workloads),
			strings.Join(candidate.Notes, " | "),
		}
		if err := w.Write(record); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}
	return nil
}

func WriteSamplesCSV(
	path string,
	protocol evaluator.MeasurementProtocol,
	cfg Config,
	candidates []Candidate,
) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create samples csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"policy_version",
		"measurement_version",
		"scheduler_interval_seconds",
		"pod_poll_interval_seconds",
		"workload_resource_metric_source",
		"search_mode",
		"seed",
		"candidate_id",
		"name",
		"source",
		"generation",
		"creation_ordinal",
		"free_cpu",
		"free_mem",
		"balance",
		"consolidation",
		"evaluation_id",
		"sample_kind",
		"sample_ordinal",
		"budget_class",
		"budget_ordinal",
		"cumulative_optimizer_samples_attempted",
		"cumulative_workload_executions_attempted",
		"started_at",
		"finished_at",
		"duration_seconds",
		"sample_avg_p95_scheduling_decision_latency_seconds",
		"sample_avg_max_workload_occupied_worker_nodes",
		"aggregate_avg_p95_scheduling_decision_latency_seconds",
		"aggregate_avg_max_workload_occupied_worker_nodes",
		"workloads",
		"notes",
	}); err != nil {
		return fmt.Errorf("write samples csv header: %w", err)
	}

	for _, candidate := range candidates {
		for _, sample := range candidate.EvaluationSamples {
			record := []string{
				policy.Version,
				protocol.Version,
				strconv.FormatFloat(protocol.SchedulerIntervalSeconds, 'f', 6, 64),
				strconv.FormatFloat(protocol.PodPollIntervalSeconds, 'f', 6, 64),
				protocol.WorkloadResourceMetricSource,
				string(cfg.SearchMode),
				strconv.FormatInt(cfg.Seed, 10),
				candidate.ID,
				candidate.Name,
				candidate.Source,
				strconv.Itoa(candidate.Generation),
				strconv.Itoa(candidate.CreationOrdinal),
				strconv.FormatFloat(candidate.Weights.FreeCPU, 'g', -1, 64),
				strconv.FormatFloat(candidate.Weights.FreeMem, 'g', -1, 64),
				strconv.FormatFloat(candidate.Weights.Balance, 'g', -1, 64),
				strconv.FormatFloat(candidate.Weights.Consolidation, 'g', -1, 64),
				sample.EvaluationID,
				string(sample.SampleKind),
				strconv.Itoa(sample.SampleOrdinal),
				string(sample.BudgetClass),
				strconv.Itoa(sample.BudgetOrdinal),
				strconv.Itoa(sample.CumulativeOptimizerSamplesAttempted),
				strconv.Itoa(sample.CumulativeWorkloadExecutionsAttempted),
				sample.StartedAt.Format(time.RFC3339Nano),
				sample.FinishedAt.Format(time.RFC3339Nano),
				strconv.FormatFloat(sample.FinishedAt.Sub(sample.StartedAt).Seconds(), 'f', 6, 64),
				strconv.FormatFloat(sample.Data.Objectives.AverageP95SchedulingDecisionLatency, 'f', 6, 64),
				strconv.FormatFloat(sample.Data.Objectives.AverageMaxWorkloadOccupiedWorkerNodes, 'f', 6, 64),
				strconv.FormatFloat(sample.CandidateObjectivesAfterSample.AverageP95SchedulingDecisionLatency, 'f', 6, 64),
				strconv.FormatFloat(sample.CandidateObjectivesAfterSample.AverageMaxWorkloadOccupiedWorkerNodes, 'f', 6, 64),
				formatWorkloadMetrics(sample.Data.Workloads),
				strings.Join(sample.Data.Notes, " | "),
			}
			if err := w.Write(record); err != nil {
				return fmt.Errorf("write samples csv row: %w", err)
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush samples csv: %w", err)
	}
	return nil
}

func formatWorkloadMetrics(workloads []WorkloadMetrics) string {
	parts := make([]string, 0, len(workloads))
	for _, workload := range workloads {
		parts = append(parts, fmt.Sprintf(
			"%s:p95Decision=%0.3f,maxOccupiedWorkers=%d,meanLoadVariance=%0.4f",
			workload.Workload,
			workload.P95SchedulingDecisionLatency,
			workload.MaxWorkloadOccupiedWorkerNodes,
			workload.MeanWorkloadDominantRequestedLoadVariance,
		))
	}
	return strings.Join(parts, " | ")
}
