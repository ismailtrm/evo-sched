package evaluator

import (
	"time"

	"evok8s-scheduler/pkg/measurement"
	"evok8s-scheduler/pkg/policy"
)

const PolicyVersion = policy.Version
const MeasurementVersion = measurement.Version

type WorkloadItem struct {
	Name            string  `json:"name"`
	ArrivalSeconds  int     `json:"arrivalSeconds"`
	CPUMilli        int64   `json:"cpuMilli"`
	MemoryMiB       int64   `json:"memoryMiB"`
	CPUFraction     float64 `json:"cpuFraction,omitempty"`
	MemoryFraction  float64 `json:"memoryFraction,omitempty"`
	DurationSeconds int     `json:"durationSeconds"`
}

type WorkloadBase struct {
	WorkerCount     int   `json:"workerCount"`
	WorkerCPUMilli  int64 `json:"workerCPUMilli"`
	WorkerMemoryMiB int64 `json:"workerMemoryMiB"`
}

type WeightSet = policy.WeightSet

type ExperimentRunSpec struct {
	Name    string    `json:"name"`
	Weights WeightSet `json:"weights"`
}

type ExperimentSpec struct {
	PolicyVersion string              `json:"policyVersion"`
	Runs          []ExperimentRunSpec `json:"runs"`
}

type PodTiming struct {
	Name                             string     `json:"name"`
	SubmittedAt                      *time.Time `json:"submittedAt,omitempty"`
	CreatedAt                        *time.Time `json:"createdAt,omitempty"`
	SchedulerDecisionAt              *time.Time `json:"schedulerDecisionAt,omitempty"`
	ScheduledAt                      *time.Time `json:"scheduledAt,omitempty"`
	EvaluatorObservedScheduledAt     *time.Time `json:"evaluatorObservedScheduledAt,omitempty"`
	StartedAt                        *time.Time `json:"startedAt,omitempty"`
	FinishedAt                       *time.Time `json:"finishedAt,omitempty"`
	NodeName                         string     `json:"nodeName,omitempty"`
	Phase                            string     `json:"phase,omitempty"`
	SchedulingDecisionLatencySeconds float64    `json:"schedulingDecisionLatencySeconds"`
	PendingSeconds                   float64    `json:"pendingSeconds"`
	EvaluatorObservationLagSeconds   float64    `json:"evaluatorObservationLagSeconds"`
	StartLatencySeconds              float64    `json:"startLatencySeconds"`
}

type Summary struct {
	PodCount           int     `json:"podCount"`
	ScheduledCount     int     `json:"scheduledCount"`
	CompletedCount     int     `json:"completedCount"`
	UnschedulableCount int     `json:"unschedulableCount"`
	AveragePending     float64 `json:"averagePendingSeconds"`
	P95Pending         float64 `json:"p95PendingSeconds"`
	MaxActiveNodes     int     `json:"maxActiveNodes"`
}

type RunResult struct {
	PolicyVersion      string      `json:"policyVersion"`
	MeasurementVersion string      `json:"measurementVersion"`
	Name               string      `json:"name"`
	RunID              string      `json:"runId"`
	Namespace          string      `json:"namespace"`
	Scheduler          string      `json:"schedulerName"`
	Workload           string      `json:"workload"`
	Weights            WeightSet   `json:"weights"`
	StartedAt          time.Time   `json:"startedAt"`
	FinishedAt         time.Time   `json:"finishedAt"`
	Summary            Summary     `json:"summary"`
	Pods               []PodTiming `json:"pods"`
	ActiveNodes        map[int]int `json:"activeNodesBySample"`
	Notes              []string    `json:"notes,omitempty"`
}

type ComparisonRow struct {
	Name               string    `json:"name"`
	Weights            WeightSet `json:"weights"`
	ScheduledCount     int       `json:"scheduledCount"`
	CompletedCount     int       `json:"completedCount"`
	UnschedulableCount int       `json:"unschedulableCount"`
	AveragePending     float64   `json:"averagePendingSeconds"`
	P95Pending         float64   `json:"p95PendingSeconds"`
	MaxActiveNodes     int       `json:"maxActiveNodes"`
	Notes              []string  `json:"notes,omitempty"`
}

type BatchResult struct {
	PolicyVersion      string          `json:"policyVersion"`
	MeasurementVersion string          `json:"measurementVersion"`
	Workload           string          `json:"workload"`
	WorkloadPath       string          `json:"workloadPath"`
	WorkloadBase       *WorkloadBase   `json:"workloadBase,omitempty"`
	ResolvedWorkload   []WorkloadItem  `json:"resolvedWorkload,omitempty"`
	Results            []ComparisonRow `json:"results"`
	Runs               []RunResult     `json:"runs,omitempty"`
}
