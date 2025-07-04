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
)
