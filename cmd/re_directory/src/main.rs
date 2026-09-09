use std::path::PathBuf;

use clap::Parser;
use re_storage::client::{DEFAULT_MAX_MESSAGE_SIZE_BYTES, RemoteClient};
use re_storage::error::{Error, report};
use re_storage::tree::{format_digest, parse_digest};
use re_storage::{download, upload};

#[derive(Parser)]
enum Command {
    /// Upload a directory to a remote CAS; prints its root Directory digest.
    /// `--root`/filters work exactly like `re-memoize digest`'s (see its
    /// `--help`), so the digest this prints can also be reproduced offline,
    /// and vice versa: whatever `re-memoize digest --root R filters...`
    /// says the key would be is exactly what uploading with the same
    /// `--root`/filters here actually pushes to CAS.
    Upload {
        #[arg(long, default_value = ".")]
        root: PathBuf,
        /// Paths to include, read exactly as given (relative to the
        /// working directory, so they tab-complete normally — not relative
        /// to --root; --root only decides where each one lands in the
        /// tree). Each independently a file, directory (included fully,
        /// recursively), or symlink; must resolve to somewhere inside
        /// --root. With none given, the whole --root directory is
        /// uploaded.
        filters: Vec<PathBuf>,
        #[command(flatten)]
        connection: ConnectionArgs,
    },
    /// Download a directory from a remote CAS. Two forms of the same
    /// content are stored under two different digests (see re_storage's
    /// `tree` module doc) — pass whichever one you have: `--directory-digest`
    /// (what `upload`/`re-memoize digest` print) walks the tree
    /// breadth-first, `--tree-digest` fetches one self-describing blob
    /// directly.
    #[command(group(clap::ArgGroup::new("digest").required(true).multiple(false)))]
    Download {
        /// The slower, general-purpose form: a root Directory digest.
        #[arg(long, group = "digest")]
        directory_digest: Option<String>,
        /// The faster special case, if you already have the Tree digest.
        #[arg(long, group = "digest")]
        tree_digest: Option<String>,
        /// Directory to materialize the downloaded tree into.
        out: PathBuf,
        #[command(flatten)]
        connection: ConnectionArgs,
    },
}

/// The flags shared by every subcommand: which server, which instance
/// within it, and how hard to push against its message-size ceiling.
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
    /// Per-request byte budget re-directory stays under when sending a
    /// batched request (BatchUpdateBlobs/BatchReadBlobs/FindMissingBlobs),
    /// i.e. how many blobs get pushed/pulled per RPC.
    #[arg(long = "max-message-size", default_value_t = DEFAULT_MAX_MESSAGE_SIZE_BYTES)]
    max_message_size_bytes: usize,
}

// A one-shot CLI, not a server: no work here benefits from true OS-thread
// parallelism.
#[tokio::main(flavor = "current_thread")]
async fn main() {
    if let Err(err) = run().await {
        report(&err);
        hint_wrong_flag(&err);
        std::process::exit(1);
    }
}

/// `re_storage::error::Error::Decode`'s own message can only say "this is
/// the wrong digest kind" in general — it has no notion of `download`'s
/// `--directory-digest`/`--tree-digest` flags, since those are this CLI's
/// vocabulary, not the library's. But which flag leads to which decode is
/// fixed and 1:1 (`download_from_root`, reached only via
/// `--directory-digest`, always decodes `"Directory"`; `download_tree`,
/// reached only via `--tree-digest`, always decodes `"Tree"`), so this CLI
/// *can* name the other flag directly instead of leaving the reader to
/// work out which is which.
fn hint_wrong_flag(err: &Error) {
    let try_instead = match err {
        Error::Decode { what: "Directory", .. } => "--tree-digest",
        Error::Decode { what: "Tree", .. } => "--directory-digest",
        _ => return,
    };
    eprintln!("Hint: this looks like the wrong digest kind — try {try_instead} instead");
}

async fn run() -> Result<(), Error> {
    match Command::parse() {
        Command::Upload {
            root,
            filters,
            connection,
        } => {
            let mut client = RemoteClient::connect(
                &connection.remote,
                connection.instance_name,
                connection.ca_cert.as_deref(),
            )
            .await?
            .with_max_message_size_bytes(connection.max_message_size_bytes);
            let uploaded = upload::upload_filtered_directory(&mut client, &root, &filters).await?;
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
    }
    Ok(())
}
