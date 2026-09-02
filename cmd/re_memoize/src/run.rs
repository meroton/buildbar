use std::collections::HashMap;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::Stdio;

use bazel_remote_apis::build::bazel::remote::execution::v2::{
    Action, ActionResult, Command, Digest, OutputDirectory, OutputFile,
};
use reapi::Blob;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::process::Command as ChildCommand;

use crate::client::RemoteClient;
use crate::error::{Error, IoResultExt};
use crate::tree::{format_digest, reapi_path};
use crate::{download, upload};

// TODO(platform): `Action.platform` (REAPI v2.2+; `Command.platform` is
// deprecated) could be threaded through from a `--platform KEY=VALUE`
// flag, for servers that route/bucket ActionCache entries by platform
// properties. Left out of v1 — everything here runs on whatever machine
// invokes `re-memoize run`, so there's been no need for it yet.

/// Everything `run_cached` needs beyond the `RemoteClient` itself.
pub struct RunOptions {
    /// Root `Directory` digest, as printed by `digest`/`upload` — becomes
    /// `Action.input_root_digest` directly, no resolution needed (see
    /// `upload::UploadedTree`'s doc comment for why that digest form was
    /// chosen). `run` trusts the local filesystem already reflects it; it
    /// never fetches or verifies the input tree itself — that's the whole
    /// point, skipping what Bazel already placed there.
    pub input_root_digest: Digest,
    pub argv: Vec<String>,
    /// Paths (relative to the working directory `run` was invoked from)
    /// the command is expected to produce as plain files. Captured and
    /// uploaded on a cache miss, restored verbatim on a cache hit.
    pub output_files: Vec<PathBuf>,
    /// Same as `output_files`, but each path is a directory captured as a
    /// subtree (via `upload::upload_directory`) instead of a single blob.
    pub output_dirs: Vec<PathBuf>,
    /// Skip both the cache lookup and the write-back: always run, never
    /// persist a result.
    pub no_cache: bool,
}

/// Builds the `Command` and `Action` messages `opts` describes, digested
/// and encoded, ready to either upload (`run_cached`) or just inspect the
/// digest of (`action_digest`). Pure/offline — no network access, same as
/// `tree::build_directory`/`tree_digest`.
///
/// `Command.environment_variables` is always empty, and that's not a gap to
/// fill — it's the same tradeoff the README already states for the command
/// line itself: the child's environment is exactly one more channel of
/// "arbitrary input from external sources" that this lightweight tool
/// can't fully account for (ambient files, network, timestamps are equally
/// unhashed). Hashing just that one channel would buy false confidence,
/// not correctness. Full hermeticity is Bazel's job, not this tool's.
///
/// ```
/// use bazel_remote_apis::build::bazel::remote::execution::v2::{Action, Command};
/// use re_memoize::run::{RunOptions, build_action};
/// use prost::Message;
///
/// let opts = RunOptions {
///     input_root_digest: reapi::digest(b"fixture-input-tree"),
///     argv: vec!["echo".to_owned(), "hello".to_owned()],
///     output_files: vec!["out.txt".into()],
///     output_dirs: vec![],
///     no_cache: true,
/// };
/// let (command_blob, action_blob) = build_action(&opts).unwrap();
///
/// let command = Command::decode(command_blob.data.as_slice()).unwrap();
/// assert_eq!(command.arguments, opts.argv);
/// assert_eq!(command.output_paths, vec!["out.txt".to_owned()]);
/// assert!(command.environment_variables.is_empty());
///
/// let action = Action::decode(action_blob.data.as_slice()).unwrap();
/// assert_eq!(action.command_digest, Some(command_blob.digest));
/// assert_eq!(action.input_root_digest, Some(opts.input_root_digest));
/// assert!(action.do_not_cache);
/// ```
pub fn build_action(opts: &RunOptions) -> Result<(Blob, Blob), Error> {
    // working_directory is deliberately always empty (= the input root):
    // REAPI defines it as relative, so cache portability across machines is
    // fine in principle, but re-memoize doesn't actually `.current_dir()` the
    // child anywhere — it always runs wherever `re-memoize run` itself was
    // invoked from. A configurable value here would describe something
    // this code doesn't do, which is worse than not having the knob. The
    // same "wherever re-memoize run was invoked from" basis is what every
    // declared output path below is resolved against, both when capturing
    // (reading it off disk after the command ran) and when restoring (on a
    // cache hit).
    let mut output_paths: Vec<String> = opts
        .output_files
        .iter()
        .chain(&opts.output_dirs)
        .map(|path| reapi_path(path))
        .collect::<Result<_, _>>()?;
    output_paths.sort();
    output_paths.dedup();

    let command = Command {
        arguments: opts.argv.clone(),
        // Always empty — see this function's doc comment.
        environment_variables: Vec::new(),
        output_paths,
        ..Default::default()
    };
    let command_blob = Blob::from_message(&command);

    let action = Action {
        command_digest: Some(command_blob.digest.clone()),
        input_root_digest: Some(opts.input_root_digest.clone()),
        do_not_cache: opts.no_cache,
        ..Default::default()
    };
    let action_blob = Blob::from_message(&action);

    Ok((command_blob, action_blob))
}

/// The REAPI Action digest `run_cached(opts)` would use as its ActionCache
/// key, computed without running anything or touching the network. Useful
/// to know in advance — e.g. to manually scrub/evict that specific entry
/// from the ActionCache. Exposed as `re-memoize action-digest`.
pub fn action_digest(opts: &RunOptions) -> Result<Digest, Error> {
    let (_, action_blob) = build_action(opts)?;
    Ok(action_blob.digest)
}

/// Checks the ActionCache for `opts`'s `Action`, and either replays a prior
/// result or actually runs `opts.argv` and records the outcome. Returns
/// the child's exit code either way — a nonzero exit from the wrapped
/// command is not itself a `re-memoize` failure.
pub async fn run_cached(client: &mut RemoteClient, opts: RunOptions) -> Result<i32, Error> {
    let (command_blob, action_blob) = build_action(&opts)?;
    let action_digest = action_blob.digest.clone();

    // Needed regardless of cache outcome: a consumer inspecting the
    // ActionCache later must be able to resolve command_digest/action_digest.
    client
        .upload_if_missing(vec![command_blob, action_blob])
        .await?;

    if !opts.no_cache
        && let Some(cached) = client.action_result(&action_digest).await?
    {
        eprintln!(
            "re-memoize: cache hit ({}/{})",
            action_digest.hash, action_digest.size_bytes
        );
        replay(client, &cached, &action_digest).await?;
        return Ok(cached.exit_code);
    }

    let (exit_code, stdout, stderr) = spawn_and_tee(&opts.argv).await?;

    // Clients shouldn't populate stdout_raw/stderr_raw when writing to the
    // cache (only servers inline on the read path) — always go through CAS.
    let stdout_blob = Blob::new(stdout);
    let stderr_blob = Blob::new(stderr);
    let stdout_digest = stdout_blob.digest.clone();
    let stderr_digest = stderr_blob.digest.clone();
    client
        .upload_if_missing(vec![stdout_blob, stderr_blob])
        .await?;

    let mut output_files = Vec::new();
    for path in &opts.output_files {
        output_files.push(capture_output_file(client, path).await?);
    }
    let mut output_directories = Vec::new();
    for path in &opts.output_dirs {
        output_directories.push(capture_output_directory(client, path).await?);
    }

    let action_result = ActionResult {
        exit_code,
        stdout_digest: Some(stdout_digest),
        stderr_digest: Some(stderr_digest),
        output_files,
        output_directories,
        ..Default::default()
    };

    if !opts.no_cache {
        client
            .update_action_result(&action_digest, action_result)
            .await?;
    }

    Ok(exit_code)
}

/// Replays a cached result: writes stdout/stderr to our own, and restores
/// every declared output file/directory to disk — so a cache hit looks the
/// same to the caller as actually having run the command.
async fn replay(
    client: &mut RemoteClient,
    cached: &ActionResult,
    action_digest: &Digest,
) -> Result<(), Error> {
    write_stream(
        client,
        &cached.stdout_raw,
        cached.stdout_digest.as_ref(),
        tokio::io::stdout(),
        "stdout",
    )
    .await?;
    write_stream(
        client,
        &cached.stderr_raw,
        cached.stderr_digest.as_ref(),
        tokio::io::stderr(),
        "stderr",
    )
    .await?;
    restore_output_files(client, &cached.output_files, action_digest).await?;
    for dir in &cached.output_directories {
        let tree_digest = dir
            .tree_digest
            .clone()
            .ok_or_else(|| Error::MalformedActionResult {
                action_digest: format_digest(action_digest),
                reason: "OutputDirectory missing tree_digest",
            })?;
        download::download_tree(client, &tree_digest, Path::new(&dir.path)).await?;
    }
    Ok(())
}

/// Captures `path` (must already exist — the command was expected to
/// produce it) as an `OutputFile`: digests its content, uploads it, and
/// records the executable bit. Symlinks aren't supported yet (see
/// `Error::UnsupportedOutput`'s doc comment).
async fn capture_output_file(client: &mut RemoteClient, path: &Path) -> Result<OutputFile, Error> {
    let metadata = symlink_metadata(path)?;
    if metadata.file_type().is_symlink() {
        return Err(Error::UnsupportedOutput {
            path: path.to_owned(),
            reason: "symlinked outputs aren't supported yet",
        });
    }
    let content = fs::read(path).context(|| "Reading", path)?;
    let blob = Blob::new(content);
    let digest = blob.digest.clone();
    let is_executable = metadata.permissions().mode() & 0o100 != 0;
    client.upload_if_missing(vec![blob]).await?;
    Ok(OutputFile {
        path: reapi_path(path)?,
        digest: Some(digest),
        is_executable,
        // Inlining content directly into an OutputFile is a read-side
        // optimization a *server* may do in its GetActionResult response —
        // never something a *client* does when writing a result, so this
        // stays empty; `digest` above is what actually gets fetched.
        contents: Vec::new(),
        node_properties: None,
    })
}

/// Captures `path` as an `OutputDirectory` by uploading it exactly like the
/// primary `upload` command does (`upload::upload_directory`) — same
/// dedup-by-hash behavior, same two digests produced, just used for the
/// `tree_digest`/`root_directory_digest` half instead of what
/// `re-memoize upload` prints.
async fn capture_output_directory(
    client: &mut RemoteClient,
    path: &Path,
) -> Result<OutputDirectory, Error> {
    let metadata = symlink_metadata(path)?;
    if metadata.file_type().is_symlink() {
        return Err(Error::UnsupportedOutput {
            path: path.to_owned(),
            reason: "symlinked outputs aren't supported yet",
        });
    }
    let uploaded = upload::upload_directory(client, path).await?;
    Ok(OutputDirectory {
        path: reapi_path(path)?,
        tree_digest: Some(uploaded.tree_digest),
        is_topologically_sorted: false,
        root_directory_digest: Some(uploaded.root_digest),
    })
}

/// `fs::symlink_metadata`, translating "doesn't exist" into the more
/// specific `Error::MissingOutput` (the declared output simply wasn't
/// produced) rather than a generic `Error::Io`.
fn symlink_metadata(path: &Path) -> Result<fs::Metadata, Error> {
    fs::symlink_metadata(path).map_err(|source| {
        if source.kind() == std::io::ErrorKind::NotFound {
            Error::MissingOutput {
                path: path.to_owned(),
            }
        } else {
            Error::Io {
                action: "Reading metadata for".to_owned(),
                path: path.to_owned(),
                source,
            }
        }
    })
}

/// Batch-downloads and writes every declared output file from a cached
/// `ActionResult`, with the right permissions, creating leading
/// directories as needed (mirroring what a real REAPI worker does before
/// execution — see `Command.output_paths`'s doc comment in the vendored
/// proto).
async fn restore_output_files(
    client: &mut RemoteClient,
    output_files: &[OutputFile],
    action_digest: &Digest,
) -> Result<(), Error> {
    let malformed = |reason| Error::MalformedActionResult {
        action_digest: format_digest(action_digest),
        reason,
    };
    let digests: Vec<Digest> = output_files
        .iter()
        .map(|file| {
            file.digest
                .clone()
                .ok_or_else(|| malformed("OutputFile missing digest"))
        })
        .collect::<Result<_, _>>()?;
    let mut blobs: HashMap<String, Vec<u8>> = client.download_blobs(&digests).await?;

    for (file, digest) in output_files.iter().zip(&digests) {
        let path = PathBuf::from(&file.path);
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent).context(|| "Creating directory", parent)?;
        }
        let data = blobs
            .remove(&digest.hash)
            .ok_or_else(|| Error::BlobStatus {
                hash: digest.hash.clone(),
                size_bytes: digest.size_bytes,
                status: "server returned no response for this digest".to_owned(),
            })?;
        fs::write(&path, &data).context(|| "Writing", &path)?;
        let mode = match file.is_executable {
            true => 0o755,
            false => 0o644,
        };
        fs::set_permissions(&path, fs::Permissions::from_mode(mode))
            .context(|| "Setting permissions on", &path)?;
    }
    Ok(())
}

/// Writes `raw` if the server inlined it, otherwise fetches `digest` from
/// CAS first. Mirrors `GetActionResultRequest`'s `inline_stdout`/
/// `inline_stderr` hints, which a server is free to ignore.
async fn write_stream(
    client: &mut RemoteClient,
    raw: &[u8],
    digest: Option<&Digest>,
    mut out: impl tokio::io::AsyncWrite + Unpin,
    label: &'static str,
) -> Result<(), Error> {
    if !raw.is_empty() {
        out.write_all(raw)
            .await
            .context(|| "Writing", Path::new(label))?;
    } else if let Some(digest) = digest {
        let mut blobs = client.download_blobs(std::slice::from_ref(digest)).await?;
        if let Some(data) = blobs.remove(&digest.hash) {
            out.write_all(&data)
                .await
                .context(|| "Writing", Path::new(label))?;
        }
    }
    Ok(())
}

/// Spawns `argv[0] argv[1..]`, streaming its stdout/stderr through to ours
/// live while also capturing each into a buffer (for the `ActionResult`).
///
/// The child inherits re-memoize's own ambient environment untouched, by design
/// — see `run_cached`'s doc comment for why an allow-list isn't worth
/// building here.
async fn spawn_and_tee(argv: &[String]) -> Result<(i32, Vec<u8>, Vec<u8>), Error> {
    let (program, args) = argv
        .split_first()
        .expect("argv is non-empty (enforced by clap)");

    let mut child = ChildCommand::new(program)
        .args(args)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|source| Error::Spawn {
            program: program.clone(),
            source,
        })?;

    let mut child_stdout = child.stdout.take().expect("stdout is piped above");
    let mut child_stderr = child.stderr.take().expect("stderr is piped above");

    let (stdout, stderr) = tokio::try_join!(
        pump(&mut child_stdout, tokio::io::stdout(), "stdout"),
        pump(&mut child_stderr, tokio::io::stderr(), "stderr"),
    )?;

    let status = child.wait().await.map_err(|source| Error::Spawn {
        program: program.clone(),
        source,
    })?;
    // No exit code means the child was killed by a signal; -1 isn't a real
    // exit code either, but it's an unambiguous "abnormal" marker to
    // propagate rather than silently coercing to 0/1.
    let exit_code = status.code().unwrap_or(-1);

    Ok((exit_code, stdout, stderr))
}

/// Copies everything from `src` to `dst` (live) while also buffering it,
/// returning the buffer once `src` reaches EOF.
async fn pump(
    src: &mut (impl tokio::io::AsyncRead + Unpin),
    mut dst: impl tokio::io::AsyncWrite + Unpin,
    label: &'static str,
) -> Result<Vec<u8>, Error> {
    let mut buf = [0u8; 8192];
    let mut captured = Vec::new();
    loop {
        let n = src
            .read(&mut buf)
            .await
            .context(|| format!("Reading child {label}"), Path::new(label))?;
        if n == 0 {
            return Ok(captured);
        }
        dst.write_all(&buf[..n])
            .await
            .context(|| "Writing", Path::new(label))?;
        captured.extend_from_slice(&buf[..n]);
    }
}
