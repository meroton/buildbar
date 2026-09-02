#!/usr/bin/env python3
"""Synthesize a real Cargo.lock (v4) from a cargo-bazel `crate_universe`
lockfile JSON, for `crate.from_specs()` repos that need BOTH a `lockfile`
(cargo-bazel format, already resolved) and a `cargo_lockfile` (real
Cargo.lock format) but were never spliced from an actual Cargo.toml, so no
real Cargo.lock exists to reuse. Reads the JSON lockfile from stdin, writes
the Cargo.lock to stdout. Driven by regenerate.sh; see that script's doc
comment for when and how to run this.
"""

import json
import sys


def toml_string(s: str) -> str:
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main() -> None:
    data = json.load(sys.stdin)

    packages = []
    for crate_id, crate in sorted(data["crates"].items()):
        name = crate["name"]
        version = crate["version"]
        repo = crate.get("repository")

        dep_ids = set()
        for attrs in (crate.get("common_attrs"), crate.get("build_script_attrs")):
            if not attrs:
                continue
            deps = attrs.get("deps")
            if not deps:
                continue
            for edge in deps.get("common", []):
                dep_ids.add(edge["id"])
            for edges in deps.get("selects", {}).values():
                for edge in edges:
                    dep_ids.add(edge["id"])
        # Drop self-references (a crate's own build script depending on
        # the crate itself isn't a real second package).
        dep_ids.discard(crate_id)

        source = None
        checksum = None
        if repo is not None:
            http = repo.get("Http")
            if http is None:
                raise ValueError(f"{crate_id}: unhandled repository kind {list(repo)}")
            source = "registry+https://github.com/rust-lang/crates.io-index"
            checksum = http["sha256"]

        packages.append(
            {
                "name": name,
                "version": version,
                "source": source,
                "checksum": checksum,
                "dependencies": sorted(dep_ids),
            }
        )

    lines = [
        "# This file is automatically @generated.",
        "# It is not intended for manual editing.",
        "version = 4",
    ]
    for pkg in packages:
        lines.append("")
        lines.append("[[package]]")
        lines.append(f"name = {toml_string(pkg['name'])}")
        lines.append(f"version = {toml_string(pkg['version'])}")
        if pkg["source"]:
            lines.append(f"source = {toml_string(pkg['source'])}")
        if pkg["checksum"]:
            lines.append(f"checksum = {toml_string(pkg['checksum'])}")
        if pkg["dependencies"]:
            lines.append("dependencies = [")
            for dep in pkg["dependencies"]:
                lines.append(f" {toml_string(dep)},")
            lines.append("]")

    sys.stdout.write("\n".join(lines) + "\n")

    print(f"Wrote {len(packages)} packages", file=sys.stderr)


if __name__ == "__main__":
    main()
