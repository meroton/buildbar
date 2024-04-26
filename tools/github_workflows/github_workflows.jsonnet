local workflows_template = import 'tools/github_workflows/workflows_template.libsonnet';

workflows_template.getWorkflows(
  ['bb_completed_actions_ingester'],
  ['bb_completed_actions_ingester:bb_completed_actions_ingester'],
)
