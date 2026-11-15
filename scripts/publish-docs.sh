#!/usr/bin/env bash
# Publishes the built docsite to the root of the branch gh-pages.
#
#   scripts/publish-docs.sh SITE_DIR REMOTE_URL [MESSAGE]
#
# The branch also holds the review sites of pull requests in the folders pr-<number>/, which the
# workflow "Architecture review" writes and deletes. This script replaces everything at the root
# except those folders, .nojekyll and .git, and fails if the push would change a pr-* folder.
# The workflow of the review pushes to the same branch, so on a rejected push this script starts
# again with a fresh clone.
set -euo pipefail

site=${1:?usage: publish-docs.sh SITE_DIR REMOTE_URL [MESSAGE]}
remote=${2:?usage: publish-docs.sh SITE_DIR REMOTE_URL [MESSAGE]}
message=${3:-Publish the docsite}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

[ -f "$site/index.html" ] || { echo "::error::$site/index.html is missing: build the site first"; exit 1; }
if compgen -G "$site/pr-*" > /dev/null; then
  echo "::error::The site must not contain a folder pr-*: these belong to the review sites."
  exit 1
fi

for attempt in 1 2 3 4 5; do
  rm -rf "$work/pages"
  if git ls-remote --exit-code --heads "$remote" gh-pages > /dev/null 2>&1; then
    git clone --quiet --depth 1 --branch gh-pages "$remote" "$work/pages"
  else
    git init --quiet --initial-branch gh-pages "$work/pages"
    git -C "$work/pages" remote add origin "$remote"
  fi

  # Remove the old site: every entry of the root except the review sites, .nojekyll and .git.
  find "$work/pages" -mindepth 1 -maxdepth 1 ! -name '.git' ! -name '.nojekyll' ! -name 'pr-*' -exec rm -rf {} +
  cp -R "$site/." "$work/pages/"
  # Serve the files as they are, without Jekyll.
  touch "$work/pages/.nojekyll"

  git -C "$work/pages" add -A
  if git -C "$work/pages" diff --cached --quiet; then
    echo "The site did not change."
    exit 0
  fi
  if git -C "$work/pages" diff --cached --name-only | grep -q '^pr-'; then
    echo "::error::The publish would change a review site (pr-*). Stopping."
    exit 1
  fi
  git -C "$work/pages" -c user.name="github-actions[bot]" \
    -c user.email="41898299+github-actions[bot]@users.noreply.github.com" \
    commit --quiet -m "$message"
  if git -C "$work/pages" push --quiet origin gh-pages; then
    echo "Published the site."
    exit 0
  fi
  echo "Push rejected, attempt $attempt; trying again."
  sleep $((attempt * 5))
done
echo "::error::Could not push the site to gh-pages."
exit 1
