use std::path::PathBuf;

use bb_kv::client::{DEFAULT_MAX_MESSAGE_SIZE_BYTES, RemoteClient};
use bb_kv::error::{Error, report};
use bb_kv::run::{self, RunOptions, run_cached};
use bb_kv::tree::{build_filtered_directory, format_digest, parse_digest};
use bb_kv::{download, upload};
use clap::Parser;

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
    },
    /// Upload a directory to a remote CAS; prints its root Directory digest.
    Upload {
        path: PathBuf,
        #[command(flatten)]
        connection: ConnectionArgs,
    },
    /// Download a directory from a remote CAS. Two forms of the same
    /// content are stored under two different digests (see `tree.rs`'s
    /// module doc) — pass whichever one you have: `--directory-digest`
    /// (what `digest`/`upload` print) walks the tree breadth-first,
    /// `--tree-digest` fetches one self-describing blob directly.
    #[command(group(clap::ArgGroup::new("digest").required(true).multiple(false)))]
    Download {
        /// The slower, general-purpose form: a root Directory digest.
        #[arg(long, group = "digest")]
        directory_digest: Option<String>,
        /// The faster special case, If you already have the Tree digest.
        #[arg(long, group = "digest")]
        tree_digest: Option<String>,
        out: PathBuf,
        #[command(flatten)]
        connection: ConnectionArgs,
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
        /// `digest`/`upload` — same digest form as `download
        /// --directory-digest`.
        #[arg(long)]
        directory_digest: String,
        /// Skip the cache lookup and the write-back afterward: always run,
        /// never persist a result.
        #[arg(long)]
        no_cache: bool,
        #[command(flatten)]
        connection: ConnectionArgs,
        /// A file path (relative to where `bb-kv run` is invoked from) the
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
    /// Per-request byte budget bb-kv stays under when sending a batched
    /// request (BatchUpdateBlobs/BatchReadBlobs/FindMissingBlobs), i.e. how
    /// many blobs get pushed/pulled per RPC.
    #[arg(long = "max-message-size", default_value_t = DEFAULT_MAX_MESSAGE_SIZE_BYTES)]
    max_message_size_bytes: usize,
}

#[tokio::main]
async fn main() {
    if let Err(err) = run().await {
        report(&err);
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Error> {
    match Command::parse() {
        Command::Digest { root, filters } => {
            let digest = build_filtered_directory(&root, &filters)?.digest;
            println!("{}", format_digest(&digest));
        }
        Command::Upload { path, connection } => {
            let mut client = RemoteClient::connect(
                &connection.remote,
                connection.instance_name,
                connection.ca_cert.as_deref(),
            )
            .await?
            .with_max_message_size_bytes(connection.max_message_size_bytes);
            let uploaded = upload::upload_directory(&mut client, &path).await?;
            println!("{}", format_digest(&uploaded.root_digest));
        }
        Command::Download {
            directory_digest,
            tree_digest,
            out,
            connection,
        } => {
            let mut client = RemoteClient::connect(
                &connection.remote,
                connection.instance_name,
                connection.ca_cert.as_deref(),
            )
            .await?
            .with_max_message_size_bytes(connection.max_message_size_bytes);
            match (directory_digest, tree_digest) {
                (Some(digest), None) => {
                    download::download_from_root(&mut client, &parse_digest(&digest)?, &out).await?
                }
                (None, Some(digest)) => {
                    download::download_tree(&mut client, &parse_digest(&digest)?, &out).await?
                }
                (None, None) | (Some(_), Some(_)) => {
                    unreachable!(
                        "internal error: clap's ArgGroup requires exactly one of directory_digest/tree_digest"
                    )
                }
            }
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
    }
    Ok(())
}
