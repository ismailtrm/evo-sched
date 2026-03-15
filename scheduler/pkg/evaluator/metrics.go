package evaluator

import (
	"math"
	"sort"
)

func SummarizePods(pods []PodTiming, maxActiveNodes int) Summary {
	summary := Summary{
		PodCount:       len(pods),
		MaxActiveNodes: maxActiveNodes,
	}

	pendingValues := make([]float64, 0, len(pods))
	for _, pod := range pods {
		if pod.ScheduledAt != nil {
			summary.ScheduledCount++
			pendingValues = append(pendingValues, pod.PendingSeconds)
		} else {
			summary.UnschedulableCount++
		}
		if pod.FinishedAt != nil {
			summary.CompletedCount++
		}
	}

	if len(pendingValues) == 0 {
		return summary
	}

	var total float64
	for _, v := range pendingValues {
		total += v
	}
	summary.AveragePending = total / float64(len(pendingValues))
	summary.P95Pending = percentile(pendingValues, 95)
	return summary
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if len(values) == 1 {
		return values[0]
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	rank := (p / 100.0) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}

	weight := rank - float64(lower)
	return sorted[lower] + (sorted[upper]-sorted[lower])*weight
}
