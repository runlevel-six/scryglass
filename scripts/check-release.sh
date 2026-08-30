#!/usr/bin/env bash
#
# Assert the version pins in the published manifests agree — with each other
# always, and with the tag being cut when there is one.
#
# This exists because they did not. Every committed manifest pinned v0.1.0
# across five milestones, so anyone deploying from this repository got an image
# from before the tile wall existed. Nothing failed, nothing warned; the pins
# were simply never part of releasing.
#
# The discipline it enforces: bump the pins to the version you are about to
# tag, commit that, then tag. A release whose manifests name the previous
# release is a release that deploys the previous release.

set -euo pipefail

fail() { printf '%s\n' "$*" >&2; exit 1; }

# Only the published tree. overlays/local is gitignored and is nobody's
# business but its owner's.
mapfile -t files < <(
  find deploy -name '*.yaml' -not -path 'deploy/*/overlays/local/*' | sort
)

[ ${#files[@]} -gt 0 ] || fail "no manifests found under deploy/"

# Every place a version can hide: kustomize's newTag, and the tag written into
# an image reference. Both exist deliberately — the raw YAML is meant to be
# deployable on its own and to say what it deploys — which is exactly why they
# can disagree.
#
# A bare newTag says nothing about which image it belongs to, and this tree
# also deploys an identity provider with a version of its own. So the newTag
# is only read from the line following this project's image name.
image=ghcr.io/runlevel-six/scryglass

pins=$(
  {
    grep -hA1 -- "name: ${image}\$" "${files[@]}" | grep -oE 'newTag:[[:space:]]*\S+' | awk '{print $2}'
    grep -hoE "image:[[:space:]]*${image}:\S+" "${files[@]}" | sed 's/.*://'
  } | sort -u
)

[ -n "$pins" ] || fail "no version pins found in the manifests; this check is not checking anything"

if [ "$(printf '%s\n' "$pins" | wc -l)" -ne 1 ]; then
  printf 'manifests disagree about the version:\n' >&2
  printf '%s\n' "$pins" | sed 's/^/  /' >&2
  printf '\nfound in:\n' >&2
  grep -nE 'newTag:|image:\s*ghcr\.io/runlevel-six/scryglass:' "${files[@]}" | sed 's/^/  /' >&2
  exit 1
fi

pinned=$pins

case "$pinned" in
  latest|edge|dev|*-SNAPSHOT)
    fail "manifests pin a moving tag ($pinned); an apply that changes no spec triggers no rollout"
    ;;
esac

printf 'manifests agree: %s\n' "$pinned"

# On a tag, the pins must be that tag. Elsewhere, agreeing with each other is
# all that can be asked — the pins name the last release, which is correct
# between releases.
tag=${1:-}
if [ -z "$tag" ]; then
  printf 'no tag given; consistency only\n'
  exit 0
fi

[ "$pinned" = "$tag" ] || fail "releasing $tag but the manifests pin $pinned; bump the pins, commit, then tag"

printf 'manifests match the tag being released\n'
