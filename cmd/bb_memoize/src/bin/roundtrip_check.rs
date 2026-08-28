//! Standalone program (not a `#[test]`, not run by `cargo test`) that
//! exercises the full upload → download round trip against a real REAPI v2
//! CAS: builds a small fixture directory, uploads it, downloads it back to
//! a fresh directory, and asserts the two are equivalent. Run manually
//! against an already-running server, e.g.:
//!
//! ```text
//! cargo run --bin roundtrip_check -- --remote http://localhost:8980
//! ```

#[path = "common/mod.rs"]
mod common;

use std::fs;
use std::os::unix::fs::{PermissionsExt, symlink};
use std::path::Path;

use bb_memoize::client::{DEFAULT_MAX_MESSAGE_SIZE_BYTES, RemoteClient};
use bb_memoize::error::{Error, IoResultExt, report};
use bb_memoize::tree::build_directory;
use bb_memoize::{download, upload};
use clap::Parser;
use common::ScratchDir;

#[derive(Parser)]
struct Args {
    #[arg(long)]
    remote: String,
    #[arg(long, default_value = "")]
    instance_name: String,
    #[arg(long = "max-message-size", default_value_t = DEFAULT_MAX_MESSAGE_SIZE_BYTES)]
    max_message_size_bytes: usize,
    /// Don't clean up the fixture/output temp dirs, and print their paths,
    /// so a failure can be inspected afterward.
    #[arg(long)]
    keep_temporary_files: bool,
}

// One fixture, uploaded then downloaded sequentially — no concurrent
// fan-out to benefit from true OS-thread parallelism.
#[tokio::main(flavor = "current_thread")]
async fn main() {
    if let Err(err) = run().await {
        report(&err);
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Error> {
    let args = Args::parse();

    let src = ScratchDir::new("roundtrip-check-fixture", args.keep_temporary_files)?;
    let out = ScratchDir::new("roundtrip-check-out", args.keep_temporary_files)?;
    if args.keep_temporary_files {
        println!("fixture dir: {}", src.path().display());
        println!("output dir:  {}", out.path().display());
    }

    build_fixture(src.path())?;

    let mut client = RemoteClient::connect(&args.remote, args.instance_name.clone(), None)
        .await?
        .with_max_message_size_bytes(args.max_message_size_bytes);
    let uploaded = upload::upload_directory(&mut client, src.path()).await?;
    download::download_from_root(&mut client, &uploaded.root_digest, out.path()).await?;

    // Strongest single assertion: re-digesting the downloaded tree must
    // reproduce exactly the root Directory digest we uploaded.
    let recomputed = build_directory(out.path())?.digest;
    assert_eq!(
        recomputed,
        uploaded.root_digest,
        "downloaded tree re-digests to {}/{}, expected the uploaded {}/{}",
        recomputed.hash,
        recomputed.size_bytes,
        uploaded.root_digest.hash,
        uploaded.root_digest.size_bytes,
    );

    // Defense-in-depth: explicit per-file checks with clearer failure
    // messages than "digests differ".
    assert_files_equal(&src.path().join("plain.txt"), &out.path().join("plain.txt"))?;
    assert_files_equal(
        &src.path().join("subdir/nested.txt"),
        &out.path().join("subdir/nested.txt"),
    )?;

    let exe_out = out.path().join("run.sh");
    assert_files_equal(&src.path().join("run.sh"), &exe_out)?;
    let mode = fs::metadata(&exe_out)
        .context(|| "reading metadata for", &exe_out)?
        .permissions()
        .mode();
    assert!(
        mode & 0o100 != 0,
        "{} lost its executable bit",
        exe_out.display()
    );

    let link_out = out.path().join("link-to-plain.txt");
    let target = fs::read_link(&link_out).context(|| "reading symlink target for", &link_out)?;
    assert_eq!(
        target,
        Path::new("plain.txt"),
        "symlink {} has target {:?}, expected \"plain.txt\"",
        link_out.display(),
        target,
    );

    println!(
        "roundtrip_check: ok (tree {}/{})",
        uploaded.root_digest.hash, uploaded.root_digest.size_bytes
    );

    // Second upload of the same tree should find every blob already
    // present via find_missing_blobs and push nothing new — not asserted
    // here (upload_blobs doesn't report a "pushed 0 blobs" count to check
    // against), but exercised by simply running this program twice.
    Ok(())
}

/// A plain file, an executable file, a nested subdirectory, and a symlink
/// to a sibling file — enough to exercise multi-level `DirectoryNode`
/// resolution, `Tree.children` flattening, and `SymlinkNode` round-trip.
fn build_fixture(dir: &Path) -> Result<(), Error> {
    let plain = dir.join("plain.txt");
    fs::write(&plain, b"just a regular file").context(|| "writing", &plain)?;

    let exe = dir.join("run.sh");
    fs::write(&exe, b"#!/usr/bin/env sh\necho hi\n").context(|| "writing", &exe)?;
    fs::set_permissions(&exe, fs::Permissions::from_mode(0o755))
        .context(|| "setting permissions on", &exe)?;

    let subdir = dir.join("subdir");
    fs::create_dir(&subdir).context(|| "creating directory", &subdir)?;
    let nested = subdir.join("nested.txt");
    fs::write(&nested, b"nested file content").context(|| "writing", &nested)?;

    let link = dir.join("link-to-plain.txt");
    symlink("plain.txt", &link).context(|| "creating symlink to plain.txt at", &link)?;

    Ok(())
}

fn assert_files_equal(expected: &Path, actual: &Path) -> Result<(), Error> {
    let expected_content = fs::read(expected).context(|| "reading", expected)?;
    let actual_content = fs::read(actual).context(|| "reading", actual)?;
    assert_eq!(
        expected_content,
        actual_content,
        "{} and {} differ",
        expected.display(),
        actual.display(),
    );
    Ok(())
}
