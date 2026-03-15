package evaluator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadWorkloadArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workload.json")
	content := `[
	  {"name":"pod-b","arrivalSeconds":3,"cpuMilli":200,"memoryMiB":128,"durationSeconds":10},
	  {"name":"pod-a","arrivalSeconds":1,"cpuMilli":100,"memoryMiB":64,"durationSeconds":5}
	]`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write workload: %v", err)
	}

	items, err := LoadWorkload(path)
	if err != nil {
		t.Fatalf("LoadWorkload() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Name != "pod-a" || items[1].Name != "pod-b" {
		t.Fatalf("unexpected sort order: %+v", items)
	}
}

func TestParseWeightSet(t *testing.T) {
	got, err := ParseWeightSet("1.0, 0.8, 0.5, 1.2")
	if err != nil {
		t.Fatalf("ParseWeightSet() error = %v", err)
	}
	want := WeightSet{FreeCPU: 1.0, FreeMem: 0.8, PodPenalty: 0.5, Packing: 1.2}
	if got != want {
		t.Fatalf("ParseWeightSet() = %+v, want %+v", got, want)
	}
}

func TestSummarizePods(t *testing.T) {
	now := time.Now()
	pods := []PodTiming{
		{
			Name:           "scheduled-fast",
			ScheduledAt:    ptrTime(now),
			FinishedAt:     ptrTime(now.Add(10 * time.Second)),
			PendingSeconds: 1.0,
		},
		{
			Name:           "scheduled-slow",
			ScheduledAt:    ptrTime(now),
			PendingSeconds: 5.0,
		},
		{
			Name: "never-scheduled",
		},
	}

	got := SummarizePods(pods, 2)
	if got.PodCount != 3 || got.ScheduledCount != 2 || got.CompletedCount != 1 || got.UnschedulableCount != 1 {
		t.Fatalf("unexpected summary counts: %+v", got)
	}
	if got.AveragePending != 3.0 {
		t.Fatalf("expected average pending 3.0, got %v", got.AveragePending)
	}
	if got.P95Pending <= 4.0 {
		t.Fatalf("expected p95 pending above 4.0, got %v", got.P95Pending)
	}
	if got.MaxActiveNodes != 2 {
		t.Fatalf("expected max active nodes 2, got %d", got.MaxActiveNodes)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
