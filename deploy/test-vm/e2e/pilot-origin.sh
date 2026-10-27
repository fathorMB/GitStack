#!/usr/bin/env bash
# Populate the existing mirror-dest bare repository with the pilot source.
# prepare BARE FIXTURE WORKDIR; tag WORKDIR TAG
set -eu
fail() { printf 'STEP FAIL origin: %s\n' "$1" >&2; exit 1; }
case "${1:-}" in
  prepare)
    [ "$#" -eq 4 ] || fail 'prepare requires bare repository, fixture directory and new workdir'
    bare=$2; fixture=$3; work=$4
    [ -d "$bare" ] && [ -d "$fixture" ] && [ ! -e "$work" ] || fail 'invalid paths'
    git clone -q "$bare" "$work" || fail 'clone'
    mkdir -p "$work/.gitstack/workflows"
    for file in Dockerfile.api Dockerfile.sito server.cjs identity.cjs; do
      [ -f "$fixture/$file" ] || fail "missing $file"
      cp "$fixture/$file" "$work/$file"
    done
    cp "$fixture/workflow.yml" "$work/.gitstack/workflows/pilot.yml"
    git -C "$work" config user.name 'GitStack E2E'
    git -C "$work" config user.email 'e2e@example.invalid'
    git -C "$work" add -A
    git -C "$work" commit -qm 'Pilot fixture' || fail 'commit'
    git -C "$work" push -q origin HEAD:refs/heads/main || fail 'push main'
    git -C "$work" rev-parse HEAD
    echo 'STEP ok origin-prepare'
    ;;
  tag)
    [ "$#" -eq 3 ] || fail 'tag requires workdir and tag'
    work=$2; tag=$3
    case "$tag" in v[0-9]*) ;; *) fail 'invalid tag' ;; esac
    git -C "$work" check-ref-format "refs/tags/$tag" || fail 'invalid tag'
    git -C "$work" tag "$tag" || fail 'create tag'
    git -C "$work" push -q origin "refs/tags/$tag" || fail 'push tag'
    git -C "$work" rev-parse "refs/tags/$tag^{commit}"
    echo 'STEP ok origin-tag'
    ;;
  *) fail 'use prepare or tag' ;;
esac
