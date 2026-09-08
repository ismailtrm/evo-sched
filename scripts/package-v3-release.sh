#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_tag="${RELEASE_TAG:-v3-evidence-2026-09-08}"
head_sha="$(git -C "$repo_root" rev-parse HEAD)"
short_sha="$(git -C "$repo_root" rev-parse --short=12 HEAD)"
output_dir="${1:-${TMPDIR:-/tmp}/evo-sched-v3-release-${short_sha}}"

protocol_tags=(
  protocol-v2-baselines-2026-08-27
  protocol-v3-official-search-2026-09-07
  protocol-v3-shared-validation-2026-09-07
  protocol-v3-survivor-holdout-2026-09-08
)

official_paths=(
  experiments/protocol-v3-budget-matched-resampling.json
  experiments/protocol-v3-survivor-holdout.json
  experiments/weights-v2.json
  experiments/weights-v3-validation-finalists.json
  experiments/weights-v3-holdout-survivors.json
  paper/evidence-audit-v3.json
  results/2026-08-28-v2-official-baselines
  results/2026-09-07-v3-budget-matched-official-search
  results/2026-09-07-v3-shared-validation
  results/2026-09-08-v3-survivor-holdout
  workloads/v2
)

source_paths=(
  LICENSE
  Makefile
  README.md
  WALKTHROUGH.md
  cfp
  deploy.yaml
  experiments
  kind-config-4workers.yaml
  paper/IEEEtran.cls
  paper/evidence-audit-v3.json
  paper/figures
  paper/main.tex
  presentation
  rbac.yaml
  scheduler
  scripts
  workloads/v2
)

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_annotated_tag_at() {
  local tag="$1"
  local expected="$2"
  local target

  if ! git -C "$repo_root" rev-parse --verify --quiet "refs/tags/$tag" >/dev/null; then
    echo "missing required tag: $tag" >&2
    exit 1
  fi
  if [[ "$(git -C "$repo_root" cat-file -t "refs/tags/$tag")" != "tag" ]]; then
    echo "required tag is not annotated: $tag" >&2
    exit 1
  fi
  target="$(git -C "$repo_root" rev-parse "${tag}^{commit}")"
  if [[ "$target" != "$expected" ]]; then
    echo "tag $tag points to $target, expected $expected" >&2
    exit 1
  fi
}

for command in git jq make qpdf sha256sum stat tar; do
  require_command "$command"
done

if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
  echo "V3 release packaging requires a clean Git worktree" >&2
  exit 1
fi

branch="$(git -C "$repo_root" symbolic-ref --short HEAD)"
if [[ "$branch" != "research/noise-aware-resampling-v3" ]]; then
  echo "V3 release packaging requires branch research/noise-aware-resampling-v3, found $branch" >&2
  exit 1
fi

if [[ -e "$output_dir" ]]; then
  echo "release output already exists: $output_dir" >&2
  exit 1
fi

require_annotated_tag_at "$release_tag" "$head_sha"

protocol_tags_json="$({
  for tag in "${protocol_tags[@]}"; do
    target="$(git -C "$repo_root" rev-parse "${tag}^{commit}")"
    if ! git -C "$repo_root" merge-base --is-ancestor "$target" "$head_sha"; then
      echo "protocol tag is not an ancestor of HEAD: $tag" >&2
      exit 1
    fi
    jq -n --arg tag "$tag" --arg target "$target" \
      '{tag: $tag, targetCommit: $target}'
  done
} | jq -s '.')"

for path in "${official_paths[@]}" "${source_paths[@]}"; do
  if ! git -C "$repo_root" cat-file -e "$head_sha:$path" 2>/dev/null; then
    echo "release path is not committed at HEAD: $path" >&2
    exit 1
  fi
done

echo "[1/6] Verify official V3 evidence, code, and paper"
make -C "$repo_root" verify

mkdir -p "$output_dir"
paper_asset="$output_dir/evo-sched-v3-paper.pdf"
evidence_asset="$output_dir/evo-sched-v3-official-evidence.tar.gz"
source_asset="$output_dir/evo-sched-v3-reproducibility-source.tar.gz"
manifest_asset="$output_dir/evo-sched-v3-release-manifest.json"
checksums_asset="$output_dir/SHA256SUMS"

echo "[2/6] Copy generated paper"
cp "$repo_root/paper/main.pdf" "$paper_asset"

echo "[3/6] Archive committed official evidence"
git -C "$repo_root" archive \
  --format=tar.gz \
  --prefix=evo-sched-v3-official-evidence/ \
  --output="$evidence_asset" \
  "$head_sha" \
  "${official_paths[@]}"

echo "[4/6] Archive committed reproducibility source"
git -C "$repo_root" archive \
  --format=tar.gz \
  --prefix=evo-sched-v3-source/ \
  --output="$source_asset" \
  "$head_sha" \
  "${source_paths[@]}"

paper_size="$(stat -c '%s' "$paper_asset")"
evidence_size="$(stat -c '%s' "$evidence_asset")"
source_size="$(stat -c '%s' "$source_asset")"
paper_sha="$(sha256sum "$paper_asset" | awk '{print $1}')"
evidence_sha="$(sha256sum "$evidence_asset" | awk '{print $1}')"
source_sha="$(sha256sum "$source_asset" | awk '{print $1}')"
paper_source_commit="$(jq -r '.auditedPaper.gitCommit' "$repo_root/paper/evidence-audit-v3.json")"
audit_source_commit="$(git -C "$repo_root" log -1 --format=%H -- paper/evidence-audit-v3.json)"
official_sources="$(jq '.officialEvidenceSources' "$repo_root/paper/evidence-audit-v3.json")"
claim_boundaries="$(jq '.claimBoundaries' "$repo_root/paper/evidence-audit-v3.json")"
limitations="$(jq '.residualLimitations' "$repo_root/paper/evidence-audit-v3.json")"
origin_url="$(git -C "$repo_root" remote get-url origin)"

echo "[5/6] Write release manifest and checksums"
jq -n \
  --arg releaseName "EvoSched V3 Noise-Aware Search Evidence" \
  --arg releaseTag "$release_tag" \
  --arg repository "$origin_url" \
  --arg branch "$branch" \
  --arg headCommit "$head_sha" \
  --arg paperSourceCommit "$paper_source_commit" \
  --arg auditSourceCommit "$audit_source_commit" \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg paperSha "$paper_sha" \
  --arg evidenceSha "$evidence_sha" \
  --arg sourceSha "$source_sha" \
  --argjson paperSize "$paper_size" \
  --argjson evidenceSize "$evidence_size" \
  --argjson sourceSize "$source_size" \
  --argjson protocolTags "$protocol_tags_json" \
  --argjson officialEvidenceSources "$official_sources" \
  --argjson claimBoundaries "$claim_boundaries" \
  --argjson knownLimitations "$limitations" \
  '{
    schemaVersion: "v2",
    releaseName: $releaseName,
    releaseTag: $releaseTag,
    repository: $repository,
    branch: $branch,
    headCommit: $headCommit,
    cleanWorktree: true,
    generatedAt: $generatedAt,
    packagingStatus: "locally-verified",
    publicationStatus: "not-verified-by-packager",
    verification: {
      command: "make verify",
      result: "passed"
    },
    protocolTags: $protocolTags,
    officialEvidenceSources: $officialEvidenceSources,
    paper: {
      sourcePath: "paper/main.tex",
      sourceCommit: $paperSourceCommit,
      auditPath: "paper/evidence-audit-v3.json",
      auditCommit: $auditSourceCommit,
      buildCommand: "make paper",
      generatedAsset: "evo-sched-v3-paper.pdf"
    },
    artifacts: [
      {
        name: "evo-sched-v3-paper.pdf",
        purpose: "Six-page IEEE paper generated from the V3 evidence-closed source",
        sizeBytes: $paperSize,
        sha256: $paperSha,
        sourceCommit: $headCommit
      },
      {
        name: "evo-sched-v3-official-evidence.tar.gz",
        purpose: "Git-tracked V2 baseline reference and official V3 search, shared-validation, holdout, protocol, workload, and audit artifacts",
        sizeBytes: $evidenceSize,
        sha256: $evidenceSha,
        sourceCommit: $headCommit
      },
      {
        name: "evo-sched-v3-reproducibility-source.tar.gz",
        purpose: "Git-tracked scheduler, evaluator, optimizer, manifests, scripts, paper source, and experiment ledger",
        sizeBytes: $sourceSize,
        sha256: $sourceSha,
        sourceCommit: $headCommit
      }
    ],
    claimBoundaries: $claimBoundaries,
    knownLimitations: $knownLimitations
  }' >"$manifest_asset"

(
  cd "$output_dir"
  sha256sum \
    "$(basename "$paper_asset")" \
    "$(basename "$evidence_asset")" \
    "$(basename "$source_asset")" \
    "$(basename "$manifest_asset")" \
    >"$(basename "$checksums_asset")"
)

echo "[6/6] Validate packaged assets"
(
  cd "$output_dir"
  sha256sum -c SHA256SUMS
  jq -e --arg head "$head_sha" --arg tag "$release_tag" '
    .cleanWorktree == true and
    .verification.result == "passed" and
    .headCommit == $head and
    .releaseTag == $tag and
    .packagingStatus == "locally-verified" and
    .publicationStatus == "not-verified-by-packager" and
    (.protocolTags | length) == 4 and
    (.artifacts | length) == 3
  ' evo-sched-v3-release-manifest.json >/dev/null
  qpdf --check evo-sched-v3-paper.pdf >/dev/null 2>&1
  tar -tzf evo-sched-v3-official-evidence.tar.gz >/dev/null
  tar -tzf evo-sched-v3-reproducibility-source.tar.gz >/dev/null
  tar -tzf evo-sched-v3-official-evidence.tar.gz | rg \
    '^evo-sched-v3-official-evidence/results/2026-09-08-v3-survivor-holdout/analysis/holdout-summary.json$' \
    >/dev/null
  tar -tzf evo-sched-v3-reproducibility-source.tar.gz | rg \
    '^evo-sched-v3-source/scripts/verify-official-v3.sh$' \
    >/dev/null
)

echo "V3 release package ready: $output_dir"
