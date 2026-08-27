package evaluator

import (
	"math"
	"sort"
)

func SummarizePods(pods []PodTiming, maxWorkloadOccupiedWorkerNodes int) Summary {
	summary := Summary{
		PodCount:                       len(pods),
		MaxWorkloadOccupiedWorkerNodes: maxWorkloadOccupiedWorkerNodes,
	}

	decisionLatencies := make([]float64, 0, len(pods))
	kubernetesPendingValues := make([]float64, 0, len(pods))
	observationLags := make([]float64, 0, len(pods))
	for _, pod := range pods {
		if pod.KubernetesScheduledAt != nil || pod.NodeName != "" {
			summary.ScheduledCount++
		} else {
			summary.UnschedulableCount++
		}
		if pod.SubmittedAt != nil && pod.SchedulerDecisionAt != nil {
			summary.DecisionTimestampCount++
			decisionLatencies = append(decisionLatencies, pod.SchedulingDecisionLatencySeconds)
		}
		if pod.CreatedAt != nil && pod.KubernetesScheduledAt != nil {
			summary.KubernetesScheduledTimestampCount++
			kubernetesPendingValues = append(kubernetesPendingValues, pod.KubernetesPendingSeconds)
		}
		if pod.EvaluatorObservedScheduledAt != nil {
			summary.EvaluatorObservationTimestampCount++
		}
		if pod.SchedulerDecisionAt != nil && pod.EvaluatorObservedScheduledAt != nil {
			observationLags = append(observationLags, pod.EvaluatorObservationLagSeconds)
		}
		if pod.FinishedAt != nil {
			summary.CompletedCount++
		}
	}

	summary.AverageSchedulingDecisionLatency = average(decisionLatencies)
	summary.P95SchedulingDecisionLatency = percentile(decisionLatencies, 95)
	summary.AverageKubernetesPending = average(kubernetesPendingValues)
	summary.P95KubernetesPending = percentile(kubernetesPendingValues, 95)
	summary.AverageEvaluatorObservationLag = average(observationLags)
	summary.P95EvaluatorObservationLag = percentile(observationLags, 95)
	return summary
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var total float64
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
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
