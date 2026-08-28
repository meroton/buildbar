//! Shared REAPI (Remote Execution API v2) plumbing for this repo's Rust
//! tools — currently: content digesting and connecting to a REAPI server.
//! Internal to this repo, not published; the bare `reapi` name (no `bb-`
//! prefix) reflects that.

use std::path::Path;

use bazel_remote_apis::build::bazel::remote::execution::v2::Digest;
use prost::Message;
use sha2::{Digest as _, Sha256};
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint};

/// SHA-256 digest of a raw byte slice.
///
/// ```
/// use reapi::digest;
///
/// let d = digest(b"hello");
/// assert_eq!(d.size_bytes, 5);
/// assert_eq!(d.hash.len(), 64);
/// ```
// TODO: Parameterize the hash function?
pub fn digest(bytes: &[u8]) -> Digest {
    Digest {
        hash: hex::encode(Sha256::digest(bytes)),
        size_bytes: bytes.len() as i64,
    }
}

/// SHA-256 digest of a protobuf message's canonical serialized bytes.
pub fn digest_message(msg: &impl Message) -> Digest {
    digest(&msg.encode_to_vec())
}

/// A blob's content, tagged with the digest that addresses it in CAS.
///
/// Deliberately doesn't derive `Clone`: once a blob's content has been
/// moved into an upload call, callers only ever need the (cheap) `digest`
/// again, not the blob itself — cloning `digest` there keeps that cheap
/// and explicit, instead of `Blob` inviting an accidental clone of
/// potentially large `data`.
// TODO: Parameterize the hash function?
pub struct Blob {
    pub digest: Digest,
    pub data: Vec<u8>,
}

impl Blob {
    /// Digests `data` and wraps both together.
    ///
    /// ```
    /// use reapi::Blob;
    ///
    /// let blob = Blob::new(b"hello".to_vec());
    /// assert_eq!(blob.digest.size_bytes, 5);
    /// assert_eq!(blob.data, b"hello");
    /// ```
    pub fn new(data: Vec<u8>) -> Self {
        let digest = digest(&data);
        Self { digest, data }
    }

    /// Serializes and digests a protobuf message in one pass — unlike
    /// separately calling `digest_message(msg)` and `msg.encode_to_vec()`,
    /// which each serialize `msg` independently.
    pub fn from_message(msg: &impl Message) -> Self {
        let data = msg.encode_to_vec();
        let digest = digest(&data);
        Self { digest, data }
    }
}

/// Errors from [`connect`].
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("connecting to {remote}")]
    Connect {
        remote: String,
        #[source]
        source: tonic::transport::Error,
    },
    #[error("{remote:?}: unsupported endpoint scheme")]
    UnsupportedEndpoint { remote: String },
    #[error("reading CA certificate {path:?}")]
    ReadCaCert {
        path: std::path::PathBuf,
        #[source]
        source: std::io::Error,
    },
}

/// Connects to `remote`, which may be:
/// - `grpc://host:port` / `http://host:port` — plaintext.
/// - `grpcs://host:port` / `https://host:port` — TLS, optionally with
///   `ca_cert` as a custom CA (native root certificates are trusted
///   otherwise).
///
/// No authentication metadata (credential helpers, static headers) is
/// attached here yet — that's still to come.
pub async fn connect(remote: &str, ca_cert: Option<&Path>) -> Result<Channel, Error> {
    let normalized = normalize_endpoint(remote)?;
    let mut endpoint =
        Endpoint::from_shared(normalized.clone()).map_err(|source| Error::Connect {
            remote: remote.to_owned(),
            source,
        })?;
    if normalized.starts_with("https://") {
        let mut tls = ClientTlsConfig::new().with_native_roots();
        if let Some(path) = ca_cert {
            let pem = std::fs::read(path).map_err(|source| Error::ReadCaCert {
                path: path.to_owned(),
                source,
            })?;
            tls = tls.ca_certificate(Certificate::from_pem(pem));
        }
        endpoint = endpoint.tls_config(tls).map_err(|source| Error::Connect {
            remote: remote.to_owned(),
            source,
        })?;
    }
    endpoint.connect().await.map_err(|source| Error::Connect {
        remote: remote.to_owned(),
        source,
    })
}

/// Accepts `grpc://`/`grpcs://` as aliases for `http://`/`https://` (common
/// REAPI convention), and passes `http://`/`https://` through unchanged.
/// Anything else is rejected — better an evident error here than a
/// confusing failure once it reaches `Endpoint`.
fn normalize_endpoint(remote: &str) -> Result<String, Error> {
    if let Some(rest) = remote.strip_prefix("grpc://") {
        return Ok(format!("http://{rest}"));
    }
    if let Some(rest) = remote.strip_prefix("grpcs://") {
        return Ok(format!("https://{rest}"));
    }
    if remote.starts_with("http://") || remote.starts_with("https://") {
        return Ok(remote.to_owned());
    }
    Err(Error::UnsupportedEndpoint {
        remote: remote.to_owned(),
    })
}
