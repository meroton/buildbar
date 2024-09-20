#!/bin/bash
# Downloads the proto files from Bazel so that golang rules can be
# generated using Gazelle. Bzlmod is not an option as patches cannot
# be applied externally to the bzlmod repository.
set -eu

cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

if [ -e bazel-dist.zip ]; then
    echo "ERROR: bazel-dist.zip already exists" 1>&2
    exit 1
fi

bazel_version=$(cat "../../.bazelversion")

curl --fail -L -o bazel-dist.zip "https://github.com/bazelbuild/bazel/archive/refs/tags/${bazel_version}.zip"

function extract_file {
    echo "Extracting $1" 1>&2
    output_file="$(basename "$1")"
    unzip -p bazel-dist.zip "$1" > "${output_file}"
}

extract_file "bazel-${bazel_version}/src/main/java/com/google/devtools/build/lib/buildeventstream/proto/build_event_stream.proto"
extract_file "bazel-${bazel_version}/src/main/java/com/google/devtools/build/lib/packages/metrics/package_load_metrics.proto"
extract_file "bazel-${bazel_version}/src/main/protobuf/action_cache.proto"
extract_file "bazel-${bazel_version}/src/main/protobuf/command_line.proto"
extract_file "bazel-${bazel_version}/src/main/protobuf/failure_details.proto"
extract_file "bazel-${bazel_version}/src/main/protobuf/invocation_policy.proto"
extract_file "bazel-${bazel_version}/src/main/protobuf/option_filters.proto"
extract_file "bazel-${bazel_version}/src/main/protobuf/strategy_policy.proto"

rm bazel-dist.zip
