package evaluator

import (
	"path/filepath"
	"testing"
	"time"
)

func TestV2WorkloadsResolveAndMeetSamplingContract(t *testing.T) {
	base := &WorkloadBase{
		WorkerCount:     4,
		WorkerCPUMilli:  4000,
		WorkerMemoryMiB: 8192,
	}
	cases := []struct {
		name string
		path string
	}{
		{name: "cpu tight train", path: "cpu-tight-train.json"},
		{name: "memory tight train", path: "mem-tight-train.json"},
		{name: "fragmenting mix train", path: "fragmenting-mix-train.json"},
		{name: "mixed burst holdout", path: "mixed-burst-holdout.json"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := LoadWorkloadWithBase(filepath.Join("..", "..", "..", "workloads", "v2", tc.path), base)
			if err != nil {
				t.Fatalf("LoadWorkloadWithBase() error = %v", err)
			}
			if len(items) != 24 {
				t.Fatalf("workload pod count = %d, want 24", len(items))
			}

			profile, err := buildWorkloadSamplingProfile(items, 250*time.Millisecond)
			if err != nil {
				t.Fatalf("buildWorkloadSamplingProfile() error = %v", err)
			}
			if profile.MinimumPodDurationSeconds != 10 {
				t.Fatalf("minimum pod duration = %ds, want 10s", profile.MinimumPodDurationSeconds)
			}
			if profile.MinimumDurationToPollIntervalRatio < 40 {
				t.Fatalf("duration to poll interval ratio = %.2f, want at least 40", profile.MinimumDurationToPollIntervalRatio)
			}
		})
	}
}
