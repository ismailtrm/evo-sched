package evaluator

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

type workloadEnvelope struct {
	Pods []WorkloadItem `json:"pods"`
}

func LoadWorkload(path string) ([]WorkloadItem, error) {
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
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ArrivalSeconds == items[j].ArrivalSeconds {
			return items[i].Name < items[j].Name
		}
		return items[i].ArrivalSeconds < items[j].ArrivalSeconds
	})

	return items, nil
}
