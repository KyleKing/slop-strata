#!/bin/bash
# setup.sh WORK: lay out a work directory outside this repository for the
# mining and replay scripts. It copies the scripts and the hand verdicts,
# links the three data files, and clones every repository of repos.tsv at
# the commit the dataset was mined at. A repository with a fourth column goes
# to WORK/<column>-repos: it was read for labels and never mined.
set -euo pipefail
work=${1:?usage: setup.sh WORK}
here=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$work/scripts" "$work/data" "$work/repos" "$work/logs"
cp "$here"/scripts/*.py "$work/scripts/"
cp "$here/labels/verdicts.tsv" "$work/data/verdicts.tsv"
for f in events.jsonl rejected.jsonl controls.jsonl; do
  ln -sf "$here/$f" "$work/$f"
done
while IFS=$'\t' read -r name url sha set; do
  # A fourth column names a set read for labels only, which is cloned beside
  # the mined repositories so the scripts that walk repos/ do not read it.
  dir="$work/repos"
  [ -z "$set" ] || dir="$work/$set-repos"
  mkdir -p "$dir"
  [ -d "$dir/$name" ] || git clone -q "$url" "$dir/$name"
  git -C "$dir/$name" checkout -q --detach "$sha"
  echo "$name at $sha"
done <"$here/repos.tsv"
