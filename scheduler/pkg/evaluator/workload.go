package evaluator

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
)

type workloadEnvelope struct {
	Pods []WorkloadItem `json:"pods"`
}

func LoadWorkload(path string) ([]WorkloadItem, error) {
	return LoadWorkloadWithBase(path, nil)
}

func LoadWorkloadWithBase(path string, base *WorkloadBase) ([]WorkloadItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workload: %w", err)
	}

	var items []WorkloadItem
	if err := json.Unmarshal(data, &items); err != nil {
		var env workloadEnvelope
		if err2 := json.Unmarshal(data, &env); err2 != nil {
			return nil, fmt.Errorf("parse workload: %w", err)
		}
		items = env.Pods
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("workload is empty")
	}

	seen := make(map[string]struct{}, len(items))
	for i := range items {
		item := &items[i]
		if item.Name == "" {
			return nil, fmt.Errorf("workload item %d has empty name", i)
		}
		if _, ok := seen[item.Name]; ok {
			return nil, fmt.Errorf("duplicate workload item name %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		if item.ArrivalSeconds < 0 {
			return nil, fmt.Errorf("workload item %q has negative arrivalSeconds", item.Name)
		}
		if item.CPUMilli < 0 || item.MemoryMiB < 0 || item.DurationSeconds < 0 {
			return nil, fmt.Errorf("workload item %q has negative resource or duration values", item.Name)
		}
		if item.CPUFraction < 0 || item.MemoryFraction < 0 {
			return nil, fmt.Errorf("workload item %q has negative fraction values", item.Name)
		}
		if item.CPUFraction > 1 || item.MemoryFraction > 1 {
			return nil, fmt.Errorf("workload item %q has fraction values above 1.0", item.Name)
		}
		if item.CPUMilli > 0 && item.CPUFraction > 0 {
			return nil, fmt.Errorf("workload item %q mixes cpuMilli and cpuFraction", item.Name)
		}
		if item.MemoryMiB > 0 && item.MemoryFraction > 0 {
			return nil, fmt.Errorf("workload item %q mixes memoryMiB and memoryFraction", item.Name)
		}
		if item.CPUFraction > 0 || item.MemoryFraction > 0 {
			if base == nil {
				return nil, fmt.Errorf("workload item %q requires workload base for fractional resources", item.Name)
			}
			if base.WorkerCPUMilli <= 0 || base.WorkerMemoryMiB <= 0 {
				return nil, fmt.Errorf("workload base must have positive worker cpu and memory values")
			}
			if item.CPUMilli == 0 && item.CPUFraction > 0 {
				item.CPUMilli = fractionToMilli(base.WorkerCPUMilli, item.CPUFraction)
			}
			if item.MemoryMiB == 0 && item.MemoryFraction > 0 {
				item.MemoryMiB = fractionToMiB(base.WorkerMemoryMiB, item.MemoryFraction)
			}
		}
		if item.CPUMilli == 0 && item.CPUFraction == 0 {
			return nil, fmt.Errorf("workload item %q has no cpu request", item.Name)
		}
		if item.MemoryMiB == 0 && item.MemoryFraction == 0 {
			return nil, fmt.Errorf("workload item %q has no memory request", item.Name)
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ArrivalSeconds == items[j].ArrivalSeconds {
			return items[i].Name < items[j].Name
		}
		return items[i].ArrivalSeconds < items[j].ArrivalSeconds
	})

	return items, nil
}

func fractionToMilli(total int64, fraction float64) int64 {
	return int64(math.Ceil(float64(total) * fraction))
}

func fractionToMiB(total int64, fraction float64) int64 {
	return int64(math.Ceil(float64(total) * fraction))
}
