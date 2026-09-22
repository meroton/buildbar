use std::collections::HashMap;
use std::path::{Path, PathBuf};

use bazel_remote_apis::build::bazel::remote::execution::v2::{Digest, Tree};
use reapi::Blob;

use crate::client::RemoteClient;
use crate::error::Error;
use crate::tree::{BuiltDirectory, build_directory, build_filtered_directory};

/// The two digests that identify an uploaded directory tree: the root
/// `Directory` digest (what REAPI's `Action.input_root_digest` and
/// `OutputDirectory.root_directory_digest` want) and the flattened `Tree`
/// digest (what `OutputDirectory.tree_digest` wants, and what lets
/// `download_tree` fetch the whole shape in one blob). Both blobs are
/// always uploaded together, so callers pick whichever digest their use
/// case needs.
pub struct UploadedTree {
    pub root_digest: Digest,
    pub tree_digest: Digest,
}

/// Walks `path`, uploads every blob it references that isn't already in
/// CAS (files, every `Directory` message root-and-descendants, and the
/// flattened `Tree` message itself), and returns both digests that
/// identify the result.
pub async fn upload_directory(
    client: &mut RemoteClient,
    path: &Path,
) -> Result<UploadedTree, Error> {
    upload_built(client, build_directory(path)?).await
}

/// Same as [`upload_directory`], but rooted at `root` and limited to
/// `filters` — mirrors `tree::build_filtered_directory`, so the digest an
/// upload produces here can be reproduced offline by `re-memoize digest
/// --root root filters...`, and vice versa. With no filters, identical to
/// `upload_directory(client, root)`.
pub async fn upload_filtered_directory(
    client: &mut RemoteClient,
    root: &Path,
    filters: &[PathBuf],
) -> Result<UploadedTree, Error> {
    upload_built(client, build_filtered_directory(root, filters)?).await
}

/// Shared tail of both entry points above: given an already-built
/// directory tree, push every blob it depends on that isn't already in
/// CAS, and return both digests that identify it.
async fn upload_built(
    client: &mut RemoteClient,
    built: BuiltDirectory,
) -> Result<UploadedTree, Error> {
    let root_digest = built.digest.clone();

    let tree = Tree {
        root: Some(built.directory),
        children: built.descendants,
    };
    let tree_blob = Blob::from_message(&tree);
    let tree_digest = tree_blob.digest.clone();

    // Assemble every blob this Tree depends on, deduped by hash: file
    // contents, every Directory message (root + descendants, via
    // tree.root/tree.children so we don't need the pre-move copies), and
    // the Tree blob itself.
    let mut blobs: HashMap<String, Blob> = HashMap::new();
    for blob in built.file_blobs {
        blobs.insert(blob.digest.hash.clone(), blob);
    }
    for dir in tree.root.iter().chain(tree.children.iter()) {
        let blob = Blob::from_message(dir);
        blobs.insert(blob.digest.hash.clone(), blob);
    }
    blobs.insert(tree_digest.hash.clone(), tree_blob);

    client
        .upload_if_missing(blobs.into_values().collect())
        .await?;

    Ok(UploadedTree {
        root_digest,
        tree_digest,
    })
}
