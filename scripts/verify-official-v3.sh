#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/evo-sched-v3-verify.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

for command in go git jq rg latexmk qpdf pdfinfo pdffonts cmp; do
  require_command "$command"
done

if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
  echo "official V3 verification requires a clean Git tree" >&2
  exit 1
fi

echo "[1/7] Go tests"
(
  cd "$repo_root/scheduler"
  go test -count=1 ./...
)

echo "[2/7] Go vet"
(
  cd "$repo_root/scheduler"
  go vet ./...
)

echo "[3/7] Regenerate official V3 analyses"
(
  cd "$repo_root/scheduler"

  go run ./cmd/v3-search-summary \
    --one-shot-inputs ../results/2026-09-07-v3-budget-matched-official-search/seed-42/one-shot-control/seed-42/result.json,../results/2026-09-07-v3-budget-matched-official-search/seed-123/one-shot-control/seed-123/result.json,../results/2026-09-07-v3-budget-matched-official-search/seed-999/one-shot-control/seed-999/result.json \
    --two-stage-inputs ../results/2026-09-07-v3-budget-matched-official-search/seed-42/two-stage-resampling/seed-42/result.json,../results/2026-09-07-v3-budget-matched-official-search/seed-123/two-stage-resampling/seed-123/result.json,../results/2026-09-07-v3-budget-matched-official-search/seed-999/two-stage-resampling/seed-999/result.json \
    --baselines ../experiments/weights-v2.json \
    --protocol ../experiments/protocol-v3-budget-matched-resampling.json \
    --manifest ../results/2026-09-07-v3-budget-matched-official-search/manifest.json \
    --output "$tmp_dir/search-summary.json" \
    --csv-output "$tmp_dir/pooled-pareto.csv" \
    --weights-output "$tmp_dir/weights-v3-validation-finalists.json"

  go run ./cmd/v3-validation-summary \
    --inputs ../results/2026-09-07-v3-shared-validation/cpu-tight-train.json,../results/2026-09-07-v3-shared-validation/mem-tight-train.json,../results/2026-09-07-v3-shared-validation/fragmenting-mix-train.json \
    --protocol ../experiments/protocol-v3-budget-matched-resampling.json \
    --manifest ../results/2026-09-07-v3-shared-validation/manifest.json \
    --search-summary ../results/2026-09-07-v3-budget-matched-official-search/analysis/search-summary.json \
    --weights ../experiments/weights-v3-validation-finalists.json \
    --output "$tmp_dir/validation-summary.json" \
    --csv-output "$tmp_dir/validation-summary.csv"

  go run ./cmd/v3-holdout-summary \
    --input ../results/2026-09-08-v3-survivor-holdout/mixed-burst-holdout.json \
    --protocol ../experiments/protocol-v3-survivor-holdout.json \
    --manifest ../results/2026-09-08-v3-survivor-holdout/manifest.json \
    --shared-summary ../results/2026-09-07-v3-shared-validation/analysis/validation-summary.json \
    --weights ../experiments/weights-v3-holdout-survivors.json \
    --output "$tmp_dir/holdout-summary.json" \
    --csv-output "$tmp_dir/holdout-summary.csv"
)

echo "[4/7] Compare regenerated V3 analyses"
normalize_summary() {
  jq -S 'del(.generatedAt, .generator)' "$1"
}

normalize_summary "$repo_root/results/2026-09-07-v3-budget-matched-official-search/analysis/search-summary.json" >"$tmp_dir/search-committed.json"
normalize_summary "$tmp_dir/search-summary.json" >"$tmp_dir/search-regenerated.json"
cmp "$tmp_dir/search-committed.json" "$tmp_dir/search-regenerated.json"
cmp "$repo_root/results/2026-09-07-v3-budget-matched-official-search/analysis/pooled-pareto.csv" "$tmp_dir/pooled-pareto.csv"
cmp "$repo_root/experiments/weights-v3-validation-finalists.json" "$tmp_dir/weights-v3-validation-finalists.json"

normalize_summary "$repo_root/results/2026-09-07-v3-shared-validation/analysis/validation-summary.json" >"$tmp_dir/validation-committed.json"
normalize_summary "$tmp_dir/validation-summary.json" >"$tmp_dir/validation-regenerated.json"
cmp "$tmp_dir/validation-committed.json" "$tmp_dir/validation-regenerated.json"
cmp "$repo_root/results/2026-09-07-v3-shared-validation/analysis/validation-summary.csv" "$tmp_dir/validation-summary.csv"

normalize_summary "$repo_root/results/2026-09-08-v3-survivor-holdout/analysis/holdout-summary.json" >"$tmp_dir/holdout-committed.json"
normalize_summary "$tmp_dir/holdout-summary.json" >"$tmp_dir/holdout-regenerated.json"
cmp "$tmp_dir/holdout-committed.json" "$tmp_dir/holdout-regenerated.json"
cmp "$repo_root/results/2026-09-08-v3-survivor-holdout/analysis/holdout-summary.csv" "$tmp_dir/holdout-summary.csv"

echo "[5/7] Verify protocol identities and evidence audit"
audit="$repo_root/paper/evidence-audit-v3.json"
jq -e '
  .status == "closed" and
  .decision.submissionEvidenceReady == true and
  .decision.paperSubmissionBlocked == false and
  (.verifiedClaims | length) == 8
' "$audit" >/dev/null

while IFS=$'\t' read -r tag expected; do
  actual="$(git -C "$repo_root" rev-parse "${tag}^{commit}")"
  test "$actual" = "$expected"
done < <(jq -r '.officialEvidenceSources[] | select(.protocolTag != null) | [.protocolTag, .protocolTagCommit] | @tsv' "$audit")

paper_commit="$(jq -r '.auditedPaper.gitCommit' "$audit")"
git -C "$repo_root" diff --quiet "$paper_commit" -- paper/main.tex paper/figures

jq -e '
  .arms[0].falseParetoDiscoveries == 2 and
  .arms[1].falseParetoDiscoveries == 1 and
  .acceptance.pods == 6480 and
  .acceptance.unschedulable == 0
' "$repo_root/results/2026-09-07-v3-shared-validation/analysis/validation-summary.json" >/dev/null
jq -e '
  .survivorOutcome.remainedNondominated == 1 and
  .survivorOutcome.frozenSurvivors == 3 and
  .acceptance.pods == 1440 and
  .acceptance.unschedulable == 0
' "$repo_root/results/2026-09-08-v3-survivor-holdout/analysis/holdout-summary.json" >/dev/null

echo "[6/7] Race-check V3 analyzers"
(
  cd "$repo_root/scheduler"
  go test -race -count=1 ./cmd/v3-search-summary ./cmd/v3-validation-summary ./cmd/v3-holdout-summary
)

echo "[7/7] Build and inspect paper"
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

echo "Official V3 evidence, code, and paper verification passed."
