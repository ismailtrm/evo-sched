#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/evo-sched-verify.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

for command in go git jq rg latexmk qpdf pdfinfo pdffonts; do
  require_command "$command"
done

echo "[1/6] Go tests"
(
  cd "$repo_root/scheduler"
  go test -count=1 ./...
)

echo "[2/6] Go vet"
(
  cd "$repo_root/scheduler"
  go vet ./...
)

echo "[3/6] Regenerate official analyses"
(
  cd "$repo_root/scheduler"

  go run ./cmd/baseline-summary \
    --inputs ../results/2026-08-28-v2-official-baselines/cpu-tight-custom.json,../results/2026-08-28-v2-official-baselines/cpu-tight-default.json,../results/2026-08-28-v2-official-baselines/mem-tight-custom.json,../results/2026-08-28-v2-official-baselines/mem-tight-default.json,../results/2026-08-28-v2-official-baselines/fragmenting-mix-custom.json,../results/2026-08-28-v2-official-baselines/fragmenting-mix-default.json \
    --output "$tmp_dir/baseline-summary.json" \
    --csv-output "$tmp_dir/baseline-summary.csv"

  go run ./cmd/optimizer-summary \
    --inputs ../results/2026-08-28-v2-official-optimizer-3seed/seed-42/result.json,../results/2026-08-28-v2-official-optimizer-3seed/seed-123/result.json,../results/2026-08-28-v2-official-optimizer-3seed/seed-999/result.json \
    --output "$tmp_dir/optimizer-summary.json" \
    --csv-output "$tmp_dir/optimizer-summary.csv"

  go run ./cmd/baseline-summary \
    --inputs ../results/2026-08-28-v2-official-finalist-validation/cpu-tight-train.json,../results/2026-08-28-v2-official-finalist-validation/mem-tight-train.json,../results/2026-08-28-v2-official-finalist-validation/fragmenting-mix-train.json \
    --output "$tmp_dir/validation-summary.json" \
    --csv-output "$tmp_dir/validation-summary.csv"

  go run ./cmd/baseline-summary \
    --inputs ../results/2026-08-29-v2-official-holdout-baselines/mixed-burst-holdout.json \
    --output "$tmp_dir/holdout-summary.json" \
    --csv-output "$tmp_dir/holdout-summary.csv"
)

echo "[4/6] Compare regenerated analyses"
# The baseline summary predates CI and Pareto fields added to the summarizer.
# Remove only those additive fields before checking all original values.
jq -S '.' \
  "$repo_root/results/2026-08-28-v2-official-baselines/summary.json" \
  >"$tmp_dir/baseline-committed-normalized.json"
jq -S \
  '.schemaVersion="v1" | del(.repeatedMeanPareto) | walk(if type == "object" then del(.confidence95Lower, .confidence95Upper) else . end)' \
  "$tmp_dir/baseline-summary.json" \
  >"$tmp_dir/baseline-regenerated-normalized.json"
cmp "$tmp_dir/baseline-committed-normalized.json" "$tmp_dir/baseline-regenerated-normalized.json"

# Optimizer output is deterministic except for the analysis-generation timestamp.
jq -S 'del(.generatedAt)' \
  "$repo_root/results/2026-08-28-v2-official-optimizer-3seed/summary.json" \
  >"$tmp_dir/optimizer-committed-normalized.json"
jq -S 'del(.generatedAt)' \
  "$tmp_dir/optimizer-summary.json" \
  >"$tmp_dir/optimizer-regenerated-normalized.json"
cmp "$tmp_dir/optimizer-committed-normalized.json" "$tmp_dir/optimizer-regenerated-normalized.json"
cmp "$tmp_dir/optimizer-summary.csv" \
  "$repo_root/results/2026-08-28-v2-official-optimizer-3seed/summary-pareto.csv"

cmp "$tmp_dir/validation-summary.json" \
  "$repo_root/results/2026-08-28-v2-official-finalist-validation/summary.json"
cmp "$tmp_dir/validation-summary.csv" \
  "$repo_root/results/2026-08-28-v2-official-finalist-validation/summary.csv"
cmp "$tmp_dir/holdout-summary.json" \
  "$repo_root/results/2026-08-29-v2-official-holdout-baselines/summary.json"
cmp "$tmp_dir/holdout-summary.csv" \
  "$repo_root/results/2026-08-29-v2-official-holdout-baselines/summary.csv"

echo "[5/6] Verify evidence audit and paper source"
paper_commit="$(jq -r '.closureAudit.paperCommit' "$repo_root/paper/evidence-audit-v2.json")"
jq -e \
  '.status == "closed" and .overallAssessment.submissionEvidenceReady == true and .closureAudit.resolvedFindingCount == 10' \
  "$repo_root/paper/evidence-audit-v2.json" >/dev/null
git -C "$repo_root" diff --quiet "$paper_commit" -- paper/main.tex paper/figures

echo "[6/6] Build and inspect paper"
(
  cd "$repo_root/paper"
  latexmk -pdf -interaction=nonstopmode -halt-on-error main.tex >/dev/null
  qpdf --check main.pdf >/dev/null 2>&1
  test "$(pdfinfo main.pdf | awk -F: '/^Pages/ {gsub(/[[:space:]]/, "", $2); print $2}')" = "6"
  if rg -n \
    'LaTeX Error|Undefined control sequence|Citation.*undefined|Reference.*undefined|There were undefined|Overfull|Fatal error' \
    main.log; then
    echo "paper log contains blocking diagnostics" >&2
    exit 1
  fi
  if pdffonts main.pdf | tail -n +3 | rg -v 'Type 1' >/dev/null; then
    echo "paper contains a non-Type-1 font" >&2
    exit 1
  fi
)

echo "Official V2 evidence, code, and paper verification passed."
