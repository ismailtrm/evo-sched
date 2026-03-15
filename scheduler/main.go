package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	schedulerName := getenv("SCHEDULER_NAME", "evok8s-scheduler")
	interval := 2 * time.Second

	clientset, err := newClientset()
	if err != nil {
		log.Fatalf("client init failed: %v", err)
	}

	log.Printf("scheduler started name=%s interval=%s", schedulerName, interval)

	for {
		if err := tick(context.Background(), clientset, schedulerName); err != nil {
			log.Printf("tick error: %v", err)
		}
		time.Sleep(interval)
	}
}

func tick(ctx context.Context, cs *kubernetes.Clientset, schedulerName string) error {
	// Only pods with empty spec.nodeName are unscheduled.
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=",
	})
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return nil
	}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list nodes: %w", err)
	}
	if len(nodes.Items) == 0 {
		return fmt.Errorf("no nodes found")
	}

	// Build a fresh resource snapshot for each node from current pod requests.
	allPods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list all pods: %w", err)
	}
	podCount := map[string]int{}
	for _, p := range allPods.Items {
		if p.Spec.NodeName != "" {
			podCount[p.Spec.NodeName]++
		}
	}

	for _, p := range pods.Items {
		// Only handle pods targeting our scheduler.
		if p.Spec.SchedulerName != schedulerName {
			continue
		}
		// Skip if terminating or weird states
		if p.DeletionTimestamp != nil {
			continue
		}

		candidates := filterNodesForPod(&p, nodes.Items)
		if len(candidates) == 0 {
			log.Printf("no feasible nodes pod=%s/%s", p.Namespace, p.Name)
			continue
		}

		nodeName := chooseNode(candidates, podCount)
		if nodeName == "" {
			continue
		}

		if err := bindPod(ctx, cs, p, nodeName); err != nil {
			log.Printf("bind failed pod=%s/%s node=%s err=%v", p.Namespace, p.Name, nodeName, err)
			continue
		}

		podCount[nodeName]++
		log.Printf("bound pod=%s/%s -> node=%s", p.Namespace, p.Name, nodeName)
	}

	return nil
}

func chooseNode(nodes []corev1.Node, podCount map[string]int) string {
	if len(nodes) == 0 {
		return ""
	}

	type cand struct {
		name  string
		count int
	}
	cands := make([]cand, 0, len(nodes))
	for _, n := range nodes {
		cands = append(cands, cand{name: n.Name, count: podCount[n.Name]})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].count == cands[j].count {
			return cands[i].name < cands[j].name
		}
		return cands[i].count < cands[j].count
	})
	return cands[0].name
}

func filterNodesForPod(pod *corev1.Pod, nodes []corev1.Node) []corev1.Node {
	out := make([]corev1.Node, 0, len(nodes))
	for _, n := range nodes {
		if !isNodeReady(&n) {
			continue
		}
		if n.Spec.Unschedulable {
			continue
		}
		if !toleratesNodeTaints(pod, &n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func toleratesNodeTaints(pod *corev1.Pod, node *corev1.Node) bool {
	for _, taint := range node.Spec.Taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		if !podToleratesTaint(pod, &taint) {
			return false
		}
	}
	return true
}

func podToleratesTaint(pod *corev1.Pod, taint *corev1.Taint) bool {
	for _, tol := range pod.Spec.Tolerations {
		if tol.Effect != "" && tol.Effect != taint.Effect {
			continue
		}

		switch tol.Operator {
		case corev1.TolerationOpExists:
			if tol.Key == "" || tol.Key == taint.Key {
				return true
			}
		case corev1.TolerationOpEqual, "":
			if tol.Key == taint.Key && tol.Value == taint.Value {
				return true
			}
		}
	}
	return false
}

func bindPod(ctx context.Context, cs *kubernetes.Clientset, p corev1.Pod, nodeName string) error {
	b := &corev1.Binding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.Name,
			Namespace: p.Namespace,
			UID:       p.UID,
		},
		Target: corev1.ObjectReference{
			Kind: "Node",
			Name: nodeName,
		},
	}
	return cs.CoreV1().Pods(p.Namespace).Bind(ctx, b, metav1.CreateOptions{})
}

func newClientset() (*kubernetes.Clientset, error) {
	// Prefer in-cluster config when running inside Kubernetes.
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return kubernetes.NewForConfig(cfg)
	}

	// Fallback: local kubeconfig (useful for running scheduler locally).
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.ExpandEnv("$HOME/.kube/config")
	}
	cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	return v
}
