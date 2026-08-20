//! Standalone program (not a `#[test]`, not run by `cargo test`) that
//! exercises `run` against a real REAPI v2 CAS + ActionCache: builds a tiny
//! fixture input tree, runs a shell command through `run_cached` twice with
//! identical inputs, and asserts the second call was a cache hit — the
//! child never ran again — by checking a side-channel counter file the
//! command itself increments. Run manually against an already-running
//! server, e.g.:
//!
//! ```text
//! cargo run --bin run_check -- --remote http://localhost:8980
//! ```

#[path = "common/mod.rs"]
mod common;

use std::fs;
use std::path::PathBuf;

use bb_kv::client::{DEFAULT_MAX_MESSAGE_SIZE_BYTES, RemoteClient};
use bb_kv::error::{Error, IoResultExt, report};
use bb_kv::run::{RunOptions, run_cached};
use bb_kv::upload;
use clap::Parser;
use common::ScratchDir;

#[derive(Parser)]
struct Args {
    #[arg(long)]
    remote: String,
    #[arg(long, default_value = "")]
    instance_name: String,
    #[arg(long = "max-message-size", default_value_t = DEFAULT_MAX_MESSAGE_SIZE_BYTES)]
    max_message_size_bytes: usize,
    /// Don't clean up the fixture/scratch temp dirs, and print their
    /// paths, so a failure can be inspected afterward.
    #[arg(long)]
    keep_temporary_files: bool,
}

#[tokio::main]
async fn main() {
    if let Err(err) = run().await {
        report(&err);
        std::process::exit(1);
    }
}

async fn run() -> Result<(), Error> {
    let args = Args::parse();

    let fixture = ScratchDir::new("run-check-fixture", args.keep_temporary_files)?;
    let scratch = ScratchDir::new("run-check-scratch", args.keep_temporary_files)?;
    if args.keep_temporary_files {
        println!("fixture dir: {}", fixture.path().display());
        println!("scratch dir: {}", scratch.path().display());
    }

    let input = fixture.path().join("input");
    fs::create_dir(&input).context(|| "creating directory", &input)?;
    let hello = input.join("hello.txt");
    fs::write(&hello, b"hello").context(|| "writing", &hello)?;

    let mut client = RemoteClient::connect(&args.remote, args.instance_name.clone())
        .await?
        .with_max_message_size_bytes(args.max_message_size_bytes);
    let uploaded = upload::upload_directory(&mut client, &input).await?;

    // Declared output paths must be relative (REAPI's Command.output_paths
    // requirement, enforced by tree::reapi_path) — the same "wherever
    // bb-kv run was invoked from" basis run_cached documents for real
    // usage. A real caller would `cd` into their build directory first;
    // this check does the same into its scratch dir, then works in
    // relative paths from here on.
    std::env::set_current_dir(scratch.path())
        .context(|| "changing directory to", scratch.path())?;

    // The command's side effects: appending one byte to `counter` (proof it
    // actually executed, independent of anything else it does, and outside
    // any declared output so it's never restored by a cache hit), a
    // declared output file, and a declared output directory with a nested
    // file (to exercise both restore paths: direct blob writes and
    // download_tree).
    let counter = PathBuf::from("counter");
    let out_file = PathBuf::from("out.txt");
    let out_dir = PathBuf::from("out_dir");
    let nested_file = out_dir.join("nested/file.txt");
    // Everything else about this Action (input tree, argv text, output
    // paths) is identical across separate runs of this check binary, so
    // without a nonce, a second process invocation would be a genuine cache
    // hit against the *previous* invocation's real, persisted ActionCache
    // entry — correct behavior for `run`, but it would invalidate this
    // check's "the first call always actually executes" assumption. `:` is
    // the shell no-op builtin; it evaluates and discards its argument.
    let nonce = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    let argv: Vec<String> = vec![
        "sh".to_owned(),
        "-c".to_owned(),
        format!(
            ": {nonce}; echo ran to stdout; echo ran to stderr >&2; printf x >> {}; \
             echo produced > {}; mkdir -p {} && echo dir-content > {}",
            counter.display(),
            out_file.display(),
            nested_file.parent().unwrap().display(),
            nested_file.display(),
        ),
    ];

    let opts = || RunOptions {
        input_root_digest: uploaded.root_digest.clone(),
        argv: argv.clone(),
        output_files: vec![out_file.clone()],
        output_dirs: vec![out_dir.clone()],
        no_cache: false,
    };

    let first_exit = run_cached(&mut client, opts()).await?;
    assert_eq!(first_exit, 0, "first run: expected exit code 0");
    let after_first = fs::read_to_string(&counter).context(|| "reading", &counter)?;
    assert_eq!(
        after_first, "x",
        "first run: expected the command to have actually executed once"
    );
    let out_file_content = fs::read_to_string(&out_file).context(|| "reading", &out_file)?;
    assert_eq!(
        out_file_content,
        "produced\n",
        "first run: unexpected {} content",
        out_file.display()
    );
    let nested_content = fs::read_to_string(&nested_file).context(|| "reading", &nested_file)?;
    assert_eq!(
        nested_content,
        "dir-content\n",
        "first run: unexpected {} content",
        nested_file.display()
    );

    // Delete both declared outputs before the second (cached) call, so
    // their reappearing below is proof restoration actually happened, not
    // just that the first run's files were never cleaned up.
    fs::remove_file(&out_file).context(|| "removing", &out_file)?;
    fs::remove_dir_all(&out_dir).context(|| "removing", &out_dir)?;

    let second_exit = run_cached(&mut client, opts()).await?;
    assert_eq!(second_exit, 0, "second (cached) run: expected exit code 0");
    let after_second = fs::read_to_string(&counter).context(|| "reading", &counter)?;
    assert_eq!(
        after_second, "x",
        "second run: counter changed ({after_first:?} -> {after_second:?}) \
         - the command ran again instead of replaying the cached result"
    );
    let restored_out_file = fs::read_to_string(&out_file)
        .context(|| "reading", &out_file)
        .expect("second (cached) run: expected out.txt to have been restored");
    assert_eq!(
        restored_out_file,
        "produced\n",
        "second (cached) run: unexpected restored {} content",
        out_file.display()
    );
    let restored_nested = fs::read_to_string(&nested_file)
        .context(|| "reading", &nested_file)
        .expect("second (cached) run: expected the output directory to have been restored");
    assert_eq!(
        restored_nested,
        "dir-content\n",
        "second (cached) run: unexpected restored {} content",
        nested_file.display()
    );

    println!(
        "run_check: ok (ran once, second call was a cache hit, declared outputs correctly restored)"
    );
    Ok(())
}
