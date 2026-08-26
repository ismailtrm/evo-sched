package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"time"

	"evok8s-scheduler/pkg/policy"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type ResourceRequest struct {
	CPUMilli    int64
	MemoryBytes int64
}

type NodeState struct {
	Name            string
	AllocCPU        int64
	AllocMem        int64
	UsedCPU         int64
	UsedMem         int64
	FreeCPU         int64
	FreeMem         int64
	ManagedPodCount int
}

type ScoreWeights = policy.WeightSet

func main() {
	schedulerName := getenv("SCHEDULER_NAME", "evok8s-scheduler")
	interval := 2 * time.Second
	weights, err := loadScoreWeights()
	if err != nil {
		log.Fatalf("score weights init failed: %v", err)
	}

	clientset, err := newClientset()
	if err != nil {
		log.Fatalf("client init failed: %v", err)
	}

	log.Printf("scheduler started name=%s interval=%s weights=%+v", schedulerName, interval, weights)

	for {
		if err := tick(context.Background(), clientset, schedulerName, weights); err != nil {
			log.Printf("tick error: %v", err)
		}
		time.Sleep(interval)
	}
}

func tick(ctx context.Context, cs *kubernetes.Clientset, schedulerName string, weights ScoreWeights) error {
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
	nodeStates := buildNodeStates(nodes.Items, allPods.Items, schedulerName)

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

		req := podRequests(&p)
		nodeName, score := chooseBestNode(candidates, nodeStates, req, weights)
		if nodeName == "" {
			log.Printf(
				"no feasible nodes after resource fit pod=%s/%s reqCPUm=%d reqMemBytes=%d",
				p.Namespace,
				p.Name,
				req.CPUMilli,
				req.MemoryBytes,
			)
			continue
		}

		if err := bindPod(ctx, cs, p, nodeName); err != nil {
			log.Printf("bind failed pod=%s/%s node=%s err=%v", p.Namespace, p.Name, nodeName, err)
			continue
		}

		if state := nodeStates[nodeName]; state != nil {
			applyRequestToNodeState(state, req, true)
		}

		log.Printf("bound pod=%s/%s -> node=%s score=%.4f", p.Namespace, p.Name, nodeName, score)
	}

	return nil
}

func chooseBestNode(
	nodes []corev1.Node,
	states map[string]*NodeState,
	req ResourceRequest,
	weights ScoreWeights,
) (string, float64) {
	feasible := make([]*NodeState, 0, len(nodes))
	for _, n := range nodes {
		state := states[n.Name]
		if state == nil || !fitsRequest(state, req) {
			continue
		}
		feasible = append(feasible, state)
	}
	if len(feasible) == 0 {
		return "", 0
	}

	bestName := ""
	bestScore := math.Inf(-1)
	for _, state := range feasible {
		score := scoreNode(state, req, weights)
		if score > bestScore+1e-9 || (math.Abs(score-bestScore) <= 1e-9 && (bestName == "" || state.Name < bestName)) {
			bestName = state.Name
			bestScore = score
		}
	}

	return bestName, bestScore
}

func buildNodeStates(nodes []corev1.Node, pods []corev1.Pod, schedulerName string) map[string]*NodeState {
	states := make(map[string]*NodeState, len(nodes))
	for _, n := range nodes {
		allocCPU := n.Status.Allocatable.Cpu().MilliValue()
		allocMem := n.Status.Allocatable.Memory().Value()
		states[n.Name] = &NodeState{
			Name:     n.Name,
			AllocCPU: allocCPU,
			AllocMem: allocMem,
			FreeCPU:  allocCPU,
			FreeMem:  allocMem,
		}
	}

	for _, p := range pods {
		if p.Spec.NodeName == "" || isTerminalPod(&p) {
			continue
		}
		state := states[p.Spec.NodeName]
		if state == nil {
			continue
		}
		applyRequestToNodeState(state, podRequests(&p), p.Spec.SchedulerName == schedulerName)
	}

	return states
}

func podRequests(pod *corev1.Pod) ResourceRequest {
	var req ResourceRequest
	for _, c := range pod.Spec.Containers {
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			req.CPUMilli += q.MilliValue()
		}
		if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			req.MemoryBytes += q.Value()
		}
	}
	if pod.Spec.Overhead != nil {
		if q, ok := pod.Spec.Overhead[corev1.ResourceCPU]; ok {
			req.CPUMilli += q.MilliValue()
		}
		if q, ok := pod.Spec.Overhead[corev1.ResourceMemory]; ok {
			req.MemoryBytes += q.Value()
		}
	}
	return req
}

func applyRequestToNodeState(state *NodeState, req ResourceRequest, managed bool) {
	state.UsedCPU += req.CPUMilli
	state.UsedMem += req.MemoryBytes
	if managed {
		state.ManagedPodCount++
	}
	state.FreeCPU = maxInt64(0, state.AllocCPU-state.UsedCPU)
	state.FreeMem = maxInt64(0, state.AllocMem-state.UsedMem)
}

func fitsRequest(state *NodeState, req ResourceRequest) bool {
	return state.FreeCPU >= req.CPUMilli && state.FreeMem >= req.MemoryBytes
}

func scoreNode(state *NodeState, req ResourceRequest, weights ScoreWeights) float64 {
	postFreeCPU := fraction(maxInt64(0, state.FreeCPU-req.CPUMilli), state.AllocCPU)
	postFreeMem := fraction(maxInt64(0, state.FreeMem-req.MemoryBytes), state.AllocMem)
	balance := 1.0 - math.Abs(postFreeCPU-postFreeMem)

	consolidation := 0.0
	if state.ManagedPodCount > 0 {
		consolidation = 1.0
	}

	return (weights.FreeCPU * postFreeCPU) +
		(weights.FreeMem * postFreeMem) +
		(weights.Balance * balance) +
		(weights.Consolidation * consolidation)
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

func isTerminalPod(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

func fraction(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	v := float64(numerator) / float64(denominator)
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func loadScoreWeights() (ScoreWeights, error) {
	freeCPU, err := getenvFloat64("WEIGHT_FREE_CPU", 1.0)
	if err != nil {
		return ScoreWeights{}, err
	}
	freeMem, err := getenvFloat64("WEIGHT_FREE_MEM", 1.0)
	if err != nil {
		return ScoreWeights{}, err
	}
	balance, err := getenvFloat64("WEIGHT_BALANCE", 1.0)
	if err != nil {
		return ScoreWeights{}, err
	}
	consolidation, err := getenvFloat64("WEIGHT_CONSOLIDATION", 1.0)
	if err != nil {
		return ScoreWeights{}, err
	}
	return normalizeScoreWeights(ScoreWeights{
		FreeCPU:       freeCPU,
		FreeMem:       freeMem,
		Balance:       balance,
		Consolidation: consolidation,
	})
}

func normalizeScoreWeights(weights ScoreWeights) (ScoreWeights, error) {
	return policy.Normalize(weights)
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

func getenvFloat64(k string, def float64) (float64, error) {
	v := os.Getenv(k)
	if v == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", k, v, err)
	}
	return f, nil
}
