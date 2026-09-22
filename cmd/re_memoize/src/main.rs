use std::fs;
use std::path::{Path, PathBuf};

use clap::Parser;
use re_memoize::error::{Error, report};
use re_memoize::run::{self, ActionKey, LoadOutcome, RunOptions, load_cached, run_cached, store_result};
use re_storage::client::{DEFAULT_MAX_MESSAGE_SIZE_BYTES, RemoteClient};
use re_storage::error::IoResultExt;
use re_storage::tree::{TreeEntryKind, build_filtered_directory, format_digest, list_entries, parse_digest};

#[derive(Parser)]
enum Command {
    /// Compute the REAPI v2 root Directory digest for a filtered selection
    /// under a directory (offline). Re-rooting changes the digest: the same
    /// files produce a different tree (and so a different digest) depending
    /// on where `--root` is set — see `tree.rs`'s `build_filtered_directory`
    /// doc for why.
    Digest {
        #[arg(long, default_value = ".")]
        root: PathBuf,
        /// Paths to include, read exactly as given (relative to the
        /// working directory, so they tab-complete normally — not relative
        /// to --root; --root only decides where each one lands in the
        /// tree). Each independently a file, directory (included fully,
        /// recursively), or symlink; must resolve to somewhere inside
        /// --root. At least one is required.
        #[arg(required = true)]
        filters: Vec<PathBuf>,
        /// Print every file, symlink, and directory the resulting digest
        /// actually covers, each at its full path within the tree — so
        /// you can verify --root/filters selected what you meant before
        /// trusting the digest as a cache key. Written to stderr, so it
        /// never interferes with capturing the digest itself, e.g.
        /// `digest=$(re-memoize digest ...)`.
        #[arg(long, short = 'v')]
        verbose: bool,
    },
    /// Run a command unless an identical (command, input tree) has already
    /// been cached in Buildbarn's ActionCache; either way, replay/produce
    /// its stdout, stderr, and exit code.
    ///
    /// This is meant as a Pareto Principle buildsystem with RBE.
    /// Or in other words, where a full conversion to a strict build system like
    /// Bazel is not possible. To use the RBE for action caching is the next
    /// best thing. And beats many other possible solutions with semaphore in
    /// REDIS databases and the like.
    Run {
        /// Root Directory digest of the command's inputs, as printed by
        /// `digest`/`re-directory upload` — same digest form as
        /// `re-directory download --directory-digest`.
        #[arg(long)]
        directory_digest: String,
        /// Skip the cache lookup and the write-back afterward: always run,
        /// never persist a result.
        #[arg(long)]
        no_cache: bool,
        #[command(flatten)]
        connection: ConnectionArgs,
        /// A file path (relative to where `re-memoize run` is invoked from) the
        /// command is expected to produce; captured and cached, restored
        /// verbatim on a cache hit. Repeatable.
        #[arg(long = "output-file")]
        output_files: Vec<PathBuf>,
        /// Same as `--output-file`, but for a directory captured as a
        /// subtree. Repeatable.
        #[arg(long = "output-dir")]
        output_dirs: Vec<PathBuf>,
        /// The command to run and its arguments.
        #[arg(trailing_var_arg = true, required = true, allow_hyphen_values = true)]
        argv: Vec<String>,
    },
    /// Print the REAPI Action digest a `run` with these arguments would use
    /// as its ActionCache key — computed offline, no network access,
    /// nothing run. Useful to know in advance, e.g. to manually scrub that
    /// specific entry from the ActionCache.
    ///
    /// Takes the same `--directory-digest`/`--output-file`/`--output-dir`/
    /// argv as the corresponding `run` invocation would (they must match
    /// exactly to compute the same digest) — but never `--no-cache`: this
    /// always computes the digest as if caching were on, since an Action
    /// run with `--no-cache` is never written to the ActionCache in the
    /// first place, so there'd be nothing to scrub.
    ActionDigest {
        #[arg(long)]
        directory_digest: String,
        #[arg(long = "output-file")]
        output_files: Vec<PathBuf>,
        #[arg(long = "output-dir")]
        output_dirs: Vec<PathBuf>,
        #[arg(trailing_var_arg = true, required = true, allow_hyphen_values = true)]
        argv: Vec<String>,
    },
    /// Check the ActionCache for a `run` with these arguments, without
    /// running anything — the manual, split-apart half of `run` for a
    /// caller who needs to batch/cluster command execution themselves
    /// (e.g. onto a shared, expensive external resource) while still
    /// caching each one individually: check every command ahead of time,
    /// and only actually execute — then `store` — whichever ones come
    /// back a miss.
    ///
    /// Takes the same `--directory-digest`/argv `run` would (they must
    /// match exactly to compute the same digest) — but no
    /// `--output-file`/`--output-dir`/`--no-cache`: declaring output
    /// files isn't supported here, and there'd be nothing to load with
    /// caching off.
    ///
    /// On a hit, behaves exactly like `run`'s cache-hit path — replays
    /// stdout/stderr and exits with the cached exit code — and is
    /// otherwise indistinguishable from having actually run the command.
    /// On a miss, nothing is replayed and the process exits 125 (matching
    /// `git bisect run`'s "untestable, skip this one" convention: a value
    /// distinct from any exit code a real command is likely to produce,
    /// though — like that convention — not airtight against a real
    /// command that happens to also use 125).
    Load {
        #[arg(long)]
        directory_digest: String,
        #[command(flatten)]
        connection: ConnectionArgs,
        #[arg(trailing_var_arg = true, required = true, allow_hyphen_values = true)]
        argv: Vec<String>,
    },
    /// Persist an already-known result under the Action digest these
    /// arguments would use — the other half of manual cache orchestration
    /// (see `load`'s doc): after running a command yourself outside
    /// `re-memoize` (e.g. as part of a batch on a shared external
    /// resource), record its result so a later `load` for the same
    /// digest/argv hits.
    ///
    /// As with `load`, no `--output-file`/`--output-dir`: only the
    /// command's exit code and captured stdout/stderr are stored.
    Store {
        #[arg(long)]
        directory_digest: String,
        /// The exit code the command actually produced when you ran it.
        #[arg(long = "exit-code")]
        exit_code: i32,
        /// File holding the command's captured stdout; omit for empty.
        #[arg(long)]
        stdout: Option<PathBuf>,
        /// File holding the command's captured stderr; omit for empty.
        #[arg(long)]
        stderr: Option<PathBuf>,
        #[command(flatten)]
        connection: ConnectionArgs,
        #[arg(trailing_var_arg = true, required = true, allow_hyphen_values = true)]
        argv: Vec<String>,
    },
}

/// The flags shared by every subcommand that talks to a remote: which
/// server, which instance within it, and how hard to push against its
/// message-size ceiling.
#[derive(clap::Args)]
struct ConnectionArgs {
    /// grpc://, grpcs://, http://, or https://. grpc(s):// is accepted as
    /// an alias for http(s):// — both are the same underlying REAPI
    /// convention.
    #[arg(long)]
    remote: String,
    #[arg(long, default_value = "")]
    instance_name: String,
    /// Custom CA certificate (PEM) to trust for a grpcs:// or https://
    /// remote, in addition to the system's native root certificates.
    /// Ignored for a plaintext remote.
    #[arg(long)]
    ca_cert: Option<PathBuf>,
    /// Per-request byte budget re-memoize stays under when sending a batched
    /// request (BatchUpdateBlobs/BatchReadBlobs/FindMissingBlobs), i.e. how
    /// many blobs get pushed/pulled per RPC.
    #[arg(long = "max-message-size", default_value_t = DEFAULT_MAX_MESSAGE_SIZE_BYTES)]
    max_message_size_bytes: usize,
}

/// Prints every entry `built` contains, one per line, to stderr — never
/// stdout, so `--verbose` can't interfere with capturing the digest
/// itself (`digest=$(re-memoize digest ...)` only ever reads stdout).
/// Tab-separated: kind (a file's mode, "dir", or "link"), path, and
/// digest (files/directories) or symlink target.
fn print_tree(built: &re_storage::tree::BuiltDirectory) {
    for entry in list_entries(built) {
        let path = entry.path.display();
        match entry.kind {
            TreeEntryKind::File {
                digest,
                is_executable,
            } => {
                let mode = if is_executable { "755" } else { "644" };
                eprintln!("{mode}\t{path}\t{}", format_digest(&digest));
            }
            TreeEntryKind::Directory { digest } => {
                eprintln!("dir\t{path}/\t{}", format_digest(&digest));
            }
            TreeEntryKind::Symlink { target } => {
                eprintln!("link\t{path} -> {target}");
            }
        }
    }
}

// A one-shot CLI, not a server: no work here benefits from true OS-thread
// parallelism (see run.rs's `spawn_and_tee`, which concurrently pumps a
// child's stdout/stderr via `try_join!` — that's task interleaving on one
// thread, not multi-threading).
#[tokio::main(flavor = "current_thread")]
async fn main() {
    if let Err(err) = run().await {
        report(&err);
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Error> {
    if let Some(dir) = std::env::var_os("BUILD_WORKING_DIRECTORY") {
        std::env::set_current_dir(&dir).context(|| "Changing directory to", Path::new(&dir))?;
    }

    match Command::parse() {
        Command::Digest {
            root,
            filters,
            verbose,
        } => {
            let built = build_filtered_directory(&root, &filters)?;
            if verbose {
                print_tree(&built);
            }
            println!("{}", format_digest(&built.digest));
        }
        Command::Run {
            directory_digest,
            no_cache,
            connection,
            output_files,
            output_dirs,
            argv,
        } => {
            let input_root_digest = parse_digest(&directory_digest)?;
            let mut client = RemoteClient::connect(
                &connection.remote,
                connection.instance_name,
                connection.ca_cert.as_deref(),
            )
            .await?
            .with_max_message_size_bytes(connection.max_message_size_bytes);
            let exit_code = run_cached(
                &mut client,
                RunOptions {
                    input_root_digest,
                    argv,
                    output_files,
                    output_dirs,
                    no_cache,
                },
            )
            .await?;
            std::process::exit(exit_code);
        }
        Command::ActionDigest {
            directory_digest,
            output_files,
            output_dirs,
            argv,
        } => {
            let input_root_digest = parse_digest(&directory_digest)?;
            let digest = run::action_digest(&RunOptions {
                input_root_digest,
                argv,
                output_files,
                output_dirs,
                no_cache: false,
            })?;
            println!("{}", format_digest(&digest));
        }
        Command::Load {
            directory_digest,
            connection,
            argv,
        } => {
            let input_root_digest = parse_digest(&directory_digest)?;
            let mut client = RemoteClient::connect(
                &connection.remote,
                connection.instance_name,
                connection.ca_cert.as_deref(),
            )
            .await?
            .with_max_message_size_bytes(connection.max_message_size_bytes);
            let key = ActionKey {
                input_root_digest,
                argv,
            };
            match load_cached(&mut client, key).await? {
                LoadOutcome::Hit { exit_code } => std::process::exit(exit_code),
                LoadOutcome::Miss => {
                    eprintln!("re-memoize: cache miss");
                    std::process::exit(125);
                }
            }
        }
        Command::Store {
            directory_digest,
            exit_code,
            stdout,
            stderr,
            connection,
            argv,
        } => {
            let input_root_digest = parse_digest(&directory_digest)?;
            let stdout = match stdout {
                Some(path) => fs::read(&path).context(|| "Reading", &path)?,
                None => Vec::new(),
            };
            let stderr = match stderr {
                Some(path) => fs::read(&path).context(|| "Reading", &path)?,
                None => Vec::new(),
            };
            let mut client = RemoteClient::connect(
                &connection.remote,
                connection.instance_name,
                connection.ca_cert.as_deref(),
            )
            .await?
            .with_max_message_size_bytes(connection.max_message_size_bytes);
            let key = ActionKey {
                input_root_digest,
                argv,
            };
            store_result(&mut client, key, exit_code, stdout, stderr).await?;
        }
    }
    Ok(())
}
