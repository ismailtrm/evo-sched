package evaluator

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const managedPodsSelector = "app=evo-sched-eval,evo-sched/managed=true"

type RunnerOptions struct {
	Namespace           string
	SchedulerNamespace  string
	SchedulerDeployment string
	SchedulerName       string
	Image               string
	PollInterval        time.Duration
	Timeout             time.Duration
	Cleanup             bool
}

func DefaultRunnerOptions() RunnerOptions {
	return RunnerOptions{
		Namespace:           "evo-sched-eval",
		SchedulerNamespace:  "evo-sched",
		SchedulerDeployment: "evok8s-scheduler",
		SchedulerName:       "evok8s-scheduler",
		Image:               "busybox:1.36.1",
		PollInterval:        time.Second,
		Cleanup:             true,
	}
}

func NewClientset() (*kubernetes.Clientset, error) {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return kubernetes.NewForConfig(cfg)
	}

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

func CurrentDeploymentWeights(ctx context.Context, cs kubernetes.Interface, namespace, deployment string) (WeightSet, error) {
	deploy, err := cs.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return WeightSet{}, fmt.Errorf("get scheduler deployment for weights: %w", err)
	}
	if len(deploy.Spec.Template.Spec.Containers) == 0 {
		return WeightSet{}, fmt.Errorf("scheduler deployment has no containers")
	}

	return WeightSet{
		FreeCPU:    envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_FREE_CPU", 1.0),
		FreeMem:    envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_FREE_MEM", 1.0),
		PodPenalty: envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_POD_PENALTY", 0.5),
		Packing:    envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_PACKING", 0.2),
	}, nil
}

func CurrentWorkloadBase(ctx context.Context, cs kubernetes.Interface) (WorkloadBase, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return WorkloadBase{}, fmt.Errorf("list nodes for workload base: %w", err)
	}

	base := WorkloadBase{}
	for _, node := range nodes.Items {
		if !isWorkerCandidate(&node) {
			continue
		}

		cpuMilli := node.Status.Allocatable.Cpu().MilliValue()
		memMiB := node.Status.Allocatable.Memory().Value() / (1024 * 1024)
		if cpuMilli <= 0 || memMiB <= 0 {
			continue
		}

		base.WorkerCount++
		if base.WorkerCPUMilli == 0 || cpuMilli < base.WorkerCPUMilli {
			base.WorkerCPUMilli = cpuMilli
		}
		if base.WorkerMemoryMiB == 0 || memMiB < base.WorkerMemoryMiB {
			base.WorkerMemoryMiB = memMiB
		}
	}

	if base.WorkerCount == 0 {
		return WorkloadBase{}, fmt.Errorf("no ready schedulable worker nodes found for workload base")
	}
	return base, nil
}

func RunBatch(ctx context.Context, cs kubernetes.Interface, opts RunnerOptions, workloadPath string, runSpecs []ExperimentRunSpec) (BatchResult, error) {
	if workloadPath == "" {
		return BatchResult{}, fmt.Errorf("workload path is required")
	}
	if len(runSpecs) == 0 {
		return BatchResult{}, fmt.Errorf("at least one experiment run is required")
	}

	workloadBase, workload, err := loadResolvedWorkload(ctx, cs, workloadPath)
	if err != nil {
		return BatchResult{}, err
	}
	if err := ensureNamespace(ctx, cs, opts.Namespace); err != nil {
		return BatchResult{}, err
	}

	originalWeights, err := CurrentDeploymentWeights(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment)
	if err != nil {
		return BatchResult{}, err
	}
	defer func() {
		if restoreErr := applyWeights(context.Background(), cs, opts.SchedulerNamespace, opts.SchedulerDeployment, originalWeights); restoreErr != nil {
			log.Printf("restore scheduler weights error: %v", restoreErr)
		}
	}()

	batch := BatchResult{
		Workload:         workloadName(workloadPath),
		WorkloadPath:     workloadPath,
		WorkloadBase:     &workloadBase,
		ResolvedWorkload: append([]WorkloadItem(nil), workload...),
		Results:          make([]ComparisonRow, 0, len(runSpecs)),
		Runs:             make([]RunResult, 0, len(runSpecs)),
	}

	for _, spec := range runSpecs {
		if err := cleanupManagedPods(ctx, cs, opts.Namespace); err != nil {
			return batch, err
		}
		if err := applyWeights(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment, spec.Weights); err != nil {
			return batch, err
		}

		runResult, runErr := executeRun(ctx, cs, opts, workloadPath, workload, spec)
		batch.Runs = append(batch.Runs, runResult)
		batch.Results = append(batch.Results, ComparisonRowFromRun(runResult))
		if runErr != nil {
			return batch, runErr
		}
	}

	return batch, nil
}

func EvaluateWorkloads(ctx context.Context, cs kubernetes.Interface, opts RunnerOptions, workloadPaths []string, spec ExperimentRunSpec) ([]BatchResult, error) {
	if len(workloadPaths) == 0 {
		return nil, fmt.Errorf("at least one workload is required")
	}
	if err := ensureNamespace(ctx, cs, opts.Namespace); err != nil {
		return nil, err
	}

	originalWeights, err := CurrentDeploymentWeights(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment)
	if err != nil {
		return nil, err
	}
	defer func() {
		if restoreErr := applyWeights(context.Background(), cs, opts.SchedulerNamespace, opts.SchedulerDeployment, originalWeights); restoreErr != nil {
			log.Printf("restore scheduler weights error: %v", restoreErr)
		}
	}()

	if err := applyWeights(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment, spec.Weights); err != nil {
		return nil, err
	}

	workloadBase, err := CurrentWorkloadBase(ctx, cs)
	if err != nil {
		return nil, err
	}

	results := make([]BatchResult, 0, len(workloadPaths))
	for _, workloadPath := range workloadPaths {
		workload, err := LoadWorkloadWithBase(workloadPath, &workloadBase)
		if err != nil {
			return results, err
		}

		batch := BatchResult{
			Workload:         workloadName(workloadPath),
			WorkloadPath:     workloadPath,
			WorkloadBase:     &workloadBase,
			ResolvedWorkload: append([]WorkloadItem(nil), workload...),
			Results:          make([]ComparisonRow, 0, 1),
			Runs:             make([]RunResult, 0, 1),
		}

		if err := cleanupManagedPods(ctx, cs, opts.Namespace); err != nil {
			return results, err
		}

		runResult, runErr := executeRun(ctx, cs, opts, workloadPath, workload, spec)
		batch.Runs = append(batch.Runs, runResult)
		batch.Results = append(batch.Results, ComparisonRowFromRun(runResult))
		results = append(results, batch)
		if runErr != nil {
			return results, runErr
		}
	}

	return results, nil
}

func loadResolvedWorkload(ctx context.Context, cs kubernetes.Interface, workloadPath string) (WorkloadBase, []WorkloadItem, error) {
	workloadBase, err := CurrentWorkloadBase(ctx, cs)
	if err != nil {
		return WorkloadBase{}, nil, err
	}

	workload, err := LoadWorkloadWithBase(workloadPath, &workloadBase)
	if err != nil {
		return WorkloadBase{}, nil, err
	}

	return workloadBase, workload, nil
}

func isWorkerCandidate(node *corev1.Node) bool {
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		return false
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		return false
	}
	if node.Spec.Unschedulable {
		return false
	}
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func executeRun(
	ctx context.Context,
	cs kubernetes.Interface,
	opts RunnerOptions,
	workloadPath string,
	workload []WorkloadItem,
	spec ExperimentRunSpec,
) (RunResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = estimatedTimeout(workload)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runID := makeRunID(spec.Name)
	startedAt := time.Now().UTC()
	selector := runSelector(runID)
	results := make(map[string]*PodTiming, len(workload))
	for _, item := range workload {
		results[item.Name] = &PodTiming{Name: item.Name}
	}

	var maxActiveNodes atomic.Int64
	var creatorDone atomic.Bool
	activeSamples := map[int]int{}
	var mu sync.Mutex
	doneCh := make(chan error, 1)

	go func() {
		doneCh <- monitorPods(
			runCtx,
			cs,
			opts.Namespace,
			selector,
			opts.PollInterval,
			results,
			&mu,
			&maxActiveNodes,
			activeSamples,
			&creatorDone,
			len(workload),
		)
	}()

	for _, item := range workload {
		if err := waitUntilArrival(runCtx, startedAt, item.ArrivalSeconds); err != nil {
			return emptyRunResult(opts, spec, workloadPath, startedAt), err
		}
		if err := createWorkloadPod(runCtx, cs, opts, runID, item, results[item.Name]); err != nil {
			return emptyRunResult(opts, spec, workloadPath, startedAt), err
		}
	}
	creatorDone.Store(true)

	monitorErr := <-doneCh
	finishedAt := time.Now().UTC()
	if opts.Cleanup {
		if err := cleanupPodsBySelector(context.Background(), cs, opts.Namespace, selector); err != nil {
			log.Printf("cleanup error for run=%s: %v", spec.Name, err)
		}
	}

	pods := flattenResults(results)
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	runResult := RunResult{
		Name:        spec.Name,
		RunID:       runID,
		Namespace:   opts.Namespace,
		Scheduler:   opts.SchedulerName,
		Workload:    workloadPath,
		Weights:     spec.Weights,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Summary:     SummarizePods(pods, int(maxActiveNodes.Load())),
		Pods:        pods,
		ActiveNodes: activeSamples,
	}
	if monitorErr != nil {
		runResult.Notes = append(runResult.Notes, monitorErr.Error())
	}

	return runResult, monitorErr
}

func emptyRunResult(opts RunnerOptions, spec ExperimentRunSpec, workloadPath string, startedAt time.Time) RunResult {
	return RunResult{
		Name:       spec.Name,
		Namespace:  opts.Namespace,
		Scheduler:  opts.SchedulerName,
		Workload:   workloadPath,
		Weights:    spec.Weights,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
	}
}

func ensureNamespace(ctx context.Context, cs kubernetes.Interface, name string) error {
	if _, err := cs.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	}
	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return fmt.Errorf("ensure namespace %s: %w", name, err)
	}
	return nil
}

func cleanupManagedPods(ctx context.Context, cs kubernetes.Interface, namespace string) error {
	return cleanupPodsBySelector(ctx, cs, namespace, managedPodsSelector)
}

func cleanupPodsBySelector(ctx context.Context, cs kubernetes.Interface, namespace, selector string) error {
	deletePolicy := metav1.DeletePropagationBackground
	if err := cs.CoreV1().Pods(namespace).DeleteCollection(ctx, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}, metav1.ListOptions{LabelSelector: selector}); err != nil {
		return fmt.Errorf("delete pods selector=%s: %w", selector, err)
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait cleanup selector=%s: %w", selector, ctx.Err())
		case <-ticker.C:
			pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("list pods during cleanup: %w", err)
			}
			if len(pods.Items) == 0 {
				return nil
			}
		}
	}
}

func applyWeights(ctx context.Context, cs kubernetes.Interface, namespace, name string, weights WeightSet) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		deploy, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get scheduler deployment: %w", err)
		}
		if len(deploy.Spec.Template.Spec.Containers) == 0 {
			return fmt.Errorf("scheduler deployment has no containers")
		}

		env := deploy.Spec.Template.Spec.Containers[0].Env
		env = upsertEnv(env, "WEIGHT_FREE_CPU", fmt.Sprintf("%g", weights.FreeCPU))
		env = upsertEnv(env, "WEIGHT_FREE_MEM", fmt.Sprintf("%g", weights.FreeMem))
		env = upsertEnv(env, "WEIGHT_POD_PENALTY", fmt.Sprintf("%g", weights.PodPenalty))
		env = upsertEnv(env, "WEIGHT_PACKING", fmt.Sprintf("%g", weights.Packing))
		deploy.Spec.Template.Spec.Containers[0].Env = env

		if _, err := cs.AppsV1().Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{}); err != nil {
			if apierrors.IsConflict(err) {
				lastErr = err
				time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
				continue
			}
			return fmt.Errorf("update scheduler deployment: %w", err)
		}

		return waitForRollout(ctx, cs, namespace, name)
	}

	return fmt.Errorf("update scheduler deployment after retries: %w", lastErr)
}

func upsertEnv(env []corev1.EnvVar, name, value string) []corev1.EnvVar {
	for i := range env {
		if env[i].Name == name {
			env[i].Value = value
			return env
		}
	}
	return append(env, corev1.EnvVar{Name: name, Value: value})
}

func envFloat64(env []corev1.EnvVar, name string, def float64) float64 {
	for _, item := range env {
		if item.Name != name {
			continue
		}
		v, err := strconv.ParseFloat(item.Value, 64)
		if err != nil {
			return def
		}
		return v
	}
	return def
}

func waitForRollout(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for rollout: %w", ctx.Err())
		case <-ticker.C:
			deploy, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("get deployment during rollout: %w", err)
			}
			if deploymentReady(deploy) {
				return nil
			}
		}
	}
}

func deploymentReady(deploy *appsv1.Deployment) bool {
	replicas := int32(1)
	if deploy.Spec.Replicas != nil {
		replicas = *deploy.Spec.Replicas
	}
	return deploy.Status.ObservedGeneration >= deploy.Generation &&
		deploy.Status.UpdatedReplicas == replicas &&
		deploy.Status.ReadyReplicas == replicas &&
		deploy.Status.AvailableReplicas == replicas
}

func estimatedTimeout(workload []WorkloadItem) time.Duration {
	maxArrival := 0
	maxDuration := 0
	for _, item := range workload {
		if item.ArrivalSeconds > maxArrival {
			maxArrival = item.ArrivalSeconds
		}
		if item.DurationSeconds > maxDuration {
			maxDuration = item.DurationSeconds
		}
	}
	return time.Duration(maxArrival+maxDuration+60) * time.Second
}

func waitUntilArrival(ctx context.Context, startedAt time.Time, arrivalSeconds int) error {
	target := startedAt.Add(time.Duration(arrivalSeconds) * time.Second)
	wait := time.Until(target)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func createWorkloadPod(
	ctx context.Context,
	cs kubernetes.Interface,
	opts RunnerOptions,
	runID string,
	item WorkloadItem,
	result *PodTiming,
) error {
	memBytes := item.MemoryMiB * 1024 * 1024
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", runID, item.Name),
			Namespace: opts.Namespace,
			Labels: map[string]string{
				"app":               "evo-sched-eval",
				"evo-sched/run-id":  runID,
				"evo-sched/item":    item.Name,
				"evo-sched/managed": "true",
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName:                 opts.SchedulerName,
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: int64Ptr(0),
			Containers: []corev1.Container{
				{
					Name:    "runner",
					Image:   opts.Image,
					Command: []string{"sh", "-c", fmt.Sprintf("sleep %d", item.DurationSeconds)},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    *apiresource.NewMilliQuantity(item.CPUMilli, apiresource.DecimalSI),
							corev1.ResourceMemory: *apiresource.NewQuantity(memBytes, apiresource.BinarySI),
						},
					},
				},
			},
		},
	}

	created, err := cs.CoreV1().Pods(opts.Namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod %s: %w", item.Name, err)
	}

	createdAt := created.CreationTimestamp.Time.UTC()
	result.CreatedAt = &createdAt
	return nil
}

func monitorPods(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace, selector string,
	pollInterval time.Duration,
	results map[string]*PodTiming,
	mu *sync.Mutex,
	maxActiveNodes *atomic.Int64,
	activeSamples map[int]int,
	creatorDone *atomic.Bool,
	expected int,
) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	sample := 0
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("monitor timeout: %w", ctx.Err())
		case <-ticker.C:
			pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("list evaluator pods: %w", err)
			}

			active := make(map[string]struct{})
			completed := 0
			for _, pod := range pods.Items {
				itemName := pod.Labels["evo-sched/item"]
				mu.Lock()
				result := results[itemName]
				if result != nil {
					updateTiming(result, &pod)
				}
				mu.Unlock()

				if pod.Spec.NodeName != "" && !isTerminalPhase(pod.Status.Phase) {
					active[pod.Spec.NodeName] = struct{}{}
				}
				if isTerminalPhase(pod.Status.Phase) {
					completed++
				}
			}

			count := int64(len(active))
			for {
				prev := maxActiveNodes.Load()
				if count <= prev || maxActiveNodes.CompareAndSwap(prev, count) {
					break
				}
			}

			mu.Lock()
			activeSamples[sample] = len(active)
			mu.Unlock()
			sample++

			if creatorDone.Load() && len(pods.Items) == expected && completed == expected {
				return nil
			}
		}
	}
}

func updateTiming(result *PodTiming, pod *corev1.Pod) {
	result.NodeName = pod.Spec.NodeName
	result.Phase = string(pod.Status.Phase)

	if result.CreatedAt == nil && !pod.CreationTimestamp.IsZero() {
		t := pod.CreationTimestamp.Time.UTC()
		result.CreatedAt = &t
	}

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionTrue && result.ScheduledAt == nil {
			t := cond.LastTransitionTime.Time.UTC()
			result.ScheduledAt = &t
			if result.CreatedAt != nil {
				result.PendingSeconds = t.Sub(*result.CreatedAt).Seconds()
			}
		}
	}

	if result.StartedAt == nil {
		for _, status := range pod.Status.ContainerStatuses {
			if status.State.Running != nil {
				t := status.State.Running.StartedAt.Time.UTC()
				result.StartedAt = &t
				if result.CreatedAt != nil {
					result.StartLatencySeconds = t.Sub(*result.CreatedAt).Seconds()
				}
				break
			}
		}
	}

	for _, status := range pod.Status.ContainerStatuses {
		if status.State.Terminated != nil {
			t := status.State.Terminated.FinishedAt.Time.UTC()
			result.FinishedAt = &t
			break
		}
	}
}

func flattenResults(results map[string]*PodTiming) []PodTiming {
	out := make([]PodTiming, 0, len(results))
	for _, result := range results {
		if result != nil {
			out = append(out, *result)
		}
	}
	return out
}

func workloadName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func makeRunID(name string) string {
	var b strings.Builder
	b.WriteString("eval-")
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	runID := strings.Trim(b.String(), "-")
	if len(runID) > 40 {
		runID = runID[:40]
	}
	return fmt.Sprintf("%s-%d", runID, time.Now().Unix())
}

func runSelector(runID string) string {
	return fmt.Sprintf("%s,evo-sched/run-id=%s", managedPodsSelector, runID)
}

func isTerminalPhase(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed
}

func int64Ptr(v int64) *int64 {
	return &v
}
