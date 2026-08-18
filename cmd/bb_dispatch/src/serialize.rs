use crate::digest::digest;
use anyhow::Context;
use anyhow::Result;
use anyhow::bail;
use bazel_remote_apis::build::bazel::remote::execution::v2::Digest;
use bazel_remote_apis::build::bazel::remote::execution::v2::Directory;
use bazel_remote_apis::build::bazel::remote::execution::v2::DirectoryNode;
use bazel_remote_apis::build::bazel::remote::execution::v2::FileNode;
use bazel_remote_apis::build::bazel::remote::execution::v2::SymlinkNode;
use clap::Parser;
use prost::Message;
use rayon::prelude::*;
use serde::Serialize;
use std::collections::BTreeMap;
use std::fs;
use std::path::Path;
use std::path::PathBuf;
use walkdir::WalkDir;

#[derive(Parser)]
pub struct Args {
    root: PathBuf,
    output: PathBuf,
}

#[derive(Default)]
struct DirectoryBuilder {
    files: BTreeMap<String, FileNode>,
    directories: BTreeMap<String, DirectoryBuilder>,
    symlinks: BTreeMap<String, SymlinkNode>,
}

struct PendingFile {
    components: Vec<String>,
    path: PathBuf,
    is_executable: bool,
}

#[derive(Serialize)]
struct Manifest {
    #[serde(rename = "type")]
    kind: &'static str,
    root_digest: ManifestDigest,
}

#[derive(Serialize)]
struct ManifestDigest {
    hash: String,
    size_bytes: i64,
}

pub fn run(args: Args) -> Result<()> {
    let root = args
        .root
        .canonicalize()
        .with_context(|| format!("failed to resolve {}", args.root.display()))?;
    if !root.is_dir() {
        bail!("{} is not a directory", root.display());
    }

    let mut builder = DirectoryBuilder::default();
    let mut pending_files = Vec::new();
    // Modifying the input tree during serialization is unsupported and may produce an
    // inconsistent snapshot because files are inspected here and read later.
    for entry in WalkDir::new(&root).follow_links(false).sort_by_file_name() {
        let entry = entry?;
        let path = entry.path();
        if path == root {
            continue;
        }
        let relative = path.strip_prefix(&root)?;
        let components = relative_components(relative)?;
        if entry.file_type().is_dir() {
            builder.ensure_dir(&components)?;
        } else if entry.file_type().is_file() {
            pending_files.push(PendingFile {
                components,
                path: path.to_owned(),
                is_executable: is_executable(path)?,
            });
        } else if entry.file_type().is_symlink() {
            let target = fs::read_link(path)
                .with_context(|| format!("failed to read symlink {}", path.display()))?;
            let target = validate_symlink_target(&root, path, &target)
                .with_context(|| format!("unsupported symlink target for {}", path.display()))?;
            builder.add_symlink(&components, target)?;
        } else {
            bail!("unsupported filesystem entry: {}", path.display());
        }
    }

    let files = pending_files
        .into_par_iter()
        .map(|file| {
            let bytes = fs::read(&file.path)
                .with_context(|| format!("failed to read {}", file.path.display()))?;
            Ok((file.components, digest(&bytes), file.is_executable))
        })
        .collect::<Result<Vec<_>>>()?;
    for (components, digest, is_executable) in files {
        builder.add_file(&components, digest, is_executable)?;
    }

    let (_root_directory, root_digest) = builder.into_directory();

    let manifest = Manifest {
        kind: "tree",
        root_digest: ManifestDigest {
            hash: root_digest.hash,
            size_bytes: root_digest.size_bytes,
        },
    };
    fs::write(
        &args.output,
        serde_json::to_string_pretty(&manifest)? + "\n",
    )
    .with_context(|| format!("failed to write {}", args.output.display()))?;

    Ok(())
}

impl DirectoryBuilder {
    fn ensure_dir(&mut self, components: &[String]) -> Result<()> {
        if components.is_empty() {
            return Ok(());
        }
        let (head, tail) = components.split_first().unwrap();
        self.directories
            .entry(head.clone())
            .or_default()
            .ensure_dir(tail)
    }

    fn add_file(
        &mut self,
        components: &[String],
        digest: Digest,
        is_executable: bool,
    ) -> Result<()> {
        let (file_name, parents) = components
            .split_last()
            .context("file path must contain at least one component")?;
        let mut directory = self;
        for parent in parents {
            directory = directory.directories.entry(parent.clone()).or_default();
        }
        if directory.files.contains_key(file_name)
            || directory.directories.contains_key(file_name)
            || directory.symlinks.contains_key(file_name)
        {
            bail!("duplicate directory entry `{file_name}`");
        }
        directory.files.insert(
            file_name.clone(),
            FileNode {
                name: file_name.clone(),
                digest: Some(digest),
                is_executable,
                ..Default::default()
            },
        );
        Ok(())
    }

    fn add_symlink(&mut self, components: &[String], target: String) -> Result<()> {
        let (link_name, parents) = components
            .split_last()
            .context("symlink path must contain at least one component")?;
        let mut directory = self;
        for parent in parents {
            directory = directory.directories.entry(parent.clone()).or_default();
        }
        if directory.files.contains_key(link_name)
            || directory.directories.contains_key(link_name)
            || directory.symlinks.contains_key(link_name)
        {
            bail!("duplicate directory entry `{link_name}`");
        }
        directory.symlinks.insert(
            link_name.clone(),
            SymlinkNode {
                name: link_name.clone(),
                target,
                ..Default::default()
            },
        );
        Ok(())
    }

    fn into_directory(self) -> (Directory, Digest) {
        let child_results = self
            .directories
            .into_par_iter()
            .map(|(name, child_builder)| {
                let (_child_directory, child_digest) = child_builder.into_directory();
                (name, child_digest)
            })
            .collect::<Vec<_>>();

        let mut directory_nodes = Vec::new();
        for (name, child_digest) in child_results {
            directory_nodes.push(DirectoryNode {
                name,
                digest: Some(child_digest),
            });
        }

        let directory = Directory {
            files: self.files.into_values().collect(),
            directories: directory_nodes,
            symlinks: self.symlinks.into_values().collect(),
            ..Default::default()
        };
        let digest = digest(&directory.encode_to_vec());
        (directory, digest)
    }
}

fn validate_symlink_target(root: &Path, link_path: &Path, target: &Path) -> Result<String> {
    if target.is_absolute() {
        bail!(
            "absolute symlink targets are not hermetic: {}",
            target.display()
        );
    }

    let link_parent = link_path
        .parent()
        .with_context(|| format!("symlink has no parent: {}", link_path.display()))?;
    let resolved = link_parent
        .join(target)
        .canonicalize()
        .with_context(|| format!("failed to resolve symlink target: {}", target.display()))?;
    if !resolved.starts_with(root) {
        bail!("symlink target escapes input root: {}", target.display());
    }

    let mut parts = Vec::new();
    for component in target.components() {
        match component {
            std::path::Component::Normal(part) => parts.push(
                part.to_str()
                    .with_context(|| {
                        format!("symlink target is not valid UTF-8: {}", target.display())
                    })?
                    .to_owned(),
            ),
            std::path::Component::CurDir => {}
            std::path::Component::ParentDir => parts.push("..".to_owned()),
            _ => bail!("unsupported symlink target: {}", target.display()),
        }
    }
    if parts.is_empty() {
        bail!("empty symlink target");
    }

    Ok(parts.join("/"))
}

fn relative_components(path: &Path) -> Result<Vec<String>> {
    let mut components = Vec::new();
    for component in path.components() {
        let std::path::Component::Normal(component) = component else {
            bail!("unsupported path component in {}", path.display());
        };
        let component = component
            .to_str()
            .with_context(|| format!("path is not valid UTF-8: {}", path.display()))?;
        if component == "." || component == ".." || component.contains('/') {
            bail!("unsupported path component `{component}`");
        }
        components.push(component.to_owned());
    }
    Ok(components)
}

fn is_executable(path: &Path) -> Result<bool> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        Ok(fs::metadata(path)?.permissions().mode() & 0o111 != 0)
    }
    #[cfg(not(unix))]
    {
        let _ = path;
        Ok(false)
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;
    use uuid::Uuid;

    #[test]
    fn rejects_escape_through_intermediate_symlink() -> Result<()> {
        let test_dir = std::env::temp_dir().join(format!(
            "private-bb-dispatch-symlink-test-{}",
            Uuid::new_v4()
        ));
        let root = test_dir.join("root");
        fs::create_dir_all(root.join("d"))?;
        fs::create_dir(root.join("x"))?;
        fs::create_dir(test_dir.join("victim"))?;
        let root = root.canonicalize()?;
        symlink("../x", root.join("d/link"))?;

        assert!(validate_symlink_target(&root, &root.join("d/link"), Path::new("../x")).is_ok());
        let result =
            validate_symlink_target(&root, &root.join("out"), Path::new("d/link/../../victim"));

        fs::remove_dir_all(test_dir)?;
        assert!(result.is_err());
        Ok(())
    }
}
