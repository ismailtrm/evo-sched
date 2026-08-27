package main

import (
	"math"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
)

func TestWeightKeyPreservesDistinctFloatValues(t *testing.T) {
	a := evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	b := a
	b.FreeCPU = math.Nextafter(a.FreeCPU, 1)

	if weightKey(a) == weightKey(b) {
		t.Fatalf("distinct weights collapsed to one cache key: a=%+v b=%+v", a, b)
	}
}
