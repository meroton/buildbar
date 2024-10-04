package bazelevents

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/buildbarn/bb-storage/pkg/blobstore"
	"github.com/buildbarn/bb-storage/pkg/digest"
	"github.com/buildbarn/bb-storage/pkg/util"
	"github.com/meroton/buildbar/pkg/buildevents"
	"github.com/meroton/buildbar/pkg/elasticsearch"
	"github.com/prometheus/client_golang/prometheus"

	build "google.golang.org/genproto/googleapis/devtools/build/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

/*
func doit() {
	buffer := map[string]bytes.Buffer
	idBuffer := []bytes.Buffer
	totBufferSize := 0
	if totBufferSize > 50 MiB {
		// Append to AC.
		// TODO: Why join the objects in the end? It might just create very large blobs.
		write()
		releaseReceiveABit()
	}
	switch id.(type) {
	case xyz:
		return "xyz"
	}

	ac.Get()
	"id-<from>-<to>" shows ingested documents so far.
	Check sequence number.
}
*/

var (
	blobStoreWriterPrometheusMetrics sync.Once

	blobStoreWriterEventsWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "buildbar",
		Subsystem: "buildevents",
		Name:      "cas_events_written",
		Help:      "Number of Build Events that has been written.",
	})
)

type blobStoreWriterBuffer {
	buffer List	
}

// blobStoreWriter is used to store the Bazel events in the CAS and references
// in the action cache.
type blobStoreWriter struct {
	contentAdressableStorage blobstore.BlobAccess
	actionCache              blobstore.BlobAccess
}

// NewBlobStoreWriter creates a new writer that stores the Bazel events in
// the CAS and references in the action cache.
func NewBlobStoreWriter(
	contentAdressableStorage blobstore.BlobAccess,
	actionCache blobstore.BlobAccess,
) buildevents.BuildEventServer {
	blobStoreWriterPrometheusMetrics.Do(func() {
		prometheus.MustRegister(blobStoreWriterEventsWritten)
	})
	return &blobStoreWriter{
		contentAdressableStorage: contentAdressableStorage,
		actionCache:              actionCache,
	}
}

func (bsw *blobStoreWriter) PublishLifecycleEvent(ctx context.Context, in *build.PublishLifecycleEventRequest) error {
	// noop
	return nil
}

func (bsw *blobStoreWriter) PublishBuildToolEventStream(ctx context.Context) (buildevents.BuildToolEventStreamClient, error) {
	bsw.
	return &blobStoreWriterStreamClient{
		ctx:         ctx,
		errorLogger: i.errorLogger,
		uploader:    i.uploader,

		converterFactory: i.converterFactory,
		converter:        nil,

		sendResponses: make(chan struct{}),
		ackCounter:    0,
	}, nil
}

type blobStoreWriterStreamClient struct {
	ctx         context.Context
	errorLogger util.ErrorLogger
	uploader    elasticsearch.Uploader

	converterFactory BazelEventConverterFactory
	converter        BazelEventConverter
	// instanceName is valid if converter is non-nil.
	instanceName digest.InstanceName
	// streamID is valid if converter is non-nil.
	streamID *build.StreamId

	sendResponses chan struct{}
	ackCounter    int
}

func (s *ingestingStreamClient) Send(request *buildevents.BufferedPublishBuildToolEventStreamRequest) error {
	select {
	case <-s.sendResponses:
		return status.Error(codes.InvalidArgument, "Last message already received")
	default:
		// noop
	}
	ingesterEventsReceived.Inc()

	if s.converter == nil {
		streamID := request.OrderedBuildEvent.GetStreamId()
		if streamID == nil {
			return status.Error(codes.InvalidArgument, "Stream ID is empty")
		}
		instanceName, err := digest.NewInstanceName(request.ProjectId)
		if err != nil {
			return util.StatusWrap(err, "Bad build tool event instance name")
		}
		if streamID.InvocationId == "" {
			return status.Errorf(codes.InvalidArgument, "Invocation ID is empty")
		}
		// Wrap the logger with invocationID from now on.
		s.errorLogger = &ingestingWrappedErrorLogger{
			backend:      s.errorLogger,
			instanceName: instanceName,
			invocationID: streamID.InvocationId,
		}
		s.streamID = streamID
		s.instanceName = instanceName
		s.converter = s.converterFactory(s.errorLogger)
	}

	eventTime := request.GetOrderedBuildEvent().GetEvent().GetEventTime()
	event, err := request.GetBufferedBazelBuildEvent()
	if err != nil {
		return util.StatusWrap(err, "Failed to ingest event")
	}
	if event != nil {
		documents, err := s.converter.ExtractInterestingData(s.ctx, eventTime, event)
		if err != nil {
			return util.StatusWrapf(err, "Bazel event for invocation %s", s.streamID.InvocationId)
		}
		for suffix, document := range documents {
			uuid := s.streamID.InvocationId + "-" + suffix
			documentToIngest := make(map[string]interface{}, len(document)+4)
			for k, v := range document {
				documentToIngest[k] = v
			}
			documentToIngest["ingest_time"] = time.Now().Format(time.RFC3339)
			documentToIngest["invocation_id"] = s.streamID.GetInvocationId()
			documentToIngest["correlated_invocation_id"] = s.streamID.GetBuildId()
			documentToIngest["instance_name"] = s.instanceName.String()
			if err := s.uploader.Put(s.ctx, uuid, documentToIngest); err != nil {
				return util.StatusWrapf(err, "Bazel event %s for invocation %s", uuid, s.streamID.InvocationId)
			}
		}

		// As we need all messages to grab metadata and potentially build up
		// all the file sets, the acks cannot be sent until after the
		// last message has been processed, to make sure that all messages
		// are resent on connection loss.
		// TODO: Read back BuildMetadata from some other cache.
		s.ackCounter++
		if event.LastMessage {
			close(s.sendResponses)
		}
	} else {
		s.ackCounter++
	}
	return nil
}

func (s *ingestingStreamClient) CloseSend() {
	// noop
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
