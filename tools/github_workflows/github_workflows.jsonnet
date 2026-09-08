local buildbar_workflows_template = import 'tools/github_workflows/buildbar_workflows_template.libsonnet';
local workflows_template = import 'tools/github_workflows/workflows_template.libsonnet';

// Copy a Rust binary out of Bazel and upload it as a release artifact.
local rustBinarySteps(package, binary, platform) = [
  {
    'if': 'matrix.host.upload',
    name: '%s: copy %s' % [platform, binary],
    run: 'rm -f %(binary)s.%(platform)s && bazel run --run_under cp --platforms=@com_github_buildbarn_bb_storage//tools/platforms:%(platform)s //cmd/%(package)s:%(binary)s $(pwd)/%(binary)s.%(platform)s' % {
      package: package,
      binary: binary,
      platform: platform,
    },
  },
  {
    'if': 'matrix.host.upload',
    name: '%s: upload %s' % [platform, binary],
    uses: 'actions/upload-artifact@v4',
    with: {
      name: '%s.%s' % [binary, platform],
      path: '%s.%s' % [binary, platform],
    },
  },
];

buildbar_workflows_template.merge(workflows_template.getWorkflows(
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
  // The re- programs are Rust binaries with only a linux_amd64 rust
  // toolchain registered (see //:MODULE.bazel) — unlike the Go
  // binaries above, they aren't built for every other platform, only
  // this one leg.
  std.flattenArrays([
    rustBinarySteps(package, std.strReplace(package, '_', '-'), 'linux_amd64')
    for package in [
      're_memoize',
      're_dispatch',
      're_directory',
    ]
  ])
))
