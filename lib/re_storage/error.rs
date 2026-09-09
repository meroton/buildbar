use std::path::{Path, PathBuf};

use bazel_remote_apis::build::bazel::remote::execution::v2::Digest;

/// Structured, matchable errors for this crate's REAPI CAS/ActionCache
/// operations. Every fallible function here returns `Result<T, Error>`; no
/// `anyhow` anywhere.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// Any filesystem operation. `action` names what was being attempted
    /// ("Reading", "Listing directory entries in", "Writing", ...) —
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
    #[error("Calling {rpc} (instance {instance_name:?})")]
    Rpc {
        rpc: &'static str,
        instance_name: String,
        #[source]
        source: tonic::Status,
    },
    /// This crate only ever decodes a `Directory` or `Tree` blob (see
    /// `download.rs`), so a decode failure here is far more often a caller
    /// passing the wrong digest kind (they're the same `<hash>/<size>`
    /// shape, so nothing catches this earlier) than real corruption — the
    /// message hints at that rather than just reporting the raw prost
    /// error.
    #[error(
        "Decoding {what} blob (digest {hash}/{size_bytes}) -- Directory and Tree digests \
         look identical but aren't interchangeable; check you passed the right kind"
    )]
    Decode {
        what: &'static str,
        hash: String,
        size_bytes: i64,
        #[source]
        source: prost::DecodeError,
    },
    #[error("Blob {hash}/{size_bytes}: server reported {status}")]
    BlobStatus {
        hash: String,
        size_bytes: i64,
        status: String,
    },
    #[error("Invalid digest {input:?}: expected \"<hex-hash>/<size-bytes>\"")]
    InvalidDigest { input: String },
    /// The fetched `Tree` message is internally inconsistent (missing
    /// root, or a `DirectoryNode` referencing a digest not present in
    /// `Tree.children`) — a server protocol violation, not a decode
    /// failure (the bytes parsed fine as a `Tree`, the *content* is
    /// wrong), so distinct from `Decode`.
    #[error("Malformed Tree (digest {hash}/{size_bytes}): {reason}")]
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
    #[error("Malformed {rpc} response: {reason}")]
    MalformedResponse {
        rpc: &'static str,
        reason: &'static str,
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
/// `action` is a closure, not a plain value: a bare argument (`.context("Reading", path)`,
/// or worse `.context(format!("Creating symlink to {target} at"), path)`) would be
/// evaluated by the caller *before* `context` is even entered — unconditionally,
/// on the success path too. Taking `impl FnOnce() -> S` defers that to
/// `map_err`'s closure, which only runs at all if `self` is `Err`, mirroring
/// `anyhow`'s `.with_context(|| ...)` (as opposed to eager `.context(...)`).
///
/// ```
/// use re_storage::error::IoResultExt;
/// use std::path::Path;
///
/// let path = Path::new("/does/not/exist");
/// let err = std::fs::read(path).context(|| "Reading", path).unwrap_err();
/// assert_eq!(err.to_string(), "Reading /does/not/exist");
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

/// Attaches an [`Error::Decode`] `what`/digest to a
/// `Result<T, prost::DecodeError>` without losing the ability to
/// `?`-chain it — mirrors [`IoResultExt`] above for `Error::Io`. `what` is
/// always a `&'static str` literal at both call sites, so there's no
/// eager-evaluation cost to defer with a closure the way `IoResultExt`
/// does for `action`.
pub trait DecodeResultExt<T> {
    fn context(self, what: &'static str, digest: &Digest) -> Result<T, Error>;
}

impl<T> DecodeResultExt<T> for Result<T, prost::DecodeError> {
    fn context(self, what: &'static str, digest: &Digest) -> Result<T, Error> {
        self.map_err(|source| Error::Decode {
            what,
            hash: digest.hash.clone(),
            size_bytes: digest.size_bytes,
            source,
        })
    }
}

/// Prints an error and its full `#[source]` chain to stderr, one level per
/// line — see [`write_report`], which does the actual formatting and is
/// what's tested (this is a thin wrapper hardcoding the stderr sink, which
/// a doctest can't easily capture).
pub fn report(err: &Error) {
    write_report(err, std::io::stderr()).expect("writing to stderr");
}

/// Writes an error and its full `#[source]` chain to `out`, one level per
/// line, e.g.:
///
/// ```text
/// Error: Reading /tmp/does-not-exist
/// Caused by: No such file or directory (os error 2)
/// ```
///
/// A level whose message is identical to the one printed just before it is
/// skipped (but still walked past, in case a level past it has something
/// new to say) — some libraries (tonic's transport errors, at least) wrap a
/// lower-level error in a level that adds nothing of its own, so printing
/// every `#[source]` link unconditionally would show the same text twice
/// in a row for no reason.
///
/// ```
/// use re_storage::error::{Error, write_report};
/// use std::path::PathBuf;
///
/// let source = std::io::Error::from_raw_os_error(2); // ENOENT
/// let err = Error::Io {
///     action: "Reading".to_owned(),
///     path: PathBuf::from("/tmp/does-not-exist"),
///     source,
/// };
///
/// let mut out = Vec::new();
/// write_report(&err, &mut out).unwrap();
/// assert_eq!(
///     String::from_utf8(out).unwrap(),
///     "Error: Reading /tmp/does-not-exist\nCaused by: No such file or directory (os error 2)\n",
/// );
/// ```
pub fn write_report(err: &Error, mut out: impl std::io::Write) -> std::io::Result<()> {
    writeln!(out, "Error: {err}")?;
    let mut source = std::error::Error::source(err);
    let mut previous = err.to_string();
    while let Some(e) = source {
        let message = e.to_string();
        if message != previous {
            writeln!(out, "Caused by: {message}")?;
        }
        previous = message;
        source = e.source();
    }
    Ok(())
}
