package bazelevents_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"testing"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/buildbarn/bb-storage/pkg/testutil"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/bazelevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runfile(t *testing.T, path string) string {
	return path
	// TODO: Is Bazel's runfiles library needed?
	// "github.com/bazelbuild/rules_go/go/runfiles"
	// resolvedPath, err := runfiles.Rlocation(path)
	// require.NoError(t, err)
	// return resolvedPath
}

func mustToAndFromJSON(t *testing.T, value interface{}) interface{} {
	jsonString, err := json.Marshal(value)
	require.NoError(t, err)
	var rawValue interface{}
	require.NoError(t, json.Unmarshal(jsonString, &rawValue))
	return rawValue
}

func TestExtractInterestingBazelEventData(t *testing.T) {
	ctrl, ctx := gomock.WithContext(context.Background(), t)

	errorLogger := mock.NewMockErrorLogger(ctrl)

	t.Run("GoldenSuccessfulTest", func(t *testing.T) {
		converter := bazelevents.NewBazelEventConverter(errorLogger)

		// The expected output is a JSON encoded list where each
		// element is a list of the expected produced documents.
		resultsPath := runfile(t, "testdata/golden.documents.json")
		documentsRaw, err := os.ReadFile(resultsPath)
		require.NoError(t, err, "Failed to open %s", resultsPath)
		var expectedDocumentsAny interface{}
		require.NoError(t, json.Unmarshal(documentsRaw, &expectedDocumentsAny))
		expectedDocuments := expectedDocumentsAny.([]interface{})

		// Read the input line by line, one event per line.
		inputPath := runfile(t, "testdata/golden.bep.ndjson")
		inputFile, err := os.Open(inputPath)
		require.NoError(t, err, "Failed to open %s", inputPath)
		defer inputFile.Close()
		inputScanner := bufio.NewScanner(inputFile)

		lineIdx := 0
		for ; inputScanner.Scan(); lineIdx++ {
			line := inputScanner.Bytes()
			bazelEvent := &buildeventstream.BuildEvent{}
			require.NoError(t, protojson.Unmarshal(line, bazelEvent))

			eventTime := &timestamppb.Timestamp{Seconds: int64(lineIdx + 1)}
			actualDocuments, err := converter.ExtractInterestingData(ctx, eventTime, bazelEvent)
			require.NoError(t, err)

			// Print the documents for debugging.
			/*
				actualJsonString, err := json.Marshal(actualDocuments)
				require.NoError(t, err)
				fmt.Println(string(actualJsonString))
			*/
			require.Equal(t, expectedDocuments[lineIdx], mustToAndFromJSON(t, actualDocuments))
		}
		require.NoError(t, inputScanner.Err())
		require.Equal(t, len(expectedDocuments), lineIdx)
	})

	// The BuildMetadata event should arrive as one of the first events.
	t.Run("IgnoreBuildMetadataAfterSomeEvents", func(t *testing.T) {
		errorLogger.EXPECT().Log(
			testutil.EqStatus(t, status.Error(codes.FailedPrecondition, "Too many documents accumulated without build metadata")),
		).Times(1)

		converter := bazelevents.NewBazelEventConverter(errorLogger)
		ingestContext := context.WithValue(ctx, "key", "value")

		for i := 0; i < 60; i++ {
			eventTime := &timestamppb.Timestamp{Seconds: int64(i)}
			bazelEvent := &buildeventstream.BuildEvent{
				Id: &buildeventstream.BuildEventId{
					Id: &buildeventstream.BuildEventId_TargetSummary{
						TargetSummary: &buildeventstream.BuildEventId_TargetSummaryId{
							Label: strconv.FormatInt(int64(i), 10),
							Configuration: &buildeventstream.BuildEventId_ConfigurationId{
								Id: "cfg",
							},
						},
					},
				},
				Payload: &buildeventstream.BuildEvent_TargetSummary{
					TargetSummary: &buildeventstream.TargetSummary{
						OverallBuildSuccess: false,
						OverallTestStatus:   buildeventstream.TestStatus_PASSED,
					},
				},
			}
			actualDocuments, err := converter.ExtractInterestingData(ingestContext, eventTime, bazelEvent)
			require.NoError(t, err)

			require.Len(t, actualDocuments, 1)
			require.Equal(t, map[string]interface{}{
				fmt.Sprintf("%d-cfg", i): map[string]interface{}{
					"event_time": fmt.Sprintf("1970-01-01T00:00:%02dZ", i),
					"metadata": map[string]interface{}{
						"no-metadata-received-yet": "1",
					},
					"type":  "TargetSummary",
					"label": strconv.FormatInt(int64(i), 10),
					"platform": map[string]interface{}{
						"mnemonic": "unknown",
						"name":     "unknown",
						"type":     "unknown",
					},
					"target_summary": map[string]interface{}{
						"overall_build_success": false,
						"overall_test_status":   "PASSED",
					},
				},
			}, mustToAndFromJSON(t, actualDocuments))
		}
		lotsOfDocuments, err := converter.ExtractInterestingData(
			ingestContext,
			&timestamppb.Timestamp{Seconds: int64(1000)},
			&buildeventstream.BuildEvent{
				Payload: &buildeventstream.BuildEvent_BuildMetadata{
					BuildMetadata: &buildeventstream.BuildMetadata{
						Metadata: map[string]string{
							"key": "value",
						},
					},
				},
			},
		)
		require.NoError(t, err)
		require.Len(t, lotsOfDocuments, 26)
		require.Equal(t, map[string]interface{}{
			"event_time": "1970-01-01T00:16:40Z",
			"metadata":   map[string]interface{}{"key": "value"},
			"type":       "BuildMetadata",
		}, mustToAndFromJSON(t, lotsOfDocuments["build-metadata"]))
		// Check two of the TargetSummary documents.
		require.Equal(t, map[string]interface{}{
			"event_time": "1970-01-01T00:00:00Z",
			"metadata":   map[string]interface{}{"key": "value"},
			"type":       "TargetSummary",
			"label":      "0",
			"platform": map[string]interface{}{
				"mnemonic": "unknown",
				"name":     "unknown",
				"type":     "unknown",
			},
			"target_summary": map[string]interface{}{
				"overall_build_success": false,
				"overall_test_status":   "PASSED",
			},
		}, mustToAndFromJSON(t, lotsOfDocuments["0-cfg"]))
		require.Equal(t, map[string]interface{}{
			"event_time": "1970-01-01T00:00:24Z",
			"metadata":   map[string]interface{}{"key": "value"},
			"type":       "TargetSummary",
			"label":      "24",
			"platform": map[string]interface{}{
				"mnemonic": "unknown",
				"name":     "unknown",
				"type":     "unknown",
			},
			"target_summary": map[string]interface{}{
				"overall_build_success": false,
				"overall_test_status":   "PASSED",
			},
		}, mustToAndFromJSON(t, lotsOfDocuments["24-cfg"]))
	})
}
