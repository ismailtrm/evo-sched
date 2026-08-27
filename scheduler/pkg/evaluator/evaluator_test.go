package evaluator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evok8s-scheduler/pkg/measurement"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLoadWorkloadArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workload.json")
	content := `[
	  {"name":"pod-b","arrivalSeconds":3,"cpuMilli":200,"memoryMiB":128,"durationSeconds":10},
	  {"name":"pod-a","arrivalSeconds":1,"cpuMilli":100,"memoryMiB":64,"durationSeconds":5}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workload: %v", err)
	}

	items, err := LoadWorkload(path)
	if err != nil {
		t.Fatalf("LoadWorkload() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Name != "pod-a" || items[1].Name != "pod-b" {
		t.Fatalf("unexpected sort order: %+v", items)
	}
}

func TestLoadWorkloadWithBase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workload-relative.json")
	content := `[
	  {"name":"cpu-heavy","arrivalSeconds":0,"cpuFraction":0.5,"memoryFraction":0.25,"durationSeconds":10},
	  {"name":"mixed","arrivalSeconds":1,"cpuMilli":300,"memoryFraction":0.1,"durationSeconds":5}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workload: %v", err)
	}

	items, err := LoadWorkloadWithBase(path, &WorkloadBase{
		WorkerCount:     4,
		WorkerCPUMilli:  4000,
		WorkerMemoryMiB: 8192,
	})
	if err != nil {
		t.Fatalf("LoadWorkloadWithBase() error = %v", err)
	}
	if got, want := items[0].CPUMilli, int64(2000); got != want {
		t.Fatalf("cpu-heavy cpu = %d, want %d", got, want)
	}
	if got, want := items[0].MemoryMiB, int64(2048); got != want {
		t.Fatalf("cpu-heavy memory = %d, want %d", got, want)
	}
	if got, want := items[1].CPUMilli, int64(300); got != want {
		t.Fatalf("mixed cpu = %d, want %d", got, want)
	}
	if got, want := items[1].MemoryMiB, int64(820); got != want {
		t.Fatalf("mixed memory = %d, want %d", got, want)
	}
}

func TestLoadWorkloadWithFractionsRequiresBase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workload-relative.json")
	content := `[{"name":"cpu-heavy","arrivalSeconds":0,"cpuFraction":0.5,"memoryFraction":0.25,"durationSeconds":10}]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workload: %v", err)
	}

	if _, err := LoadWorkload(path); err == nil {
		t.Fatalf("LoadWorkload() expected error for fractional workload without base")
	}
}

func TestParseWeightSet(t *testing.T) {
	got, err := ParseWeightSet("2, 1, 1, 0")
	if err != nil {
		t.Fatalf("ParseWeightSet() error = %v", err)
	}
	want := WeightSet{FreeCPU: 0.5, FreeMem: 0.25, Balance: 0.25, Consolidation: 0}
	if got != want {
		t.Fatalf("ParseWeightSet() = %+v, want %+v", got, want)
	}
}

func TestLoadExperiment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weights.json")
	content := `{
	  "policyVersion": "v2",
	  "runs": [
	    {"name":"balanced","weights":{"freeCPU":2.0,"freeMem":1.0,"balance":1.0,"consolidation":0.0}},
	    {"name":"consolidation-heavy","weights":{"freeCPU":0.5,"freeMem":0.5,"balance":1.0,"consolidation":4.0}}
	  ]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write experiment: %v", err)
	}

	spec, err := LoadExperiment(path)
	if err != nil {
		t.Fatalf("LoadExperiment() error = %v", err)
	}
	if len(spec.Runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(spec.Runs))
	}
	if spec.PolicyVersion != PolicyVersion {
		t.Fatalf("policy version = %q, want %q", spec.PolicyVersion, PolicyVersion)
	}
	if spec.Runs[1].Name != "consolidation-heavy" {
		t.Fatalf("unexpected second run: %+v", spec.Runs[1])
	}
	if spec.Runs[0].Weights != (WeightSet{FreeCPU: 0.5, FreeMem: 0.25, Balance: 0.25}) {
		t.Fatalf("first run weights were not normalized: %+v", spec.Runs[0].Weights)
	}
}

func TestLoadExperimentRejectsLegacyWeightFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "weights-v1.json")
	content := `{
	  "runs": [
	    {"name":"legacy","weights":{"freeCPU":1.0,"freeMem":1.0,"podPenalty":0.5,"packing":0.2}}
	  ]
	}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write experiment: %v", err)
	}

	if _, err := LoadExperiment(path); err == nil {
		t.Fatal("LoadExperiment() error = nil, want legacy contract rejection")
	}
}

func TestCurrentDeploymentWeightsNormalizesV2Environment(t *testing.T) {
	cs := fake.NewSimpleClientset(testSchedulerDeployment([]corev1.EnvVar{
		{Name: "WEIGHT_FREE_CPU", Value: "2"},
		{Name: "WEIGHT_FREE_MEM", Value: "1"},
		{Name: "WEIGHT_BALANCE", Value: "1"},
		{Name: "WEIGHT_CONSOLIDATION", Value: "0"},
	}))

	got, err := CurrentDeploymentWeights(context.Background(), cs, "evo-sched", "evok8s-scheduler")
	if err != nil {
		t.Fatalf("CurrentDeploymentWeights() error = %v", err)
	}
	want := WeightSet{FreeCPU: 0.5, FreeMem: 0.25, Balance: 0.25}
	if got != want {
		t.Fatalf("CurrentDeploymentWeights() = %+v, want %+v", got, want)
	}
}

func TestCurrentDeploymentWeightsRejectsInvalidEnvironment(t *testing.T) {
	cs := fake.NewSimpleClientset(testSchedulerDeployment([]corev1.EnvVar{
		{Name: "WEIGHT_FREE_CPU", Value: "not-a-number"},
	}))

	if _, err := CurrentDeploymentWeights(context.Background(), cs, "evo-sched", "evok8s-scheduler"); err == nil {
		t.Fatal("CurrentDeploymentWeights() error = nil, want invalid env rejection")
	}
}

func TestWriteComparisonCSV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.csv")
	rows := []ComparisonRow{
		{
			Name:               "baseline",
			Weights:            WeightSet{FreeCPU: 0.4, FreeMem: 0.3, Balance: 0.2, Consolidation: 0.1},
			ScheduledCount:     4,
			CompletedCount:     4,
			UnschedulableCount: 0,
			AveragePending:     1.5,
			P95Pending:         2.0,
			MaxActiveNodes:     2,
		},
	}

	if err := WriteComparisonCSV(path, rows); err != nil {
		t.Fatalf("WriteComparisonCSV() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "policy_version,run_name,free_cpu,free_mem,balance,consolidation") {
		t.Fatalf("missing csv header: %s", content)
	}
	if !strings.Contains(content, "v2,baseline,0.4,0.3,0.2,0.1,4,4,0,1.500000,2.000000,2,") {
		t.Fatalf("missing csv row: %s", content)
	}
}

func TestSummarizePods(t *testing.T) {
	now := time.Now()
	pods := []PodTiming{
		{
			Name:           "scheduled-fast",
			ScheduledAt:    ptrTime(now),
			FinishedAt:     ptrTime(now.Add(10 * time.Second)),
			PendingSeconds: 1.0,
		},
		{
			Name:           "scheduled-slow",
			ScheduledAt:    ptrTime(now),
			PendingSeconds: 5.0,
		},
		{
			Name: "never-scheduled",
		},
	}

	got := SummarizePods(pods, 2)
	if got.PodCount != 3 || got.ScheduledCount != 2 || got.CompletedCount != 1 || got.UnschedulableCount != 1 {
		t.Fatalf("unexpected summary counts: %+v", got)
	}
	if got.AveragePending != 3.0 {
		t.Fatalf("expected average pending 3.0, got %v", got.AveragePending)
	}
	if got.P95Pending <= 4.0 {
		t.Fatalf("expected p95 pending above 4.0, got %v", got.P95Pending)
	}
	if got.MaxActiveNodes != 2 {
		t.Fatalf("expected max active nodes 2, got %d", got.MaxActiveNodes)
	}
}

func TestUpdateTimingSeparatesMeasurementSources(t *testing.T) {
	submittedAt := time.Date(2026, time.August, 27, 4, 40, 0, 100_000_000, time.UTC)
	decisionAt := submittedAt.Add(275 * time.Millisecond)
	kubernetesScheduledAt := time.Date(2026, time.August, 27, 4, 40, 1, 0, time.UTC)
	observedAt := decisionAt.Add(725 * time.Millisecond)
	result := &PodTiming{Name: "pod-a"}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod-a",
			Annotations: map[string]string{
				measurement.SubmittedAtAnnotation:        measurement.FormatTimestamp(submittedAt),
				measurement.DecisionAtAnnotation:         measurement.FormatTimestamp(decisionAt),
				measurement.MeasurementVersionAnnotation: measurement.Version,
				measurement.PolicyVersionAnnotation:      PolicyVersion,
			},
		},
		Spec: corev1.PodSpec{NodeName: "worker-a"},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{
			Type:               corev1.PodScheduled,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(kubernetesScheduledAt),
		}}},
	}

	if err := updateTiming(result, pod, observedAt); err != nil {
		t.Fatalf("updateTiming() error = %v", err)
	}
	if result.SubmittedAt == nil || !result.SubmittedAt.Equal(submittedAt) {
		t.Fatalf("SubmittedAt = %v, want %v", result.SubmittedAt, submittedAt)
	}
	if result.SchedulerDecisionAt == nil || !result.SchedulerDecisionAt.Equal(decisionAt) {
		t.Fatalf("SchedulerDecisionAt = %v, want %v", result.SchedulerDecisionAt, decisionAt)
	}
	if result.ScheduledAt == nil || !result.ScheduledAt.Equal(kubernetesScheduledAt) {
		t.Fatalf("ScheduledAt = %v, want %v", result.ScheduledAt, kubernetesScheduledAt)
	}
	if result.EvaluatorObservedScheduledAt == nil || !result.EvaluatorObservedScheduledAt.Equal(observedAt) {
		t.Fatalf("EvaluatorObservedScheduledAt = %v, want %v", result.EvaluatorObservedScheduledAt, observedAt)
	}
	if result.SchedulingDecisionLatencySeconds != 0.275 {
		t.Fatalf("SchedulingDecisionLatencySeconds = %v, want 0.275", result.SchedulingDecisionLatencySeconds)
	}
	if result.EvaluatorObservationLagSeconds != 0.725 {
		t.Fatalf("EvaluatorObservationLagSeconds = %v, want 0.725", result.EvaluatorObservationLagSeconds)
	}
}

func TestUpdateTimingRejectsInvalidMeasurementTimestamp(t *testing.T) {
	result := &PodTiming{Name: "pod-a"}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		measurement.DecisionAtAnnotation:         "invalid",
		measurement.MeasurementVersionAnnotation: measurement.Version,
		measurement.PolicyVersionAnnotation:      PolicyVersion,
	}}}

	if err := updateTiming(result, pod, time.Now()); err == nil {
		t.Fatal("updateTiming() error = nil, want invalid annotation error")
	}
}

func TestUpdateTimingRejectsMeasurementVersionMismatch(t *testing.T) {
	result := &PodTiming{Name: "pod-a"}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		measurement.DecisionAtAnnotation:         measurement.FormatTimestamp(time.Now()),
		measurement.MeasurementVersionAnnotation: "v1",
		measurement.PolicyVersionAnnotation:      PolicyVersion,
	}}}

	if err := updateTiming(result, pod, time.Now()); err == nil {
		t.Fatal("updateTiming() error = nil, want measurement version mismatch")
	}
}

func TestCreateWorkloadPodStoresHighResolutionSubmissionTimestamp(t *testing.T) {
	client := fake.NewSimpleClientset()
	result := &PodTiming{Name: "pod-a"}
	opts := DefaultRunnerOptions()
	opts.Namespace = "eval"

	if err := createWorkloadPod(
		context.Background(),
		client,
		opts,
		"run-a",
		WorkloadItem{Name: "pod-a", CPUMilli: 100, MemoryMiB: 64, DurationSeconds: 1},
		result,
	); err != nil {
		t.Fatalf("createWorkloadPod() error = %v", err)
	}

	pod, err := client.CoreV1().Pods("eval").Get(context.Background(), "run-a-pod-a", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get created pod: %v", err)
	}
	raw := pod.Annotations[measurement.SubmittedAtAnnotation]
	got, err := measurement.ParseTimestamp(raw)
	if err != nil {
		t.Fatalf("parse submitted annotation: %v", err)
	}
	if result.SubmittedAt == nil || !result.SubmittedAt.Equal(got) {
		t.Fatalf("result submittedAt = %v, annotation = %v", result.SubmittedAt, got)
	}
	if pod.Annotations[measurement.MeasurementVersionAnnotation] != measurement.Version {
		t.Fatalf("measurement version annotation = %q", pod.Annotations[measurement.MeasurementVersionAnnotation])
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func testSchedulerDeployment(env []corev1.EnvVar) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "evok8s-scheduler", Namespace: "evo-sched"},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "scheduler", Env: env}},
		}}},
	}
}
