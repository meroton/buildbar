package bazelevents_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	buildeventstream "github.com/bazelbuild/bazel/src/main/java/com/google/devtools/build/lib/buildeventstream/proto"
	"github.com/meroton/buildbar/internal/mock"
	"github.com/meroton/buildbar/pkg/bazelevents"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func runfile(t *testing.T, path string) string {
	return path
	// "github.com/bazelbuild/rules_go/go/runfiles"
	// resolvedPath, err := runfiles.Rlocation(path)
	// require.NoError(t, err)
	// return resolvedPath
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
		var expectedDocumentsRaw interface{}
		require.NoError(t, json.Unmarshal(documentsRaw, &expectedDocumentsRaw))
		expectedDocuments := expectedDocumentsRaw.([]interface{})

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

			foo, err := json.Marshal(actualDocuments)
			require.NoError(t, err)
			fmt.Println(bazelEvent)
			fmt.Println(string(foo))
			// require.Equal(t, expectedDocuments[lineIdx], actualDocuments)
		}
		require.NoError(t, inputScanner.Err())
		require.Equal(t, len(expectedDocuments), lineIdx)
	})
	/*
	   // The BuildMetadata event should arrive as one of the first events.

	   	t.Run("IgnoreBuildMetadataAfterSomeEvents", func(t *testing.T) {
	   		ingestContext := context.WithValue(ctx, "key", "value")
	   		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime1, bazelEventStarted).Return(startedDocuments, nil)
	   		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, bazelEventMetadata).Return(metadataDocuments, nil)
	   		converter.EXPECT().ExtractInterestingData(ingestContext, eventTime2, bazelEventFinished).Return(finishedDocuments, nil)
	   		uploader.EXPECT().Put(ingestContext, "my_invocation_id-started", startedDocuments["started"]).Return(nil)
	   		uploader.EXPECT().Put(ingestContext, "my_invocation_id-metadata", metadataDocuments["metadata"]).Return(nil)
	   		uploader.EXPECT().Put(ingestContext, "my_invocation_id-finished", finishedDocuments["finished"]).Return(nil)

	   		ingester := bazelevents.NewIngester(converterFactory, uploader, errorLogger)
	   		stream, err := ingester.PublishBazelEvents(ingestContext, digest.MustNewInstanceName("my-instance"), &build.StreamId{
	   			InvocationId: "my_invocation_id",
	   		})
	   		require.NoError(t, err)
	   		require.NoError(t, stream.Send(eventTime1, bazelEventStarted))
	   		require.NoError(t, stream.Send(eventTime2, bazelEventMetadata))
	   		require.NoError(t, stream.Send(eventTime2, bazelEventFinished))
	   		require.NoError(t, stream.Recv())
	   		require.NoError(t, stream.Recv())
	   		require.NoError(t, stream.Recv())
	   	})
	*/
}
