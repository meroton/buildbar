//! Shared scaffolding for this repo's manual E2E check binaries
//! (`cmd/integration_test`'s `run_check`, `roundtrip_check`) — code that's
//! about testing but lives outside `cargo test`/`bazel test`, so it can't
//! sit in a `#[cfg(test)]` module. Not meant for general use: kept under
//! `//lib` (per `CONTRIBUTING.rst`'s "rust libraries" convention) but with
//! restricted Bazel visibility, since Rust/Bazel has no equivalent to Go's
//! `internal/` import restriction to lean on instead.

use std::fs;
use std::path::{Path, PathBuf};

use re_storage::error::{Error, IoResultExt};

/// Either a self-cleaning `TempDir` or a path that's been deliberately
/// leaked (via `TempDir::keep`) so it survives a later panic.
pub enum ScratchDir {
    Managed(tempfile::TempDir),
    Kept(PathBuf),
}

impl ScratchDir {
    /// `namespace` groups a check binary's own scratch dirs under one
    /// parent (e.g. `"re-memoize"`, `"re-directory"`), so
    /// `ls $TMPDIR/<namespace>` stays legible with several checks' dirs
    /// mixed in, instead of scattered directly in the OS temp dir among
    /// everyone else's. `label` names what this particular scratch dir is
    /// for (e.g. `"run-check-fixture"`). The tempfile-prefix convention of
    /// a trailing separator before the random suffix is this function's
    /// business, not the caller's — pass a plain label, not
    /// `"run-check-fixture-"`.
    pub fn new(namespace: &str, label: &str, keep: bool) -> Result<Self, Error> {
        let base = std::env::temp_dir().join(namespace);
        fs::create_dir_all(&base).context(|| "Creating directory", &base)?;
        let dir = tempfile::Builder::new()
            .prefix(&format!("{label}-"))
            .tempdir_in(&base)
            .context(|| "Creating temp dir under", &base)?;
        Ok(if keep {
            Self::Kept(dir.keep())
        } else {
            Self::Managed(dir)
        })
    }

    pub fn path(&self) -> &Path {
        match self {
            Self::Managed(dir) => dir.path(),
            Self::Kept(path) => path,
        }
    }
}
