//! Two different digests identify a directory tree in this crate, and it's
//! easy to reach for the wrong one — this module doc exists to make the
//! difference stick.
//!
//! - **The root `Directory` digest** (`build_directory(path)?.digest`,
//!   printed by the `digest`/`upload` CLI commands) is the digest of just
//!   one `Directory` proto: the top-level names, file/subdirectory digests,
//!   and symlinks directly inside `path`. Every subdirectory has its own,
//!   separate `Directory` digest, addressed independently in CAS. This is
//!   what REAPI's `Action.input_root_digest` and
//!   `OutputDirectory.root_directory_digest` mean by "a directory digest" —
//!   it's the standard, portable REAPI concept, and what `run
//!   --directory-digest` consumes directly with no extra lookup.
//!
//! - **The `Tree` digest** (`tree_digest(path)`, or the `tree_digest` half
//!   of [`upload::UploadedTree`](crate::upload::UploadedTree)) is the
//!   digest of a *different*, larger proto: a `Tree` message that flattens
//!   the root `Directory` plus every descendant `Directory` into one
//!   self-describing blob. Fetching it needs exactly one CAS read
//!   regardless of tree depth. This is what REAPI's
//!   `OutputDirectory.tree_digest` means (that field is specified as a
//!   `Tree`-message digest, not a `Directory` digest) — so it's needed
//!   whenever `run` records/restores an action's output directories.
//!
//! Both digests exist in CAS for anything uploaded via
//! `upload::upload_directory` (it always builds and pushes both). Which one
//! a caller *has in hand* determines which download path is efficient:
//! given a root `Directory` digest, [`download::download_from_root`]
//! breadth-first-walks individual `Directory` blobs (round trips scale with
//! tree depth); given a `Tree` digest, [`download::download_tree`] fetches
//! the one flattened blob directly. `bb-memoize download` exposes both as
//! `--directory-digest`/`--tree-digest` for exactly this reason — use
//! whichever one you already have.
//!
//! [`download::download_from_root`]: crate::download::download_from_root
//! [`download::download_tree`]: crate::download::download_tree

use std::collections::BTreeMap;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};

use bazel_remote_apis::build::bazel::remote::execution::v2::{
    Digest, Directory, DirectoryNode, FileNode, SymlinkNode, Tree,
};
use reapi::{Blob, digest_message};

use crate::error::{Error, IoResultExt};

/// Parses a `<hex-hash>/<size-bytes>` digest string, as printed by the
/// `digest`/`upload` commands.
///
/// ```
/// use bb_memoize::tree::parse_digest;
///
/// let digest = parse_digest(
///     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855/0",
/// )
/// .unwrap();
/// assert_eq!(digest.size_bytes, 0);
///
/// assert!(parse_digest("not-a-digest").is_err());
/// assert!(parse_digest("tooshort/0").is_err());
/// ```
pub fn parse_digest(s: &str) -> Result<Digest, Error> {
    let invalid = || Error::InvalidDigest {
        input: s.to_owned(),
    };
    let (hash, size_bytes) = s.rsplit_once('/').ok_or_else(invalid)?;
    if hash.len() != 64 || !hash.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(invalid());
    }
    let size_bytes: i64 = size_bytes.parse().map_err(|_| invalid())?;
    Ok(Digest {
        hash: hash.to_owned(),
        size_bytes,
    })
}

/// Formats a digest as `<hex-hash>/<size-bytes>`, the inverse of
/// [`parse_digest`].
///
/// ```
/// use bb_memoize::tree::format_digest;
/// use reapi::digest;
///
/// let digest = digest(b"hello");
/// assert_eq!(format_digest(&digest), format!("{}/{}", digest.hash, digest.size_bytes));
/// ```
pub fn format_digest(digest: &Digest) -> String {
    format!("{}/{}", digest.hash, digest.size_bytes)
}

/// A directory, built bottom-up: its own `Directory` message and digest,
/// together with every descendant `Directory` message (flattened, not
/// including itself — this is exactly the shape `Tree.children` wants) and
/// every file's content blob encountered along the way (so callers don't
/// need to re-read the filesystem to upload them).
pub struct BuiltDirectory {
    pub directory: Directory,
    pub digest: Digest,
    pub descendants: Vec<Directory>,
    pub file_blobs: Vec<Blob>,
}

/// Recursively walks `path`, building the REAPI v2 `Directory` tree rooted
/// there.
///
/// ```
/// use bb_memoize::tree::build_directory;
/// use std::fs;
///
/// let dir = tempfile::Builder::new().prefix("bb-memoize-doctest-build_directory-").tempdir().unwrap();
/// fs::write(dir.path().join("hello.txt"), b"hello").unwrap();
///
/// let built = build_directory(dir.path()).unwrap();
/// assert_eq!(built.directory.files.len(), 1);
/// assert_eq!(built.directory.files[0].name, "hello.txt");
/// assert_eq!(built.digest.hash.len(), 64);
/// ```
pub fn build_directory(path: &Path) -> Result<BuiltDirectory, Error> {
    let mut files = Vec::new();
    let mut directories = Vec::new();
    let mut symlinks = Vec::new();
    let mut descendants = Vec::new();
    let mut file_blobs = Vec::new();

    let entries = fs::read_dir(path).context(|| "listing directory entries in", path)?;
    for entry in entries {
        let entry = entry.context(|| "listing directory entries in", path)?;
        let name = entry.file_name().to_string_lossy().into_owned();
        let entry_path = entry.path();
        let file_type = entry
            .file_type()
            .context(|| "reading file type of", &entry_path)?;

        if file_type.is_symlink() {
            let target =
                fs::read_link(&entry_path).context(|| "reading symlink target for", &entry_path)?;
            symlinks.push(SymlinkNode {
                name,
                target: target.to_string_lossy().into_owned(),
                node_properties: None,
            });
        } else if file_type.is_dir() {
            let built = build_directory(&entry_path)?;
            directories.push(DirectoryNode {
                name,
                digest: Some(built.digest),
            });
            descendants.push(built.directory);
            descendants.extend(built.descendants);
            file_blobs.extend(built.file_blobs);
        } else {
            let content = fs::read(&entry_path).context(|| "reading", &entry_path)?;
            let blob = Blob::new(content);
            let is_executable = entry
                .metadata()
                .context(|| "reading metadata for", &entry_path)?
                .permissions()
                .mode()
                & 0o100
                != 0;
            let digest = blob.digest.clone();
            file_blobs.push(blob);
            files.push(FileNode {
                name,
                digest: Some(digest),
                is_executable,
                node_properties: None,
            });
        }
    }

    files.sort_by(|a, b| a.name.cmp(&b.name));
    directories.sort_by(|a, b| a.name.cmp(&b.name));
    symlinks.sort_by(|a, b| a.name.cmp(&b.name));

    let directory = Directory {
        files,
        directories,
        symlinks,
        node_properties: None,
    };
    let digest = digest_message(&directory);

    Ok(BuiltDirectory {
        directory,
        digest,
        descendants,
        file_blobs,
    })
}

/// Builds the REAPI v2 `Directory` tree rooted at `root`, containing only
/// the paths named in `filters` — each independently a file, a directory
/// (included fully and recursively, via [`build_directory`]), or a
/// symlink. With no filters, the whole `root` directory is included —
/// identical to `build_directory(root)`.
///
/// `filters` are read from disk exactly as given — relative to the current
/// working directory (so shell tab-completion works normally), *not*
/// relative to `root`. `root` only decides where each filter *lands in the
/// tree*: a filter's tree position is wherever it resolves to relative to
/// `root`, independent of how it was actually spelled. This split matters
/// in practice: switching `--root` to explore the same selection re-rooted
/// elsewhere doesn't require re-typing the filters too.
///
/// Re-rooting changes the digest even though the filters didn't change:
/// the *same* files on disk produce a *different* tree depending on where
/// `root` is set, since a `Directory` proto's shape is defined by what's
/// nested under what. Given `a/b/c` on disk,
/// `build_filtered_directory("a", &["a/b/c"])` and
/// `build_filtered_directory("a/b", &["a/b/c"])` describe the same file
/// but as different trees — `b/c` vs. just `c` at the top level.
///
/// A filter that doesn't resolve to somewhere inside `root` is an error,
/// as is a filter that overlaps another one already given (e.g. a
/// directory and something inside it) — ambiguity fails loudly rather
/// than being silently resolved one way or the other, matching this
/// crate's general stance (see e.g. `check_blob_status`'s doc comment in
/// `client.rs`).
///
/// ```
/// use bb_memoize::tree::build_filtered_directory;
/// use std::fs;
///
/// let dir = tempfile::Builder::new().prefix("bb-memoize-doctest-build_filtered_directory-").tempdir().unwrap();
/// fs::create_dir_all(dir.path().join("a/b")).unwrap();
/// fs::write(dir.path().join("a/b/c"), b"hello").unwrap();
/// fs::write(dir.path().join("a/excluded"), b"not part of the filter").unwrap();
///
/// // Rooted at "a": "b/c" is where the filter lands, "excluded" doesn't appear.
/// let filter = dir.path().join("a/b/c");
/// let rooted_at_a = build_filtered_directory(&dir.path().join("a"), &[filter.clone()]).unwrap();
/// assert_eq!(rooted_at_a.directory.files.len(), 0);
/// assert_eq!(rooted_at_a.directory.directories.len(), 1);
/// assert_eq!(rooted_at_a.directory.directories[0].name, "b");
///
/// // The exact same filter argument, rooted one level deeper: "c" is now a
/// // top-level file, and the resulting tree — so its digest — differs.
/// let rooted_at_a_b = build_filtered_directory(&dir.path().join("a/b"), &[filter]).unwrap();
/// assert_eq!(rooted_at_a_b.directory.files.len(), 1);
/// assert_eq!(rooted_at_a_b.directory.files[0].name, "c");
/// assert_ne!(rooted_at_a.digest.hash, rooted_at_a_b.digest.hash);
/// ```
pub fn build_filtered_directory(root: &Path, filters: &[PathBuf]) -> Result<BuiltDirectory, Error> {
    if filters.is_empty() {
        return build_directory(root);
    }

    let canonical_root = fs::canonicalize(root).context(|| "resolving --root", root)?;

    let mut trie: BTreeMap<String, FilterNode> = BTreeMap::new();
    for filter in filters {
        // Kind (and, for a file, the executable bit) comes from `filter`
        // exactly as given — relative to the working directory, not
        // `root` — so it tab-completes normally and doesn't need
        // re-typing when `--root` changes.
        let metadata = fs::symlink_metadata(filter).context(|| "reading metadata for", filter)?;
        let leaf = match metadata.file_type() {
            file_type if file_type.is_symlink() => FilterNode::Symlink(filter.clone()),
            file_type if file_type.is_dir() => FilterNode::Directory(filter.clone()),
            _ => {
                let is_executable = metadata.permissions().mode() & 0o100 != 0;
                FilterNode::File {
                    path: filter.clone(),
                    is_executable,
                }
            }
        };

        let components = position_relative_to_root(filter, &canonical_root)?;
        insert_filter(&mut trie, &components, leaf, filter)?;
    }
    build_group(&trie)
}

/// One filter's resolved kind, or a synthesized directory level holding a
/// mix of these — built purely from filter paths' shared prefixes, never
/// from an actual `fs::read_dir`.
enum FilterNode {
    File { path: PathBuf, is_executable: bool },
    Directory(PathBuf),
    Symlink(PathBuf),
    Group(BTreeMap<String, FilterNode>),
}

/// Resolves `filter` (as typed — relative to the working directory, or
/// absolute) to where it sits relative to `canonical_root`, and returns
/// that as path components: the position `filter` occupies in the
/// synthesized tree. Errors if `filter` doesn't resolve to somewhere
/// inside `canonical_root` at all.
///
/// Only `filter`'s *parent* is resolved through `fs::canonicalize` (which
/// also settles any `..`/`.`/symlinked-parent-directory weirdness in
/// `filter` itself) — not `filter`'s own final component — so a `filter`
/// that's itself a symlink keeps its own identity here rather than being
/// silently followed to whatever it points at.
fn position_relative_to_root(filter: &Path, canonical_root: &Path) -> Result<Vec<String>, Error> {
    let not_under_root = || Error::InvalidPath {
        path: filter.to_owned(),
        reason: "doesn't resolve to a distinct location inside --root",
    };

    let parent = match filter.parent() {
        Some(parent) if !parent.as_os_str().is_empty() => parent,
        _ => Path::new("."),
    };
    let name = filter.file_name().ok_or_else(not_under_root)?;
    let canonical_parent = fs::canonicalize(parent).context(|| "resolving parent of", filter)?;
    let absolute_filter = canonical_parent.join(name);

    let relative = absolute_filter
        .strip_prefix(canonical_root)
        .map_err(|_| not_under_root())?;
    let components: Vec<String> = relative
        .components()
        .map(|component| component.as_os_str().to_string_lossy().into_owned())
        .collect();
    if components.is_empty() {
        return Err(not_under_root());
    }
    Ok(components)
}

/// Inserts `leaf` into `trie` at `components`, creating synthesized `Group`
/// levels for every component but the last. Errors (reusing
/// [`Error::InvalidPath`]) if `original` overlaps a filter already
/// inserted — either it needs to descend through a slot that's already a
/// leaf, or it lands on a slot that's already occupied by anything.
fn insert_filter(
    trie: &mut BTreeMap<String, FilterNode>,
    components: &[String],
    leaf: FilterNode,
    original: &Path,
) -> Result<(), Error> {
    let overlap = || Error::InvalidPath {
        path: original.to_owned(),
        reason: "overlaps another --filter path (e.g. a directory and something inside it were both given)",
    };
    let (name, rest) = components
        .split_first()
        .expect("non-empty, checked by filter_components");
    if rest.is_empty() {
        if trie.contains_key(name) {
            return Err(overlap());
        }
        trie.insert(name.clone(), leaf);
        Ok(())
    } else {
        match trie
            .entry(name.clone())
            .or_insert_with(|| FilterNode::Group(BTreeMap::new()))
        {
            FilterNode::Group(children) => insert_filter(children, rest, leaf, original),
            _ => Err(overlap()),
        }
    }
}

/// Converts a synthesized trie level into a `BuiltDirectory`, recursing
/// into real directories (via `build_directory`) and synthesized child
/// groups alike. `BTreeMap` already iterates in sorted-by-name order, so
/// (unlike `build_directory`'s post-hoc `.sort_by`) each of `files`/
/// `directories`/`symlinks` ends up sorted for free — a sorted sequence's
/// subsequences are themselves sorted.
fn build_group(entries: &BTreeMap<String, FilterNode>) -> Result<BuiltDirectory, Error> {
    let mut files = Vec::new();
    let mut directories = Vec::new();
    let mut symlinks = Vec::new();
    let mut descendants = Vec::new();
    let mut file_blobs = Vec::new();

    for (name, node) in entries {
        match node {
            FilterNode::File {
                path,
                is_executable,
            } => {
                let content = fs::read(path).context(|| "reading", path)?;
                let blob = Blob::new(content);
                let digest = blob.digest.clone();
                file_blobs.push(blob);
                files.push(FileNode {
                    name: name.clone(),
                    digest: Some(digest),
                    is_executable: *is_executable,
                    node_properties: None,
                });
            }
            FilterNode::Symlink(path) => {
                let target = fs::read_link(path).context(|| "reading symlink target for", path)?;
                symlinks.push(SymlinkNode {
                    name: name.clone(),
                    target: target.to_string_lossy().into_owned(),
                    node_properties: None,
                });
            }
            FilterNode::Directory(path) => {
                let built = build_directory(path)?;
                directories.push(DirectoryNode {
                    name: name.clone(),
                    digest: Some(built.digest),
                });
                descendants.push(built.directory);
                descendants.extend(built.descendants);
                file_blobs.extend(built.file_blobs);
            }
            FilterNode::Group(children) => {
                let built = build_group(children)?;
                directories.push(DirectoryNode {
                    name: name.clone(),
                    digest: Some(built.digest),
                });
                descendants.push(built.directory);
                descendants.extend(built.descendants);
                file_blobs.extend(built.file_blobs);
            }
        }
    }

    let directory = Directory {
        files,
        directories,
        symlinks,
        node_properties: None,
    };
    let digest = digest_message(&directory);

    Ok(BuiltDirectory {
        directory,
        digest,
        descendants,
        file_blobs,
    })
}

/// Converts a relative filesystem path to a REAPI path string: forward-slash
/// separated, no leading or trailing slash. REAPI requires this exact form
/// for `Command.working_directory`, `Command.output_paths`, and
/// `OutputFile`/`OutputDirectory.path` — an absolute path is rejected since
/// every one of those fields is defined as relative to the working
/// directory.
///
/// ```
/// use bb_memoize::tree::reapi_path;
/// use std::path::Path;
///
/// assert_eq!(reapi_path(Path::new("out/report.txt")).unwrap(), "out/report.txt");
/// assert!(reapi_path(Path::new("/absolute/path")).is_err());
/// ```
pub fn reapi_path(path: &Path) -> Result<String, Error> {
    if path.is_absolute() {
        return Err(Error::InvalidPath {
            path: path.to_owned(),
            reason: "must be relative, REAPI paths are relative to the working directory",
        });
    }
    let s = path.to_string_lossy();
    Ok(if std::path::MAIN_SEPARATOR == '/' {
        s.into_owned()
    } else {
        s.replace(std::path::MAIN_SEPARATOR, "/")
    })
}

/// Computes the REAPI v2 Tree digest for `path`: walks the directory,
/// wraps the result in a `Tree` message (root + flattened descendants),
/// and digests that. Pure/offline — no network access.
///
/// ```
/// use bb_memoize::tree::tree_digest;
///
/// let dir = tempfile::Builder::new().prefix("bb-memoize-doctest-tree_digest-").tempdir().unwrap();
/// std::fs::write(dir.path().join("hello.txt"), b"hello").unwrap();
///
/// let digest = tree_digest(dir.path()).unwrap();
/// assert_eq!(digest.hash.len(), 64);
/// ```
pub fn tree_digest(path: &Path) -> Result<Digest, Error> {
    let built = build_directory(path)?;
    let tree = Tree {
        root: Some(built.directory),
        children: built.descendants,
    };
    Ok(digest_message(&tree))
}
