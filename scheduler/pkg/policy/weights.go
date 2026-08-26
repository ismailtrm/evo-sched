package policy

import (
	"fmt"
	"math"
)

const Version = "v2"

type WeightSet struct {
	FreeCPU       float64 `json:"freeCPU"`
	FreeMem       float64 `json:"freeMem"`
	Balance       float64 `json:"balance"`
	Consolidation float64 `json:"consolidation"`
}

func Normalize(weights WeightSet) (WeightSet, error) {
	values := []struct {
		name  string
		value float64
	}{
		{name: "FreeCPU", value: weights.FreeCPU},
		{name: "FreeMem", value: weights.FreeMem},
		{name: "Balance", value: weights.Balance},
		{name: "Consolidation", value: weights.Consolidation},
	}

	total := 0.0
	for _, item := range values {
		if math.IsNaN(item.value) || math.IsInf(item.value, 0) {
			return WeightSet{}, fmt.Errorf("score weight %s must be finite", item.name)
		}
		if item.value < 0 {
			return WeightSet{}, fmt.Errorf("score weight %s must be non-negative", item.name)
		}
		total += item.value
	}
	if total <= 0 {
		return WeightSet{}, fmt.Errorf("score weights must have a positive sum")
	}

	return WeightSet{
		FreeCPU:       weights.FreeCPU / total,
		FreeMem:       weights.FreeMem / total,
		Balance:       weights.Balance / total,
		Consolidation: weights.Consolidation / total,
	}, nil
}
