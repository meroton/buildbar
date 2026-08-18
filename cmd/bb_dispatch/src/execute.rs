use crate::digest::digest;
use anyhow::Context;
use anyhow::Result;
use anyhow::bail;
use bazel_remote_apis::build::bazel::remote::execution::v2::Action;
use bazel_remote_apis::build::bazel::remote::execution::v2::Command;
use bazel_remote_apis::build::bazel::remote::execution::v2::Digest;
use bazel_remote_apis::build::bazel::remote::execution::v2::Directory;
use bazel_remote_apis::build::bazel::remote::execution::v2::DirectoryNode;
use bazel_remote_apis::build::bazel::remote::execution::v2::ExecuteRequest;
use bazel_remote_apis::build::bazel::remote::execution::v2::ExecuteResponse;
use bazel_remote_apis::build::bazel::remote::execution::v2::Platform;
use bazel_remote_apis::build::bazel::remote::execution::v2::execution_client::ExecutionClient;
use bazel_remote_apis::build::bazel::remote::execution::v2::platform;
use bazel_remote_apis::google::bytestream::ReadRequest;
use bazel_remote_apis::google::bytestream::WriteRequest;
use bazel_remote_apis::google::bytestream::byte_stream_client::ByteStreamClient;
use bazel_remote_apis::google::longrunning::operation::Result as OperationResult;
use clap::Parser;
use futures_util::StreamExt;
use hyper_util::rt::TokioIo;
use prost::Message;
use serde::Deserialize;
use serde::Serialize;
use std::collections::BTreeMap;
use std::io::Write;
use std::path::Path;
use std::path::PathBuf;
use std::process::Command as ProcessCommand;
use std::process::ExitCode;
use std::process::Stdio;
use tokio::net::UnixStream;
use tonic::Request;
use tonic::metadata::AsciiMetadataKey;
use tonic::metadata::AsciiMetadataValue;
use tonic::metadata::MetadataMap;
use tonic::transport::Certificate;
use tonic::transport::Channel;
use tonic::transport::ClientTlsConfig;
use tonic::transport::Endpoint;
use tonic::transport::Uri;
use tower::service_fn;
use uuid::Uuid;

#[derive(Parser)]
pub struct Args {
    #[arg(long)]
    endpoint: String,
    #[arg(long, default_value = "")]
    instance_name: String,
    #[arg(long)]
    ca_cert: Option<PathBuf>,
    #[arg(long)]
    credential_helper: Option<PathBuf>,
    #[arg(long)]
    credential_helper_uri: Option<String>,
    #[arg(required = true)]
    spec: Vec<PathBuf>,
    #[arg(long, default_value_t = std::thread::available_parallelism().map(usize::from).unwrap_or(1))]
    jobs: usize,
}

struct ActionOutput {
    stdout: Vec<u8>,
    stderr: Vec<u8>,
    exit_code: i32,
}

#[derive(Deserialize)]
struct ReV2ExecuteSpec {
    #[serde(default)]
    input_trees: Vec<ReV2InputTreeSpec>,
    #[serde(default)]
    inputs: Vec<ReV2InputSpec>,
    arguments: Vec<String>,
    platform: BTreeMap<String, String>,
}

#[derive(Deserialize)]
struct ReV2InputTreeSpec {
    path: String,
    root_digest: ReV2DigestSpec,
}

#[derive(Deserialize)]
struct ReV2InputSpec {
    path: String,
    digest: ReV2DigestSpec,
    #[serde(default)]
    is_executable: bool,
}

#[derive(Deserialize)]
struct ReV2DigestSpec {
    hash: String,
    size_bytes: i64,
}

type Blob = (Digest, Vec<u8>);
type InputRoot = (Digest, Vec<u8>, Vec<Blob>);

const WRITE_CHUNK_SIZE: usize = 1024 * 1024;

struct UploadState {
    data: Vec<u8>,
    offset: usize,
}

pub async fn run(args: Args) -> Result<ExitCode> {
    if args.jobs == 0 {
        bail!("--jobs must be greater than zero");
    }
    let channel = connect(&args.endpoint, args.ca_cert.as_deref()).await?;
    let metadata = credential_metadata(
        args.credential_helper.as_deref(),
        args.credential_helper_uri.as_deref(),
    )?;
    let instance_name = &args.instance_name;

    let results = futures_util::stream::iter(args.spec)
        .map(|spec| {
            let channel = channel.clone();
            let metadata = metadata.clone();
            async move {
                let result = run_one(channel, metadata, instance_name, spec.clone()).await;
                (spec, result)
            }
        })
        .buffer_unordered(args.jobs);
    futures_util::pin_mut!(results);

    let mut had_failure = false;
    while let Some((spec, result)) = results.next().await {
        match result {
            Ok(output) => {
                print_action_output(&output)?;
                had_failure |= output.exit_code != 0;
            }
            Err(error) => {
                eprintln!("{}: {error:#}", spec.display());
                had_failure = true;
            }
        }
    }

    Ok(if had_failure {
        ExitCode::FAILURE
    } else {
        ExitCode::SUCCESS
    })
}

async fn run_one(
    channel: Channel,
    metadata: MetadataMap,
    instance_name: &str,
    spec_path: PathBuf,
) -> Result<ActionOutput> {
    let spec = read_re_v2_spec(&spec_path)?;

    let command = Command {
        arguments: spec.arguments.clone(),
        ..Default::default()
    };
    let command_blob = command.encode_to_vec();
    let command_digest = digest(&command_blob);

    let (root_digest, root_blob, mut directory_blobs) = input_root_from_spec(&spec)?;

    let action = Action {
        command_digest: Some(command_digest.clone()),
        input_root_digest: Some(root_digest.clone()),
        do_not_cache: true,
        platform: Some(platform_from_spec(&spec)),
        ..Default::default()
    };
    let action_blob = action.encode_to_vec();
    let action_digest = digest(&action_blob);

    let mut blobs = vec![
        (command_digest, command_blob),
        (action_digest.clone(), action_blob),
    ];
    if !root_blob.is_empty() {
        blobs.push((root_digest.clone(), root_blob));
    }
    blobs.append(&mut directory_blobs);
    upload_blobs(channel.clone(), instance_name, &metadata, blobs).await?;

    let response = execute(channel.clone(), instance_name, &metadata, action_digest).await?;
    if let Some(status) = &response.status
        && status.code != 0
    {
        bail!(
            "remote execution failed with status {}: {}",
            status.code,
            status.message
        );
    }
    let result = response
        .result
        .context("execute response did not contain an ActionResult")?;
    let exit_code = result.exit_code;
    let stdout = if !result.stdout_raw.is_empty() {
        result.stdout_raw
    } else if let Some(stdout_digest) = result.stdout_digest {
        read_blob(channel.clone(), instance_name, &metadata, &stdout_digest).await?
    } else {
        Vec::new()
    };
    let stderr = if !result.stderr_raw.is_empty() {
        result.stderr_raw
    } else if let Some(stderr_digest) = result.stderr_digest {
        read_blob(channel.clone(), instance_name, &metadata, &stderr_digest).await?
    } else {
        Vec::new()
    };

    Ok(ActionOutput {
        stdout,
        stderr,
        exit_code,
    })
}

fn print_action_output(output: &ActionOutput) -> Result<()> {
    if !output.stdout.is_empty() {
        std::io::stdout()
            .write_all(&output.stdout)
            .context("failed to write remote stdout")?;
    }
    if !output.stderr.is_empty() {
        std::io::stderr()
            .write_all(&output.stderr)
            .context("failed to write remote stderr")?;
    }
    if output.stdout.is_empty() && output.stderr.is_empty() {
        eprintln!(
            "remote action produced no stdout/stderr; exit_code={}",
            output.exit_code
        );
    }
    Ok(())
}

fn read_re_v2_spec(path: &Path) -> Result<ReV2ExecuteSpec> {
    let bytes =
        std::fs::read(path).with_context(|| format!("failed to read {}", path.display()))?;
    let spec: ReV2ExecuteSpec = serde_json::from_slice(&bytes)
        .with_context(|| format!("failed to decode {}", path.display()))?;
    Ok(spec)
}

#[derive(Default)]
struct DirectoryComposer {
    files: BTreeMap<String, bazel_remote_apis::build::bazel::remote::execution::v2::FileNode>,
    directories: BTreeMap<String, DirectoryComposer>,
    mounted_directories: BTreeMap<String, Digest>,
}

fn input_root_from_spec(spec: &ReV2ExecuteSpec) -> Result<InputRoot> {
    if spec.input_trees.len() == 1 && spec.input_trees[0].path == "." && spec.inputs.is_empty() {
        return Ok((
            Digest {
                hash: spec.input_trees[0].root_digest.hash.clone(),
                size_bytes: spec.input_trees[0].root_digest.size_bytes,
            },
            Vec::new(),
            Vec::new(),
        ));
    }

    let mut composer = DirectoryComposer::default();
    for input in &spec.inputs {
        composer.add_file(input)?;
    }
    for input_tree in &spec.input_trees {
        composer.add_input_tree(input_tree)?;
    }
    let mut blobs = Vec::new();
    let directory = composer.into_directory(&mut blobs)?;
    let blob = directory.encode_to_vec();
    let root_digest = digest(&blob);
    Ok((root_digest, blob, blobs))
}

impl DirectoryComposer {
    fn add_file(&mut self, input: &ReV2InputSpec) -> Result<()> {
        let components = spec_path_components(&input.path)?;
        let (name, parents) = components
            .split_last()
            .context("file input path must contain at least one component")?;
        let directory = self.directory_mut(parents)?;
        directory.ensure_name_available(name)?;
        directory.files.insert(
            name.clone(),
            bazel_remote_apis::build::bazel::remote::execution::v2::FileNode {
                name: name.clone(),
                digest: Some(Digest {
                    hash: input.digest.hash.clone(),
                    size_bytes: input.digest.size_bytes,
                }),
                is_executable: input.is_executable,
                ..Default::default()
            },
        );
        Ok(())
    }

    fn add_input_tree(&mut self, input_tree: &ReV2InputTreeSpec) -> Result<()> {
        if input_tree.path == "." {
            bail!("root-mounted input tree cannot be combined with other inputs yet");
        }
        let components = spec_path_components(&input_tree.path)?;
        let (name, parents) = components
            .split_last()
            .context("input tree path must contain at least one component")?;
        let directory = self.directory_mut(parents)?;
        directory.ensure_name_available(name)?;
        directory.mounted_directories.insert(
            name.clone(),
            Digest {
                hash: input_tree.root_digest.hash.clone(),
                size_bytes: input_tree.root_digest.size_bytes,
            },
        );
        Ok(())
    }

    fn directory_mut(&mut self, components: &[String]) -> Result<&mut DirectoryComposer> {
        let mut directory = self;
        for component in components {
            if directory.files.contains_key(component)
                || directory.mounted_directories.contains_key(component)
            {
                bail!("path component `{component}` conflicts with an existing input");
            }
            directory = directory.directories.entry(component.clone()).or_default();
        }
        Ok(directory)
    }

    fn ensure_name_available(&self, name: &str) -> Result<()> {
        if self.files.contains_key(name)
            || self.directories.contains_key(name)
            || self.mounted_directories.contains_key(name)
        {
            bail!("duplicate input path component `{name}`");
        }
        Ok(())
    }

    fn into_directory(self, blobs: &mut Vec<Blob>) -> Result<Directory> {
        let mut directories = Vec::new();
        for (name, child) in self.directories {
            let child_directory = child.into_directory(blobs)?;
            let child_blob = child_directory.encode_to_vec();
            let child_digest = digest(&child_blob);
            blobs.push((child_digest.clone(), child_blob));
            directories.push(DirectoryNode {
                name,
                digest: Some(child_digest),
            });
        }
        for (name, digest) in self.mounted_directories {
            directories.push(DirectoryNode {
                name,
                digest: Some(digest),
            });
        }
        directories.sort_by(|left, right| left.name.cmp(&right.name));

        Ok(Directory {
            files: self.files.into_values().collect(),
            directories,
            ..Default::default()
        })
    }
}

fn spec_path_components(path: &str) -> Result<Vec<String>> {
    if path.is_empty() || path.starts_with('/') {
        bail!("input path must be relative: `{path}`");
    }
    let mut components = Vec::new();
    for component in path.split('/') {
        if component.is_empty() || component == "." || component == ".." {
            bail!("unsupported input path `{path}`");
        }
        components.push(component.to_owned());
    }
    Ok(components)
}

fn platform_from_spec(spec: &ReV2ExecuteSpec) -> Platform {
    Platform {
        properties: spec
            .platform
            .iter()
            .map(|(name, value)| platform::Property {
                name: name.clone(),
                value: value.clone(),
            })
            .collect(),
    }
}

async fn read_blob(
    channel: Channel,
    instance_name: &str,
    metadata: &MetadataMap,
    expected_digest: &Digest,
) -> Result<Vec<u8>> {
    let expected_size = usize::try_from(expected_digest.size_bytes)
        .with_context(|| format!("invalid blob size: {}", expected_digest.size_bytes))?;
    let mut client = ByteStreamClient::new(channel);
    let resource_name = read_resource_name(instance_name, expected_digest);
    let mut request = Request::new(ReadRequest {
        resource_name: resource_name.clone(),
        read_offset: 0,
        read_limit: 0,
    });
    *request.metadata_mut() = metadata.clone();
    request.metadata_mut().insert(
        "build.bazel.remote.execution.v2.resource-name",
        AsciiMetadataValue::try_from(resource_name.as_str())?,
    );

    let mut stream = client.read(request).await?.into_inner();
    let mut data = Vec::new();
    while let Some(chunk) = stream.message().await? {
        let received_size = data
            .len()
            .checked_add(chunk.data.len())
            .context("received blob size overflow")?;
        if received_size > expected_size {
            bail!(
                "blob size exceeded digest: expected {} bytes, received at least {}",
                expected_size,
                received_size
            );
        }
        data.extend_from_slice(&chunk.data);
    }
    if data.len() != expected_size {
        bail!(
            "blob size did not match digest: expected {} bytes, received {}",
            expected_size,
            data.len()
        );
    }
    let actual_digest = digest(&data);
    if actual_digest.hash != expected_digest.hash {
        bail!(
            "blob hash did not match digest: expected {}, received {}",
            expected_digest.hash,
            actual_digest.hash
        );
    }
    Ok(data)
}

fn read_resource_name(instance_name: &str, digest: &Digest) -> String {
    let blob = format!("blobs/{}/{}", digest.hash, digest.size_bytes);

    if instance_name.is_empty() {
        blob
    } else {
        format!("{}/{}", instance_name.trim_end_matches('/'), blob)
    }
}

async fn connect(endpoint: &str, ca_cert: Option<&Path>) -> Result<Channel> {
    if let Some(path) = endpoint.strip_prefix("unix://") {
        let path = path.to_owned();
        let endpoint = Endpoint::try_from("http://[::]:50051")?;
        return endpoint
            .connect_with_connector(service_fn(move |_: Uri| {
                let path = path.clone();
                async move { UnixStream::connect(path).await.map(TokioIo::new) }
            }))
            .await
            .context("failed to connect to REv2 Unix socket");
    }

    let endpoint = normalize_endpoint(endpoint)?;
    let mut channel = Channel::from_shared(endpoint.clone())?;
    if endpoint.starts_with("https://") {
        let mut tls = ClientTlsConfig::new();
        if let Some(ca_cert) = ca_cert {
            let pem = std::fs::read(ca_cert)
                .with_context(|| format!("failed to read CA certificate {}", ca_cert.display()))?;
            tls = tls.ca_certificate(Certificate::from_pem(pem));
        }
        channel = channel.tls_config(tls)?;
    }
    channel
        .connect()
        .await
        .context("failed to connect to REv2 endpoint")
}

fn normalize_endpoint(endpoint: &str) -> Result<String> {
    if let Some(rest) = endpoint.strip_prefix("grpc://") {
        return Ok(format!("http://{rest}"));
    }
    if let Some(rest) = endpoint.strip_prefix("grpcs://") {
        return Ok(format!("https://{rest}"));
    }
    if endpoint.starts_with("http://") || endpoint.starts_with("https://") {
        return Ok(endpoint.to_owned());
    }

    bail!("unsupported endpoint scheme; use grpc://, grpcs://, http://, or https://")
}

async fn upload_blobs(
    channel: Channel,
    instance_name: &str,
    metadata: &MetadataMap,
    blobs: Vec<Blob>,
) -> Result<()> {
    let mut client = ByteStreamClient::new(channel);
    for (digest, data) in blobs {
        let data_size = i64::try_from(data.len()).context("blob is too large to upload")?;
        if data_size != digest.size_bytes {
            bail!(
                "blob size did not match digest: expected {} bytes, found {}",
                digest.size_bytes,
                data_size
            );
        }
        let resource_name = upload_resource_name(instance_name, &digest);
        let resource_name_metadata = AsciiMetadataValue::try_from(resource_name.as_str())?;
        let first_end = WRITE_CHUNK_SIZE.min(data.len());
        // ByteStream requires the resource name only on the first request, so later chunks omit it
        // rather than cloning the same string for every request.
        let first_write = WriteRequest {
            resource_name,
            write_offset: 0,
            finish_write: first_end == data.len(),
            data: data[..first_end].to_vec(),
        };
        let remaining_writes = futures_util::stream::unfold(
            UploadState {
                data,
                offset: first_end,
            },
            |mut state| async move {
                if state.offset == state.data.len() {
                    return None;
                }
                let end = state
                    .offset
                    .saturating_add(WRITE_CHUNK_SIZE)
                    .min(state.data.len());
                let finish_write = end == state.data.len();
                let write = WriteRequest {
                    resource_name: String::new(),
                    write_offset: state.offset as i64,
                    finish_write,
                    data: state.data[state.offset..end].to_vec(),
                };
                state.offset = end;
                Some((write, state))
            },
        );
        let writes = futures_util::stream::iter([first_write]).chain(remaining_writes);
        let mut request = Request::new(writes);
        *request.metadata_mut() = metadata.clone();
        request.metadata_mut().insert(
            "build.bazel.remote.execution.v2.resource-name",
            resource_name_metadata,
        );
        let response = client.write(request).await?.into_inner();
        if response.committed_size != digest.size_bytes {
            bail!(
                "blob upload committed {} bytes, expected {}",
                response.committed_size,
                digest.size_bytes
            );
        }
    }
    Ok(())
}

fn upload_resource_name(instance_name: &str, digest: &Digest) -> String {
    let blob = format!("blobs/{}/{}", digest.hash, digest.size_bytes);
    let upload = format!("uploads/{}/{}", Uuid::new_v4(), blob);

    if instance_name.is_empty() {
        upload
    } else {
        format!("{}/{}", instance_name.trim_end_matches('/'), upload)
    }
}

async fn execute(
    channel: Channel,
    instance_name: &str,
    metadata: &MetadataMap,
    action_digest: Digest,
) -> Result<ExecuteResponse> {
    let mut client = ExecutionClient::new(channel);
    let mut request = Request::new(ExecuteRequest {
        instance_name: instance_name.to_owned(),
        action_digest: Some(action_digest),
        skip_cache_lookup: true,
        ..Default::default()
    });
    *request.metadata_mut() = metadata.clone();

    let mut stream = client.execute(request).await?.into_inner();

    while let Some(operation) = stream.message().await? {
        if !operation.done {
            continue;
        }
        return match operation.result {
            Some(OperationResult::Response(response)) => {
                let value = response.value;
                ExecuteResponse::decode(value.as_slice())
                    .context("failed to decode ExecuteResponse")
            }
            Some(OperationResult::Error(status)) => {
                bail!("execute operation failed: {}", status.message)
            }
            None => bail!("execute operation completed without a result"),
        };
    }

    bail!("execute stream ended before operation completed")
}

#[derive(Deserialize)]
struct CredentialHelperResponse {
    headers: BTreeMap<String, Vec<String>>,
}

#[derive(Serialize)]
struct CredentialHelperRequest<'a> {
    uri: &'a str,
}

fn credential_metadata(
    helper: Option<&Path>,
    credential_helper_uri: Option<&str>,
) -> Result<MetadataMap> {
    let Some(helper) = helper else {
        return Ok(MetadataMap::new());
    };
    let uri = credential_helper_uri
        .context("--credential-helper-uri is required when using --credential-helper")?;
    let mut child = ProcessCommand::new(helper)
        .arg("get")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .with_context(|| format!("failed to run credential helper {}", helper.display()))?;
    let write_result = (|| -> Result<()> {
        let mut stdin = child
            .stdin
            .take()
            .context("credential helper stdin was not available")?;
        serde_json::to_writer(&mut stdin, &CredentialHelperRequest { uri })
            .context("failed to write credential helper request")?;
        Ok(())
    })();
    let output = child
        .wait_with_output()
        .context("failed to wait for credential helper")?;
    write_result?;
    if !output.status.success() {
        bail!(
            "credential helper failed: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }

    let response: CredentialHelperResponse = serde_json::from_slice(&output.stdout)
        .context("failed to decode credential helper response")?;
    let mut metadata = MetadataMap::new();
    for (name, values) in response.headers {
        let name = AsciiMetadataKey::from_bytes(name.to_ascii_lowercase().as_bytes())?;
        for value in values {
            metadata.append(name.clone(), AsciiMetadataValue::try_from(value.as_str())?);
        }
    }
    Ok(metadata)
}
