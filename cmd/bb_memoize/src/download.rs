use std::collections::HashMap;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};

use bazel_remote_apis::build::bazel::remote::execution::v2::{Digest, Directory, Tree};
use prost::Message;
use reapi::digest_message;

use crate::client::RemoteClient;
use crate::error::{Error, IoResultExt};

/// Fetches `root_digest` (a root `Directory` digest, as printed by
/// `digest`/`upload`) and materializes the whole tree under `out_dir`.
///
/// Auxiliary/secondary command, so it's allowed to pay for what `run`
/// can't: given only a root `Directory` digest, there's no single
/// self-describing blob to fetch (that's what the flattened `Tree` blob
/// was for — see `download_tree` below). Instead this walks the tree
/// breadth-first: fetch one level of `Directory` blobs (batched), collect
/// the next level's digests from their `directories` entries, repeat.
/// Round trips scale with tree depth, not size, and it needs no new RPC —
/// just repeated calls to the already-batched `RemoteClient::download_blobs`.
pub async fn download_from_root(
    client: &mut RemoteClient,
    root_digest: &Digest,
    out_dir: &Path,
) -> Result<(), Error> {
    let mut frontier = vec![(root_digest.clone(), out_dir.to_path_buf())];
    let mut file_targets = Vec::new();

    while !frontier.is_empty() {
        let digests: Vec<Digest> = frontier.iter().map(|(digest, _)| digest.clone()).collect();
        let blobs = client.download_blobs(&digests).await?;

        let mut next_frontier = Vec::new();
        for (digest, target) in &frontier {
            let bytes = blobs
                .get(&digest.hash)
                .ok_or_else(|| no_response_for(digest))?;
            let dir = Directory::decode(&bytes[..]).map_err(|source| Error::Decode {
                what: "Directory",
                hash: digest.hash.clone(),
                size_bytes: digest.size_bytes,
                source,
            })?;
            fs::create_dir_all(target).context(|| "creating directory", target)?;

            for entry in &dir.directories {
                let child_digest = entry.digest.clone().ok_or_else(|| Error::MalformedTree {
                    hash: digest.hash.clone(),
                    size_bytes: digest.size_bytes,
                    reason: "DirectoryNode missing digest",
                })?;
                next_frontier.push((child_digest, target.join(&entry.name)));
            }
            for file in &dir.files {
                let file_digest = file.digest.clone().ok_or_else(|| Error::MalformedTree {
                    hash: digest.hash.clone(),
                    size_bytes: digest.size_bytes,
                    reason: "FileNode missing digest",
                })?;
                file_targets.push((target.join(&file.name), file_digest, file.is_executable));
            }
            for symlink in &dir.symlinks {
                let link_path = target.join(&symlink.name);
                std::os::unix::fs::symlink(&symlink.target, &link_path).context(
                    || format!("creating symlink to {} at", symlink.target),
                    &link_path,
                )?;
            }
        }
        frontier = next_frontier;
    }

    write_files(client, file_targets).await
}

/// Fetches the `Tree` blob at `tree_digest` and materializes it under
/// `out_dir`: subdirectories, file contents (with executable bits), and
/// symlinks.
///
/// Deliberately doesn't use the `GetTree` RPC: a `Tree` blob is already
/// self-describing (root + every descendant `Directory` flattened inline),
/// so there's nothing left for the server to walk on our behalf — `GetTree`
/// exists for the case where a caller only has a root `Directory` digest,
/// which is `download_from_root`'s situation, not this function's (this one
/// is reached with a `Tree` digest already in hand: `run`'s cache-hit path
/// restoring an `ActionResult`'s `output_directories`, whose `tree_digest`
/// field is specified as a `Tree`-message digest per REAPI — see
/// `tree.rs`'s module doc for the full Tree-vs-Directory distinction).
/// Also exposed directly as `bb-memoize download --tree-digest` for callers who
/// already have a `Tree` digest in hand (e.g. printed by another tool).
pub async fn download_tree(
    client: &mut RemoteClient,
    tree_digest: &Digest,
    out_dir: &Path,
) -> Result<(), Error> {
    let mut tree_blob = client
        .download_blobs(std::slice::from_ref(tree_digest))
        .await?;
    let tree_bytes = tree_blob
        .remove(&tree_digest.hash)
        .ok_or_else(|| no_response_for(tree_digest))?;
    let tree = Tree::decode(&tree_bytes[..]).map_err(|source| Error::Decode {
        what: "Tree",
        hash: tree_digest.hash.clone(),
        size_bytes: tree_digest.size_bytes,
        source,
    })?;

    let mut lookup: HashMap<String, &Directory> = HashMap::new();
    for dir in &tree.children {
        lookup.insert(digest_message(dir).hash, dir);
    }

    let root = tree.root.as_ref().ok_or_else(|| Error::MalformedTree {
        hash: tree_digest.hash.clone(),
        size_bytes: tree_digest.size_bytes,
        reason: "missing root directory",
    })?;

    let mut file_targets = Vec::new();
    materialize(root, out_dir, &lookup, tree_digest, &mut file_targets)?;

    write_files(client, file_targets).await
}

/// Batch-fetches the content for every `(path, digest, is_executable)` in
/// `file_targets` (deduped by hash) and writes each to `path` with the
/// right permissions. Shared tail of both `download_tree` and
/// `download_from_root` — the only difference between them is how they
/// arrive at `file_targets`.
async fn write_files(
    client: &mut RemoteClient,
    file_targets: Vec<(PathBuf, Digest, bool)>,
) -> Result<(), Error> {
    let mut file_digests: HashMap<String, Digest> = HashMap::new();
    for (_, digest, _) in &file_targets {
        file_digests.insert(digest.hash.clone(), digest.clone());
    }
    let contents = client
        .download_blobs(&file_digests.into_values().collect::<Vec<_>>())
        .await?;

    for (path, digest, is_executable) in file_targets {
        let data = contents
            .get(&digest.hash)
            .ok_or_else(|| no_response_for(&digest))?;
        fs::write(&path, data).context(|| "writing", &path)?;
        let mode = match is_executable {
            true => 0o755,
            false => 0o644,
        };
        fs::set_permissions(&path, fs::Permissions::from_mode(mode))
            .context(|| "setting permissions on", &path)?;
    }

    Ok(())
}

/// Recursively creates `out_dir`'s subdirectories and symlinks, and
/// collects `(path, digest, is_executable)` for every file so their
/// content can be fetched in one batched call rather than one RPC per file.
///
/// ```
/// use std::collections::HashMap;
/// use bazel_remote_apis::build::bazel::remote::execution::v2::{
///     Directory, DirectoryNode, FileNode, SymlinkNode,
/// };
/// use bb_memoize::download::materialize;
/// use reapi::{digest, digest_message};
///
/// let out = tempfile::Builder::new().prefix("bb-memoize-doctest-materialize-").tempdir().unwrap();
///
/// let file_digest = digest(b"hello");
/// let child = Directory {
///     files: vec![FileNode {
///         name: "hello.txt".to_owned(),
///         digest: Some(file_digest.clone()),
///         is_executable: true,
///         node_properties: None,
///     }],
///     directories: vec![],
///     symlinks: vec![],
///     node_properties: None,
/// };
/// let child_digest = digest_message(&child);
///
/// let root = Directory {
///     files: vec![],
///     directories: vec![DirectoryNode { name: "sub".to_owned(), digest: Some(child_digest.clone()) }],
///     symlinks: vec![SymlinkNode {
///         name: "link".to_owned(),
///         target: "sub/hello.txt".to_owned(),
///         node_properties: None,
///     }],
///     node_properties: None,
/// };
///
/// let lookup: HashMap<String, &Directory> = [(child_digest.hash.clone(), &child)].into_iter().collect();
/// let tree_digest = digest(b"tree");
/// let mut file_targets = Vec::new();
/// materialize(&root, out.path(), &lookup, &tree_digest, &mut file_targets).unwrap();
///
/// assert_eq!(file_targets, vec![(out.path().join("sub/hello.txt"), file_digest, true)]);
/// assert!(out.path().join("sub").is_dir());
/// assert_eq!(
///     std::fs::read_link(out.path().join("link")).unwrap(),
///     std::path::Path::new("sub/hello.txt"),
/// );
/// ```
pub fn materialize(
    dir: &Directory,
    out_dir: &Path,
    lookup: &HashMap<String, &Directory>,
    tree_digest: &Digest,
    file_targets: &mut Vec<(PathBuf, Digest, bool)>,
) -> Result<(), Error> {
    fs::create_dir_all(out_dir).context(|| "creating directory", out_dir)?;

    for entry in &dir.directories {
        // A `DirectoryNode`/`FileNode` without a `digest` is a malformed
        // Tree, not something to silently paper over with an empty
        // default digest (see `MalformedResponse`'s doc comment for why
        // that's the wrong instinct here).
        let digest = entry.digest.clone().ok_or_else(|| Error::MalformedTree {
            hash: tree_digest.hash.clone(),
            size_bytes: tree_digest.size_bytes,
            reason: "DirectoryNode missing digest",
        })?;
        let child = *lookup
            .get(&digest.hash)
            .ok_or_else(|| Error::MalformedTree {
                hash: tree_digest.hash.clone(),
                size_bytes: tree_digest.size_bytes,
                reason: "DirectoryNode references a digest not present in Tree.children",
            })?;
        materialize(
            child,
            &out_dir.join(&entry.name),
            lookup,
            tree_digest,
            file_targets,
        )?;
    }

    for file in &dir.files {
        let digest = file.digest.clone().ok_or_else(|| Error::MalformedTree {
            hash: tree_digest.hash.clone(),
            size_bytes: tree_digest.size_bytes,
            reason: "FileNode missing digest",
        })?;
        file_targets.push((out_dir.join(&file.name), digest, file.is_executable));
    }

    for symlink in &dir.symlinks {
        let link_path = out_dir.join(&symlink.name);
        std::os::unix::fs::symlink(&symlink.target, &link_path).context(
            || format!("creating symlink to {} at", symlink.target),
            &link_path,
        )?;
    }

    Ok(())
}

/// A digest we asked `BatchReadBlobs` for but got no entry back for at all
/// (as opposed to an entry with a non-OK status, which `RemoteClient`
/// already turns into `Error::BlobStatus`).
fn no_response_for(digest: &Digest) -> Error {
    Error::BlobStatus {
        hash: digest.hash.clone(),
        size_bytes: digest.size_bytes,
        status: "server returned no response for this digest".to_string(),
    }
}
