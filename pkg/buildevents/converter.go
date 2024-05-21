package completedaction

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	"github.com/buildbarn/bb-remote-execution/pkg/proto/resourceusage"
	"github.com/buildbarn/bb-storage/pkg/blobstore"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/kballard/go-shellquote"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Converter converts a CompletedAction into a format that is
// suitable for Elasticsearch.
type Converter interface {
	FlattenCompletedAction(ctx context.Context, completedAction *cal_proto.CompletedAction) (map[string]interface{}, error)
}

type converter struct {
	contentAddressableStorage blobstore.BlobAccess
	maximumMessageSizeBytes   int
}

// NewConverter creates a new Converter.
func NewConverter(
	contentAddressableStorage blobstore.BlobAccess,
	maximumMessageSizeBytes int,
) Converter {
	return &converter{
		contentAddressableStorage: contentAddressableStorage,
		maximumMessageSizeBytes:   maximumMessageSizeBytes,
	}
}

// FlattenCompletedAction expands certain array structures to be able to index
// data in Elasticsearch. It also populates the message with extra useful
// attributes.
func (cac *converter) FlattenCompletedAction(ctx context.Context, completedAction *cal_proto.CompletedAction) (map[string]interface{}, error) {
	document := map[string]interface{}{
		"action_digest":   convertDigest(completedAction.HistoricalExecuteResponse.GetActionDigest()),
		"uuid":            completedAction.Uuid,
		"instance_name":   completedAction.InstanceName,
		"digest_function": completedAction.DigestFunction.String(),
	}

	action, command, getActionErr := cac.getAction(ctx, completedAction)
	if getActionErr != nil {
		document["action_fetch_error"] = status.Convert(getActionErr).Message()
	}
	if action != nil {
		platformMap := map[string]string{}
		for _, platformProperty := range action.Platform.GetProperties() {
			existingValue, ok := platformMap[platformProperty.Name]
			if ok {
				existingValue += ","
			}
			platformMap[platformProperty.Name] = existingValue + platformProperty.Value
		}
		document["action"] = map[string]interface{}{
			"command_digest":    convertDigest(action.CommandDigest),
			"input_root_digest": convertDigest(action.InputRootDigest),
			"timeout":           action.Timeout.AsDuration().Seconds(),
			"salt":              string(action.Salt),
			"salt_bytes":        hex.EncodeToString(action.Salt),
			"platform":          platformMap,
			"platform_list":     protoListToJSONToInterface(action.Platform.GetProperties()),
		}
	}
	if command != nil {
		environmentMap := map[string]string{}
		for _, env := range command.EnvironmentVariables {
			environmentMap[env.Name] = env.Value
		}
		convertedCommand := map[string]interface{}{
			"arguments_list":          command.Arguments,
			"arguments":               shellquote.Join(command.Arguments...),
			"environment":             environmentMap,
			"environment_list":        protoListToJSONToInterface(command.EnvironmentVariables),
			"output_paths":            command.OutputPaths,
			"working_directory":       command.WorkingDirectory,
			"output_directory_format": command.OutputDirectoryFormat.String(),
		}
		// argv0 is often interesting. Sometimes also a few more arguments,
		// for example `python3 -B file.py`.`
		for i, arg := range command.Arguments {
			if i >= 4 {
				// Don't bloat the database with too many fields.
				break
			}
			convertedCommand[fmt.Sprintf("argv%d", i)] = arg
		}
		// Do the same but for `sh -c '...'`.
		if len(command.Arguments) >= 3 && command.Arguments[1] == "-c" {
			for i, arg := range strings.Split(command.Arguments[2], " ") {
				if i >= 4 {
					// Don't bloat the database with too many fields.
					break
				}
				convertedCommand[fmt.Sprintf("cmd%d", i)] = arg
			}
		}
		document["command"] = convertedCommand
	}
	executeResponse := completedAction.HistoricalExecuteResponse.GetExecuteResponse()
	if executeResponse != nil {
		document["execute_response"] = map[string]interface{}{
			"message":       executeResponse.Message,
			"cached_result": executeResponse.CachedResult,
			"status":        codes.Code(executeResponse.Status.GetCode()).String(),
			"server_logs":   protoMapToJSONToInterface(executeResponse.ServerLogs),
		}
	}
	result := executeResponse.GetResult()
	if result != nil {
		convertedOutputFiles := make([]interface{}, len(result.OutputFiles))
		for i, outputFile := range result.OutputFiles {
			convertedOutputFiles[i] = map[string]interface{}{
				"path":            outputFile.Path,
				"digest":          convertDigest(outputFile.Digest),
				"is_executable":   outputFile.IsExecutable,
				"node_properties": protoToJSONToInterface(outputFile.NodeProperties),
			}
		}
		document["result"] = map[string]interface{}{
			"exit_code":          result.ExitCode,
			"output_directories": protoListToJSONToInterface(result.OutputDirectories),
			"output_files":       convertedOutputFiles,
			"output_symlinks":    protoListToJSONToInterface(result.OutputSymlinks),
			"stdout_digest":      convertDigest(result.GetStdoutDigest()),
			"stderr_digest":      convertDigest(result.GetStderrDigest()),
			// Calculate some extra metrics, nice to have.
			"output_directories_count": len(result.OutputDirectories),
			"output_files_count":       len(result.OutputFiles),
			"output_symlinks_count":    len(result.OutputSymlinks),
			"total_output_count":       len(result.OutputDirectories) + len(result.OutputFiles) + len(result.OutputSymlinks),
		}
	}
	executionMetadata := result.GetExecutionMetadata()
	if executionMetadata != nil {
		metadata := protoToJSONToInterface(executionMetadata)
		delete(metadata, "auxiliary_metadata")
		// Decode the worker string in case it is a JSON formatted string.
		var workerJSON interface{}
		if json.Unmarshal([]byte(executionMetadata.GetWorker()), &workerJSON) == nil {
			metadata["worker_json"] = workerJSON
		}
		// Convert durations
		metadata["virtual_execution_duration"] = convertDuration(executionMetadata.VirtualExecutionDuration)
		if executionMetadata.WorkerStartTimestamp.IsValid() && executionMetadata.QueuedTimestamp.IsValid() {
			metadata["queued_duration"] = executionMetadata.WorkerStartTimestamp.AsTime().Sub(
				executionMetadata.QueuedTimestamp.AsTime()).Seconds()
		}
		if executionMetadata.InputFetchStartTimestamp.IsValid() && executionMetadata.WorkerStartTimestamp.IsValid() {
			metadata["startup_duration"] = executionMetadata.InputFetchStartTimestamp.AsTime().Sub(
				executionMetadata.WorkerStartTimestamp.AsTime()).Seconds()
		}
		if executionMetadata.InputFetchCompletedTimestamp.IsValid() && executionMetadata.InputFetchStartTimestamp.IsValid() {
			metadata["input_fetch_duration"] = executionMetadata.InputFetchCompletedTimestamp.AsTime().Sub(
				executionMetadata.InputFetchStartTimestamp.AsTime()).Seconds()
		}
		if executionMetadata.ExecutionCompletedTimestamp.IsValid() && executionMetadata.ExecutionStartTimestamp.IsValid() {
			metadata["wall_execution_duration"] = executionMetadata.ExecutionCompletedTimestamp.AsTime().Sub(
				executionMetadata.ExecutionStartTimestamp.AsTime()).Seconds()
		}
		if executionMetadata.OutputUploadCompletedTimestamp.IsValid() && executionMetadata.OutputUploadStartTimestamp.IsValid() {
			metadata["output_upload_duration"] = executionMetadata.OutputUploadCompletedTimestamp.AsTime().Sub(
				executionMetadata.OutputUploadStartTimestamp.AsTime()).Seconds()
		}
		if executionMetadata.WorkerCompletedTimestamp.IsValid() && executionMetadata.WorkerStartTimestamp.IsValid() {
			metadata["total_worker_duration"] = executionMetadata.WorkerCompletedTimestamp.AsTime().Sub(
				executionMetadata.WorkerStartTimestamp.AsTime()).Seconds()
		}
		if executionMetadata.WorkerCompletedTimestamp.IsValid() && executionMetadata.QueuedTimestamp.IsValid() {
			metadata["total_queue_and_execute_duration"] = executionMetadata.WorkerCompletedTimestamp.AsTime().Sub(
				executionMetadata.QueuedTimestamp.AsTime()).Seconds()
		}

		var unknownMetadataTypes []string
		for _, auxiliaryMetadata := range executionMetadata.GetAuxiliaryMetadata() {
			var filePool resourceusage.FilePoolResourceUsage
			var inputRoot resourceusage.InputRootResourceUsage
			var monetary resourceusage.MonetaryResourceUsage
			var posix resourceusage.POSIXResourceUsage
			var request remoteexecution.RequestMetadata
			if auxiliaryMetadata.UnmarshalTo(&filePool) == nil {
				metadata["file_pool"] = map[string]interface{}{
					"files_created":         float64(filePool.FilesCreated),
					"files_count_peak":      float64(filePool.FilesCountPeak),
					"files_size_bytes_peak": float64(filePool.FilesSizeBytesPeak),
					"reads_count":           float64(filePool.ReadsCount),
					"reads_size_bytes":      float64(filePool.ReadsSizeBytes),
					"writes_count":          float64(filePool.WritesCount),
					"writes_size_bytes":     float64(filePool.WritesSizeBytes),
					"truncates_count":       float64(filePool.TruncatesCount),
				}
			} else if auxiliaryMetadata.UnmarshalTo(&inputRoot) == nil {
				metadata["input_root"] = map[string]interface{}{
					"directories_resolved": float64(inputRoot.DirectoriesResolved),
					"directories_read":     float64(inputRoot.DirectoriesRead),
					"files_read":           float64(inputRoot.FilesRead),
				}
			} else if auxiliaryMetadata.UnmarshalTo(&monetary) == nil {
				metadata["monetary"] = protoToJSONToInterface(&monetary)
			} else if auxiliaryMetadata.UnmarshalTo(&posix) == nil {
				metadata["posix"] = map[string]interface{}{
					"user_time":                    convertDuration(posix.UserTime),
					"system_time":                  convertDuration(posix.SystemTime),
					"maximum_resident_set_size":    float64(posix.MaximumResidentSetSize),
					"page_reclaims":                float64(posix.PageReclaims),
					"page_faults":                  float64(posix.PageFaults),
					"swaps":                        float64(posix.Swaps),
					"block_input_operations":       float64(posix.BlockInputOperations),
					"block_output_operations":      float64(posix.BlockOutputOperations),
					"messages_sent":                float64(posix.MessagesSent),
					"messages_received":            float64(posix.MessagesReceived),
					"signals_received":             float64(posix.SignalsReceived),
					"voluntary_context_switches":   float64(posix.VoluntaryContextSwitches),
					"involuntary_context_switches": float64(posix.InvoluntaryContextSwitches),
				}
			} else if auxiliaryMetadata.UnmarshalTo(&request) == nil {
				metadata["request"] = protoToJSONToInterface(&request)
			} else {
				typeStr := strings.TrimPrefix(auxiliaryMetadata.TypeUrl, "type.googleapis.com/")
				unknownMetadataTypes = append(unknownMetadataTypes, typeStr)
			}
		}
		document["metadata"] = metadata
		if unknownMetadataTypes != nil {
			document["unknown_metadata_types"] = unknownMetadataTypes
		}
	}
	return document, nil
}

func convertDigest(digest *remoteexecution.Digest) map[string]interface{} {
	if digest == nil {
		return nil
	}
	return map[string]interface{}{
		"hash":       digest.Hash,
		"size_bytes": float64(digest.SizeBytes),
	}
}

func convertDuration(duration *durationpb.Duration) float64 {
	if duration == nil {
		return 0
	}
	return duration.AsDuration().Seconds()
}

func protoToJSONToInterface(m protoreflect.ProtoMessage) map[string]interface{} {
	marshaled, err := protojson.MarshalOptions{
		UseEnumNumbers:    false,
		UseProtoNames:     true,
		EmitDefaultValues: true,
	}.Marshal(m)
	if err != nil {
		return map[string]interface{}{
			"error": status.Convert(util.StatusWrap(err, "Failed to marshal")).Message(),
		}
	}
	ret := map[string]interface{}{}
	if err := json.Unmarshal(marshaled, &ret); err != nil {
		return map[string]interface{}{
			"error": status.Convert(util.StatusWrap(err, "Failed to unmarshal")).Message(),
		}
	}
	return ret
}

func protoListToJSONToInterface[V protoreflect.ProtoMessage](m []V) []interface{} {
	ret := make([]interface{}, len(m))
	for i, entry := range m {
		ret[i] = protoToJSONToInterface(entry)
	}
	return ret
}

func protoMapToJSONToInterface[V protoreflect.ProtoMessage](m map[string]V) map[string]interface{} {
	ret := make(map[string]interface{}, len(m))
	for key, value := range m {
		ret[key] = protoToJSONToInterface(value)
	}
	return ret
}

func (cac *converter) getAction(ctx context.Context, completedAction *cal_proto.CompletedAction) (
	*remoteexecution.Action, *remoteexecution.Command, error,
) {
	instanceName, err := digest.NewInstanceName(completedAction.InstanceName)
	if err != nil {
		return nil, nil, util.StatusWrapf(err, "Invalid instance name %#v", completedAction.InstanceName)
	}
	digestFunction, err := instanceName.GetDigestFunction(completedAction.DigestFunction, len(completedAction.HistoricalExecuteResponse.GetActionDigest().GetHash()))
	if err != nil {
		return nil, nil, err
	}

	actionDigest, err := digestFunction.NewDigestFromProto(completedAction.HistoricalExecuteResponse.GetActionDigest())
	if err != nil {
		return nil, nil, util.StatusWrap(err, "Invalid action digest")
	}
	actionMsg, err := cac.contentAddressableStorage.Get(ctx, actionDigest).ToProto(
		&remoteexecution.Action{},
		cac.maximumMessageSizeBytes,
	)
	if err != nil {
		return nil, nil, util.StatusWrap(err, "Failed to get action")
	}
	action := actionMsg.(*remoteexecution.Action)

	commandDigest, err := digestFunction.NewDigestFromProto(action.CommandDigest)
	if err != nil {
		return action, nil, util.StatusWrap(err, "Failed to get command")
	}
	commandMsg, err := cac.contentAddressableStorage.Get(ctx, commandDigest).ToProto(
		&remoteexecution.Command{},
		cac.maximumMessageSizeBytes,
	)
	if err != nil {
		return action, nil, util.StatusWrap(err, "Failed to get command")
	}
	command := commandMsg.(*remoteexecution.Command)
	return action, command, nil
}
