#!/bin/sh -e

# The stamped artifacts should only be built under the CI post submit workflow.
if test "${GITHUB_ACTIONS}" = "true"; then
  echo "BUILD_SCM_REVISION $(git rev-parse --short HEAD)"
  echo "BUILD_SCM_TIMESTAMP $(TZ=UTC date --date "@$(git show -s --format=%ct HEAD)" +%Y%m%dT%H%M%SZ)"
fi
