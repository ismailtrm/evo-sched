#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_tag="${RELEASE_TAG:-v2-evidence-2026-08-31}"
head_sha="$(git -C "$repo_root" rev-parse HEAD)"
short_sha="$(git -C "$repo_root" rev-parse --short=12 HEAD)"
output_dir="${1:-${TMPDIR:-/tmp}/evo-sched-v2-release-${short_sha}}"

protocol_tags=(
  protocol-v2-baselines-2026-08-27
  protocol-v2-optimizer-2026-08-28
  protocol-v2-finalist-validation-2026-08-28
  protocol-v2-holdout-2026-08-29
)

official_paths=(
  paper/evidence-audit-v2.json
  results/2026-08-28-v2-official-baselines
  results/2026-08-28-v2-official-optimizer-3seed
  results/2026-08-28-v2-official-finalist-validation
  results/2026-08-29-v2-official-holdout-baselines
)

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

for command in git jq make sha256sum stat tar; do
  require_command "$command"
done

if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
  echo "release packaging requires a clean Git worktree" >&2
  exit 1
fi

branch="$(git -C "$repo_root" symbolic-ref --short HEAD)"
if [[ "$branch" != "research/policy-space-v2" ]]; then
  echo "release packaging requires branch research/policy-space-v2, found $branch" >&2
  exit 1
fi

if [[ -e "$output_dir" ]]; then
  echo "release output already exists: $output_dir" >&2
  exit 1
fi

protocol_tags_json="$({
  for tag in "${protocol_tags[@]}"; do
    target="$(git -C "$repo_root" rev-parse "${tag}^{}")"
    if ! git -C "$repo_root" merge-base --is-ancestor "$target" "$head_sha"; then
      echo "protocol tag is not an ancestor of HEAD: $tag" >&2
      exit 1
    fi
    jq -n --arg tag "$tag" --arg target "$target" \
      '{tag: $tag, targetCommit: $target}'
  done
} | jq -s '.')"

echo "[1/5] Verify official V2 evidence and paper"
make -C "$repo_root" verify

mkdir -p "$output_dir"
paper_asset="$output_dir/evo-sched-v2-paper.pdf"
evidence_asset="$output_dir/evo-sched-v2-official-evidence.tar.gz"
manifest_asset="$output_dir/evo-sched-v2-release-manifest.json"
checksums_asset="$output_dir/SHA256SUMS"

echo "[2/5] Copy generated paper"
cp "$repo_root/paper/main.pdf" "$paper_asset"

echo "[3/5] Archive committed official evidence"
git -C "$repo_root" archive \
  --format=tar.gz \
  --prefix=evo-sched-v2-official-evidence/ \
  --output="$evidence_asset" \
  "$head_sha" \
  "${official_paths[@]}"

paper_size="$(stat -c '%s' "$paper_asset")"
evidence_size="$(stat -c '%s' "$evidence_asset")"
paper_sha="$(sha256sum "$paper_asset" | awk '{print $1}')"
evidence_sha="$(sha256sum "$evidence_asset" | awk '{print $1}')"
paper_source_commit="$(jq -r '.closureAudit.paperCommit' "$repo_root/paper/evidence-audit-v2.json")"
official_sources="$(jq '.officialEvidenceSources' "$repo_root/paper/evidence-audit-v2.json")"
limitations="$(jq '.closureAudit.residualLimitations' "$repo_root/paper/evidence-audit-v2.json")"
origin_url="$(git -C "$repo_root" remote get-url origin)"

echo "[4/5] Write release manifest and checksums"
jq -n \
  --arg releaseName "EvoSched V2 Official Evidence" \
  --arg intendedTag "$release_tag" \
  --arg repository "$origin_url" \
  --arg branch "$branch" \
  --arg headCommit "$head_sha" \
  --arg paperSourceCommit "$paper_source_commit" \
  --arg generatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg paperSha "$paper_sha" \
  --arg evidenceSha "$evidence_sha" \
  --argjson paperSize "$paper_size" \
  --argjson evidenceSize "$evidence_size" \
  --argjson protocolTags "$protocol_tags_json" \
  --argjson officialEvidenceSources "$official_sources" \
  --argjson knownLimitations "$limitations" \
  '{
    schemaVersion: "v1",
    releaseName: $releaseName,
    intendedTag: $intendedTag,
    repository: $repository,
    branch: $branch,
    headCommit: $headCommit,
    cleanWorktree: true,
    generatedAt: $generatedAt,
    verification: {
      command: "make verify",
      result: "passed"
    },
    protocolTags: $protocolTags,
    officialEvidenceSources: $officialEvidenceSources,
    paper: {
      sourcePath: "paper/main.tex",
      sourceCommit: $paperSourceCommit,
      buildCommand: "make paper",
      generatedAsset: "evo-sched-v2-paper.pdf"
    },
    artifacts: [
      {
        name: "evo-sched-v2-paper.pdf",
        purpose: "Six-page IEEE paper generated from the evidence-closed source",
        sizeBytes: $paperSize,
        sha256: $paperSha
      },
      {
        name: "evo-sched-v2-official-evidence.tar.gz",
        purpose: "Git-tracked official V2 raw results, analyses, manifests, and evidence audit",
        sizeBytes: $evidenceSize,
        sha256: $evidenceSha
      }
    ],
    knownLimitations: $knownLimitations
  }' >"$manifest_asset"

(
  cd "$output_dir"
  sha256sum \
    "$(basename "$paper_asset")" \
    "$(basename "$evidence_asset")" \
    "$(basename "$manifest_asset")" \
    >"$(basename "$checksums_asset")"
)

echo "[5/5] Validate packaged assets"
(
  cd "$output_dir"
  sha256sum -c SHA256SUMS
  jq -e \
    '.cleanWorktree == true and .verification.result == "passed" and (.protocolTags | length) == 4' \
    evo-sched-v2-release-manifest.json >/dev/null
  tar -tzf evo-sched-v2-official-evidence.tar.gz >/dev/null
)

echo "Release package ready: $output_dir"
du -h "$output_dir"/*
