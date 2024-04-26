#!/bin/bash
set -eu -o pipefail

while read -r line
do
    # shellcheck disable=SC2001
    # SC2001: See if you can use ${variable//search/replace} instead.
    name=$(echo "$line" | sed 's,^.*:\([^/]*\)_go_proto$,\1,')
    # shellcheck disable=SC2001
    # SC2001: See if you can use ${variable//search/replace} instead.
    build_file=$(echo "$line" | sed 's,^//\(.*\):.*$,\1/BUILD.bazel,')
    echo "Updating $build_file" >&2
    cat << EOF >> "$build_file"
load("@aspect_bazel_lib//lib:write_source_files.bzl", "write_source_file")
filegroup(
    name = "${name}_go_proto_pb_go",
    srcs = [":${name}_go_proto"],
    output_group = "go_generated_srcs",
)
write_source_file(
    name = "${name}_pb_go",
    in_file = ":${name}_go_proto_pb_go",
    out_file = "${name}.pb.go",
)
EOF
done < <(bazel query 'kind(go_proto_library, //proto/...)')
set -x

bazel run //:buildifier_fix
