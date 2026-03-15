package evaluator

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func WriteComparisonCSV(path string, rows []ComparisonRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create csv: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"run_name",
		"free_cpu",
		"free_mem",
		"pod_penalty",
		"packing",
		"scheduled_count",
		"completed_count",
		"unschedulable_count",
		"avg_pending",
		"p95_pending",
		"max_active_nodes",
		"notes",
	}); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}

	for _, row := range rows {
		record := []string{
			row.Name,
			strconv.FormatFloat(row.Weights.FreeCPU, 'g', -1, 64),
			strconv.FormatFloat(row.Weights.FreeMem, 'g', -1, 64),
			strconv.FormatFloat(row.Weights.PodPenalty, 'g', -1, 64),
			strconv.FormatFloat(row.Weights.Packing, 'g', -1, 64),
			strconv.Itoa(row.ScheduledCount),
			strconv.Itoa(row.CompletedCount),
			strconv.Itoa(row.UnschedulableCount),
			strconv.FormatFloat(row.AveragePending, 'f', 6, 64),
			strconv.FormatFloat(row.P95Pending, 'f', 6, 64),
			strconv.Itoa(row.MaxActiveNodes),
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
