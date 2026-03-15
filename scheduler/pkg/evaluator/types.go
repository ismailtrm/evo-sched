package evaluator

import "time"

type WorkloadItem struct {
	Name            string `json:"name"`
	ArrivalSeconds  int    `json:"arrivalSeconds"`
	CPUMilli        int64  `json:"cpuMilli"`
	MemoryMiB       int64  `json:"memoryMiB"`
	DurationSeconds int    `json:"durationSeconds"`
}

type WeightSet struct {
	FreeCPU    float64 `json:"freeCPU"`
	FreeMem    float64 `json:"freeMem"`
	PodPenalty float64 `json:"podPenalty"`
	Packing    float64 `json:"packing"`
}

type PodTiming struct {
	Name                string     `json:"name"`
	CreatedAt           *time.Time `json:"createdAt,omitempty"`
	ScheduledAt         *time.Time `json:"scheduledAt,omitempty"`
	StartedAt           *time.Time `json:"startedAt,omitempty"`
	FinishedAt          *time.Time `json:"finishedAt,omitempty"`
	NodeName            string     `json:"nodeName,omitempty"`
	Phase               string     `json:"phase,omitempty"`
	PendingSeconds      float64    `json:"pendingSeconds"`
	StartLatencySeconds float64    `json:"startLatencySeconds"`
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
	RunID       string      `json:"runId"`
	Namespace   string      `json:"namespace"`
	Scheduler   string      `json:"schedulerName"`
	Workload    string      `json:"workload"`
	Weights     WeightSet   `json:"weights"`
	StartedAt   time.Time   `json:"startedAt"`
	FinishedAt  time.Time   `json:"finishedAt"`
	Summary     Summary     `json:"summary"`
	Pods        []PodTiming `json:"pods"`
	ActiveNodes map[int]int `json:"activeNodesBySample"`
	Notes       []string    `json:"notes,omitempty"`
}
