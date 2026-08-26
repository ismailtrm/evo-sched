package policy

import (
	"math"
	"testing"
)

func TestNormalizeCanonicalizesPositiveScale(t *testing.T) {
	base, err := Normalize(WeightSet{FreeCPU: 2, FreeMem: 1, Balance: 1})
	if err != nil {
		t.Fatalf("Normalize(base) error = %v", err)
	}
	scaled, err := Normalize(WeightSet{FreeCPU: 20, FreeMem: 10, Balance: 10})
	if err != nil {
		t.Fatalf("Normalize(scaled) error = %v", err)
	}
	assertWeightSetsClose(t, base, scaled)
}

func TestNormalizeRejectsInvalidVectors(t *testing.T) {
	tests := []struct {
		name    string
		weights WeightSet
	}{
		{name: "all zero", weights: WeightSet{}},
		{name: "negative", weights: WeightSet{FreeCPU: 1, Balance: -0.1}},
		{name: "nan", weights: WeightSet{FreeCPU: math.NaN(), FreeMem: 1}},
		{name: "infinity", weights: WeightSet{FreeCPU: math.Inf(1), FreeMem: 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Normalize(tc.weights); err == nil {
				t.Fatal("Normalize() error = nil, want non-nil")
			}
		})
	}
}

func assertWeightSetsClose(t *testing.T, got, want WeightSet) {
	t.Helper()
	gotValues := []float64{got.FreeCPU, got.FreeMem, got.Balance, got.Consolidation}
	wantValues := []float64{want.FreeCPU, want.FreeMem, want.Balance, want.Consolidation}
	for i := range gotValues {
		if math.Abs(gotValues[i]-wantValues[i]) > 1e-12 {
			t.Fatalf("weights[%d] = %.15f, want %.15f; got=%+v want=%+v", i, gotValues[i], wantValues[i], got, want)
		}
	}
}
