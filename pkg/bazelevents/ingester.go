package bazelevents

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"sync"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/kballard/go-shellquote"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	buildbarutil "github.com/meroton/buildbar/pkg/util"
	"github.com/prometheus/client_golang/prometheus"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ingesterPrometheusMetrics sync.Once

	ingesterEventsReceived = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "buildevents",
		Name:      "ingester_events_received",
		Help:      "Number of Build Events that has been received.",
	})
	ingesterInvalidEvents = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "buildevents",
		Name:      "ingester_invalid_events",
		Help:      "Number of Build Events that could not be handled.",
	})
)

// The Ingester can be used to receive BuildEvents and
// push them into some database. This allows for realtime or post-build
// analysis in a remote service. This is particularly useful for understanding
// how builds change over time by inspecting the aggregated BuildEvent metadata.
type ingester struct {
	uploader elasticsearch.Uploader
}

// NewIngester creates a new Ingester that uploads BuildEvents to Elasticsearch.
func NewIngester(
	uploader elasticsearch.Uploader,
) BazelEventServer {
	ingesterPrometheusMetrics.Do(func() {
		prometheus.MustRegister(ingesterEventsReceived)
		prometheus.MustRegister(ingesterInvalidEvents)
	})
	return &ingester{
		uploader: uploader,
	}
}

func (i *ingester) PublishBazelEvents(ctx context.Context, instanceName digest.InstanceName, streamID *build.StreamId) (BazelEventStreamClient, error) {
	if streamID.InvocationId == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Invocation ID is empty")
	}
	return &ingestingStreamClient{
		ctx:           ctx,
		sendResponses: make(chan struct{}),
		ackCounter:    0,
		uploader:      i.uploader,
		instanceName:  instanceName,
		streamID:      streamID,
		configurations: map[string]platformInfo{
			// build_event_stream.ConfigurationId describes a special "none" id.
			"none": nonePlatform,
		},
	}, nil
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

type ingestingStreamClient struct {
	ctx           context.Context
	sendResponses chan struct{}
	ackCounter    int

	uploader     elasticsearch.Uploader
	instanceName digest.InstanceName
	streamID     *build.StreamId
	errorLogger  util.ErrorLogger

	buildMetadata  map[string]string
	configurations map[string]platformInfo
	buildStartTime *timestamppb.Timestamp
}

func (s *ingestingStreamClient) getPlatformInfo(configurationID string) platformInfo {
	if pi, ok := s.configurations[configurationID]; ok {
		return pi
	}
	return unknownPlatform
}

func (s *ingestingStreamClient) Send(eventTime *timestamppb.Timestamp, event *buildeventstream.BuildEvent) error {
	select {
	case <-s.sendResponses:
		return status.Error(codes.InvalidArgument, "Last message already received")
	default:
		// noop
	}
	ingesterEventsReceived.Inc()

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
		s.publishStartedEvent(eventTime, p.Started)
	case *buildeventstream.BuildEvent_UnstructuredCommandLine: // UnstructuredCommandLineId (unstructured_command_line)
		// noop
	case *buildeventstream.BuildEvent_StructuredCommandLine: // StructuredCommandLineId (structured_command_line)
		// noop
	case *buildeventstream.BuildEvent_OptionsParsed: // OptionsParsedId (options_parsed)
		s.publishOptionsParsed(eventTime, p.OptionsParsed)
	case *buildeventstream.BuildEvent_WorkspaceStatus: // WorkspaceStatusId (workspace_status)
		s.publishWorkspaceStatus(eventTime, p.WorkspaceStatus)
	case *buildeventstream.BuildEvent_Fetch: // FetchId (fetch)
		id := event.Id.GetFetch()
		if id.GetUrl() == "" {
			return status.Error(codes.InvalidArgument, "Empty fetch url")
		}
		s.publishFetch(eventTime, id, p.Fetch)
	case *buildeventstream.BuildEvent_Configuration: // ConfigurationId (configuration)
		id := event.Id.GetConfiguration()
		if id == nil {
			return status.Error(codes.InvalidArgument, "Missing configuration ID")
		}
		s.collectAndPublishConfiguration(eventTime, id, p.Configuration)
	case *buildeventstream.BuildEvent_Expanded: // PatternExpandedId (pattern)
		// noop
	case *buildeventstream.BuildEvent_Configured: // TargetConfiguredId (target_configured)
		// noop
	case *buildeventstream.BuildEvent_Action: // ActionCompletedId (action_completed)
		id := event.Id.GetActionCompleted()
		if id == nil {
			return status.Error(codes.InvalidArgument, "Missing action_completed ID")
		}
		s.publishFailedAction(eventTime, id, p.Action)
	case *buildeventstream.BuildEvent_NamedSetOfFiles: // NamedSetOfFilesId (named_set)
		// noop
	case *buildeventstream.BuildEvent_Completed: // TargetCompletedId (target_completed)
		id := event.Id.GetTargetCompleted()
		if id == nil {
			return status.Error(codes.InvalidArgument, "Missing target_completed ID")
		}
		s.publishTargetCompleted(eventTime, id, p.Completed)
	case *buildeventstream.BuildEvent_TestResult: // TestResultId (test_result)
		id := event.Id.GetTestResult()
		if id == nil {
			return status.Error(codes.InvalidArgument, "Missing test_result ID")
		}
		s.publishTestResult(eventTime, id, p.TestResult)
	case *buildeventstream.BuildEvent_TestProgress: // TestProgressId (test_progress)
		// noop
	case *buildeventstream.BuildEvent_TestSummary: // TestSummaryId (test_summary)
		id := event.Id.GetTestSummary()
		if id == nil {
			return status.Error(codes.InvalidArgument, "Missing test_summary ID")
		}
		s.publishTestSummary(eventTime, id, p.TestSummary)
	case *buildeventstream.BuildEvent_TargetSummary: // TargetSummaryId (target_summary)
		id := event.Id.GetTargetSummary()
		if id == nil {
			return status.Error(codes.InvalidArgument, "Missing target_summary ID")
		}
		s.publishTargetSummary(eventTime, id, p.TargetSummary)
	case *buildeventstream.BuildEvent_Finished: // BuildFinishedId (build_finished)
		s.publishBuildFinished(eventTime, p.Finished)
	case *buildeventstream.BuildEvent_BuildToolLogs: // BuildToolLogsId (build_tool_logs)
		// noop
	case *buildeventstream.BuildEvent_BuildMetrics: // BuildMetricsId (build_metrics)
		s.publishBuildMetrics(eventTime, p.BuildMetrics)
	case *buildeventstream.BuildEvent_WorkspaceInfo: // WorkspaceConfigId (workspace)
		// noop
	case *buildeventstream.BuildEvent_BuildMetadata: // BuildMetadataId (build_metadata)
		s.collectAndPublishBuildMetadata(eventTime, p.BuildMetadata)
	case *buildeventstream.BuildEvent_ConvenienceSymlinksIdentified: // ConvenienceSymlinksIdentifiedId (convenience_symlinks_identified)
		// noop
	case *buildeventstream.BuildEvent_ExecRequest: // ExecRequestId (exec_request)
		// TODO: May contain secrets on the command line or in the environment, log it?
	}

	// As we need all messages to build up all the file sets, the acks cannot
	// be sent until after the last message has been processed.
	s.ackCounter++
	if event.LastMessage {
		close(s.sendResponses)
	}
	return nil
}

func (s *ingestingStreamClient) Recv() error {
	select {
	case <-s.sendResponses:
		if s.ackCounter == 0 {
			return io.EOF
		}
		s.ackCounter--
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *ingestingStreamClient) uploadDocument(eventTime *timestamppb.Timestamp, id string, document map[string]interface{}) {
	fullDocument := map[string]interface{}{
		"event_time": eventTime.String(),
		"metadata":   s.buildMetadata,
	}
	for key, value := range document {
		fullDocument[key] = value
	}
}

func (s *ingestingStreamClient) publishStartedEvent(eventTime *timestamppb.Timestamp, payload *buildeventstream.BuildStarted) {
	s.buildStartTime = payload.StartTime
	document := map[string]interface{}{
		"type":                "Started",
		"uuid":                payload.Uuid,
		"start_time":          payload.StartTime.String(),
		"build_tool_version":  payload.BuildToolVersion,
		"options_description": payload.OptionsDescription,
		"started_command":     payload.Command,
		"working_directory":   payload.WorkingDirectory,
		"workspace_directory": payload.WorkspaceDirectory,
	}
	s.uploadDocument(eventTime, "started", document)
}

func (s *ingestingStreamClient) publishOptionsParsed(eventTime *timestamppb.Timestamp, payload *buildeventstream.OptionsParsed) {
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
	s.uploadDocument(eventTime, "command-line", map[string]interface{}{
		"type":    "OptionsParsed",
		"options": options,
	})
}

func (s *ingestingStreamClient) publishWorkspaceStatus(eventTime *timestamppb.Timestamp, payload *buildeventstream.WorkspaceStatus) {
	workspaceStatus := map[string]interface{}{}
	for _, item := range payload.Item {
		workspaceStatus[item.Key] = item.Value
	}
	s.uploadDocument(eventTime, "workspace-status", map[string]interface{}{
		"type":             "WorkspaceStatus",
		"workspace_status": workspaceStatus,
	})
}

func (s *ingestingStreamClient) publishFetch(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_FetchId, payload *buildeventstream.Fetch) {
	fetch := map[string]interface{}{
		"url":     id.Url,
		"success": payload.Success,
	}
	if eventTime.IsValid() && s.buildStartTime.IsValid() {
		fetch["duration_from_start"] = eventTime.AsTime().Sub(s.buildStartTime.AsTime()).Seconds
	}
	document := map[string]interface{}{
		"type":  "Fetch",
		"fetch": fetch,
	}
	digestBytes := md5.Sum([]byte(id.Url))
	digestHex := hex.EncodeToString(digestBytes[:])
	s.uploadDocument(eventTime, "fetch-"+digestHex, document)
}

func (s *ingestingStreamClient) collectAndPublishConfiguration(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_ConfigurationId, payload *buildeventstream.Configuration) {
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
	s.configurations[id.Id] = platformInfo
	document := map[string]interface{}{
		"type": "Configuration",
		"configuration": map[string]interface{}{
			"mnemonic": payload.Mnemonic,
			"id":       id.Id,
			"name":     payload.PlatformName,
			"type":     platformType,
		},
	}
	s.uploadDocument(eventTime, "configuration-"+id.Id, document)
}

func (s *ingestingStreamClient) publishFailedAction(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_ActionCompletedId, payload *buildeventstream.ActionExecuted) {
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
		document := map[string]interface{}{
			"type":     "FailedAction",
			"label":    id.Label,
			"platform": s.getPlatformInfo(id.Configuration.GetId()),
			"action":   action,
		}
		s.uploadDocument(eventTime, "action-completed-"+id.PrimaryOutput, document)
	}
}

func (s *ingestingStreamClient) publishTargetCompleted(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_TargetCompletedId, payload *buildeventstream.TargetComplete) {
	// TODO: List first few filed per output group, requires collection of file sets.
	// outputGroups := map[string]interface{}
	outputGroupNames := []string{}
	for _, pbOutputGroup := range payload.OutputGroup {
		outputGroupNames = append(outputGroupNames, pbOutputGroup.Name)
	}
	document := map[string]interface{}{
		"type":     "TargetCompleted",
		"label":    id.Label,
		"platform": s.getPlatformInfo(id.Configuration.GetId()),
		"aspect":   id.Aspect,
		"target_result": map[string]interface{}{
			"success":            payload.Success,
			"output_group_names": outputGroupNames,
			"tags":               payload.Tag,
			"test_timeout":       payload.TestTimeout.AsDuration().Seconds,
			"failure_message":    payload.FailureDetail.GetMessage(),
			// TODO: Add the failure detail enums.
		},
	}
	docID := id.Label + "-" + id.Configuration.GetId()
	if id.Aspect != "" {
		docID += "-" + id.Aspect
	}
	s.uploadDocument(eventTime, docID, document)
}

func (s *ingestingStreamClient) publishTestResult(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_TestResultId, payload *buildeventstream.TestResult) {
	testResult := map[string]interface{}{
		"run":     id.Run,
		"shard":   id.Shard,
		"attempt": id.Attempt,

		"status":             payload.Status.String(),
		"cached_locally":     payload.CachedLocally,
		"start_time":         payload.TestAttemptStart,
		"duration":           payload.TestAttemptDuration.AsDuration().Seconds,
		"execution_strategy": payload.ExecutionInfo.GetStrategy(),
		"remote_cache_hit":   payload.ExecutionInfo.GetCachedRemotely(),
		"exit_code":          payload.ExecutionInfo.GetStrategy(),
		"executor_hostname":  payload.ExecutionInfo.GetHostname(),
	}
	if payload.TestAttemptStart.IsValid() {
		testResult["end_time"] = payload.TestAttemptStart.AsTime().Add(
			payload.TestAttemptDuration.AsDuration())
	}
	document := map[string]interface{}{
		"type":        "TestResult",
		"label":       id.Label,
		"platform":    s.getPlatformInfo(id.Configuration.GetId()),
		"test_result": testResult,
	}
	docID := fmt.Sprintf("%s-%s-%d-%d-%d", id.Label, id.Configuration.GetId(), id.Run, id.Shard, id.Attempt)
	s.uploadDocument(eventTime, docID, document)
}

func (s *ingestingStreamClient) publishTestSummary(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_TestSummaryId, payload *buildeventstream.TestSummary) {
	document := map[string]interface{}{
		"type":     "TestSummary",
		"label":    id.Label,
		"platform": s.getPlatformInfo(id.Configuration.GetId()),
		"test_summary": map[string]interface{}{
			"overall_status":           payload.OverallStatus,
			"total_run_count":          payload.TotalRunCount,
			"attempt_count":            payload.AttemptCount,
			"shard_count":              payload.ShardCount,
			"total_num_cached_actions": payload.TotalNumCached,
			"total_run_duration":       payload.TotalRunDuration.AsDuration().Seconds,
		},
	}
	s.uploadDocument(eventTime, id.Label+"-"+id.Configuration.GetId(), document)
}

func (s *ingestingStreamClient) publishTargetSummary(eventTime *timestamppb.Timestamp, id *buildeventstream.BuildEventId_TargetSummaryId, payload *buildeventstream.TargetSummary) {
	document := map[string]interface{}{
		"type":     "TargetSummary",
		"label":    id.Label,
		"platform": s.getPlatformInfo(id.Configuration.GetId()),
		"target_summary": map[string]interface{}{
			"overall_build_success": payload.OverallBuildSuccess,
			"overall_test_status":   payload.OverallTestStatus.String(),
		},
	}
	s.uploadDocument(eventTime, id.Label+"-"+id.Configuration.GetId(), document)
}

func (s *ingestingStreamClient) publishBuildFinished(eventTime *timestamppb.Timestamp, payload *buildeventstream.BuildFinished) {
	finished := map[string]interface{}{
		"exit_code":       payload.ExitCode.GetCode(),
		"exit_code_name":  payload.ExitCode.GetName(),
		"finish_time":     payload.FinishTime.String(),
		"failure_message": payload.FailureDetail.GetMessage(),
		// TODO: Add the failure detail enums.
	}
	if payload.FinishTime.IsValid() && s.buildStartTime.IsValid() {
		duration := payload.FinishTime.AsTime().Sub(s.buildStartTime.AsTime())
		finished["build_duration"] = duration.Seconds()
	}
	document := map[string]interface{}{
		"type":     "BuildFinished",
		"finished": finished,
	}
	s.uploadDocument(eventTime, "finished", document)
}

func (s *ingestingStreamClient) publishBuildMetrics(eventTime *timestamppb.Timestamp, payload *buildeventstream.BuildMetrics) {
	actionDataMap := map[string]interface{}{}
	for _, pbActionData := range payload.ActionSummary.GetActionData() {
		actionData := map[string]interface{}{
			"mnemonic":          pbActionData.Mnemonic,
			"actions_executed":  pbActionData.ActionsExecuted,
			"first_started":     float64(pbActionData.FirstStartedMs) / 1000.0,
			"last_ended":        float64(pbActionData.LastEndedMs) / 1000.0,
			"total_system_time": pbActionData.SystemTime.AsDuration().Seconds,
			"total_user_time":   pbActionData.UserTime.AsDuration().Seconds,
		}
		actionDataMap[pbActionData.Mnemonic] = actionData
		// TODO: Remove unless used in the dashboards.
		s.uploadDocument(eventTime, "metrics-action-data-"+pbActionData.Mnemonic, map[string]interface{}{
			"type":        "BuildMetrics-ActionData",
			"action_data": actionData,
		})
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
		s.uploadDocument(eventTime, "metrics-runner-count-"+pbRunnerCount.Name, map[string]interface{}{
			"type":         "BuildMetrics-RunnerCount",
			"runner_count": runnerCount,
		})
	}

	localMissDetailMap := map[string]interface{}{}
	for _, pbLocalMissDetails := range payload.ActionSummary.GetActionCacheStatistics().GetMissDetails() {
		localMissDetailMap[pbLocalMissDetails.Reason.String()] = pbLocalMissDetails.Count
	}
	garbageCollected := int64(0)
	for _, pbGarbageMetrics := range payload.MemoryMetrics.GetGarbageMetrics() {
		garbageCollected += pbGarbageMetrics.GetGarbageCollected()
	}

	document := map[string]interface{}{
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
			"cpu_time":             float64(payload.TimingMetrics.GetCpuTimeInMs()) / 1000.0,
			"wall_time":            float64(payload.TimingMetrics.GetWallTimeInMs()) / 1000.0,
			"analysis_phase_time":  float64(payload.TimingMetrics.GetAnalysisPhaseTimeInMs()) / 1000.0,
			"execution_phase_time": float64(payload.TimingMetrics.GetExecutionPhaseTimeInMs()) / 1000.0,
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
	s.uploadDocument(eventTime, "metrics", document)
}

func (s *ingestingStreamClient) collectAndPublishBuildMetadata(eventTime *timestamppb.Timestamp, payload *buildeventstream.BuildMetadata) {
	s.buildMetadata = payload.Metadata
	s.uploadDocument(eventTime, "build-metadata", map[string]interface{}{
		"type": "BuildMetadata",
	})
}
