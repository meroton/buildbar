use std::collections::HashMap;
use std::path::Path;

use bazel_remote_apis::build::bazel::remote::execution::v2::{Digest, Tree};

use crate::client::RemoteClient;
use crate::error::Error;
use crate::tree::{Blob, build_directory};

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
    let built = build_directory(path)?;
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
