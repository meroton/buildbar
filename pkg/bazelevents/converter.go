package bazelevents

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/kballard/go-shellquote"
	buildbarutil "github.com/meroton/buildbar/pkg/util"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MaxAccumulatedDocuments is the maximum number of documents that can be
// accumulated without build metadata.
const MaxAccumulatedDocuments = 25

// ConvertedDocuments is a map from document id suffix to
// the content of the document.
type ConvertedDocuments map[string]interface{}

// BazelEventConverter converts a CompletedAction into a format that is
// suitable for Elasticsearch.
type BazelEventConverter interface {
	ExtractInterestingData(ctx context.Context, eventTime *timestamppb.Timestamp, event *buildeventstream.BuildEvent) (map[string]ConvertedDocuments, error)
}

type platformInfo map[string]string

var nonePlatform = platformInfo{
	"mnemonic": "source",
	"name":     "source",
	"type":     "source",
}

var unknownPlatform = platformInfo{
	"mnemonic": "unknown",
	"name":     "unknown",
	"type":     "unknown",
}

type bazelEventConverter struct {
	errorLogger util.ErrorLogger

	accumulatedDocuments map[string]ConvertedDocuments
	buildMetadata        map[string]string
	configurations       map[string]platformInfo
	buildStartTime       *timestamppb.Timestamp
}

// NewBazelEventConverter creates a new BazelEventConverter for extracting
// interesting data from Bazel BuildEvents into Elasticsearch documents.
func NewBazelEventConverter(errorLogger util.ErrorLogger) BazelEventConverter {
	return &bazelEventConverter{
		errorLogger: errorLogger,

		accumulatedDocuments: map[string]ConvertedDocuments{},
		// No build metadata received yet.
		buildMetadata: nil,
		configurations: map[string]platformInfo{
			// build_event_stream.ConfigurationId describes a special "none" id.
			"none": nonePlatform,
		},
		// No build start time received yet.
		buildStartTime: nil,
	}
}

var invalidRunesIdSuffixRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-+~]`)

func cleanIdSuffix(id string) string {
	id = strings.ReplaceAll(id, "/", "~") // Simply remove @ in labels.
	id = invalidRunesIdSuffixRegex.ReplaceAllString(id, "")
	if len(id) > 50 {
		digestBytes := md5.Sum([]byte(id))
		digestHex := hex.EncodeToString(digestBytes[:])
		id = id[:50] + digestHex
	}
	return id
}

func (bec *bazelEventConverter) getPlatformInfo(configurationID string) platformInfo {
	if pi, ok := bec.configurations[configurationID]; ok {
		return pi
	}
	return unknownPlatform
}

func (bec *bazelEventConverter) ExtractInterestingData(ctx context.Context, eventTime *timestamppb.Timestamp, event *buildeventstream.BuildEvent) (map[string]ConvertedDocuments, error) {
	var documents map[string]ConvertedDocuments

	// TODO: FRME save documents until metadata has been collected.

	// UnknownBuildEventId (unknown) is not handled
	// PatternExpandedId (pattern_skipped) is not handled
	// UnconfiguredLabelId (unconfigured_label) is not handled
	// ConfiguredLabelId (configured_label) is not handled
	switch p := event.Payload.(type) {
	case *buildeventstream.BuildEvent_Progress: // ProgressId (progress)
		// noop
	case *buildeventstream.BuildEvent_Aborted: // Any or no ID.
		// noop
	case *buildeventstream.BuildEvent_Started: // BuildStartedId (started)
		// TODO: Parse TargetPatternId
		documents = bec.publishStartedEvent(p.Started)
	case *buildeventstream.BuildEvent_UnstructuredCommandLine: // UnstructuredCommandLineId (unstructured_command_line)
		// noop
	case *buildeventstream.BuildEvent_StructuredCommandLine: // StructuredCommandLineId (structured_command_line)
		// noop
	case *buildeventstream.BuildEvent_OptionsParsed: // OptionsParsedId (options_parsed)
		documents = bec.publishOptionsParsed(p.OptionsParsed)
	case *buildeventstream.BuildEvent_WorkspaceStatus: // WorkspaceStatusId (workspace_status)
		documents = bec.publishWorkspaceStatus(p.WorkspaceStatus)
	case *buildeventstream.BuildEvent_Fetch: // FetchId (fetch)
		id := event.Id.GetFetch()
		if id.GetUrl() == "" {
			return nil, status.Error(codes.InvalidArgument, "Empty fetch url")
		}
		documents = bec.publishFetch(eventTime, id, p.Fetch)
	case *buildeventstream.BuildEvent_Configuration: // ConfigurationId (configuration)
		id := event.Id.GetConfiguration()
		if id == nil {
			return nil, status.Error(codes.InvalidArgument, "Missing configuration ID")
		}
		documents = bec.collectAndPublishConfiguration(id, p.Configuration)
	case *buildeventstream.BuildEvent_Expanded: // PatternExpandedId (pattern)
		// noop
	case *buildeventstream.BuildEvent_Configured: // TargetConfiguredId (target_configured)
		// noop
	case *buildeventstream.BuildEvent_Action: // ActionCompletedId (action_completed)
		id := event.Id.GetActionCompleted()
		if id == nil {
			return nil, status.Error(codes.InvalidArgument, "Missing action_completed ID")
		}
		documents = bec.publishFailedAction(id, p.Action)
	case *buildeventstream.BuildEvent_NamedSetOfFiles: // NamedSetOfFilesId (named_set)
		// noop
	case *buildeventstream.BuildEvent_Completed: // TargetCompletedId (target_completed)
		id := event.Id.GetTargetCompleted()
		if id == nil {
			return nil, status.Error(codes.InvalidArgument, "Missing target_completed ID")
		}
		documents = bec.publishTargetCompleted(id, p.Completed)
	case *buildeventstream.BuildEvent_TestResult: // TestResultId (test_result)
		id := event.Id.GetTestResult()
		if id == nil {
			return nil, status.Error(codes.InvalidArgument, "Missing test_result ID")
		}
		documents = bec.publishTestResult(id, p.TestResult)
	case *buildeventstream.BuildEvent_TestProgress: // TestProgressId (test_progress)
		// noop
	case *buildeventstream.BuildEvent_TestSummary: // TestSummaryId (test_summary)
		id := event.Id.GetTestSummary()
		if id == nil {
			return nil, status.Error(codes.InvalidArgument, "Missing test_summary ID")
		}
		documents = bec.publishTestSummary(id, p.TestSummary)
	case *buildeventstream.BuildEvent_TargetSummary: // TargetSummaryId (target_summary)
		id := event.Id.GetTargetSummary()
		if id == nil {
			return nil, status.Error(codes.InvalidArgument, "Missing target_summary ID")
		}
		documents = bec.publishTargetSummary(id, p.TargetSummary)
	case *buildeventstream.BuildEvent_Finished: // BuildFinishedId (build_finished)
		documents = bec.publishBuildFinished(p.Finished)
	case *buildeventstream.BuildEvent_BuildToolLogs: // BuildToolLogsId (build_tool_logs)
		// noop
	case *buildeventstream.BuildEvent_BuildMetrics: // BuildMetricsId (build_metrics)
		documents = bec.publishBuildMetrics(p.BuildMetrics)
	case *buildeventstream.BuildEvent_WorkspaceInfo: // WorkspaceConfigId (workspace)
		// noop
	case *buildeventstream.BuildEvent_BuildMetadata: // BuildMetadataId (build_metadata)
		documents = bec.collectAndPublishBuildMetadata(p.BuildMetadata)
	case *buildeventstream.BuildEvent_ConvenienceSymlinksIdentified: // ConvenienceSymlinksIdentifiedId (convenience_symlinks_identified)
		// noop
	case *buildeventstream.BuildEvent_ExecRequest: // ExecRequestId (exec_request)
		// TODO: May contain secrets on the command line or in the environment, log it?
	}

	fullDocuments := map[string]ConvertedDocuments{}
	for suffix, document := range documents {
		fullDocument := map[string]interface{}{
			"event_time": buildbarutil.ProtoValueToJSONToInterface(eventTime),
		}
		if bec.buildMetadata != nil {
			fullDocument["metadata"] = bec.buildMetadata
		} else {
			fullDocument["metadata"] = map[string]string{
				"no-metadata-received-yet": "1",
			}
		}
		for key, value := range document {
			fullDocument[key] = value
		}
		fullDocuments[suffix] = fullDocument
	}

	if bec.buildMetadata == nil {
		// Store the documents for later, when we have metadata.
		for suffix, value := range fullDocuments {
			if len(bec.accumulatedDocuments) < MaxAccumulatedDocuments {
				bec.accumulatedDocuments[suffix] = value
				if len(bec.accumulatedDocuments) == MaxAccumulatedDocuments {
					bec.errorLogger.Log(status.Error(codes.FailedPrecondition, "Too many documents accumulated without build metadata"))
				}
			}
		}
		// Keep the documents map to upload them without metadata.
		// They will be overwritten when the metadata arrives.
	} else {
		// Metadata is available, upload cached documents agian.
		for suffix, document := range bec.accumulatedDocuments {
			document["metadata"] = bec.buildMetadata
			fullDocuments[suffix] = document
		}
		bec.accumulatedDocuments = nil
	}

	return fullDocuments, nil
}

func (bec *bazelEventConverter) publishStartedEvent(payload *buildeventstream.BuildStarted) map[string]ConvertedDocuments {
	bec.buildStartTime = payload.StartTime
	return map[string]ConvertedDocuments{
		"started": {
			"type":                "Started",
			"uuid":                payload.Uuid,
			"start_time":          buildbarutil.ProtoValueToJSONToInterface(payload.StartTime),
			"build_tool_version":  payload.BuildToolVersion,
			"options_description": payload.OptionsDescription,
			"started_command":     payload.Command,
			"working_directory":   payload.WorkingDirectory,
			"workspace_directory": payload.WorkspaceDirectory,
		},
	}
}

func (bec *bazelEventConverter) publishOptionsParsed(payload *buildeventstream.OptionsParsed) map[string]ConvertedDocuments {
	options := map[string]interface{}{
		"startup_options_list":          payload.StartupOptions,
		"startup_options":               shellquote.Join(payload.StartupOptions...),
		"explicit_startup_options_list": payload.ExplicitStartupOptions,
		"explicit_startup_options":      shellquote.Join(payload.ExplicitStartupOptions...),
		"cmd_line_list":                 payload.CmdLine,
		"cmd_line":                      shellquote.Join(payload.CmdLine...),
		"explicit_cmd_line_list":        payload.ExplicitCmdLine,
		"explicit_cmd_line":             shellquote.Join(payload.ExplicitCmdLine...),
		"invocation_policy":             buildbarutil.ProtoToJSONToInterface(payload.InvocationPolicy),
		"tool_tag":                      payload.ToolTag,
	}
	return map[string]ConvertedDocuments{
		"command-line": {
			"type":    "OptionsParsed",
			"options": options,
		},
	}
}

func (bec *bazelEventConverter) publishWorkspaceStatus(payload *buildeventstream.WorkspaceStatus) map[string]ConvertedDocuments {
	workspaceStatus := map[string]interface{}{}
	for _, item := range payload.Item {
		workspaceStatus[item.Key] = item.Value
	}
	return map[string]ConvertedDocuments{
		"workspace-status": {
			"type":             "WorkspaceStatus",
			"workspace_status": workspaceStatus,
		},
	}
}

func (bec *bazelEventConverter) publishFetch(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_FetchId, payload *buildeventstream.Fetch) map[string]ConvertedDocuments {
	fetch := map[string]interface{}{
		"url":     id.Url,
		"success": payload.Success,
	}
	if eventTime.IsValid() && bec.buildStartTime.IsValid() {
		fetch["duration_from_start"] = eventTime.AsTime().Sub(bec.buildStartTime.AsTime()).Seconds()
	}
	digestBytes := md5.Sum([]byte(id.Url))
	digestHex := hex.EncodeToString(digestBytes[:])
	return map[string]ConvertedDocuments{
		"fetch-" + digestHex: {
			"type":  "Fetch",
			"fetch": fetch,
		},
	}
}

func (bec *bazelEventConverter) collectAndPublishConfiguration(id *buildeventstream.BuildEventId_ConfigurationId, payload *buildeventstream.Configuration) map[string]ConvertedDocuments {
	var platformType string
	if payload.IsTool {
		platformType = "exec"
	} else {
		platformType = "target"
	}
	platformInfo := platformInfo{
		"mnemonic": payload.Mnemonic,
		"name":     payload.PlatformName,
		"type":     platformType,
	}
	bec.configurations[id.Id] = platformInfo
	return map[string]ConvertedDocuments{
		"configuration-" + cleanIdSuffix(id.Id): {
			"type": "Configuration",
			"configuration": map[string]interface{}{
				"mnemonic": payload.Mnemonic,
				"id":       id.Id,
				"name":     payload.PlatformName,
				"type":     platformType,
			},
		},
	}
}

func (bec *bazelEventConverter) publishFailedAction(id *buildeventstream.BuildEventId_ActionCompletedId, payload *buildeventstream.ActionExecuted) map[string]ConvertedDocuments {
	if !payload.Success {
		action := map[string]interface{}{
			// "success":        payload.Success,
			"primary_output": id.PrimaryOutput,
			"mnemonic":       payload.Type,
			"exit_code":      payload.ExitCode,
			"command_list":   payload.CommandLine,
			"command":        shellquote.Join(payload.CommandLine...),
			"start_time":     payload.StartTime,
			"end_time":       payload.EndTime,
		}
		if payload.StartTime.IsValid() && payload.EndTime.IsValid() {
			action["duration"] = payload.EndTime.AsTime().Sub(
				payload.StartTime.AsTime()).Seconds()
		}
		return map[string]ConvertedDocuments{
			"action-completed-" + cleanIdSuffix(id.PrimaryOutput): {
				"type":     "FailedAction",
				"label":    id.Label,
				"platform": bec.getPlatformInfo(id.Configuration.GetId()),
				"action":   action,
			},
		}
	}
	return nil
}

func (bec *bazelEventConverter) publishTargetCompleted(id *buildeventstream.BuildEventId_TargetCompletedId, payload *buildeventstream.TargetComplete) map[string]ConvertedDocuments {
	// TODO: List first few filed per output group, requires collection of file sets.
	// outputGroups := map[string]interface{}
	outputGroupNames := []string{}
	for _, pbOutputGroup := range payload.OutputGroup {
		outputGroupNames = append(outputGroupNames, pbOutputGroup.Name)
	}
	docID := id.Label + "-" + id.Configuration.GetId()
	if id.Aspect != "" {
		docID += "-" + id.Aspect
	}
	return map[string]ConvertedDocuments{
		"target-completed-" + cleanIdSuffix(docID): {
			"type":     "TargetCompleted",
			"label":    id.Label,
			"platform": bec.getPlatformInfo(id.Configuration.GetId()),
			"aspect":   id.Aspect,
			"target_result": map[string]interface{}{
				"success":            payload.Success,
				"output_group_names": outputGroupNames,
				"tags":               payload.Tag,
				"test_timeout":       payload.TestTimeout.AsDuration().Seconds(),
				"failure_message":    payload.FailureDetail.GetMessage(),
				// TODO: Add the failure detail enums.
			},
		},
	}
}

func (bec *bazelEventConverter) publishTestResult(id *buildeventstream.BuildEventId_TestResultId, payload *buildeventstream.TestResult) map[string]ConvertedDocuments {
	testResult := map[string]interface{}{
		"run":     id.Run,
		"shard":   id.Shard,
		"attempt": id.Attempt,

		"status":             payload.Status.String(),
		"cached_locally":     payload.CachedLocally,
		"start_time":         payload.TestAttemptStart,
		"duration":           payload.TestAttemptDuration.AsDuration().Seconds(),
		"execution_strategy": payload.ExecutionInfo.GetStrategy(),
		"remote_cache_hit":   payload.ExecutionInfo.GetCachedRemotely(),
		"exit_code":          payload.ExecutionInfo.GetStrategy(),
		"executor_hostname":  payload.ExecutionInfo.GetHostname(),
	}
	if payload.TestAttemptStart.IsValid() {
		testResult["end_time"] = payload.TestAttemptStart.AsTime().Add(
			payload.TestAttemptDuration.AsDuration())
	}
	docID := fmt.Sprintf("%s-%s-%d-%d-%d", id.Label, id.Configuration.GetId(), id.Run, id.Shard, id.Attempt)
	return map[string]ConvertedDocuments{
		"test-result-" + cleanIdSuffix(docID): {
			"type":        "TestResult",
			"label":       id.Label,
			"platform":    bec.getPlatformInfo(id.Configuration.GetId()),
			"test_result": testResult,
		},
	}
}

func (bec *bazelEventConverter) publishTestSummary(id *buildeventstream.BuildEventId_TestSummaryId, payload *buildeventstream.TestSummary) map[string]ConvertedDocuments {
	document := map[string]interface{}{
		"type":     "TestSummary",
		"label":    id.Label,
		"platform": bec.getPlatformInfo(id.Configuration.GetId()),
		"test_summary": map[string]interface{}{
			"overall_status":           payload.OverallStatus,
			"total_run_count":          payload.TotalRunCount,
			"attempt_count":            payload.AttemptCount,
			"shard_count":              payload.ShardCount,
			"total_num_cached_actions": payload.TotalNumCached,
			"total_run_duration":       payload.TotalRunDuration.AsDuration().Seconds(),
		},
	}
	docID := id.Label + "-" + id.Configuration.GetId()
	return map[string]ConvertedDocuments{
		"test-summary-" + cleanIdSuffix(docID): document,
	}
}

func (bec *bazelEventConverter) publishTargetSummary(id *buildeventstream.BuildEventId_TargetSummaryId, payload *buildeventstream.TargetSummary) map[string]ConvertedDocuments {
	document := map[string]interface{}{
		"type":     "TargetSummary",
		"label":    id.Label,
		"platform": bec.getPlatformInfo(id.Configuration.GetId()),
		"target_summary": map[string]interface{}{
			"overall_build_success": payload.OverallBuildSuccess,
			"overall_test_status":   payload.OverallTestStatus.String(),
		},
	}
	docID := id.Label + "-" + id.Configuration.GetId()
	return map[string]ConvertedDocuments{
		"target-summary-" + cleanIdSuffix(docID): document,
	}
}

func (bec *bazelEventConverter) publishBuildFinished(payload *buildeventstream.BuildFinished) map[string]ConvertedDocuments {
	finished := map[string]interface{}{
		"exit_code":       payload.ExitCode.GetCode(),
		"exit_code_name":  payload.ExitCode.GetName(),
		"finish_time":     buildbarutil.ProtoValueToJSONToInterface(payload.FinishTime),
		"failure_message": payload.FailureDetail.GetMessage(),
		// TODO: Add the failure detail enums.
	}
	if payload.FinishTime.IsValid() && bec.buildStartTime.IsValid() {
		duration := payload.FinishTime.AsTime().Sub(bec.buildStartTime.AsTime())
		finished["build_duration"] = duration.Seconds()
	}
	return map[string]ConvertedDocuments{
		"finished": {
			"type":     "BuildFinished",
			"finished": finished,
		},
	}
}

func (bec *bazelEventConverter) publishBuildMetrics(payload *buildeventstream.BuildMetrics) map[string]ConvertedDocuments {
	documents := map[string]ConvertedDocuments{}

	actionDataMap := map[string]interface{}{}
	for _, pbActionData := range payload.ActionSummary.GetActionData() {
		actionData := map[string]interface{}{
			"mnemonic":          pbActionData.Mnemonic,
			"actions_executed":  pbActionData.ActionsExecuted,
			"first_started":     float64(pbActionData.FirstStartedMs) / 1000.0,
			"last_ended":        float64(pbActionData.LastEndedMs) / 1000.0,
			"total_system_time": pbActionData.SystemTime.AsDuration().Seconds(),
			"total_user_time":   pbActionData.UserTime.AsDuration().Seconds(),
			"actions_created":   pbActionData.ActionsCreated,
		}
		actionDataMap[pbActionData.Mnemonic] = actionData
		// TODO: Remove unless used in the dashboards.
		documents["metrics-action-data-"+cleanIdSuffix(pbActionData.Mnemonic)] = map[string]interface{}{
			"type":        "BuildMetrics-ActionData",
			"action_data": actionData,
		}
	}
	runnerCountMap := map[string]interface{}{}
	for _, pbRunnerCount := range payload.ActionSummary.GetRunnerCount() {
		runnerCount := map[string]interface{}{
			"name":      pbRunnerCount.Name,
			"count":     pbRunnerCount.Count,
			"exec_kind": pbRunnerCount.ExecKind,
		}
		runnerCountMap[pbRunnerCount.Name] = runnerCount
		// TODO: Remove unless used in the dashboards.
		documents["metrics-runner-count-"+cleanIdSuffix(pbRunnerCount.Name)] = map[string]interface{}{
			"type":         "BuildMetrics-RunnerCount",
			"runner_count": runnerCount,
		}
	}

	localMissDetailMap := map[string]interface{}{}
	for _, pbLocalMissDetails := range payload.ActionSummary.GetActionCacheStatistics().GetMissDetails() {
		localMissDetailMap[pbLocalMissDetails.Reason.String()] = pbLocalMissDetails.Count
	}
	garbageCollected := int64(0)
	for _, pbGarbageMetrics := range payload.MemoryMetrics.GetGarbageMetrics() {
		garbageCollected += pbGarbageMetrics.GetGarbageCollected()
	}

	documents["metrics"] = map[string]interface{}{
		"type": "BuildMetrics",
		"metrics": map[string]interface{}{
			"action_summary": map[string]interface{}{
				"actions_created":                       payload.ActionSummary.GetActionsCreated(),
				"actions_created_not_including_aspects": payload.ActionSummary.GetActionsCreatedNotIncludingAspects(),
				"actions_executed":                      payload.ActionSummary.GetActionsExecuted(),
			},
			"local_action_cache": map[string]interface{}{
				"size_in_bytes": payload.ActionSummary.GetActionCacheStatistics().GetSizeInBytes(),
				"save_duration": float64(payload.ActionSummary.GetActionCacheStatistics().GetSaveTimeInMs()) / 1000.0,
				"hits":          payload.ActionSummary.GetActionCacheStatistics().GetHits(),
				"misses":        payload.ActionSummary.GetActionCacheStatistics().GetMisses(),
				"miss_details":  localMissDetailMap,
			},
			"action_data":  actionDataMap,
			"runner_count": runnerCountMap,
			"memory": map[string]interface{}{
				"used_heap_size_post_build":            float64(payload.MemoryMetrics.GetUsedHeapSizePostBuild()),
				"peak_post_gc_heap_size":               float64(payload.MemoryMetrics.GetPeakPostGcHeapSize()),
				"peak_post_gc_tenured_space_heap_size": float64(payload.MemoryMetrics.GetPeakPostGcTenuredSpaceHeapSize()),
				"bytes_collected":                      float64(garbageCollected),
			},
			"targets_configured":                       float64(payload.TargetMetrics.GetTargetsConfigured()),
			"targets_configured_not_including_aspects": float64(payload.TargetMetrics.GetTargetsConfiguredNotIncludingAspects()),
			"packages_loaded":                          float64(payload.PackageMetrics.GetPackagesLoaded()),
			// TODO: Is it possible to create dashboards for PackageLoadMetrics?
			"cpu_time":                     float64(payload.TimingMetrics.GetCpuTimeInMs()) / 1000.0,
			"wall_time":                    float64(payload.TimingMetrics.GetWallTimeInMs()) / 1000.0,
			"analysis_phase_start_time":    float64(payload.TimingMetrics.GetAnalysisPhaseTimeInMs()) / 1000.0,
			"execution_phase_start_time":   float64(payload.TimingMetrics.GetExecutionPhaseTimeInMs()) / 1000.0,
			"actions_execution_start_time": float64(payload.TimingMetrics.GetActionsExecutionStartInMs()) / 1000.0,
			"cumulative_server_metrics": map[string]interface{}{
				"num_analyses": payload.CumulativeMetrics.GetNumAnalyses(),
				"num_builds":   payload.CumulativeMetrics.GetNumBuilds(),
			},
			"artifacts": map[string]interface{}{
				"source_artifacts_read": map[string]interface{}{
					"size_bytes": float64(payload.ArtifactMetrics.GetSourceArtifactsRead().GetSizeInBytes()),
					"count":      payload.ArtifactMetrics.GetSourceArtifactsRead().GetCount(),
				},
				"output_artifacts_seen": map[string]interface{}{
					"size_bytes": float64(payload.ArtifactMetrics.GetOutputArtifactsSeen().GetSizeInBytes()),
					"count":      payload.ArtifactMetrics.GetOutputArtifactsSeen().GetCount(),
				},
				"output_artifacts_from_action_cache": map[string]interface{}{
					"size_bytes": float64(payload.ArtifactMetrics.GetOutputArtifactsFromActionCache().GetSizeInBytes()),
					"count":      payload.ArtifactMetrics.GetOutputArtifactsFromActionCache().GetCount(),
				},
				"top_level_artifacts": map[string]interface{}{
					"size_bytes": float64(payload.ArtifactMetrics.GetTopLevelArtifacts().GetSizeInBytes()),
					"count":      payload.ArtifactMetrics.GetTopLevelArtifacts().GetCount(),
				},
			},
			"build_graph": buildbarutil.ProtoToJSONToInterface(payload.BuildGraphMetrics),
			// TODO: worker_metrics
			// TODO: worker_pool_metrics
			"network": map[string]interface{}{
				"system": map[string]interface{}{
					"bytes_sent":                float64(payload.NetworkMetrics.GetSystemNetworkStats().GetBytesSent()),
					"bytes_recv":                float64(payload.NetworkMetrics.GetSystemNetworkStats().GetBytesRecv()),
					"packets_sent":              float64(payload.NetworkMetrics.GetSystemNetworkStats().GetPacketsSent()),
					"packets_recv":              float64(payload.NetworkMetrics.GetSystemNetworkStats().GetPacketsRecv()),
					"peak_bytes_sent_per_sec":   float64(payload.NetworkMetrics.GetSystemNetworkStats().GetPeakBytesSentPerSec()),
					"peak_bytes_recv_per_sec":   float64(payload.NetworkMetrics.GetSystemNetworkStats().GetPeakBytesRecvPerSec()),
					"peak_packets_sent_per_sec": float64(payload.NetworkMetrics.GetSystemNetworkStats().GetPeakPacketsSentPerSec()),
					"peak_packets_recv_per_sec": float64(payload.NetworkMetrics.GetSystemNetworkStats().GetPeakPacketsRecvPerSec()),
				},
			},
		},
	}
	return documents
}

func (bec *bazelEventConverter) collectAndPublishBuildMetadata(payload *buildeventstream.BuildMetadata) map[string]ConvertedDocuments {
	// Make sure bec.buildMetadata is non-nil.
	if payload.Metadata == nil {
		bec.buildMetadata = map[string]string{}
	} else {
		bec.buildMetadata = payload.Metadata
	}
	return map[string]ConvertedDocuments{
		"build-metadata": {
			"type": "BuildMetadata",
		},
	}
}
