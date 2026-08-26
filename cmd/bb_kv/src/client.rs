use std::collections::{HashMap, HashSet};
use std::path::Path;

use bazel_remote_apis::build::bazel::remote::execution::v2::action_cache_client::ActionCacheClient;
use bazel_remote_apis::build::bazel::remote::execution::v2::content_addressable_storage_client::ContentAddressableStorageClient;
use bazel_remote_apis::build::bazel::remote::execution::v2::{
    ActionResult, BatchReadBlobsRequest, BatchUpdateBlobsRequest, Digest, FindMissingBlobsRequest,
    GetActionResultRequest, UpdateActionResultRequest, batch_update_blobs_request, compressor,
    digest_function,
};
use tonic::transport::{Certificate, Channel, ClientTlsConfig, Endpoint};

use crate::error::{Error, IoResultExt};
use crate::tree::Blob;

/// Default budget bb-kv stays under when sending a batched request
/// (`BatchUpdateBlobs`/`BatchReadBlobs`/`FindMissingBlobs`): gRPC's common
/// 4 MiB default max message size. This is a client-side sending budget,
/// not a value read from the server — matches bb-storage's
/// `maximum_received_message_size_bytes` in spirit (staying under some
/// message-size ceiling), but that field is the receiver's hard limit;
/// this is bb-kv's own choice of how hard to push against it.
pub const DEFAULT_MAX_MESSAGE_SIZE_BYTES: usize = 4 * 1024 * 1024;

/// A connection to a REAPI v2 Content Addressable Storage and ActionCache
/// service.
pub struct RemoteClient {
    inner: ContentAddressableStorageClient<Channel>,
    action_cache: ActionCacheClient<Channel>,
    instance_name: String,
    max_message_size_bytes: usize,
}

impl RemoteClient {
    /// Connects to `remote`, which may be:
    /// - `grpc://host:port` / `http://host:port` — plaintext.
    /// - `grpcs://host:port` / `https://host:port` — TLS, optionally with
    ///   `ca_cert` as a custom CA (native root certificates are trusted
    ///   otherwise).
    ///
    /// No authentication metadata (credential helpers, static headers) is
    /// attached here yet — that's still to come.
    pub async fn connect(
        remote: &str,
        instance_name: String,
        ca_cert: Option<&Path>,
    ) -> Result<Self, Error> {
        let channel = connect_channel(remote, ca_cert).await?;
        Ok(Self {
            inner: ContentAddressableStorageClient::new(channel.clone()),
            action_cache: ActionCacheClient::new(channel),
            instance_name,
            max_message_size_bytes: DEFAULT_MAX_MESSAGE_SIZE_BYTES,
        })
    }

    /// Overrides the per-request message-size budget bb-kv sends under
    /// (default [`DEFAULT_MAX_MESSAGE_SIZE_BYTES`]).
    #[must_use]
    pub fn with_max_message_size_bytes(mut self, max_message_size_bytes: usize) -> Self {
        self.max_message_size_bytes = max_message_size_bytes;
        self
    }

    /// Returns the subset of `digests` (by hash) not already present in CAS.
    pub async fn find_missing_blobs(
        &mut self,
        digests: &[Digest],
    ) -> Result<HashSet<String>, Error> {
        let mut missing = HashSet::new();
        for batch in chunk_by_size(digests.to_vec(), self.max_message_size_bytes, |d| {
            d.hash.len() + 8
        }) {
            let request = FindMissingBlobsRequest {
                instance_name: self.instance_name.clone(),
                blob_digests: batch,
                digest_function: digest_function::Value::Sha256 as i32,
            };
            let response = self
                .inner
                .find_missing_blobs(request)
                .await
                .map_err(|source| Error::Rpc {
                    rpc: "FindMissingBlobs",
                    instance_name: self.instance_name.clone(),
                    source,
                })?
                .into_inner();
            missing.extend(response.missing_blob_digests.into_iter().map(|d| d.hash));
        }
        Ok(missing)
    }

    /// Uploads `blobs` via `BatchUpdateBlobs`.
    pub async fn upload_blobs(&mut self, blobs: Vec<Blob>) -> Result<(), Error> {
        let items: Vec<_> = blobs
            .into_iter()
            .map(|blob| batch_update_blobs_request::Request {
                digest: Some(blob.digest),
                data: blob.data,
                compressor: compressor::Value::Identity as i32,
            })
            .collect();

        for batch in chunk_by_size(items, self.max_message_size_bytes, |item| {
            item.data.len() + item.digest.as_ref().map_or(0, |d| d.hash.len())
        }) {
            let request = BatchUpdateBlobsRequest {
                instance_name: self.instance_name.clone(),
                requests: batch,
                digest_function: digest_function::Value::Sha256 as i32,
            };
            let response = self
                .inner
                .batch_update_blobs(request)
                .await
                .map_err(|source| Error::Rpc {
                    rpc: "BatchUpdateBlobs",
                    instance_name: self.instance_name.clone(),
                    source,
                })?
                .into_inner();
            for item in response.responses {
                check_blob_status("BatchUpdateBlobs", item.digest, item.status)?;
            }
        }
        Ok(())
    }

    /// Fetches `digests` via `BatchReadBlobs`.
    /// Returns content keyed by hash.
    pub async fn download_blobs(
        &mut self,
        digests: &[Digest],
    ) -> Result<HashMap<String, Vec<u8>>, Error> {
        let mut result = HashMap::new();
        for batch in chunk_by_size(digests.to_vec(), self.max_message_size_bytes, |d| {
            d.hash.len() + d.size_bytes.max(0) as usize
        }) {
            let request = BatchReadBlobsRequest {
                instance_name: self.instance_name.clone(),
                digests: batch,
                acceptable_compressors: vec![compressor::Value::Identity as i32],
                digest_function: digest_function::Value::Sha256 as i32,
            };
            let response = self
                .inner
                .batch_read_blobs(request)
                .await
                .map_err(|source| Error::Rpc {
                    rpc: "BatchReadBlobs",
                    instance_name: self.instance_name.clone(),
                    source,
                })?
                .into_inner();
            for item in response.responses {
                let digest = check_blob_status("BatchReadBlobs", item.digest, item.status)?;
                result.insert(digest.hash, item.data);
            }
        }
        Ok(result)
    }

    /// Uploads whichever of `blobs` aren't already in CAS: a
    /// `find_missing_blobs` check followed by `upload_blobs` on just the
    /// misses. The one shared "push blobs, deduped" entry point — every
    /// blob upload in this crate (an uploaded directory's files and
    /// `Directory` messages, a `run` action's `Command`/`Action`/stdout/
    /// stderr blobs) goes through this instead of re-doing the check
    /// inline.
    pub async fn upload_if_missing(&mut self, blobs: Vec<Blob>) -> Result<(), Error> {
        let digests: Vec<Digest> = blobs.iter().map(|blob| blob.digest.clone()).collect();
        let missing = self.find_missing_blobs(&digests).await?;
        let to_upload: Vec<Blob> = blobs
            .into_iter()
            .filter(|blob| missing.contains(&blob.digest.hash))
            .collect();
        self.upload_blobs(to_upload).await
    }

    /// Looks up `action_digest` in the ActionCache. `Ok(None)` means a
    /// cache miss (the server's `NotFound` is the documented way to say
    /// "no result for this Action"); any other RPC error is a real failure,
    /// not a miss.
    pub async fn action_result(
        &mut self,
        action_digest: &Digest,
    ) -> Result<Option<ActionResult>, Error> {
        let request = GetActionResultRequest {
            instance_name: self.instance_name.clone(),
            action_digest: Some(action_digest.clone()),
            inline_stdout: true,
            inline_stderr: true,
            inline_output_files: Vec::new(),
            digest_function: digest_function::Value::Sha256 as i32,
        };
        match self.action_cache.get_action_result(request).await {
            Ok(response) => Ok(Some(response.into_inner())),
            Err(source) if source.code() == tonic::Code::NotFound => Ok(None),
            Err(source) => Err(Error::Rpc {
                rpc: "GetActionResult",
                instance_name: self.instance_name.clone(),
                source,
            }),
        }
    }

    /// Stores `result` in the ActionCache under `action_digest`.
    pub async fn update_action_result(
        &mut self,
        action_digest: &Digest,
        result: ActionResult,
    ) -> Result<(), Error> {
        let request = UpdateActionResultRequest {
            instance_name: self.instance_name.clone(),
            action_digest: Some(action_digest.clone()),
            action_result: Some(result),
            results_cache_policy: None,
            digest_function: digest_function::Value::Sha256 as i32,
        };
        self.action_cache
            .update_action_result(request)
            .await
            .map_err(|source| Error::Rpc {
                rpc: "UpdateActionResult",
                instance_name: self.instance_name.clone(),
                source,
            })?;
        Ok(())
    }
}

/// Resolves `remote` to a connected `Channel`, applying TLS if the
/// (normalized) scheme calls for it.
async fn connect_channel(remote: &str, ca_cert: Option<&Path>) -> Result<Channel, Error> {
    let normalized = normalize_endpoint(remote)?;
    let mut endpoint =
        Endpoint::from_shared(normalized.clone()).map_err(|source| Error::Connect {
            remote: remote.to_owned(),
            source,
        })?;
    if normalized.starts_with("https://") {
        let mut tls = ClientTlsConfig::new().with_native_roots();
        if let Some(path) = ca_cert {
            let pem = std::fs::read(path).context(|| "reading CA certificate", path)?;
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

/// Splits `items` into batches, flushing whenever the next item would push
/// the message-size budget over its limit. A single item larger than the
/// budget still gets sent, alone, as its own batch (true streaming for
/// oversized blobs is out of scope for v1).
///
/// No separate item-count cap: REAPI doesn't document one independent of
/// total message size, so there's nothing concrete to defend against
/// beyond the message-size budget itself, which already bounds request
/// size regardless of how many items make it up.
fn chunk_by_size<T>(
    items: Vec<T>,
    max_message_size_bytes: usize,
    size_of: impl Fn(&T) -> usize,
) -> Vec<Vec<T>> {
    let mut batches = Vec::new();
    let mut current = Vec::new();
    let mut current_size = 0;

    for item in items {
        let item_size = size_of(&item);
        if !current.is_empty() && current_size + item_size > max_message_size_bytes {
            batches.push(std::mem::take(&mut current));
            current_size = 0;
        }
        current_size += item_size;
        current.push(item);
    }
    if !current.is_empty() {
        batches.push(current);
    }
    batches
}

/// Checks one item's `status` from a `BatchUpdateBlobs`/`BatchReadBlobs`
/// response — a successful RPC does not mean every blob in the batch
/// succeeded. Returns the digest on success.
///
/// `digest` being absent is treated as `Error::MalformedResponse`: there's
/// no legitimate reason for a server to omit it, and defaulting it would
/// silently drop the response (nothing would ever look up hash `""`).
/// `status` is different: verified empirically against a real Buildbarn
/// deployment, which omits `status` entirely on successful items rather
/// than sending an explicit `Status { code: 0, .. }` — the spec doesn't
/// document this either way, but it's evidently the real-world convention,
/// so absent is treated as OK rather than rejected.
fn check_blob_status(
    rpc: &'static str,
    digest: Option<Digest>,
    status: Option<bazel_remote_apis::google::rpc::Status>,
) -> Result<Digest, Error> {
    let digest = digest.ok_or(Error::MalformedResponse {
        rpc,
        reason: "response item missing digest",
    })?;
    let status = status.unwrap_or_default(); // NB: absent means success
    if status.code != 0 {
        return Err(Error::BlobStatus {
            hash: digest.hash,
            size_bytes: digest.size_bytes,
            status: format!("{} (code {})", status.message, status.code),
        });
    }
    Ok(digest)
}
