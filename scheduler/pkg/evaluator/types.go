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
	KubernetesScheduledAt            *time.Time `json:"kubernetesScheduledAt,omitempty"`
	EvaluatorObservedScheduledAt     *time.Time `json:"evaluatorObservedScheduledAt,omitempty"`
	StartedAt                        *time.Time `json:"startedAt,omitempty"`
	FinishedAt                       *time.Time `json:"finishedAt,omitempty"`
	NodeName                         string     `json:"nodeName,omitempty"`
	Phase                            string     `json:"phase,omitempty"`
	SchedulingDecisionLatencySeconds float64    `json:"schedulingDecisionLatencySeconds"`
	KubernetesPendingSeconds         float64    `json:"kubernetesPendingSeconds"`
	EvaluatorObservationLagSeconds   float64    `json:"evaluatorObservationLagSeconds"`
	StartLatencySeconds              float64    `json:"startLatencySeconds"`
}

type Summary struct {
	PodCount                           int     `json:"podCount"`
	ScheduledCount                     int     `json:"scheduledCount"`
	CompletedCount                     int     `json:"completedCount"`
	UnschedulableCount                 int     `json:"unschedulableCount"`
	DecisionTimestampCount             int     `json:"decisionTimestampCount"`
	KubernetesScheduledTimestampCount  int     `json:"kubernetesScheduledTimestampCount"`
	EvaluatorObservationTimestampCount int     `json:"evaluatorObservationTimestampCount"`
	AverageSchedulingDecisionLatency   float64 `json:"averageSchedulingDecisionLatencySeconds"`
	P95SchedulingDecisionLatency       float64 `json:"p95SchedulingDecisionLatencySeconds"`
	AverageKubernetesPending           float64 `json:"averageKubernetesPendingSeconds"`
	P95KubernetesPending               float64 `json:"p95KubernetesPendingSeconds"`
	AverageEvaluatorObservationLag     float64 `json:"averageEvaluatorObservationLagSeconds"`
	P95EvaluatorObservationLag         float64 `json:"p95EvaluatorObservationLagSeconds"`
	MaxWorkloadOccupiedWorkerNodes     int     `json:"maxWorkloadOccupiedWorkerNodes"`
	ResourceSummary
}

type NodeOccupancySample struct {
	ObservedAt                  time.Time `json:"observedAt"`
	WorkloadOccupiedWorkerNodes int       `json:"workloadOccupiedWorkerNodes"`
}

type WorkerResourceSample struct {
	Name                       string  `json:"name"`
	AllocCPUMilli              int64   `json:"allocCpuMilli"`
	AllocMemoryBytes           int64   `json:"allocMemoryBytes"`
	WorkloadRequestedCPUMilli  int64   `json:"workloadRequestedCpuMilli"`
	WorkloadRequestedMemBytes  int64   `json:"workloadRequestedMemoryBytes"`
	WorkloadPodCount           int     `json:"workloadPodCount"`
	CPURequestedUtilization    float64 `json:"cpuRequestedUtilization"`
	MemoryRequestedUtilization float64 `json:"memoryRequestedUtilization"`
	DominantRequestedLoad      float64 `json:"dominantRequestedLoad"`
}

type WorkloadResourceSample struct {
	ObservedAt                            time.Time              `json:"observedAt"`
	WorkloadOccupiedWorkerNodes           int                    `json:"workloadOccupiedWorkerNodes"`
	WorkloadCPURequestedUtilization       float64                `json:"workloadCpuRequestedUtilization"`
	WorkloadMemoryRequestedUtilization    float64                `json:"workloadMemoryRequestedUtilization"`
	WorkloadDominantRequestedLoadVariance float64                `json:"workloadDominantRequestedLoadVariance"`
	WorkloadCPUToMemoryImbalance          float64                `json:"workloadCpuToMemoryImbalance"`
	Workers                               []WorkerResourceSample `json:"workers"`
}

type ResourceSummary struct {
	MeanWorkloadCPURequestedUtilization       float64 `json:"meanWorkloadCpuRequestedUtilization"`
	MeanWorkloadMemoryRequestedUtilization    float64 `json:"meanWorkloadMemoryRequestedUtilization"`
	MeanWorkloadDominantRequestedLoadVariance float64 `json:"meanWorkloadDominantRequestedLoadVariance"`
	MeanWorkloadCPUToMemoryImbalance          float64 `json:"meanWorkloadCpuToMemoryImbalance"`
}

type WorkloadSamplingProfile struct {
	MinimumPodDurationSeconds          int     `json:"minimumPodDurationSeconds"`
	MaximumPodDurationSeconds          int     `json:"maximumPodDurationSeconds"`
	MinimumDurationToPollIntervalRatio float64 `json:"minimumDurationToPollIntervalRatio"`
}

type MeasurementProtocol struct {
	Version                             string  `json:"version"`
	SubmissionTimestampSource           string  `json:"submissionTimestampSource"`
	SchedulerDecisionTimestampSource    string  `json:"schedulerDecisionTimestampSource"`
	KubernetesScheduledTimestampSource  string  `json:"kubernetesScheduledTimestampSource"`
	EvaluatorObservationTimestampSource string  `json:"evaluatorObservationTimestampSource"`
	WorkloadOccupancySource             string  `json:"workloadOccupancySource"`
	WorkloadResourceMetricSource        string  `json:"workloadResourceMetricSource"`
	SchedulerIntervalSeconds            float64 `json:"schedulerIntervalSeconds"`
	PodPollIntervalSeconds              float64 `json:"podPollIntervalSeconds"`
}

type RunResult struct {
	PolicyVersion                     string                   `json:"policyVersion"`
	MeasurementVersion                string                   `json:"measurementVersion"`
	MeasurementProtocol               MeasurementProtocol      `json:"measurementProtocol"`
	Name                              string                   `json:"name"`
	RunID                             string                   `json:"runId"`
	Namespace                         string                   `json:"namespace"`
	Scheduler                         string                   `json:"schedulerName"`
	Workload                          string                   `json:"workload"`
	Weights                           WeightSet                `json:"weights"`
	StartedAt                         time.Time                `json:"startedAt"`
	FinishedAt                        time.Time                `json:"finishedAt"`
	Summary                           Summary                  `json:"summary"`
	Pods                              []PodTiming              `json:"pods"`
	WorkloadOccupiedWorkerNodeSamples []NodeOccupancySample    `json:"workloadOccupiedWorkerNodeSamples"`
	WorkloadResourceSamples           []WorkloadResourceSample `json:"workloadResourceSamples"`
	WorkloadSamplingProfile           WorkloadSamplingProfile  `json:"workloadSamplingProfile"`
	Notes                             []string                 `json:"notes,omitempty"`
}

type ComparisonRow struct {
	Name                               string    `json:"name"`
	Weights                            WeightSet `json:"weights"`
	PodCount                           int       `json:"podCount"`
	ScheduledCount                     int       `json:"scheduledCount"`
	CompletedCount                     int       `json:"completedCount"`
	UnschedulableCount                 int       `json:"unschedulableCount"`
	DecisionTimestampCount             int       `json:"decisionTimestampCount"`
	KubernetesScheduledTimestampCount  int       `json:"kubernetesScheduledTimestampCount"`
	EvaluatorObservationTimestampCount int       `json:"evaluatorObservationTimestampCount"`
	AverageSchedulingDecisionLatency   float64   `json:"averageSchedulingDecisionLatencySeconds"`
	P95SchedulingDecisionLatency       float64   `json:"p95SchedulingDecisionLatencySeconds"`
	AverageKubernetesPending           float64   `json:"averageKubernetesPendingSeconds"`
	P95KubernetesPending               float64   `json:"p95KubernetesPendingSeconds"`
	AverageEvaluatorObservationLag     float64   `json:"averageEvaluatorObservationLagSeconds"`
	P95EvaluatorObservationLag         float64   `json:"p95EvaluatorObservationLagSeconds"`
	MaxWorkloadOccupiedWorkerNodes     int       `json:"maxWorkloadOccupiedWorkerNodes"`
	ResourceSummary
	WorkloadSamplingProfile
	Notes []string `json:"notes,omitempty"`
}

type BatchResult struct {
	PolicyVersion           string                  `json:"policyVersion"`
	MeasurementVersion      string                  `json:"measurementVersion"`
	MeasurementProtocol     MeasurementProtocol     `json:"measurementProtocol"`
	Workload                string                  `json:"workload"`
	WorkloadPath            string                  `json:"workloadPath"`
	WorkloadBase            *WorkloadBase           `json:"workloadBase,omitempty"`
	WorkloadSamplingProfile WorkloadSamplingProfile `json:"workloadSamplingProfile"`
	ResolvedWorkload        []WorkloadItem          `json:"resolvedWorkload,omitempty"`
	Results                 []ComparisonRow         `json:"results"`
	Runs                    []RunResult             `json:"runs,omitempty"`
}
