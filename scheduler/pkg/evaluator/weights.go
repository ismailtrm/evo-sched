package evaluator

import (
	"fmt"
	"strconv"
	"strings"
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

	return WeightSet{
		FreeCPU:    vals[0],
		FreeMem:    vals[1],
		PodPenalty: vals[2],
		Packing:    vals[3],
	}, nil
}
