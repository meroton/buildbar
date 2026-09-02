#!/usr/bin/env bash
# Regenerates the two lockfiles patches/rules_jsonnet ships for
# rules_jsonnet's own `crate.from_specs(name = "crates_jsonnet", ...)`
# call (it fetches a `jrsonnet` binary via crate_universe, but its
# MODULE.bazel is missing the `lockfile`/`cargo_lockfile` a non-root
# consumer requires — see the comment above rules_jsonnet's
# single_version_override in //:MODULE.bazel for the full story).
#
# Run this whenever rules_jsonnet's pinned `jrsonnet` version changes —
# either because we bumped our own `rules_jsonnet` bazel_dep to a newer
# release, or (if patching this by hand instead) because its
# crate.spec()/crate.annotation() calls changed. Until then, both
# lockfiles are static, correct, and don't need touching.
#
# What it does: Fetches the given rules_jsonnet release tarball, adds a
# `lockfile` attribute to its from_specs call, and runs it as its own
# root module with CARGO_BAZEL_REPIN=1 to get a real cargo-bazel-format
# lockfile (crate_universe_jsonnet.lock) — genuine crate resolution, not
# fabricated. That JSON is then translated into a real Cargo.lock v4 by
# jrsonnet-synthesize-cargo-lock.py (from_specs has no Cargo.toml of its
# own, so no real Cargo.lock exists to reuse for `cargo_lockfile`).
# Finally, both are re-rendered as patches/rules_jsonnet/*-content.diff.
#
# patches/rules_jsonnet/lockfile.diff (the MODULE.bazel attribute
# additions themselves) is NOT regenerated here — it's hand-authored
# against the pristine MODULE.bazel of the pinned version, and needs
# re-diffing by hand if that pristine content has changed.
#
# Usage: tools/jrsonnet-regenerate-lockfiles.sh <rules_jsonnet-version>
set -eu -o pipefail

test $# = 1 || {
    echo "Usage: $0 <rules_jsonnet-version>" >&2
    exit 1
}
version=$1

repo_root=$(cd "$(dirname "$0")/.." && pwd)
patches_dir="$repo_root/patches/rules_jsonnet"
synthesizer="$repo_root/tools/jrsonnet-synthesize-cargo-lock.py"

scratch=$(mktemp -d --suffix -jrsonnet-regenerate-lockfiles)
trap 'rm -rf "$scratch"' EXIT

echo "Fetching rules_jsonnet $version..." >&2
curl -fsSL "https://github.com/bazelbuild/rules_jsonnet/releases/download/$version/rules_jsonnet-$version.tar.gz" \
    -o "$scratch/rules_jsonnet.tar.gz"
tar xzf "$scratch/rules_jsonnet.tar.gz" -C "$scratch"
checkout="$scratch/rules_jsonnet-$version"

sed -i '/host_tools = "@rust_host_tools_jsonnet",/a\    lockfile = "//:crate_universe_jsonnet.lock",' \
    "$checkout/MODULE.bazel"
touch "$checkout/crate_universe_jsonnet.lock"

# Generic, portable NixOS/exec-env fixes (see //:MODULE.bazel's own
# comments for why these are needed) — not the buildbar repo's own
# .bazelrc, which carries buildbar-specific flags this standalone
# checkout doesn't need.
cat > "$checkout/.bazelrc" <<EOF
build --shell_executable=$(command -v bash)
build --action_env=PATH
build --host_action_env=PATH
EOF

echo "Repinning (this builds a real rust toolchain + jrsonnet's dependency graph, may take a while)..." >&2
(
    cd "$checkout"
    CARGO_BAZEL_REPIN=1 bazel --nohome_rc build @crates_jsonnet//...
    bazel --nohome_rc clean --expunge
)

resolved_lock="$checkout/crate_universe_jsonnet.lock"
cargo_lock="$scratch/crate_universe_jsonnet.cargo.lock"
python3 "$synthesizer" < "$resolved_lock" > "$cargo_lock"

new_file_diff() {
    target_path=$1
    source_file=$2
    printf 'diff --git %s %s\n' "$target_path" "$target_path"
    printf 'new file mode 100644\n'
    printf 'index 0000000..0000000\n'
    printf -- '--- /dev/null\n'
    printf '+++ %s\n' "$target_path"
    printf '@@ -0,0 +1,%s @@\n' "$(wc -l < "$source_file")"
    sed 's/^/+/' "$source_file"
}

new_file_diff crate_universe_jsonnet.lock "$resolved_lock" > "$patches_dir/lockfile-content.diff"
new_file_diff crate_universe_jsonnet.cargo.lock "$cargo_lock" > "$patches_dir/cargo-lock-content.diff"

echo "Wrote $patches_dir/lockfile-content.diff and $patches_dir/cargo-lock-content.diff" >&2
echo "Review the diff, then re-verify with: bazel mod graph" >&2
