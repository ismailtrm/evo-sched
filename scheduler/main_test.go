package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
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
