local workflows_template = import 'tools/github_workflows/workflows_template.libsonnet';

workflows_template.getWorkflows(
  [
    'bb_completed_actions_ingester',
    'bb_slow_generic_runner',
  ],
  [
    'bb_completed_actions_ingester:bb_completed_actions_ingester',
    // bb_slow_generic_runner should only have an installer image which has not
    // been implemented yet.
  ],
  [],
  [
    // re-memoize and re-dispatch are Rust binaries with only a
    // linux_amd64 rust toolchain registered (see //:MODULE.bazel) —
    // unlike the Go binaries above, they aren't built for every
    // other platform below, only this one leg.
    {
      'if': 'matrix.host.upload',
      name: 'linux_amd64: copy re-memoize',
      run: 'rm -f re-memoize.linux_amd64 && bazel run --run_under cp --platforms=@com_github_buildbarn_bb_storage//tools/platforms:linux_amd64 //cmd/re_memoize:re-memoize $(pwd)/re-memoize.linux_amd64',
    },
    {
      'if': 'matrix.host.upload',
      name: 'linux_amd64: upload re-memoize',
      uses: 'actions/upload-artifact@v4',
      with: {
        name: 're-memoize.linux_amd64',
        path: 're-memoize.linux_amd64',
      },
    },
    {
      'if': 'matrix.host.upload',
      name: 'linux_amd64: copy re-dispatch',
      run: 'rm -f re-dispatch.linux_amd64 && bazel run --run_under cp --platforms=@com_github_buildbarn_bb_storage//tools/platforms:linux_amd64 //cmd/re_dispatch:re-dispatch $(pwd)/re-dispatch.linux_amd64',
    },
    {
      'if': 'matrix.host.upload',
      name: 'linux_amd64: upload re-dispatch',
      uses: 'actions/upload-artifact@v4',
      with: {
        name: 're-dispatch.linux_amd64',
        path: 're-dispatch.linux_amd64',
      },
    },
  ]
)
