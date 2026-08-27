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
	AverageP95Pending     float64 `json:"averageP95Pending"`
	AverageMaxActiveNodes float64 `json:"averageMaxActiveNodes"`
}

type WorkloadMetrics struct {
	Workload           string  `json:"workload"`
	AveragePending     float64 `json:"averagePendingSeconds"`
	P95Pending         float64 `json:"p95PendingSeconds"`
	MaxActiveNodes     int     `json:"maxActiveNodes"`
	ScheduledCount     int     `json:"scheduledCount"`
	CompletedCount     int     `json:"completedCount"`
	UnschedulableCount int     `json:"unschedulableCount"`
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
	PolicyVersion       string      `json:"policyVersion"`
	StartedAt           time.Time   `json:"startedAt"`
	FinishedAt          time.Time   `json:"finishedAt"`
	Config              Config      `json:"config"`
	Baselines           []Candidate `json:"baselines"`
	Evaluated           []Candidate `json:"evaluated"`
	FinalPopulation     []Candidate `json:"finalPopulation"`
	ParetoFront         []Candidate `json:"paretoFront"`
	CombinedParetoFront []Candidate `json:"combinedParetoFront"`
}

type EvaluateFunc func(weights evaluator.WeightSet) (EvaluationData, error)
