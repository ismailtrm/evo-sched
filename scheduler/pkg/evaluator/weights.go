package evaluator

import (
	"fmt"
	"strconv"
	"strings"

	"evok8s-scheduler/pkg/policy"
)

func ParseWeightSet(raw string) (WeightSet, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return WeightSet{}, fmt.Errorf("weights must have 4 comma-separated values")
	}

	vals := make([]float64, 0, 4)
	for _, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return WeightSet{}, fmt.Errorf("parse weights: %w", err)
		}
		vals = append(vals, v)
	}

	return policy.Normalize(WeightSet{
		FreeCPU:       vals[0],
		FreeMem:       vals[1],
		Balance:       vals[2],
		Consolidation: vals[3],
	})
}
