package bazelevents_test

/*
import (
	"context"
	"encoding/json"
	"testing"

	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	cas_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/cas"
	cal_proto "github.com/buildbarn/bb-remote-execution/pkg/proto/completedactionlogger"
	"github.com/buildbarn/bb-remote-execution/pkg/proto/resourceusage"
	"github.com/buildbarn/bb-storage/pkg/blobstore/buffer"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/golang/mock/gomock"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/completedaction"
	"github.com/stretchr/testify/require"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func exampleCompletedAction(t *testing.T) *cal_proto.CompletedAction {
	requestMetadata, err := anypb.New(&remoteexecution.RequestMetadata{
		ToolDetails: &remoteexecution.ToolDetails{
			ToolName:    "bazel",
			ToolVersion: "7.1.1",
		},
		ActionId:                "eb53133254a1db7290187182ba1bffcf3d444acdfadb5131776ecf9c8a91adb2",
		ToolInvocationId:        "e47e1efd-4030-4f5a-9ab9-a037f6b0d93c",
		CorrelatedInvocationsId: "8f893e8a-63e5-4c28-8e13-4137f7d0ae09",
		ActionMnemonic:          "CppCompile",
		TargetId:                "@rules_something//Package:target",
		ConfigurationId:         "41cef69dbdb6a2504c5426bee85adbdcbce09fb13c91edcd99a40b1e5e97486e",
	})
	require.NoError(t, err)

	posixResourceUsage, err := anypb.New(&resourceusage.POSIXResourceUsage{
		UserTime:                   &durationpb.Duration{Seconds: 1, Nanos: 100000000},
		SystemTime:                 &durationpb.Duration{Seconds: 1, Nanos: 200000000},
		MaximumResidentSetSize:     1003,
		PageReclaims:               1007,
		PageFaults:                 1008,
		Swaps:                      1009,
		BlockInputOperations:       1010,
		BlockOutputOperations:      1011,
		MessagesSent:               1012,
		MessagesReceived:           1013,
		SignalsReceived:            1014,
		VoluntaryContextSwitches:   1015,
		InvoluntaryContextSwitches: 1016,
	})
	require.NoError(t, err)

	filePoolResourceUsage, err := anypb.New(&resourceusage.FilePoolResourceUsage{
		FilesCreated:       2001,
		FilesCountPeak:     2002,
		FilesSizeBytesPeak: 2003,
		ReadsCount:         2004,
		ReadsSizeBytes:     2005,
		WritesCount:        2006,
		WritesSizeBytes:    2007,
		TruncatesCount:     2008,
	})
	require.NoError(t, err)

	inputRootResourceUsage, err := anypb.New(&resourceusage.InputRootResourceUsage{
		DirectoriesResolved: 3001,
		DirectoriesRead:     3002,
		FilesRead:           3003,
	})
	require.NoError(t, err)

	monetaryResourceUsage, err := anypb.New(&resourceusage.MonetaryResourceUsage{
		Expenses: map[string]*resourceusage.MonetaryResourceUsage_Expense{
			"EC2":         {Currency: "USD", Cost: 0.1851},
			"S3":          {Currency: "BTC", Cost: 0.885},
			"Electricity": {Currency: "EUR", Cost: 1.845},
			"Maintenance": {Currency: "JPY", Cost: 0.18},
		},
	})
	require.NoError(t, err)

	return &cal_proto.CompletedAction{
		HistoricalExecuteResponse: &cas_proto.HistoricalExecuteResponse{
			ActionDigest: &remoteexecution.Digest{
				Hash:      "8b1a9953c4611296a827abf8c47804d7",
				SizeBytes: 11,
			},
			ExecuteResponse: &remoteexecution.ExecuteResponse{
				Result: &remoteexecution.ActionResult{
					OutputFiles: []*remoteexecution.OutputFile{
						{
							Path: "output.o",
							Digest: &remoteexecution.Digest{
								Hash:      "8c2e88f122b6fbcf0a20d562391c93db",
								SizeBytes: 3483,
							},
						},
					},
					OutputDirectories: []*remoteexecution.OutputDirectory{
						{
							Path: "some_directory",
							TreeDigest: &remoteexecution.Digest{
								Hash:      "0342e9502cf8c4cea71de4c33669b60f",
								SizeBytes: 237944,
							},
						},
					},
					OutputSymlinks: []*remoteexecution.OutputSymlink{
						{
							Path:   "generic-symlink",
							Target: "generic-symlink-target",
						},
					},
					ExitCode:  123,
					StdoutRaw: []byte("HelloOut"),
					StdoutDigest: &remoteexecution.Digest{
						Hash:      "420601e29b3c0fb456f75fe5e9a9cb58",
						SizeBytes: 8,
					},
					StderrRaw: []byte("HelloErr"),
					StderrDigest: &remoteexecution.Digest{
						Hash:      "d4fab1f04e470bcca0b75219e1ea560d",
						SizeBytes: 8,
					},
					ExecutionMetadata: &remoteexecution.ExecutedActionMetadata{
						Worker:                         "{\"datacenter\":\"linkoping\"}",
						QueuedTimestamp:                &timestamppb.Timestamp{Seconds: 999},
						WorkerStartTimestamp:           &timestamppb.Timestamp{Seconds: 1000},
						InputFetchStartTimestamp:       &timestamppb.Timestamp{Seconds: 1011},
						InputFetchCompletedTimestamp:   &timestamppb.Timestamp{Seconds: 1032},
						ExecutionStartTimestamp:        &timestamppb.Timestamp{Seconds: 1032, Nanos: 500000000},
						ExecutionCompletedTimestamp:    &timestamppb.Timestamp{Seconds: 1043},
						OutputUploadStartTimestamp:     &timestamppb.Timestamp{Seconds: 1043},
						OutputUploadCompletedTimestamp: &timestamppb.Timestamp{Seconds: 1084},
						WorkerCompletedTimestamp:       &timestamppb.Timestamp{Seconds: 1205},
						VirtualExecutionDuration:       &durationpb.Duration{Seconds: 1, Nanos: 900000000},
						AuxiliaryMetadata: []*anypb.Any{
							requestMetadata,
							posixResourceUsage,
							filePoolResourceUsage,
							inputRootResourceUsage,
							monetaryResourceUsage,
						},
					},
				},
				Message: "Action details: https://buildbarn/my-actions/",
			},
		},
		Uuid:           "uuid",
		InstanceName:   "freebsd12",
		DigestFunction: remoteexecution.DigestFunction_MD5,
	}
}

func exampleAction(t *testing.T) *remoteexecution.Action {
	return &remoteexecution.Action{
		CommandDigest: &remoteexecution.Digest{
			Hash:      "492586011c3e1ac160fa0c524bd96091",
			SizeBytes: 34,
		},
		InputRootDigest: &remoteexecution.Digest{
			Hash:      "2dd766fc20a680ab3e1ad981521d237e",
			SizeBytes: 0,
		},
		Timeout:    &durationpb.Duration{Seconds: 3600},
		DoNotCache: false,
		Salt:       []byte("salt"),
		Platform: &remoteexecution.Platform{
			Properties: []*remoteexecution.Platform_Property{
				{
					Name:  "ISA",
					Value: "x86-64",
				},
				{
					Name:  "OSFamily",
					Value: "Linux",
				},
			},
		},
	}
}

func exampleCommand(t *testing.T) *remoteexecution.Command {
	return &remoteexecution.Command{
		Arguments: []string{"sh", "-c", "echo 'Hello world'"},
		EnvironmentVariables: []*remoteexecution.Command_EnvironmentVariable{
			{
				Name:  "MY_ENV1",
				Value: "ENV_VALUE1",
			},
			{
				Name:  "MY_ENV2",
				Value: "ENV_VALUE2",
			},
		},
		OutputPaths:           []string{"executable", "directory/file"},
		WorkingDirectory:      "cwd",
		OutputDirectoryFormat: remoteexecution.Command_DIRECTORY_ONLY,
	}
}

func TestFlattenCompletedActionFailsActionFetching(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	cas := mock.NewMockBlobAccess(ctrl)

	completedAction := exampleCompletedAction(t)
	action := exampleAction(t)
	actionDigest := digest.MustNewDigest("freebsd12", remoteexecution.DigestFunction_MD5, "8b1a9953c4611296a827abf8c47804d7", 11)
	commandDigest := digest.MustNewDigest("freebsd12", remoteexecution.DigestFunction_MD5, "492586011c3e1ac160fa0c524bd96091", 34)

	t.Run("FlattenMissingActionInCAS", func(t *testing.T) {
		cas.EXPECT().Get(ctx, actionDigest).Return(buffer.NewBufferFromError(status.Error(codes.NotFound, "Object not found")))

		document, err := completedaction.NewConverter(cas, 10240).
			FlattenCompletedAction(ctx, completedAction)
		require.Nil(t, err)
		require.NotNil(t, document)
		require.Nil(t, document["action"])
		require.Nil(t, document["command"])
		require.Equal(t, "Failed to get action: Object not found", document["action_fetch_error"])
	})

	t.Run("FlattenMissingCommandInCAS", func(t *testing.T) {
		cas.EXPECT().Get(ctx, actionDigest).Return(buffer.NewProtoBufferFromProto(action, buffer.UserProvided))
		cas.EXPECT().Get(ctx, commandDigest).Return(buffer.NewBufferFromError(status.Error(codes.NotFound, "Object not found")))

		document, err := completedaction.NewConverter(cas, 10240).
			FlattenCompletedAction(ctx, completedAction)
		require.Nil(t, err)
		require.NotNil(t, document)
		require.NotNil(t, document["action"])
		require.Nil(t, document["command"])
		require.Equal(t, "Failed to get command: Object not found", document["action_fetch_error"])
	})
}

func TestFlattenCompletedActionSuccessfully(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	cas := mock.NewMockBlobAccess(ctrl)

	completedAction := exampleCompletedAction(t)
	action := exampleAction(t)
	command := exampleCommand(t)
	actionDigest := digest.MustNewDigest("freebsd12", remoteexecution.DigestFunction_MD5, "8b1a9953c4611296a827abf8c47804d7", 11)
	commandDigest := digest.MustNewDigest("freebsd12", remoteexecution.DigestFunction_MD5, "492586011c3e1ac160fa0c524bd96091", 34)

	cas.EXPECT().Get(ctx, actionDigest).Return(buffer.NewProtoBufferFromProto(action, buffer.UserProvided))
	cas.EXPECT().Get(ctx, commandDigest).Return(buffer.NewProtoBufferFromProto(command, buffer.UserProvided))

	document, err := completedaction.NewConverter(cas, 10240).
		FlattenCompletedAction(ctx, completedAction)
	require.Nil(t, err)
	require.NotNil(t, document)

	partialexpectedJson := `
	{
		"action": {
			"command_digest": {
				"hash": "492586011c3e1ac160fa0c524bd96091",
				"size_bytes": 34
			},
			"input_root_digest": {
				"hash": "2dd766fc20a680ab3e1ad981521d237e",
				"size_bytes": 0
			},
			"timeout": 3600,
			"salt": "salt",
			"salt_bytes": "73616c74",
			"platform": null,
			"platform_list": [
				{
					"name": "ISA",
					"value": "x86-64"
				},
				{
					"name": "OSFamily",
					"value": "Linux"
				}
			]
		},
		"command": {
			"arguments": "sh -c 'echo '\\''Hello world'\\'",
			"arguments_list": null,
			"argv0": "sh",
			"argv1": "-c",
			"argv2": "echo 'Hello world'",
			"cmd0": "echo",
			"cmd1": "'Hello",
			"cmd2": "world'",
			"environment": null,
			"environment_list": [
				{
					"name": "MY_ENV1",
					"value": "ENV_VALUE1"
				},
				{
					"name": "MY_ENV2",
					"value": "ENV_VALUE2"
				}
			],
			"output_paths": null,
			"working_directory": "cwd",
			"output_directory_format": "DIRECTORY_ONLY"
		},
		"digest_function": "MD5",
		"action_digest": {
			"hash": "8b1a9953c4611296a827abf8c47804d7",
			"size_bytes": 11
		},
		"execute_response": {
			"message": "Action details: https://buildbarn/my-actions/",
			"cached_result": false,
			"status": "OK",
			"server_logs": {}
		},
		"result": {
			"exit_code": 123,
			"output_directories": [
				{
					"path": "some_directory",
					"tree_digest": {
						"hash": "0342e9502cf8c4cea71de4c33669b60f",
						"size_bytes": "237944"
					},
					"is_topologically_sorted": false
				}
			],
			"output_directories_count": 1,
			"output_files": [
				{
					"digest": {
						"hash": "8c2e88f122b6fbcf0a20d562391c93db",
						"size_bytes": 3483
					},
					"path": "output.o",
					"is_executable": false,
					"node_properties": {
						"properties": []
					}
				}
			],
			"output_files_count": 1,
			"output_symlinks": [
				{
					"path": "generic-symlink",
					"target": "generic-symlink-target"
				}
			],
			"output_symlinks_count": 1,
			"total_output_count": 3,
			"stderr_digest": {
				"hash": "d4fab1f04e470bcca0b75219e1ea560d",
				"size_bytes": 8
			},
			"stdout_digest": {
				"hash": "420601e29b3c0fb456f75fe5e9a9cb58",
				"size_bytes": 8
			}
		},
		"metadata": {
			"request": {
				"action_id": "eb53133254a1db7290187182ba1bffcf3d444acdfadb5131776ecf9c8a91adb2",
				"action_mnemonic": "CppCompile",
				"configuration_id": "41cef69dbdb6a2504c5426bee85adbdcbce09fb13c91edcd99a40b1e5e97486e",
				"correlated_invocations_id": "8f893e8a-63e5-4c28-8e13-4137f7d0ae09",
				"target_id": "@rules_something//Package:target",
				"tool_details": {
					"tool_name": "bazel",
					"tool_version": "7.1.1"
				},
				"tool_invocation_id": "e47e1efd-4030-4f5a-9ab9-a037f6b0d93c"
			},
			"posix": {
				"user_time": 1.1,
				"system_time": 1.2,
				"maximum_resident_set_size": 1003,
				"page_reclaims": 1007,
				"page_faults": 1008,
				"swaps": 1009,
				"block_input_operations": 1010,
				"block_output_operations": 1011,
				"messages_sent": 1012,
				"messages_received": 1013,
				"signals_received": 1014,
				"voluntary_context_switches": 1015,
				"involuntary_context_switches": 1016
			},
			"file_pool": {
				"files_created": 2001,
				"files_count_peak": 2002,
				"files_size_bytes_peak": 2003,
				"reads_count": 2004,
				"reads_size_bytes": 2005,
				"writes_count": 2006,
				"writes_size_bytes": 2007,
				"truncates_count": 2008
			},
			"input_root": {
				"directories_read": 3002,
				"directories_resolved": 3001,
				"files_read": 3003
			},
			"monetary": {
				"expenses": {
					"EC2": {
						"cost": 0.1851,
						"currency": "USD"
					},
					"Electricity": {
						"cost": 1.845,
						"currency": "EUR"
					},
					"Maintenance": {
						"cost": 0.18,
						"currency": "JPY"
					},
					"S3": {
						"cost": 0.885,
						"currency": "BTC"
					}
				}
			},
			"queued_timestamp": "1970-01-01T00:16:39Z",
			"worker_start_timestamp": "1970-01-01T00:16:40Z",
			"queued_duration": 1,
			"input_fetch_start_timestamp": "1970-01-01T00:16:51Z",
			"startup_duration": 11,
			"input_fetch_completed_timestamp": "1970-01-01T00:17:12Z",
			"input_fetch_duration": 21,
			"execution_start_timestamp": "1970-01-01T00:17:12.500Z",
			"execution_completed_timestamp": "1970-01-01T00:17:23Z",
			"wall_execution_duration": 10.5,
			"output_upload_start_timestamp": "1970-01-01T00:17:23Z",
			"output_upload_completed_timestamp": "1970-01-01T00:18:04Z",
			"output_upload_duration": 41,
			"worker_completed_timestamp": "1970-01-01T00:20:05Z",
			"virtual_execution_duration": 1.9,
			"total_worker_duration": 205,
			"total_queue_and_execute_duration": 206,
			"worker": "{\"datacenter\":\"linkoping\"}",
			"worker_json": {
				"datacenter": "linkoping"
			}
		},
		"instance_name": "freebsd12",
		"uuid": "uuid"
	}
	`
	expected := map[string]interface{}{}
	err = json.Unmarshal([]byte(partialexpectedJson), &expected)
	require.Nil(t, err)
	// Replace some map[string]interface{} with map[string]string as the
	// converter returns that.
	expected["action"].(map[string]interface{})["platform"] = map[string]string{
		"ISA":      "x86-64",
		"OSFamily": "Linux",
	}
	expectedCommand := expected["command"].(map[string]interface{})
	expectedCommand["arguments_list"] = []string{"sh", "-c", "echo 'Hello world'"}
	expectedCommand["environment"] = map[string]string{
		"MY_ENV1": "ENV_VALUE1",
		"MY_ENV2": "ENV_VALUE2",
	}
	expectedCommand["output_paths"] = []string{
		"executable",
		"directory/file",
	}
	expectedResult := expected["result"].(map[string]interface{})
	expectedResult["output_directories_count"] = int(expectedResult["output_directories_count"].(float64))
	expectedResult["output_files_count"] = int(expectedResult["output_files_count"].(float64))
	expectedResult["output_symlinks_count"] = int(expectedResult["output_symlinks_count"].(float64))
	expectedResult["total_output_count"] = int(expectedResult["total_output_count"].(float64))
	expectedResult["exit_code"] = int32(expectedResult["exit_code"].(float64))

	require.Equal(t, expected, document)

	documentJson, err := json.Marshal(document)
	require.Nil(t, err)

	fullExpectedJson, err := json.Marshal(expected)
	require.Nil(t, err)
	require.Equal(t, string(fullExpectedJson), string(documentJson))
}
*/
