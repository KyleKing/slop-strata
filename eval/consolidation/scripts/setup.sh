#!/bin/bash
# setup.sh WORK: lay out a work directory outside this repository for the
# mining and replay scripts. It copies the scripts and the hand verdicts,
# links the three data files, and clones every repository of repos.tsv at
# the commit the dataset was mined at.
set -euo pipefail
work=${1:?usage: setup.sh WORK}
here=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$work/scripts" "$work/data" "$work/repos" "$work/logs"
cp "$here"/scripts/*.py "$work/scripts/"
cp "$here/labels/verdicts.tsv" "$work/data/verdicts.tsv"
for f in events.jsonl rejected.jsonl controls.jsonl; do
  ln -sf "$here/$f" "$work/$f"
done
while IFS=$'\t' read -r name url sha; do
  [ -d "$work/repos/$name" ] || git clone -q "$url" "$work/repos/$name"
  git -C "$work/repos/$name" checkout -q --detach "$sha"
  echo "$name at $sha"
done <"$here/repos.tsv"
