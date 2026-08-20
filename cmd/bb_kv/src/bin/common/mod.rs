//! Shared test-scaffolding for this crate's manual E2E check binaries
//! (`roundtrip_check`, `run_check`). Lives under `src/bin/common/` rather
//! than as a loose `src/bin/common.rs` so Cargo's binary auto-discovery
//! doesn't also try to treat it as its own binary target (it has no
//! `main`). Not part of the public `bb_kv` library: it's only meaningful
//! to bb-kv's own checks, not to consumers of the crate.

use std::fs;
use std::path::{Path, PathBuf};

use bb_kv::error::{Error, IoResultExt};

/// Either a self-cleaning `TempDir` or a path that's been deliberately
/// leaked (via `TempDir::keep`) so it survives a later panic.
pub enum ScratchDir {
    Managed(tempfile::TempDir),
    Kept(PathBuf),
}

impl ScratchDir {
    /// `label` names what this scratch dir is for (e.g. `"run-check-fixture"`),
    /// so `ls $TMPDIR/bb-kv` stays legible with several checks' dirs mixed
    /// in. The tempfile-prefix convention of a trailing separator before
    /// the random suffix is this function's business, not the caller's —
    /// pass a plain label, not `"run-check-fixture-"`.
    pub fn new(label: &str, keep: bool) -> Result<Self, Error> {
        // All of this project's temp dirs live under one bb-kv/ parent, so
        // they're easy to find (`ls $TMPDIR/bb-kv`) instead of scattered
        // directly in the OS temp dir among everyone else's.
        let base = std::env::temp_dir().join("bb-kv");
        fs::create_dir_all(&base).context(|| "creating directory", &base)?;
        let dir = tempfile::Builder::new()
            .prefix(&format!("{label}-"))
            .tempdir_in(&base)
            .context(|| "creating temp dir under", &base)?;
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
