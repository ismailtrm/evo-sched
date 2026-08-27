package evaluator

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func WriteComparisonCSV(path string, protocol MeasurementProtocol, rows []ComparisonRow) error {
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
		"run_name",
		"free_cpu",
		"free_mem",
		"balance",
		"consolidation",
		"pod_count",
		"scheduled_count",
		"completed_count",
		"unschedulable_count",
		"decision_timestamp_count",
		"kubernetes_scheduled_timestamp_count",
		"evaluator_observation_timestamp_count",
		"avg_scheduling_decision_latency_seconds",
		"p95_scheduling_decision_latency_seconds",
		"avg_kubernetes_pending_seconds",
		"p95_kubernetes_pending_seconds",
		"avg_evaluator_observation_lag_seconds",
		"p95_evaluator_observation_lag_seconds",
		"max_workload_occupied_worker_nodes",
		"notes",
	}); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	for _, row := range rows {
		record := []string{
			PolicyVersion,
			protocol.Version,
			strconv.FormatFloat(protocol.SchedulerIntervalSeconds, 'f', 6, 64),
			strconv.FormatFloat(protocol.PodPollIntervalSeconds, 'f', 6, 64),
			row.Name,
			strconv.FormatFloat(row.Weights.FreeCPU, 'g', -1, 64),
			strconv.FormatFloat(row.Weights.FreeMem, 'g', -1, 64),
			strconv.FormatFloat(row.Weights.Balance, 'g', -1, 64),
			strconv.FormatFloat(row.Weights.Consolidation, 'g', -1, 64),
			strconv.Itoa(row.PodCount),
			strconv.Itoa(row.ScheduledCount),
			strconv.Itoa(row.CompletedCount),
			strconv.Itoa(row.UnschedulableCount),
			strconv.Itoa(row.DecisionTimestampCount),
			strconv.Itoa(row.KubernetesScheduledTimestampCount),
			strconv.Itoa(row.EvaluatorObservationTimestampCount),
			strconv.FormatFloat(row.AverageSchedulingDecisionLatency, 'f', 6, 64),
			strconv.FormatFloat(row.P95SchedulingDecisionLatency, 'f', 6, 64),
			strconv.FormatFloat(row.AverageKubernetesPending, 'f', 6, 64),
			strconv.FormatFloat(row.P95KubernetesPending, 'f', 6, 64),
			strconv.FormatFloat(row.AverageEvaluatorObservationLag, 'f', 6, 64),
			strconv.FormatFloat(row.P95EvaluatorObservationLag, 'f', 6, 64),
			strconv.Itoa(row.MaxWorkloadOccupiedWorkerNodes),
			strings.Join(row.Notes, " | "),
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
