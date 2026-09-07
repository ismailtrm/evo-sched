package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"evok8s-scheduler/pkg/evaluator"
	"evok8s-scheduler/pkg/optimizer"
)

func TestPooledParetoDropsCrossSeedDominatedPoints(t *testing.T) {
	candidates := []finalist{
		candidate("a", 1.0, 4.0, 0.1),
		candidate("b", 1.1, 3.0, 0.2),
		candidate("dominated", 1.2, 3.0, 0.3),
	}
	got := pooledPareto(candidates)
	if len(got) != 2 || got[0].Provenance[0].Name != "a" || got[1].Provenance[0].Name != "b" {
		t.Fatalf("pooledPareto() = %+v", got)
	}
}

func TestDeduplicatePhenotypesRetainsProvenance(t *testing.T) {
	a := candidate("seed-42", 1.0, 3.0, 0.1)
	b := a
	b.Provenance = []provenance{{Seed: 123, Name: "seed-123"}}
	got, err := deduplicatePhenotypes([]finalist{b, a})
	if err != nil {
		t.Fatalf("deduplicatePhenotypes() error = %v", err)
	}
	if len(got) != 1 || len(got[0].Provenance) != 2 || got[0].Provenance[0].Name != "seed-42" {
		t.Fatalf("deduplicatePhenotypes() = %+v", got)
	}
}

func TestDeduplicatePhenotypesRejectsConflictingObjectives(t *testing.T) {
	a := candidate("a", 1.0, 3.0, 0.1)
	b := candidate("b", 1.1, 3.0, 0.1)
	if _, err := deduplicatePhenotypes([]finalist{a, b}); err == nil {
		t.Fatal("deduplicatePhenotypes() error = nil")
	}
}

func TestSelectFinalistsRetainsExtremesThenCrowding(t *testing.T) {
	front := []finalist{
		candidate("pending", 1.0, 5.0, 0.1),
		candidate("middle-low", 1.1, 4.0, 0.2),
		candidate("middle-high", 1.8, 3.0, 0.3),
		candidate("node", 2.0, 2.0, 0.4),
	}
	assignCrowding(front)
	selectFinalists(front, 3)
	selected := make(map[string]bool)
	for _, item := range front {
		selected[item.Provenance[0].Name] = item.Selected
	}
	if !selected["pending"] || !selected["middle-high"] || !selected["node"] || selected["middle-low"] {
		t.Fatalf("selected = %+v", selected)
	}
	if front[0].Crowding != crowdingInfinity || front[3].Crowding != crowdingInfinity {
		t.Fatalf("boundary crowding = %v, %v", front[0].Crowding, front[3].Crowding)
	}
}

func TestWeightsWithinToleranceAcceptsRoundoffButNotScaledVector(t *testing.T) {
	canonical := evaluator.WeightSet{FreeCPU: 0.1, FreeMem: 0.2, Balance: 0.3, Consolidation: 0.4}
	roundoff := canonical
	roundoff.Consolidation += 2e-16
	if !weightsWithinTolerance(canonical, roundoff, 1e-12) {
		t.Fatal("floating-point roundoff was rejected")
	}
	scaled := evaluator.WeightSet{FreeCPU: 1, FreeMem: 2, Balance: 3, Consolidation: 4}
	if weightsWithinTolerance(canonical, scaled, 1e-12) {
		t.Fatal("scaled noncanonical vector was accepted")
	}
}

func TestLoadOfficialManifestVerifiesCompletionAndArtifactChecksums(t *testing.T) {
	root := t.TempDir()
	childPath := filepath.Join(root, "child.json")
	logPath := filepath.Join(root, "run.log")
	if err := os.WriteFile(childPath, []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := officialManifest{ProtocolVersion: expectedProtocolVersion, Class: "official", Status: "completed"}
	manifest.Executions = make([]struct {
		Status              string `json:"status"`
		ChildManifestPath   string `json:"childManifestPath"`
		ChildManifestSHA256 string `json:"childManifestSha256"`
		LauncherLogPath     string `json:"launcherLogPath"`
		LauncherLogSHA256   string `json:"launcherLogSha256"`
	}, 2)
	for i := range manifest.Executions {
		manifest.Executions[i].Status = "completed"
		manifest.Executions[i].ChildManifestPath = "child.json"
		manifest.Executions[i].ChildManifestSHA256 = digestString([]byte("child\n"))
		manifest.Executions[i].LauncherLogPath = "run.log"
		manifest.Executions[i].LauncherLogSHA256 = digestString([]byte("log\n"))
	}
	manifest.FinalRestoration.Status = "completed"
	manifest.FinalRestoration.Snapshot.WorkerCount = 4
	manifest.FinalRestoration.Snapshot.SchedulerRolloutReady = true
	manifest.FinalRestoration.Snapshot.SchedulerIntervalSeconds = 2
	manifest.FinalRestoration.Snapshot.SchedulerWeights = evaluator.WeightSet{FreeCPU: 0.25, FreeMem: 0.25, Balance: 0.25, Consolidation: 0.25}
	protocol := protocolSpec{ProtocolVersion: expectedProtocolVersion}
	protocol.Official.Seeds = []int64{42}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOfficialManifest(manifestPath, protocol); err != nil {
		t.Fatalf("loadOfficialManifest() error = %v", err)
	}
	if err := os.WriteFile(logPath, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOfficialManifest(manifestPath, protocol); err == nil {
		t.Fatal("loadOfficialManifest() accepted a checksum mismatch")
	}
}

func digestString(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func candidate(name string, latency, nodes, freeCPU float64) finalist {
	weights := evaluator.WeightSet{FreeCPU: freeCPU, FreeMem: 1 - freeCPU}
	return finalist{
		Arm: "one-shot-control", Weights: weights,
		Objectives: optimizer.Objectives{
			AverageP95SchedulingDecisionLatency:   latency,
			AverageMaxWorkloadOccupiedWorkerNodes: nodes,
		},
		Provenance:   []provenance{{Seed: 42, Name: name, EvaluationCount: 1}},
		phenotypeKey: weightKey(weights),
	}
}
