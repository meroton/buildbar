use std::path::PathBuf;

/// Structured, matchable errors for `re-memoize`'s own caching/run
/// pipeline. General REAPI CAS/ActionCache failures — connecting, RPCs,
/// malformed server responses, filesystem I/O, invalid digests/paths —
/// come from [`re_storage::error::Error`] and are wrapped transparently
/// below; this enum only adds what's specific to `run`'s own
/// action-building and output-capture/restore logic.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    /// Any general REAPI CAS/ActionCache failure — see
    /// [`re_storage::error::Error`] for what these cover. Transparent: its
    /// own `Display` already says exactly what went wrong.
    #[error(transparent)]
    Storage(#[from] re_storage::error::Error),
    /// A declared `--output-file`/`--output-dir` didn't exist on disk after
    /// the command ran.
    #[error("Declared output {path:?} was not produced by the command")]
    MissingOutput { path: PathBuf },
    /// A declared output exists but isn't something `run` knows how to
    /// capture yet (a symlink) — see `tree.rs`'s module-level rationale in
    /// `re_storage` for why this isn't handled: it's an unbuilt branch of
    /// an already-unbuilt feature, not a hard problem in itself.
    #[error("Declared output {path:?}: {reason}")]
    UnsupportedOutput { path: PathBuf, reason: &'static str },
    /// A cached `ActionResult` is internally inconsistent (an `OutputFile`
    /// or `OutputDirectory` missing its digest) — a server/cache protocol
    /// violation, mirroring `re_storage::error::Error::MalformedTree` for
    /// the same reason: the bytes decoded fine, the *content* doesn't hold
    /// up its own invariants.
    #[error("Malformed ActionResult (action {action_digest}): {reason}")]
    MalformedActionResult {
        action_digest: String,
        reason: &'static str,
    },
    /// The child process named by `run`'s argv[0] failed to start.
    #[error("Running {program:?}")]
    Spawn {
        program: String,
        #[source]
        source: std::io::Error,
    },
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
/// Error: Running "does-not-exist"
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
/// use re_memoize::error::{Error, write_report};
///
/// let source = std::io::Error::from_raw_os_error(2); // ENOENT
/// let err = Error::Spawn {
///     program: "does-not-exist".to_owned(),
///     source,
/// };
///
/// let mut out = Vec::new();
/// write_report(&err, &mut out).unwrap();
/// assert_eq!(
///     String::from_utf8(out).unwrap(),
///     "Error: Running \"does-not-exist\"\nCaused by: No such file or directory (os error 2)\n",
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
