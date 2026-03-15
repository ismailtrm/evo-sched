package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	t.Setenv("WEIGHT_FREE_CPU", "1.5")
	t.Setenv("WEIGHT_FREE_MEM", "0.8")
	t.Setenv("WEIGHT_POD_PENALTY", "0.3")
	t.Setenv("WEIGHT_PACKING", "2.1")

	got, err := loadScoreWeights()
	if err != nil {
		t.Fatalf("loadScoreWeights() error = %v", err)
	}

	want := ScoreWeights{
		FreeCPU:    1.5,
		FreeMem:    0.8,
		PodPenalty: 0.3,
		Packing:    2.1,
	}
	if got != want {
		t.Fatalf("loadScoreWeights() = %+v, want %+v", got, want)
	}
}

func TestChooseBestNodePrefersMoreFreeCPU(t *testing.T) {
	states := map[string]*NodeState{
		"cpu-rich": newNodeState("cpu-rich", 4000, 4000, 1000, 1000, 2),
		"cpu-poor": newNodeState("cpu-poor", 4000, 4000, 3000, 1000, 2),
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

func TestChooseBestNodePrefersLowerPodPenalty(t *testing.T) {
	states := map[string]*NodeState{
		"few-pods":  newNodeState("few-pods", 4000, 4000, 1000, 1000, 1),
		"many-pods": newNodeState("many-pods", 4000, 4000, 1000, 1000, 5),
	}

	got, _ := chooseBestNode(
		candidateNodes("few-pods", "many-pods"),
		states,
		ResourceRequest{CPUMilli: 100, MemoryBytes: 100},
		ScoreWeights{PodPenalty: 1.0},
	)
	if got != "few-pods" {
		t.Fatalf("expected few-pods, got %s", got)
	}
}

func TestChooseBestNodePrefersPacking(t *testing.T) {
	states := map[string]*NodeState{
		"spread-node": newNodeState("spread-node", 4000, 4000, 1000, 1000, 1),
		"packed-node": newNodeState("packed-node", 4000, 4000, 2500, 2500, 1),
	}

	got, _ := chooseBestNode(
		candidateNodes("spread-node", "packed-node"),
		states,
		ResourceRequest{CPUMilli: 500, MemoryBytes: 500},
		ScoreWeights{Packing: 1.0},
	)
	if got != "packed-node" {
		t.Fatalf("expected packed-node, got %s", got)
	}
}

func TestChooseBestNodeRejectsNodesThatDoNotFit(t *testing.T) {
	states := map[string]*NodeState{
		"small-a": newNodeState("small-a", 4000, 4000, 3900, 3900, 1),
		"small-b": newNodeState("small-b", 4000, 4000, 3950, 3950, 1),
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

func newNodeState(name string, allocCPU, allocMem, usedCPU, usedMem int64, podCount int) *NodeState {
	return &NodeState{
		Name:     name,
		AllocCPU: allocCPU,
		AllocMem: allocMem,
		UsedCPU:  usedCPU,
		UsedMem:  usedMem,
		FreeCPU:  allocCPU - usedCPU,
		FreeMem:  allocMem - usedMem,
		PodCount: podCount,
	}
}
