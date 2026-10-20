local lintRustJob = {
  'runs-on': 'ubuntu-latest',
  name: 'lint_rust',
  steps: [
    {
      name: 'Check out source code',
      uses: 'actions/checkout@v1',
    },
    {
      name: 'Install Rust toolchain',
      run: 'rustup toolchain install',
    },
    // The fmt/clippy steps below don't reference this cache via any
    // flag — cargo's caching is purely path-based (it always checks
    // ~/.cargo/registry and target/ for existing state, no matter
    // what). This step's only job is to restore those paths from a
    // prior run before cargo runs, and to save them again afterward
    // (via its own automatic post-step, baked into the action).
    // Removing it doesn't break correctness, only silently slows
    // down every run from here on.
    {
      name: 'Rust cache',
      uses: 'Swatinem/rust-cache@v2',
      with: {
        workspaces: '.',
      },
    },
    {
      name: 'Rust: check formatting',
      run: 'cargo fmt --all --check',
    },
    // TODO: convert to Bazel rust_clippy/rustfmt_test aspects once
    // Rust lint checks are Bazel-driven like the Go ones; plain cargo
    // for now.
    {
      name: 'Rust: clippy',
      run: 'cargo clippy --all-targets --locked -- -Dwarnings',
    },
  ],
};

// Builds only evaluate the module extensions they use, but `bazel mod`
// evaluates all of them, including broken ones in transitive modules.
local modGraphStep = {
  name: 'Bazel module graph',
  run: 'bazel mod graph > /dev/null',
};

{
  merge(workflows): std.mapWithKey(
    function(file, workflow) workflow {
      jobs+: {
        lint+: {
          steps+: [modGraphStep],
        },
        lint_rust: lintRustJob,
      },
    },
    workflows,
  ),
}
