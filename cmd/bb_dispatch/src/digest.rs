use bazel_remote_apis::build::bazel::remote::execution::v2::Digest;
use sha2::Digest as ShaDigest;
use sha2::Sha256;

pub fn digest(bytes: &[u8]) -> Digest {
    let hash = Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect();
    Digest {
        hash,
        size_bytes: bytes.len() as i64,
    }
}
