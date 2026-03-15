package main

import (
	"context"
	"encoding/json"
	"flag"
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
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"evok8s-scheduler/pkg/evaluator"
)

const managedPodsSelector = "app=evo-sched-eval,evo-sched/managed=true"

type options struct {
	workloadPath        string
	namespace           string
	schedulerNamespace  string
	schedulerDeployment string
	schedulerName       string
	image               string
	outputPath          string
	csvOutputPath       string
	weights             string
	weightsFile         string
	pollInterval        time.Duration
	timeout             time.Duration
	cleanup             bool
}

func main() {
	opts := parseFlags()
	if err := run(opts); err != nil {
		log.Fatal(err)
	}
}

func parseFlags() options {
	opts := options{}
	flag.StringVar(&opts.workloadPath, "workload", "", "Path to workload JSON file")
	flag.StringVar(&opts.namespace, "namespace", "evo-sched-eval", "Namespace for evaluator-created pods")
	flag.StringVar(&opts.schedulerNamespace, "scheduler-namespace", "evo-sched", "Namespace of the scheduler deployment")
	flag.StringVar(&opts.schedulerDeployment, "scheduler-deployment", "evok8s-scheduler", "Name of the scheduler deployment")
	flag.StringVar(&opts.schedulerName, "scheduler-name", "evok8s-scheduler", "schedulerName set on generated pods")
	flag.StringVar(&opts.image, "image", "busybox:1.36.1", "Container image for evaluator-created pods")
	flag.StringVar(&opts.outputPath, "output", "", "Optional output JSON path")
	flag.StringVar(&opts.csvOutputPath, "csv-output", "", "Optional CSV output path; defaults next to --output")
	flag.StringVar(&opts.weights, "weights", "", "Optional single run weights in FreeCPU,FreeMem,PodPenalty,Packing order")
	flag.StringVar(&opts.weightsFile, "weights-file", "", "Optional JSON file describing named experiment runs")
	flag.DurationVar(&opts.pollInterval, "poll-interval", 1*time.Second, "Polling interval for pod state collection")
	flag.DurationVar(&opts.timeout, "timeout", 0, "Per-run timeout; defaults from workload length")
	flag.BoolVar(&opts.cleanup, "cleanup", true, "Delete evaluator pods after each run")
	flag.Parse()
	return opts
}

func run(opts options) error {
	if opts.workloadPath == "" {
		return fmt.Errorf("--workload is required")
	}
	if opts.weights != "" && opts.weightsFile != "" {
		return fmt.Errorf("--weights and --weights-file cannot be used together")
	}

	workload, err := evaluator.LoadWorkload(opts.workloadPath)
	if err != nil {
		return err
	}

	clientset, err := newClientset()
	if err != nil {
		return fmt.Errorf("client init failed: %w", err)
	}

	originalWeights, err := readDeploymentWeights(context.Background(), clientset, opts.schedulerNamespace, opts.schedulerDeployment)
	if err != nil {
		return err
	}
	if opts.weights != "" || opts.weightsFile != "" {
		defer func() {
			if err := applyWeights(context.Background(), clientset, opts.schedulerNamespace, opts.schedulerDeployment, originalWeights); err != nil {
				log.Printf("restore scheduler weights error: %v", err)
			}
		}()
	}

	if err := ensureNamespace(context.Background(), clientset, opts.namespace); err != nil {
		return err
	}

	runSpecs, err := loadRunSpecs(opts, originalWeights)
	if err != nil {
		return err
	}

	batch := evaluator.BatchResult{
		Workload:     workloadName(opts.workloadPath),
		WorkloadPath: opts.workloadPath,
		Results:      make([]evaluator.ComparisonRow, 0, len(runSpecs)),
		Runs:         make([]evaluator.RunResult, 0, len(runSpecs)),
	}

	for _, spec := range runSpecs {
		log.Printf("starting experiment run name=%s weights=%+v", spec.Name, spec.Weights)

		if err := cleanupManagedPods(context.Background(), clientset, opts.namespace); err != nil {
			return err
		}
		if err := applyWeights(context.Background(), clientset, opts.schedulerNamespace, opts.schedulerDeployment, spec.Weights); err != nil {
			return err
		}

		runResult, err := executeRun(clientset, opts, workload, spec)
		if err != nil {
			batch.Runs = append(batch.Runs, runResult)
			batch.Results = append(batch.Results, evaluator.ComparisonRowFromRun(runResult))
			if emitErr := emitOutputs(batch, opts); emitErr != nil {
				return emitErr
			}
			return err
		}

		batch.Runs = append(batch.Runs, runResult)
		batch.Results = append(batch.Results, evaluator.ComparisonRowFromRun(runResult))
	}

	return emitOutputs(batch, opts)
}

func loadRunSpecs(opts options, current evaluator.WeightSet) ([]evaluator.ExperimentRunSpec, error) {
	if opts.weightsFile != "" {
		spec, err := evaluator.LoadExperiment(opts.weightsFile)
		if err != nil {
			return nil, err
		}
		return spec.Runs, nil
	}

	name := "current-deployment"
	weights := current
	if opts.weights != "" {
		name = "cli-weights"
		var err error
		weights, err = evaluator.ParseWeightSet(opts.weights)
		if err != nil {
			return nil, err
		}
	}

	return []evaluator.ExperimentRunSpec{
		{Name: name, Weights: weights},
	}, nil
}

func readDeploymentWeights(ctx context.Context, cs *kubernetes.Clientset, namespace, deployment string) (evaluator.WeightSet, error) {
	deploy, err := cs.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return evaluator.WeightSet{}, fmt.Errorf("get scheduler deployment for weights: %w", err)
	}
	if len(deploy.Spec.Template.Spec.Containers) == 0 {
		return evaluator.WeightSet{}, fmt.Errorf("scheduler deployment has no containers")
	}

	return evaluator.WeightSet{
		FreeCPU:    envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_FREE_CPU", 1.0),
		FreeMem:    envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_FREE_MEM", 1.0),
		PodPenalty: envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_POD_PENALTY", 0.5),
		Packing:    envFloat64(deploy.Spec.Template.Spec.Containers[0].Env, "WEIGHT_PACKING", 0.2),
	}, nil
}

func executeRun(
	cs *kubernetes.Clientset,
	opts options,
	workload []evaluator.WorkloadItem,
	spec evaluator.ExperimentRunSpec,
) (evaluator.RunResult, error) {
	timeout := opts.timeout
	if timeout <= 0 {
		timeout = estimatedTimeout(workload)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	runID := makeRunID(spec.Name)
	startedAt := time.Now().UTC()
	selector := runSelector(runID)
	results := make(map[string]*evaluator.PodTiming, len(workload))
	for _, item := range workload {
		results[item.Name] = &evaluator.PodTiming{Name: item.Name}
	}

	var maxActiveNodes atomic.Int64
	var creatorDone atomic.Bool
	activeSamples := map[int]int{}
	var mu sync.Mutex
	doneCh := make(chan error, 1)

	go func() {
		doneCh <- monitorPods(
			ctx,
			cs,
			opts.namespace,
			selector,
			opts.pollInterval,
			results,
			&mu,
			&maxActiveNodes,
			activeSamples,
			&creatorDone,
			len(workload),
		)
	}()

	for _, item := range workload {
		if err := waitUntilArrival(ctx, startedAt, item.ArrivalSeconds); err != nil {
			return emptyRunResult(opts, spec, startedAt), err
		}
		if err := createWorkloadPod(ctx, cs, opts, runID, item, results[item.Name]); err != nil {
			return emptyRunResult(opts, spec, startedAt), err
		}
	}
	creatorDone.Store(true)

	monitorErr := <-doneCh
	finishedAt := time.Now().UTC()
	if opts.cleanup {
		if err := cleanupPodsBySelector(context.Background(), cs, opts.namespace, selector); err != nil {
			log.Printf("cleanup error for run=%s: %v", spec.Name, err)
		}
	}

	pods := flattenResults(results)
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	runResult := evaluator.RunResult{
		Name:        spec.Name,
		RunID:       runID,
		Namespace:   opts.namespace,
		Scheduler:   opts.schedulerName,
		Workload:    opts.workloadPath,
		Weights:     spec.Weights,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
		Summary:     evaluator.SummarizePods(pods, int(maxActiveNodes.Load())),
		Pods:        pods,
		ActiveNodes: activeSamples,
	}
	if monitorErr != nil {
		runResult.Notes = append(runResult.Notes, monitorErr.Error())
	}

	return runResult, monitorErr
}

func emptyRunResult(opts options, spec evaluator.ExperimentRunSpec, startedAt time.Time) evaluator.RunResult {
	return evaluator.RunResult{
		Name:       spec.Name,
		Namespace:  opts.namespace,
		Scheduler:  opts.schedulerName,
		Workload:   opts.workloadPath,
		Weights:    spec.Weights,
		StartedAt:  startedAt,
		FinishedAt: time.Now().UTC(),
	}
}

func ensureNamespace(ctx context.Context, cs *kubernetes.Clientset, name string) error {
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

func cleanupManagedPods(ctx context.Context, cs *kubernetes.Clientset, namespace string) error {
	return cleanupPodsBySelector(ctx, cs, namespace, managedPodsSelector)
}

func cleanupPodsBySelector(ctx context.Context, cs *kubernetes.Clientset, namespace, selector string) error {
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

func applyWeights(ctx context.Context, cs *kubernetes.Clientset, namespace, name string, weights evaluator.WeightSet) error {
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
		return fmt.Errorf("update scheduler deployment: %w", err)
	}

	return waitForRollout(ctx, cs, namespace, name)
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

func waitForRollout(ctx context.Context, cs *kubernetes.Clientset, namespace, name string) error {
	ticker := time.NewTicker(1 * time.Second)
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

func estimatedTimeout(workload []evaluator.WorkloadItem) time.Duration {
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
	cs *kubernetes.Clientset,
	opts options,
	runID string,
	item evaluator.WorkloadItem,
	result *evaluator.PodTiming,
) error {
	memBytes := item.MemoryMiB * 1024 * 1024
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", runID, item.Name),
			Namespace: opts.namespace,
			Labels: map[string]string{
				"app":               "evo-sched-eval",
				"evo-sched/run-id":  runID,
				"evo-sched/item":    item.Name,
				"evo-sched/managed": "true",
			},
		},
		Spec: corev1.PodSpec{
			SchedulerName:                 opts.schedulerName,
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: int64Ptr(0),
			Containers: []corev1.Container{
				{
					Name:    "runner",
					Image:   opts.image,
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

	created, err := cs.CoreV1().Pods(opts.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create pod %s: %w", item.Name, err)
	}

	createdAt := created.CreationTimestamp.Time.UTC()
	result.CreatedAt = &createdAt
	return nil
}

func monitorPods(
	ctx context.Context,
	cs *kubernetes.Clientset,
	namespace, selector string,
	pollInterval time.Duration,
	results map[string]*evaluator.PodTiming,
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

func updateTiming(result *evaluator.PodTiming, pod *corev1.Pod) {
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

func emitOutputs(batch evaluator.BatchResult, opts options) error {
	if err := emitJSON(batch, opts.outputPath); err != nil {
		return err
	}

	csvPath := deriveCSVOutputPath(opts)
	if csvPath == "" {
		return nil
	}
	if err := evaluator.WriteComparisonCSV(csvPath, batch.Results); err != nil {
		return err
	}
	log.Printf("wrote evaluator csv to %s", csvPath)
	return nil
}

func emitJSON(batch evaluator.BatchResult, outputPath string) error {
	data, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}
	if outputPath == "" {
		fmt.Println(string(data))
		return nil
	}
	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write result: %w", err)
	}
	log.Printf("wrote evaluator result to %s", outputPath)
	return nil
}

func deriveCSVOutputPath(opts options) string {
	if opts.csvOutputPath != "" {
		return opts.csvOutputPath
	}
	if opts.outputPath == "" {
		return ""
	}
	ext := filepath.Ext(opts.outputPath)
	if ext == "" {
		return opts.outputPath + ".csv"
	}
	return strings.TrimSuffix(opts.outputPath, ext) + ".csv"
}

func runSelector(runID string) string {
	return fmt.Sprintf("%s,evo-sched/run-id=%s", managedPodsSelector, runID)
}

func flattenResults(results map[string]*evaluator.PodTiming) []evaluator.PodTiming {
	out := make([]evaluator.PodTiming, 0, len(results))
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

func isTerminalPhase(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed
}

func int64Ptr(v int64) *int64 {
	return &v
}

func newClientset() (*kubernetes.Clientset, error) {
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
