package optimizer

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"

	"evok8s-scheduler/pkg/policy"
)

func WriteCandidatesCSV(path string, candidates []Candidate) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"policy_version",
		"candidate_id",
		"name",
		"source",
		"generation",
		"free_cpu",
		"free_mem",
		"balance",
		"consolidation",
		"avg_p95_pending",
		"avg_max_active_nodes",
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
			candidate.ID,
			candidate.Name,
			candidate.Source,
			strconv.Itoa(candidate.Generation),
			strconv.FormatFloat(candidate.Weights.FreeCPU, 'g', -1, 64),
			strconv.FormatFloat(candidate.Weights.FreeMem, 'g', -1, 64),
			strconv.FormatFloat(candidate.Weights.Balance, 'g', -1, 64),
			strconv.FormatFloat(candidate.Weights.Consolidation, 'g', -1, 64),
			strconv.FormatFloat(candidate.Objectives.AverageP95Pending, 'f', 6, 64),
			strconv.FormatFloat(candidate.Objectives.AverageMaxActiveNodes, 'f', 6, 64),
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

func formatWorkloadMetrics(workloads []WorkloadMetrics) string {
	parts := make([]string, 0, len(workloads))
	for _, workload := range workloads {
		parts = append(parts, fmt.Sprintf(
			"%s:p95=%0.3f,maxActive=%d",
			workload.Workload,
			workload.P95Pending,
			workload.MaxActiveNodes,
		))
	}
	return strings.Join(parts, " | ")
}
