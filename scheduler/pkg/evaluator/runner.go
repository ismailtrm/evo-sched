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

	"evok8s-scheduler/pkg/measurement"
	"evok8s-scheduler/pkg/policy"

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

func MeasurementProtocolFor(opts RunnerOptions, schedulerInterval time.Duration) MeasurementProtocol {
	return MeasurementProtocol{
		Version:                             MeasurementVersion,
		SubmissionTimestampSource:           "evaluator-client-before-create",
		SchedulerDecisionTimestampSource:    "scheduler-before-bind-request",
		KubernetesScheduledTimestampSource:  "pod-condition-last-transition-time",
		EvaluatorObservationTimestampSource: "evaluator-list-response",
		WorkloadOccupancySource:             "non-terminal-evaluator-pods-on-run-start-worker-snapshot",
		WorkloadResourceMetricSource:        WorkloadRequestedResourceMetricSource,
		SchedulerIntervalSeconds:            schedulerInterval.Seconds(),
		PodPollIntervalSeconds:              opts.PollInterval.Seconds(),
	}
}

func validateRunnerOptions(opts RunnerOptions) error {
	if opts.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive, got %s", opts.PollInterval)
	}
	return nil
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

	env := deploy.Spec.Template.Spec.Containers[0].Env
	freeCPU, err := envFloat64(env, "WEIGHT_FREE_CPU", 1.0)
	if err != nil {
		return WeightSet{}, err
	}
	freeMem, err := envFloat64(env, "WEIGHT_FREE_MEM", 1.0)
	if err != nil {
		return WeightSet{}, err
	}
	balance, err := envFloat64(env, "WEIGHT_BALANCE", 1.0)
	if err != nil {
		return WeightSet{}, err
	}
	consolidation, err := envFloat64(env, "WEIGHT_CONSOLIDATION", 1.0)
	if err != nil {
		return WeightSet{}, err
	}

	return policy.Normalize(WeightSet{
		FreeCPU:       freeCPU,
		FreeMem:       freeMem,
		Balance:       balance,
		Consolidation: consolidation,
	})
}

func CurrentDeploymentSchedulerInterval(ctx context.Context, cs kubernetes.Interface, namespace, deployment string) (time.Duration, error) {
	deploy, err := cs.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("get scheduler deployment for interval: %w", err)
	}
	if len(deploy.Spec.Template.Spec.Containers) == 0 {
		return 0, fmt.Errorf("scheduler deployment has no containers")
	}

	for _, item := range deploy.Spec.Template.Spec.Containers[0].Env {
		if item.Name != "SCHEDULER_INTERVAL" {
			continue
		}
		interval, err := time.ParseDuration(item.Value)
		if err != nil {
			return 0, fmt.Errorf("parse SCHEDULER_INTERVAL=%q: %w", item.Value, err)
		}
		if interval <= 0 {
			return 0, fmt.Errorf("SCHEDULER_INTERVAL must be positive, got %q", item.Value)
		}
		return interval, nil
	}

	return 0, fmt.Errorf("scheduler deployment is missing SCHEDULER_INTERVAL")
}

func CurrentWorkloadBase(ctx context.Context, cs kubernetes.Interface) (WorkloadBase, error) {
	workers, err := currentWorkerNodes(ctx, cs)
	if err != nil {
		return WorkloadBase{}, err
	}

	base := WorkloadBase{WorkerCount: len(workers)}
	for _, node := range workers {
		cpuMilli := node.Status.Allocatable.Cpu().MilliValue()
		memMiB := node.Status.Allocatable.Memory().Value() / (1024 * 1024)
		if base.WorkerCPUMilli == 0 || cpuMilli < base.WorkerCPUMilli {
			base.WorkerCPUMilli = cpuMilli
		}
		if base.WorkerMemoryMiB == 0 || memMiB < base.WorkerMemoryMiB {
			base.WorkerMemoryMiB = memMiB
		}
	}

	return base, nil
}

func currentWorkerNodes(ctx context.Context, cs kubernetes.Interface) ([]corev1.Node, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list worker nodes: %w", err)
	}

	workers := make([]corev1.Node, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		if !isWorkerCandidate(&node) {
			continue
		}
		cpuMilli := node.Status.Allocatable.Cpu().MilliValue()
		memMiB := node.Status.Allocatable.Memory().Value() / (1024 * 1024)
		if cpuMilli <= 0 || memMiB <= 0 {
			continue
		}
		workers = append(workers, node)
	}

	if len(workers) == 0 {
		return nil, fmt.Errorf("no ready schedulable worker nodes found")
	}
	return workers, nil
}

func RunBatch(ctx context.Context, cs kubernetes.Interface, opts RunnerOptions, workloadPath string, runSpecs []ExperimentRunSpec) (BatchResult, error) {
	if err := validateRunnerOptions(opts); err != nil {
		return BatchResult{}, err
	}
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
	samplingProfile, err := buildWorkloadSamplingProfile(workload, opts.PollInterval)
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
	schedulerInterval, err := CurrentDeploymentSchedulerInterval(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment)
	if err != nil {
		return BatchResult{}, err
	}
	protocol := MeasurementProtocolFor(opts, schedulerInterval)
	defer func() {
		if restoreErr := applyWeights(context.Background(), cs, opts.SchedulerNamespace, opts.SchedulerDeployment, originalWeights); restoreErr != nil {
			log.Printf("restore scheduler weights error: %v", restoreErr)
		}
	}()

	batch := BatchResult{
		PolicyVersion:           PolicyVersion,
		MeasurementVersion:      MeasurementVersion,
		MeasurementProtocol:     protocol,
		Workload:                workloadName(workloadPath),
		WorkloadPath:            workloadPath,
		WorkloadBase:            &workloadBase,
		WorkloadSamplingProfile: samplingProfile,
		ResolvedWorkload:        append([]WorkloadItem(nil), workload...),
		Results:                 make([]ComparisonRow, 0, len(runSpecs)),
		Runs:                    make([]RunResult, 0, len(runSpecs)),
	}

	for _, spec := range runSpecs {
		normalized, err := policy.Normalize(spec.Weights)
		if err != nil {
			return batch, fmt.Errorf("experiment run %q weights: %w", spec.Name, err)
		}
		spec.Weights = normalized
		if err := cleanupManagedPods(ctx, cs, opts.Namespace); err != nil {
			return batch, err
		}
		if err := applyWeights(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment, spec.Weights); err != nil {
			return batch, err
		}

		runResult, runErr := executeRun(ctx, cs, opts, workloadPath, workload, spec, protocol, samplingProfile)
		batch.Runs = append(batch.Runs, runResult)
		batch.Results = append(batch.Results, ComparisonRowFromRun(runResult))
		if runErr != nil {
			return batch, runErr
		}
	}

	return batch, nil
}

func EvaluateWorkloads(ctx context.Context, cs kubernetes.Interface, opts RunnerOptions, workloadPaths []string, spec ExperimentRunSpec) ([]BatchResult, error) {
	if err := validateRunnerOptions(opts); err != nil {
		return nil, err
	}
	if len(workloadPaths) == 0 {
		return nil, fmt.Errorf("at least one workload is required")
	}
	normalized, err := policy.Normalize(spec.Weights)
	if err != nil {
		return nil, fmt.Errorf("experiment run %q weights: %w", spec.Name, err)
	}
	spec.Weights = normalized
	if err := ensureNamespace(ctx, cs, opts.Namespace); err != nil {
		return nil, err
	}

	originalWeights, err := CurrentDeploymentWeights(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment)
	if err != nil {
		return nil, err
	}
	schedulerInterval, err := CurrentDeploymentSchedulerInterval(ctx, cs, opts.SchedulerNamespace, opts.SchedulerDeployment)
	if err != nil {
		return nil, err
	}
	protocol := MeasurementProtocolFor(opts, schedulerInterval)
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
		samplingProfile, err := buildWorkloadSamplingProfile(workload, opts.PollInterval)
		if err != nil {
			return results, err
		}

		batch := BatchResult{
			PolicyVersion:           PolicyVersion,
			MeasurementVersion:      MeasurementVersion,
			MeasurementProtocol:     protocol,
			Workload:                workloadName(workloadPath),
			WorkloadPath:            workloadPath,
			WorkloadBase:            &workloadBase,
			WorkloadSamplingProfile: samplingProfile,
			ResolvedWorkload:        append([]WorkloadItem(nil), workload...),
			Results:                 make([]ComparisonRow, 0, 1),
			Runs:                    make([]RunResult, 0, 1),
		}

		if err := cleanupManagedPods(ctx, cs, opts.Namespace); err != nil {
			return results, err
		}

		runResult, runErr := executeRun(ctx, cs, opts, workloadPath, workload, spec, protocol, samplingProfile)
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
	protocol MeasurementProtocol,
	samplingProfile WorkloadSamplingProfile,
) (RunResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = estimatedTimeout(workload)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	runID := makeRunID(spec.Name)
	startedAt := time.Now().UTC()
	workerNodes, err := currentWorkerNodes(runCtx, cs)
	if err != nil {
		return emptyRunResult(opts, spec, workloadPath, startedAt, protocol, samplingProfile), err
	}

	selector := runSelector(runID)
	results := make(map[string]*PodTiming, len(workload))
	for _, item := range workload {
		results[item.Name] = &PodTiming{Name: item.Name}
	}

	var maxWorkloadOccupiedWorkerNodes atomic.Int64
	var creatorDone atomic.Bool
	occupancySamples := make([]NodeOccupancySample, 0)
	resourceSamples := make([]WorkloadResourceSample, 0)
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
			&maxWorkloadOccupiedWorkerNodes,
			&occupancySamples,
			&resourceSamples,
			&creatorDone,
			len(workload),
			workerNodes,
		)
	}()

	for _, item := range workload {
		if err := waitUntilArrival(runCtx, startedAt, item.ArrivalSeconds); err != nil {
			return emptyRunResult(opts, spec, workloadPath, startedAt, protocol, samplingProfile), err
		}
		if err := createWorkloadPod(runCtx, cs, opts, runID, item, results[item.Name]); err != nil {
			return emptyRunResult(opts, spec, workloadPath, startedAt, protocol, samplingProfile), err
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
	summary := SummarizePods(pods, int(maxWorkloadOccupiedWorkerNodes.Load()))
	summary.ResourceSummary = summarizeResourceSamples(resourceSamples)
	runResult := RunResult{
		PolicyVersion:                     PolicyVersion,
		MeasurementVersion:                MeasurementVersion,
		MeasurementProtocol:               protocol,
		Name:                              spec.Name,
		RunID:                             runID,
		Namespace:                         opts.Namespace,
		Scheduler:                         opts.SchedulerName,
		Workload:                          workloadPath,
		Weights:                           spec.Weights,
		StartedAt:                         startedAt,
		FinishedAt:                        finishedAt,
		Summary:                           summary,
		Pods:                              pods,
		WorkloadOccupiedWorkerNodeSamples: occupancySamples,
		WorkloadResourceSamples:           resourceSamples,
		WorkloadSamplingProfile:           samplingProfile,
	}
	if monitorErr != nil {
		runResult.Notes = append(runResult.Notes, monitorErr.Error())
	}

	return runResult, monitorErr
}

func emptyRunResult(opts RunnerOptions, spec ExperimentRunSpec, workloadPath string, startedAt time.Time, protocol MeasurementProtocol, samplingProfile WorkloadSamplingProfile) RunResult {
	return RunResult{
		PolicyVersion:           PolicyVersion,
		MeasurementVersion:      MeasurementVersion,
		MeasurementProtocol:     protocol,
		Name:                    spec.Name,
		Namespace:               opts.Namespace,
		Scheduler:               opts.SchedulerName,
		Workload:                workloadPath,
		Weights:                 spec.Weights,
		StartedAt:               startedAt,
		FinishedAt:              time.Now().UTC(),
		WorkloadSamplingProfile: samplingProfile,
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
	normalized, err := policy.Normalize(weights)
	if err != nil {
		return fmt.Errorf("normalize scheduler weights: %w", err)
	}
	weights = normalized

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
		env = upsertEnv(env, "WEIGHT_BALANCE", fmt.Sprintf("%g", weights.Balance))
		env = upsertEnv(env, "WEIGHT_CONSOLIDATION", fmt.Sprintf("%g", weights.Consolidation))
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

func envFloat64(env []corev1.EnvVar, name string, def float64) (float64, error) {
	for _, item := range env {
		if item.Name != name {
			continue
		}
		v, err := strconv.ParseFloat(item.Value, 64)
		if err != nil {
			return 0, fmt.Errorf("parse deployment env %s=%q: %w", name, item.Value, err)
		}
		return v, nil
	}
	return def, nil
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
	terminatingReplicas := int32(0)
	if deploy.Status.TerminatingReplicas != nil {
		terminatingReplicas = *deploy.Status.TerminatingReplicas
	}
	return deploy.Status.ObservedGeneration >= deploy.Generation &&
		deploy.Status.Replicas == replicas &&
		deploy.Status.UpdatedReplicas == replicas &&
		deploy.Status.ReadyReplicas == replicas &&
		deploy.Status.AvailableReplicas == replicas &&
		deploy.Status.UnavailableReplicas == 0 &&
		terminatingReplicas == 0
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
	submittedAt := time.Now().UTC()
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
			Annotations: map[string]string{
				measurement.SubmittedAtAnnotation:        measurement.FormatTimestamp(submittedAt),
				measurement.MeasurementVersionAnnotation: measurement.Version,
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

	result.SubmittedAt = &submittedAt
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
	maxWorkloadOccupiedWorkerNodes *atomic.Int64,
	occupancySamples *[]NodeOccupancySample,
	resourceSamples *[]WorkloadResourceSample,
	creatorDone *atomic.Bool,
	expected int,
	workerNodes []corev1.Node,
) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("monitor timeout: %w", ctx.Err())
		case <-ticker.C:
			pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return fmt.Errorf("list evaluator pods: %w", err)
			}

			observedAt := time.Now().UTC()
			completed := 0
			for _, pod := range pods.Items {
				itemName := pod.Labels["evo-sched/item"]
				mu.Lock()
				result := results[itemName]
				if result != nil {
					if err := updateTiming(result, &pod, observedAt); err != nil {
						mu.Unlock()
						return fmt.Errorf("update timing pod=%s: %w", pod.Name, err)
					}
				}
				mu.Unlock()

				if isTerminalPhase(pod.Status.Phase) {
					completed++
				}
			}

			resourceSample, err := buildWorkloadResourceSample(observedAt, pods.Items, workerNodes)
			if err != nil {
				return err
			}

			count := int64(resourceSample.WorkloadOccupiedWorkerNodes)
			for {
				prev := maxWorkloadOccupiedWorkerNodes.Load()
				if count <= prev || maxWorkloadOccupiedWorkerNodes.CompareAndSwap(prev, count) {
					break
				}
			}

			mu.Lock()
			*occupancySamples = append(*occupancySamples, NodeOccupancySample{
				ObservedAt:                  observedAt,
				WorkloadOccupiedWorkerNodes: resourceSample.WorkloadOccupiedWorkerNodes,
			})
			*resourceSamples = append(*resourceSamples, resourceSample)
			mu.Unlock()

			if creatorDone.Load() && len(pods.Items) == expected && completed == expected {
				return nil
			}
		}
	}
}

func updateTiming(result *PodTiming, pod *corev1.Pod, observedAt time.Time) error {
	result.NodeName = pod.Spec.NodeName
	result.Phase = string(pod.Status.Phase)

	if result.SubmittedAt == nil {
		if raw := pod.Annotations[measurement.SubmittedAtAnnotation]; raw != "" {
			if got := pod.Annotations[measurement.MeasurementVersionAnnotation]; got != measurement.Version {
				return fmt.Errorf("measurement version annotation = %q, want %q", got, measurement.Version)
			}
			t, err := measurement.ParseTimestamp(raw)
			if err != nil {
				return err
			}
			result.SubmittedAt = &t
		}
	}
	if result.SchedulerDecisionAt == nil {
		if raw := pod.Annotations[measurement.DecisionAtAnnotation]; raw != "" {
			if got := pod.Annotations[measurement.MeasurementVersionAnnotation]; got != measurement.Version {
				return fmt.Errorf("measurement version annotation = %q, want %q", got, measurement.Version)
			}
			if got := pod.Annotations[measurement.PolicyVersionAnnotation]; got != PolicyVersion {
				return fmt.Errorf("policy version annotation = %q, want %q", got, PolicyVersion)
			}
			t, err := measurement.ParseTimestamp(raw)
			if err != nil {
				return err
			}
			result.SchedulerDecisionAt = &t
		}
	}
	if result.SubmittedAt != nil && result.SchedulerDecisionAt != nil {
		result.SchedulingDecisionLatencySeconds = result.SchedulerDecisionAt.Sub(*result.SubmittedAt).Seconds()
	}
	if pod.Spec.NodeName != "" && result.EvaluatorObservedScheduledAt == nil {
		t := observedAt.UTC()
		result.EvaluatorObservedScheduledAt = &t
		if result.SchedulerDecisionAt != nil {
			result.EvaluatorObservationLagSeconds = t.Sub(*result.SchedulerDecisionAt).Seconds()
		}
	}

	if result.CreatedAt == nil && !pod.CreationTimestamp.IsZero() {
		t := pod.CreationTimestamp.Time.UTC()
		result.CreatedAt = &t
	}

	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionTrue && result.KubernetesScheduledAt == nil {
			t := cond.LastTransitionTime.Time.UTC()
			result.KubernetesScheduledAt = &t
			if result.CreatedAt != nil {
				result.KubernetesPendingSeconds = t.Sub(*result.CreatedAt).Seconds()
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

	return nil
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
