package optimizer

import (
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

type Config struct {
	PopulationSize int      `json:"populationSize"`
	Generations    int      `json:"generations"`
	MutationRate   float64  `json:"mutationRate"`
	MutationSigma  float64  `json:"mutationSigma"`
	MinWeight      float64  `json:"minWeight"`
	MaxWeight      float64  `json:"maxWeight"`
	Seed           int64    `json:"seed"`
	Workloads      []string `json:"workloads"`
}

type Objectives struct {
	AverageP95SchedulingDecisionLatency   float64 `json:"averageP95SchedulingDecisionLatencySeconds"`
	AverageMaxWorkloadOccupiedWorkerNodes float64 `json:"averageMaxWorkloadOccupiedWorkerNodes"`
}

type WorkloadMetrics struct {
	Workload                           string  `json:"workload"`
	AverageSchedulingDecisionLatency   float64 `json:"averageSchedulingDecisionLatencySeconds"`
	P95SchedulingDecisionLatency       float64 `json:"p95SchedulingDecisionLatencySeconds"`
	AverageKubernetesPending           float64 `json:"averageKubernetesPendingSeconds"`
	P95KubernetesPending               float64 `json:"p95KubernetesPendingSeconds"`
	AverageEvaluatorObservationLag     float64 `json:"averageEvaluatorObservationLagSeconds"`
	P95EvaluatorObservationLag         float64 `json:"p95EvaluatorObservationLagSeconds"`
	MaxWorkloadOccupiedWorkerNodes     int     `json:"maxWorkloadOccupiedWorkerNodes"`
	PodCount                           int     `json:"podCount"`
	ScheduledCount                     int     `json:"scheduledCount"`
	CompletedCount                     int     `json:"completedCount"`
	UnschedulableCount                 int     `json:"unschedulableCount"`
	DecisionTimestampCount             int     `json:"decisionTimestampCount"`
	KubernetesScheduledTimestampCount  int     `json:"kubernetesScheduledTimestampCount"`
	EvaluatorObservationTimestampCount int     `json:"evaluatorObservationTimestampCount"`
}

type EvaluationData struct {
	Objectives Objectives        `json:"objectives"`
	Workloads  []WorkloadMetrics `json:"workloads"`
	Notes      []string          `json:"notes,omitempty"`
}

type Candidate struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Source           string              `json:"source"`
	Generation       int                 `json:"generation"`
	Weights          evaluator.WeightSet `json:"weights"`
	Objectives       Objectives          `json:"objectives"`
	Workloads        []WorkloadMetrics   `json:"workloads"`
	Rank             int                 `json:"rank"`
	CrowdingDistance float64             `json:"crowdingDistance"`
	Notes            []string            `json:"notes,omitempty"`
}

type Result struct {
	PolicyVersion       string                        `json:"policyVersion"`
	MeasurementVersion  string                        `json:"measurementVersion"`
	MeasurementProtocol evaluator.MeasurementProtocol `json:"measurementProtocol"`
	StartedAt           time.Time                     `json:"startedAt"`
	FinishedAt          time.Time                     `json:"finishedAt"`
	Config              Config                        `json:"config"`
	Baselines           []Candidate                   `json:"baselines"`
	Evaluated           []Candidate                   `json:"evaluated"`
	FinalPopulation     []Candidate                   `json:"finalPopulation"`
	ParetoFront         []Candidate                   `json:"paretoFront"`
	CombinedParetoFront []Candidate                   `json:"combinedParetoFront"`
}

type EvaluateFunc func(weights evaluator.WeightSet) (EvaluationData, error)
