package aliases

import (
	"net/http"
)

// This file contains aliases for some of the interfaces provided by the
// Go standard library. The only reason this file exists is to allow the
// gomock() Bazel rule to emit mocks for them, as that rule is only
// capable of emitting mocks for interfaces built through a
// go_library().

// RoundTripper is an alias of http.RoundTripper.
type RoundTripper = http.RoundTripper
