use std::path::{Path, PathBuf};

/// Structured, matchable errors for the whole crate. Every fallible lib
/// function returns `Result<T, Error>`; no `anyhow` anywhere.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// Any filesystem operation. `action` names what was being attempted
    /// ("reading", "listing directory entries in", "writing", ...) —
    /// `std::io::Error` already carries *what* went wrong (`.kind()`), the
    /// only thing missing is *where*, so that's the only thing added here.
    #[error("{action} {path}")]
    Io {
        action: String,
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },
    /// Connecting to CAS, in every sense `reapi::connect` can fail:
    /// transport errors, an unsupported endpoint scheme, or a bad CA
    /// certificate. Transparent: `reapi::Error`'s own `Display` already
    /// says exactly what went wrong, so this variant adds nothing of its
    /// own on top.
    #[error(transparent)]
    Connect(#[from] reapi::Error),
    #[error("calling {rpc} (instance {instance_name:?})")]
    Rpc {
        rpc: &'static str,
        instance_name: String,
        #[source]
        source: tonic::Status,
    },
    #[error("decoding {what} blob (digest {hash}/{size_bytes})")]
    Decode {
        what: &'static str,
        hash: String,
        size_bytes: i64,
        #[source]
        source: prost::DecodeError,
    },
    #[error("blob {hash}/{size_bytes}: server reported {status}")]
    BlobStatus {
        hash: String,
        size_bytes: i64,
        status: String,
    },
    #[error("invalid digest {input:?}: expected \"<hex-hash>/<size-bytes>\"")]
    InvalidDigest { input: String },
    /// The fetched `Tree` message is internally inconsistent (missing
    /// root, or a `DirectoryNode` referencing a digest not present in
    /// `Tree.children`) — a server protocol violation, not a decode
    /// failure (the bytes parsed fine as a `Tree`, the *content* is
    /// wrong), so distinct from `Decode`.
    #[error("malformed Tree (digest {hash}/{size_bytes}): {reason}")]
    MalformedTree {
        hash: String,
        size_bytes: i64,
        reason: &'static str,
    },
    /// A per-item response from a batch RPC (`BatchUpdateBlobs`,
    /// `BatchReadBlobs`) omitted a field the request needs to make sense of
    /// it (`digest` or `status`). Neither field's absence is documented as
    /// meaning anything in particular by the spec, so treat it as an error
    /// rather than defaulting — defaulting `status` in particular would
    /// silently read as "OK" (`Status::default().code == 0`), which could
    /// mask a real failure.
    #[error("malformed {rpc} response: {reason}")]
    MalformedResponse {
        rpc: &'static str,
        reason: &'static str,
    },
    /// A declared `--output-file`/`--output-dir` didn't exist on disk after
    /// the command ran.
    #[error("declared output {path:?} was not produced by the command")]
    MissingOutput { path: PathBuf },
    /// A declared output exists but isn't something `run` knows how to
    /// capture yet (a symlink) — see `tree.rs`'s module-level rationale for
    /// why this isn't handled: it's an unbuilt branch of an already-unbuilt
    /// feature, not a hard problem in itself.
    #[error("declared output {path:?}: {reason}")]
    UnsupportedOutput { path: PathBuf, reason: &'static str },
    /// A cached `ActionResult` is internally inconsistent (an `OutputFile`
    /// or `OutputDirectory` missing its digest) — a server/cache protocol
    /// violation, mirroring `MalformedTree` above for the same reason: the
    /// bytes decoded fine, the *content* doesn't hold up its own invariants.
    #[error("malformed ActionResult (action {action_digest}): {reason}")]
    MalformedActionResult {
        action_digest: String,
        reason: &'static str,
    },
    /// The child process named by `run`'s argv[0] failed to start.
    #[error("running {program:?}")]
    Spawn {
        program: String,
        #[source]
        source: std::io::Error,
    },
    /// A path can't be represented as a REAPI path string (e.g. it's
    /// absolute, where REAPI requires paths relative to the working
    /// directory).
    #[error("{path:?}: {reason}")]
    InvalidPath { path: PathBuf, reason: &'static str },
}

/// Attaches an [`Error::Io`] action/path to a `std::io::Result` without
/// losing the ability to `?`-chain it.
///
/// `action` is a closure, not a plain value: a bare argument (`.context("reading", path)`,
/// or worse `.context(format!("creating symlink to {target} at"), path)`) would be
/// evaluated by the caller *before* `context` is even entered — unconditionally,
/// on the success path too. Taking `impl FnOnce() -> S` defers that to
/// `map_err`'s closure, which only runs at all if `self` is `Err`, mirroring
/// `anyhow`'s `.with_context(|| ...)` (as opposed to eager `.context(...)`).
///
/// ```
/// use bb_memoize::error::IoResultExt;
/// use std::path::Path;
///
/// let path = Path::new("/does/not/exist");
/// let err = std::fs::read(path).context(|| "reading", path).unwrap_err();
/// assert_eq!(err.to_string(), "reading /does/not/exist");
/// ```
pub trait IoResultExt<T> {
    fn context<S: Into<String>>(self, action: impl FnOnce() -> S, path: &Path) -> Result<T, Error>;
}

impl<T> IoResultExt<T> for std::io::Result<T> {
    fn context<S: Into<String>>(self, action: impl FnOnce() -> S, path: &Path) -> Result<T, Error> {
        self.map_err(|source| Error::Io {
            action: action().into(),
            path: path.to_owned(),
            source,
        })
    }
}

/// Prints an error and its full `#[source]` chain to stderr, one level per
/// line, e.g.:
///
/// ```text
/// Error: reading /tmp/does-not-exist
/// Caused by: No such file or directory (os error 2)
/// ```
///
/// A level whose message is identical to the one printed just before it is
/// skipped (but still walked past, in case a level past it has something
/// new to say) — some libraries (tonic's transport errors, at least) wrap a
/// lower-level error in a level that adds nothing of its own, so printing
/// every `#[source]` link unconditionally would show the same text twice
/// in a row for no reason.
pub fn report(err: &Error) {
    eprintln!("Error: {err}");
    let mut source = std::error::Error::source(err);
    let mut previous = err.to_string();
    while let Some(e) = source {
        let message = e.to_string();
        if message != previous {
            eprintln!("Caused by: {message}");
        }
        previous = message;
        source = e.source();
    }
}
