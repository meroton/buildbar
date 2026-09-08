//! REAPI v2 Content Addressable Storage + ActionCache operations, shared by
//! every tool in this repo that needs to push or pull blobs/directory trees
//! against a remote: `re-memoize` (uses it internally to cache `run`'s
//! inputs/outputs) and `re-directory` (exposes it directly as `upload`/
//! `download`). Built on top of `reapi`'s lower-level connect/digest
//! primitives, which carry no opinion about CAS or ActionCache at all —
//! this crate is that opinion.

pub mod client;
pub mod download;
pub mod error;
pub mod tree;
pub mod upload;
