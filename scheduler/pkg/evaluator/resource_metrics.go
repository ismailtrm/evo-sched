package evaluator

import (
	"fmt"
	"math"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
)

const (
	MinimumDurationToPollIntervalRatio    = 2.0
	WorkloadRequestedResourceMetricSource = "non-terminal-bound-evaluator-pod-requests-over-run-start-worker-allocatable"
)

func buildWorkloadSamplingProfile(workload []WorkloadItem, pollInterval time.Duration) (WorkloadSamplingProfile, error) {
	if len(workload) == 0 {
		return WorkloadSamplingProfile{}, fmt.Errorf("workload has no pods")
	}
	if pollInterval <= 0 {
		return WorkloadSamplingProfile{}, fmt.Errorf("poll interval must be positive, got %s", pollInterval)
	}

	profile := WorkloadSamplingProfile{
		MinimumPodDurationSeconds: workload[0].DurationSeconds,
		MaximumPodDurationSeconds: workload[0].DurationSeconds,
	}
	for _, item := range workload[1:] {
		if item.DurationSeconds < profile.MinimumPodDurationSeconds {
			profile.MinimumPodDurationSeconds = item.DurationSeconds
		}
		if item.DurationSeconds > profile.MaximumPodDurationSeconds {
			profile.MaximumPodDurationSeconds = item.DurationSeconds
		}
	}

	profile.MinimumDurationToPollIntervalRatio = float64(profile.MinimumPodDurationSeconds) / pollInterval.Seconds()
	if profile.MinimumDurationToPollIntervalRatio < MinimumDurationToPollIntervalRatio {
		return WorkloadSamplingProfile{}, fmt.Errorf(
			"minimum pod duration %ds is only %.3fx the poll interval %s; require at least %.1fx for occupancy sampling",
			profile.MinimumPodDurationSeconds,
			profile.MinimumDurationToPollIntervalRatio,
			pollInterval,
			MinimumDurationToPollIntervalRatio,
		)
	}

	return profile, nil
}

func buildWorkloadResourceSample(observedAt time.Time, pods []corev1.Pod, workers []corev1.Node) (WorkloadResourceSample, error) {
	if len(workers) == 0 {
		return WorkloadResourceSample{}, fmt.Errorf("worker snapshot is empty")
	}

	sortedWorkers := append([]corev1.Node(nil), workers...)
	sort.Slice(sortedWorkers, func(i, j int) bool { return sortedWorkers[i].Name < sortedWorkers[j].Name })

	sample := WorkloadResourceSample{
		ObservedAt: observedAt.UTC(),
		Workers:    make([]WorkerResourceSample, len(sortedWorkers)),
	}
	workerIndexes := make(map[string]int, len(sortedWorkers))
	var totalAllocCPU int64
	var totalAllocMemory int64
	for i, worker := range sortedWorkers {
		allocCPU := worker.Status.Allocatable.Cpu().MilliValue()
		allocMemory := worker.Status.Allocatable.Memory().Value()
		if allocCPU <= 0 || allocMemory <= 0 {
			return WorkloadResourceSample{}, fmt.Errorf("worker %s has non-positive allocatable resources", worker.Name)
		}
		workerIndexes[worker.Name] = i
		sample.Workers[i] = WorkerResourceSample{
			Name:             worker.Name,
			AllocCPUMilli:    allocCPU,
			AllocMemoryBytes: allocMemory,
		}
		totalAllocCPU += allocCPU
		totalAllocMemory += allocMemory
	}

	for _, pod := range pods {
		if pod.Spec.NodeName == "" || isTerminalPhase(pod.Status.Phase) {
			continue
		}
		index, ok := workerIndexes[pod.Spec.NodeName]
		if !ok {
			return WorkloadResourceSample{}, fmt.Errorf("evaluator pod %s assigned to node %s outside worker snapshot", pod.Name, pod.Spec.NodeName)
		}
		cpuMilli, memoryBytes := workloadPodRequests(&pod)
		worker := &sample.Workers[index]
		worker.WorkloadRequestedCPUMilli += cpuMilli
		worker.WorkloadRequestedMemBytes += memoryBytes
		worker.WorkloadPodCount++
	}

	dominantLoads := make([]float64, 0, len(sample.Workers))
	var totalRequestedCPU int64
	var totalRequestedMemory int64
	var totalImbalance float64
	for i := range sample.Workers {
		worker := &sample.Workers[i]
		worker.CPURequestedUtilization = float64(worker.WorkloadRequestedCPUMilli) / float64(worker.AllocCPUMilli)
		worker.MemoryRequestedUtilization = float64(worker.WorkloadRequestedMemBytes) / float64(worker.AllocMemoryBytes)
		worker.DominantRequestedLoad = math.Max(worker.CPURequestedUtilization, worker.MemoryRequestedUtilization)
		if worker.WorkloadPodCount > 0 {
			sample.WorkloadOccupiedWorkerNodes++
		}
		totalRequestedCPU += worker.WorkloadRequestedCPUMilli
		totalRequestedMemory += worker.WorkloadRequestedMemBytes
		totalImbalance += math.Abs(worker.CPURequestedUtilization - worker.MemoryRequestedUtilization)
		dominantLoads = append(dominantLoads, worker.DominantRequestedLoad)
	}

	sample.WorkloadCPURequestedUtilization = float64(totalRequestedCPU) / float64(totalAllocCPU)
	sample.WorkloadMemoryRequestedUtilization = float64(totalRequestedMemory) / float64(totalAllocMemory)
	sample.WorkloadDominantRequestedLoadVariance = populationVariance(dominantLoads)
	sample.WorkloadCPUToMemoryImbalance = totalImbalance / float64(len(sample.Workers))
	return sample, nil
}

func workloadPodRequests(pod *corev1.Pod) (int64, int64) {
	var cpuMilli int64
	var memoryBytes int64
	for _, container := range pod.Spec.Containers {
		cpuMilli += container.Resources.Requests.Cpu().MilliValue()
		memoryBytes += container.Resources.Requests.Memory().Value()
	}
	if pod.Spec.Overhead != nil {
		cpuMilli += pod.Spec.Overhead.Cpu().MilliValue()
		memoryBytes += pod.Spec.Overhead.Memory().Value()
	}
	return cpuMilli, memoryBytes
}

func populationVariance(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	mean := average(values)
	var total float64
	for _, value := range values {
		delta := value - mean
		total += delta * delta
	}
	return total / float64(len(values))
}

func summarizeResourceSamples(samples []WorkloadResourceSample) ResourceSummary {
	cpu := make([]float64, 0, len(samples))
	memory := make([]float64, 0, len(samples))
	variance := make([]float64, 0, len(samples))
	imbalance := make([]float64, 0, len(samples))
	for _, sample := range samples {
		cpu = append(cpu, sample.WorkloadCPURequestedUtilization)
		memory = append(memory, sample.WorkloadMemoryRequestedUtilization)
		variance = append(variance, sample.WorkloadDominantRequestedLoadVariance)
		imbalance = append(imbalance, sample.WorkloadCPUToMemoryImbalance)
	}
	return ResourceSummary{
		MeanWorkloadCPURequestedUtilization:       average(cpu),
		MeanWorkloadMemoryRequestedUtilization:    average(memory),
		MeanWorkloadDominantRequestedLoadVariance: average(variance),
		MeanWorkloadCPUToMemoryImbalance:          average(imbalance),
	}
}
