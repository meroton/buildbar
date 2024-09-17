package util

import (
	"encoding/json"

	remoteexecution "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/buildbarn/bb-storage/pkg/util"

	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ProtoDigestToJSON converts a remoteexecution.Digest to a JSON object
// where 64-bit integers are represented as floating point numbers
// instead of strings.
func ProtoDigestToJSON(digest *remoteexecution.Digest) interface{} {
	if digest == nil {
		return nil
	}
	return map[string]interface{}{
		"hash":       digest.Hash,
		"size_bytes": float64(digest.SizeBytes),
	}
}

// ProtoToJSONToInterface converts a Protobuf message to a JSON object
// where 64-bit integers are represented as floating point numbers
// instead of strings.
func ProtoToJSONToInterface(m protoreflect.ProtoMessage) map[string]interface{} {
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

// ProtoListToJSONToInterface converts a repeated Protobuf field to a JSON array
// where 64-bit integers are represented as floating point numbers
// instead of strings.
func ProtoListToJSONToInterface[V protoreflect.ProtoMessage](m []V) []interface{} {
	ret := make([]interface{}, len(m))
	for i, entry := range m {
		ret[i] = ProtoToJSONToInterface(entry)
	}
	return ret
}

// ProtoMapToJSONToInterface converts a map Protobuf field to a JSON map
// where 64-bit integers are represented as floating point numbers
// instead of strings.
func ProtoMapToJSONToInterface[V protoreflect.ProtoMessage](m map[string]V) map[string]interface{} {
	ret := make(map[string]interface{}, len(m))
	for key, value := range m {
		ret[key] = ProtoToJSONToInterface(value)
	}
	return ret
}
