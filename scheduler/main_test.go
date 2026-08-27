package main

import (
	"context"
	"math"
	"testing"
	"time"

	"evok8s-scheduler/pkg/measurement"
	"evok8s-scheduler/pkg/policy"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestFilterNodesForPod(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Tolerations: []corev1.Toleration{
				{
					Key:      "dedicated",
					Operator: corev1.TolerationOpEqual,
					Value:    "test",
					Effect:   corev1.TaintEffectNoSchedule,
				},
			},
		},
	}

	nodes := []corev1.Node{
		{
			ObjectMeta: readyNodeMeta("ready-ok"),
			Status:     readyNodeStatus(),
		},
		{
			ObjectMeta: readyNodeMeta("not-ready"),
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
				},
			},
		},
		{
			ObjectMeta: readyNodeMeta("cordoned"),
			Spec: corev1.NodeSpec{
				Unschedulable: true,
			},
			Status: readyNodeStatus(),
		},
		{
			ObjectMeta: readyNodeMeta("tainted-ok"),
			Spec: corev1.NodeSpec{
				Taints: []corev1.Taint{
					{Key: "dedicated", Value: "test", Effect: corev1.TaintEffectNoSchedule},
				},
			},
			Status: readyNodeStatus(),
		},
		{
			ObjectMeta: readyNodeMeta("tainted-blocked"),
			Spec: corev1.NodeSpec{
				Taints: []corev1.Taint{
					{Key: "dedicated", Value: "other", Effect: corev1.TaintEffectNoSchedule},
				},
			},
			Status: readyNodeStatus(),
		},
	}

	got := filterNodesForPod(pod, nodes)
	if len(got) != 2 {
		t.Fatalf("expected 2 feasible nodes, got %d", len(got))
	}
	if got[0].Name != "ready-ok" || got[1].Name != "tainted-ok" {
		t.Fatalf("unexpected nodes: %v, %v", got[0].Name, got[1].Name)
	}
}

func TestPodToleratesTaint(t *testing.T) {
	taint := &corev1.Taint{
		Key:    "gpu",
		Value:  "true",
		Effect: corev1.TaintEffectNoSchedule,
	}

	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "equal matches exact key value",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{Key: "gpu", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			want: true,
		},
		{
			name: "exists matches same key",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{Key: "gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			want: true,
		},
		{
			name: "empty key exists tolerates any key",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
					},
				},
			},
			want: true,
		},
		{
			name: "effect mismatch does not tolerate",
			pod: &corev1.Pod{
				Spec: corev1.PodSpec{
					Tolerations: []corev1.Toleration{
						{Key: "gpu", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoExecute},
					},
				},
			},
			want: false,
		},
		{
			name: "missing toleration is rejected",
			pod:  &corev1.Pod{},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := podToleratesTaint(tc.pod, taint); got != tc.want {
				t.Fatalf("podToleratesTaint() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPodRequests(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
				{
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
		},
	}

	got := podRequests(pod)
	if got.CPUMilli != 350 {
		t.Fatalf("expected 350m CPU request, got %d", got.CPUMilli)
	}
	if got.MemoryBytes != 201326592 {
		t.Fatalf("expected 201326592 bytes memory request, got %d", got.MemoryBytes)
	}
}

func TestLoadScoreWeightsFromEnv(t *testing.T) {
	t.Setenv("WEIGHT_FREE_CPU", "2")
	t.Setenv("WEIGHT_FREE_MEM", "1")
	t.Setenv("WEIGHT_BALANCE", "1")
	t.Setenv("WEIGHT_CONSOLIDATION", "0")

	got, err := loadScoreWeights()
	if err != nil {
		t.Fatalf("loadScoreWeights() error = %v", err)
	}

	want := ScoreWeights{
		FreeCPU:       0.5,
		FreeMem:       0.25,
		Balance:       0.25,
		Consolidation: 0,
	}
	assertScoreWeightsClose(t, got, want)
}

func TestLoadSchedulerInterval(t *testing.T) {
	t.Setenv("SCHEDULER_INTERVAL", "250ms")

	got, err := loadSchedulerInterval()
	if err != nil {
		t.Fatalf("loadSchedulerInterval() error = %v", err)
	}
	if got != 250*time.Millisecond {
		t.Fatalf("loadSchedulerInterval() = %s, want 250ms", got)
	}
}

func TestLoadSchedulerIntervalRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"invalid", "0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("SCHEDULER_INTERVAL", value)
			if _, err := loadSchedulerInterval(); err == nil {
				t.Fatalf("loadSchedulerInterval() error = nil for %q", value)
			}
		})
	}
}

func TestBindPodCarriesMeasurementAnnotations(t *testing.T) {
	client := fake.NewSimpleClientset()
	var got *corev1.Binding
	client.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "binding" {
			return false, nil, nil
		}
		createAction := action.(k8stesting.CreateAction)
		got = createAction.GetObject().(*corev1.Binding)
		return true, got, nil
	})

	decisionAt := time.Date(2026, time.August, 27, 4, 30, 15, 987654321, time.UTC)
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-a", Namespace: "eval"}}
	if err := bindPod(context.Background(), client, pod, "worker-a", decisionAt); err != nil {
		t.Fatalf("bindPod() error = %v", err)
	}
	if got == nil {
		t.Fatal("binding action was not captured")
	}
	if got.Annotations[measurement.DecisionAtAnnotation] != measurement.FormatTimestamp(decisionAt) {
		t.Fatalf("decision annotation = %q", got.Annotations[measurement.DecisionAtAnnotation])
	}
	if got.Annotations[measurement.MeasurementVersionAnnotation] != measurement.Version {
		t.Fatalf("measurement version annotation = %q", got.Annotations[measurement.MeasurementVersionAnnotation])
	}
	if got.Annotations[measurement.PolicyVersionAnnotation] != policy.Version {
		t.Fatalf("policy version annotation = %q", got.Annotations[measurement.PolicyVersionAnnotation])
	}
}

func TestNormalizeScoreWeightsCanonicalizesPositiveScale(t *testing.T) {
	base, err := normalizeScoreWeights(ScoreWeights{FreeCPU: 2, FreeMem: 1, Balance: 1})
	if err != nil {
		t.Fatalf("normalizeScoreWeights(base) error = %v", err)
	}
	scaled, err := normalizeScoreWeights(ScoreWeights{FreeCPU: 20, FreeMem: 10, Balance: 10})
	if err != nil {
		t.Fatalf("normalizeScoreWeights(scaled) error = %v", err)
	}
	assertScoreWeightsClose(t, base, scaled)
}

func TestNormalizeScoreWeightsRejectsInvalidVectors(t *testing.T) {
	tests := []struct {
		name    string
		weights ScoreWeights
	}{
		{name: "all zero", weights: ScoreWeights{}},
		{name: "negative", weights: ScoreWeights{FreeCPU: 1, Balance: -0.1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeScoreWeights(tc.weights); err == nil {
				t.Fatal("normalizeScoreWeights() error = nil, want non-nil")
			}
		})
	}
}

func TestChooseBestNodePrefersMoreFreeCPU(t *testing.T) {
	states := map[string]*NodeState{
		"cpu-rich": newNodeState("cpu-rich", 4000, 4000, 1000, 1000, 0),
		"cpu-poor": newNodeState("cpu-poor", 4000, 4000, 3000, 1000, 0),
	}

	got, _ := chooseBestNode(
		candidateNodes("cpu-rich", "cpu-poor"),
		states,
		ResourceRequest{CPUMilli: 500, MemoryBytes: 100},
		ScoreWeights{FreeCPU: 1.0},
	)
	if got != "cpu-rich" {
		t.Fatalf("expected cpu-rich, got %s", got)
	}
}

func TestChooseBestNodePrefersMoreFreeMemory(t *testing.T) {
	states := map[string]*NodeState{
		"memory-rich": newNodeState("memory-rich", 4000, 4000, 1000, 1000, 0),
		"memory-poor": newNodeState("memory-poor", 4000, 4000, 1000, 3000, 0),
	}

	got, _ := chooseBestNode(
		candidateNodes("memory-rich", "memory-poor"),
		states,
		ResourceRequest{CPUMilli: 100, MemoryBytes: 100},
		ScoreWeights{FreeMem: 1.0},
	)
	if got != "memory-rich" {
		t.Fatalf("expected memory-rich, got %s", got)
	}
}

func TestChooseBestNodePrefersPostPlacementBalance(t *testing.T) {
	states := map[string]*NodeState{
		"balanced-after":   newNodeState("balanced-after", 4000, 4000, 1500, 0, 0),
		"unbalanced-after": newNodeState("unbalanced-after", 4000, 4000, 0, 0, 0),
	}

	got, _ := chooseBestNode(
		candidateNodes("balanced-after", "unbalanced-after"),
		states,
		ResourceRequest{CPUMilli: 500, MemoryBytes: 2000},
		ScoreWeights{Balance: 1.0},
	)
	if got != "balanced-after" {
		t.Fatalf("expected balanced-after, got %s", got)
	}
}

func TestChooseBestNodePrefersConsolidation(t *testing.T) {
	states := map[string]*NodeState{
		"already-occupied":  newNodeState("already-occupied", 4000, 4000, 1000, 1000, 1),
		"new-workload-node": newNodeState("new-workload-node", 4000, 4000, 1000, 1000, 0),
	}

	got, _ := chooseBestNode(
		candidateNodes("already-occupied", "new-workload-node"),
		states,
		ResourceRequest{CPUMilli: 100, MemoryBytes: 100},
		ScoreWeights{Consolidation: 1.0},
	)
	if got != "already-occupied" {
		t.Fatalf("expected already-occupied, got %s", got)
	}
}

func TestChooseBestNodeRejectsNodesThatDoNotFit(t *testing.T) {
	states := map[string]*NodeState{
		"small-a": newNodeState("small-a", 4000, 4000, 3900, 3900, 0),
		"small-b": newNodeState("small-b", 4000, 4000, 3950, 3950, 0),
	}

	got, _ := chooseBestNode(
		candidateNodes("small-a", "small-b"),
		states,
		ResourceRequest{CPUMilli: 200, MemoryBytes: 200},
		ScoreWeights{FreeCPU: 1.0, FreeMem: 1.0},
	)
	if got != "" {
		t.Fatalf("expected no feasible node, got %s", got)
	}
}

func TestChooseBestNodeUsesDeterministicNameTieBreak(t *testing.T) {
	states := map[string]*NodeState{
		"node-b": newNodeState("node-b", 4000, 4000, 1000, 1000, 0),
		"node-a": newNodeState("node-a", 4000, 4000, 1000, 1000, 0),
	}

	got, _ := chooseBestNode(
		candidateNodes("node-b", "node-a"),
		states,
		ResourceRequest{CPUMilli: 100, MemoryBytes: 100},
		ScoreWeights{FreeCPU: 0.5, FreeMem: 0.5},
	)
	if got != "node-a" {
		t.Fatalf("expected node-a, got %s", got)
	}
}

func TestBuildNodeStatesTracksManagedPodsSeparately(t *testing.T) {
	nodes := []corev1.Node{{
		ObjectMeta: readyNodeMeta("worker-a"),
		Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("4"),
			corev1.ResourceMemory: resource.MustParse("4Gi"),
		}},
	}}
	pods := []corev1.Pod{
		boundPod("managed", "worker-a", "evok8s-scheduler", corev1.PodRunning, "500m", "512Mi"),
		boundPod("default", "worker-a", "default-scheduler", corev1.PodRunning, "250m", "256Mi"),
		boundPod("finished", "worker-a", "evok8s-scheduler", corev1.PodSucceeded, "1000m", "1Gi"),
	}

	states := buildNodeStates(nodes, pods, "evok8s-scheduler")
	state := states["worker-a"]
	if state == nil {
		t.Fatal("worker-a state is nil")
	}
	if state.UsedCPU != 750 {
		t.Fatalf("UsedCPU = %d, want 750", state.UsedCPU)
	}
	if state.UsedMem != 805306368 {
		t.Fatalf("UsedMem = %d, want 805306368", state.UsedMem)
	}
	if state.ManagedPodCount != 1 {
		t.Fatalf("ManagedPodCount = %d, want 1", state.ManagedPodCount)
	}
}

func readyNodeMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}

func readyNodeStatus() corev1.NodeStatus {
	return corev1.NodeStatus{
		Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		},
	}
}

func candidateNodes(names ...string) []corev1.Node {
	nodes := make([]corev1.Node, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, corev1.Node{ObjectMeta: readyNodeMeta(name)})
	}
	return nodes
}

func newNodeState(name string, allocCPU, allocMem, usedCPU, usedMem int64, managedPodCount int) *NodeState {
	return &NodeState{
		Name:            name,
		AllocCPU:        allocCPU,
		AllocMem:        allocMem,
		UsedCPU:         usedCPU,
		UsedMem:         usedMem,
		FreeCPU:         allocCPU - usedCPU,
		FreeMem:         allocMem - usedMem,
		ManagedPodCount: managedPodCount,
	}
}

func boundPod(name, nodeName, schedulerName string, phase corev1.PodPhase, cpu, memory string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PodSpec{
			NodeName:      nodeName,
			SchedulerName: schedulerName,
			Containers: []corev1.Container{{
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(cpu),
					corev1.ResourceMemory: resource.MustParse(memory),
				}},
			}},
		},
		Status: corev1.PodStatus{Phase: phase},
	}
}

func assertScoreWeightsClose(t *testing.T, got, want ScoreWeights) {
	t.Helper()
	gotValues := []float64{got.FreeCPU, got.FreeMem, got.Balance, got.Consolidation}
	wantValues := []float64{want.FreeCPU, want.FreeMem, want.Balance, want.Consolidation}
	for i := range gotValues {
		if math.Abs(gotValues[i]-wantValues[i]) > 1e-12 {
			t.Fatalf("weights[%d] = %.15f, want %.15f; got=%+v want=%+v", i, gotValues[i], wantValues[i], got, want)
		}
	}
}
