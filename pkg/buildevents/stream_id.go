package buildevents

import build "google.golang.org/genproto/googleapis/devtools/build/v1"

func equalStreamID(lhs, rhs *build.StreamId) bool {
	return lhs.BuildId == rhs.BuildId &&
		lhs.Component == rhs.Component &&
		lhs.InvocationId == rhs.InvocationId
}
