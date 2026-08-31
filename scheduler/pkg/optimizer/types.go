package optimizer

import (
	"time"

	"evok8s-scheduler/pkg/evaluator"
)

type Config struct {
	PopulationSize                int        `json:"populationSize"`
	Generations                   int        `json:"generations"`
	MutationRate                  float64    `json:"mutationRate"`
	MutationSigma                 float64    `json:"mutationSigma"`
	MinWeight                     float64    `json:"minWeight"`
	MaxWeight                     float64    `json:"maxWeight"`
	Seed                          int64      `json:"seed"`
	Workloads                     []string   `json:"workloads"`
	SearchMode                    SearchMode `json:"searchMode,omitempty"`
	OffspringPerGeneration        int        `json:"offspringPerGeneration,omitempty"`
	PromotionsPerGeneration       int        `json:"promotionsPerGeneration,omitempty"`
	AdditionalSamplesPerPromotion int        `json:"additionalSamplesPerPromotion,omitempty"`
	OptimizerSampleBudget         int        `json:"optimizerSampleBudget,omitempty"`
}

type SearchMode string

const (
	SearchModeBudgetOneShot  SearchMode = "budget-one-shot"
	SearchModeBudgetTwoStage SearchMode = "budget-two-stage"
)

type SampleKind string

const (
	SampleKindBaseline  SampleKind = "baseline"
	SampleKindScreening SampleKind = "screening"
	SampleKindPromotion SampleKind = "promotion"
)

type BudgetClass string

const (
	BudgetClassBaseline  BudgetClass = "baseline"
	BudgetClassOptimizer BudgetClass = "optimizer"
)

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
	evaluator.ResourceSummary
	evaluator.WorkloadSamplingProfile
}

type EvaluationData struct {
	Objectives Objectives        `json:"objectives"`
	Workloads  []WorkloadMetrics `json:"workloads"`
	Notes      []string          `json:"notes,omitempty"`
}

type EvaluationRequest struct {
	EvaluationID                          string              `json:"evaluationId"`
	CandidateID                           string              `json:"candidateId"`
	CandidateName                         string              `json:"candidateName"`
	CandidateCreationOrdinal              int                 `json:"candidateCreationOrdinal"`
	Generation                            int                 `json:"generation"`
	Weights                               evaluator.WeightSet `json:"weights"`
	SampleKind                            SampleKind          `json:"sampleKind"`
	SampleOrdinal                         int                 `json:"sampleOrdinal"`
	BudgetClass                           BudgetClass         `json:"budgetClass"`
	BudgetOrdinal                         int                 `json:"budgetOrdinal"`
	CumulativeOptimizerSamplesAttempted   int                 `json:"cumulativeOptimizerSamplesAttempted"`
	CumulativeWorkloadExecutionsAttempted int                 `json:"cumulativeWorkloadExecutionsAttempted"`
}

type EvaluationSample struct {
	EvaluationID                          string         `json:"evaluationId"`
	SampleKind                            SampleKind     `json:"sampleKind"`
	SampleOrdinal                         int            `json:"sampleOrdinal"`
	BudgetClass                           BudgetClass    `json:"budgetClass"`
	BudgetOrdinal                         int            `json:"budgetOrdinal"`
	CumulativeOptimizerSamplesAttempted   int            `json:"cumulativeOptimizerSamplesAttempted"`
	CumulativeWorkloadExecutionsAttempted int            `json:"cumulativeWorkloadExecutionsAttempted"`
	CandidateObjectivesAfterSample        Objectives     `json:"candidateObjectivesAfterSample"`
	StartedAt                             time.Time      `json:"startedAt"`
	FinishedAt                            time.Time      `json:"finishedAt"`
	Data                                  EvaluationData `json:"data"`
}

type BudgetSummary struct {
	ExpectedOptimizerSamples    int `json:"expectedOptimizerSamples"`
	ExpectedBaselineSamples     int `json:"expectedBaselineSamples"`
	AttemptedOptimizerSamples   int `json:"attemptedOptimizerSamples"`
	ActualOptimizerSamples      int `json:"actualOptimizerSamples"`
	AttemptedBaselineSamples    int `json:"attemptedBaselineSamples"`
	ActualBaselineSamples       int `json:"actualBaselineSamples"`
	ExpectedWorkloadExecutions  int `json:"expectedWorkloadExecutions"`
	AttemptedWorkloadExecutions int `json:"attemptedWorkloadExecutions"`
	ActualWorkloadExecutions    int `json:"actualWorkloadExecutions"`
	UniqueOptimizerCandidates   int `json:"uniqueOptimizerCandidates"`
	PromotionEvents             int `json:"promotionEvents"`
}

type Candidate struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Source            string              `json:"source"`
	Generation        int                 `json:"generation"`
	CreationOrdinal   int                 `json:"creationOrdinal,omitempty"`
	Weights           evaluator.WeightSet `json:"weights"`
	Objectives        Objectives          `json:"objectives"`
	Workloads         []WorkloadMetrics   `json:"workloads"`
	EvaluationCount   int                 `json:"evaluationCount,omitempty"`
	EvaluationSamples []EvaluationSample  `json:"evaluationSamples,omitempty"`
	Rank              int                 `json:"rank"`
	CrowdingDistance  float64             `json:"crowdingDistance"`
	Notes             []string            `json:"notes,omitempty"`
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
	Budget              *BudgetSummary                `json:"budget,omitempty"`
}

type EvaluateFunc func(weights evaluator.WeightSet) (EvaluationData, error)
type FreshEvaluateFunc func(request EvaluationRequest) (EvaluationData, error)
