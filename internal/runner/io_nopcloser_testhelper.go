package runner

import "io"

// ioNopCloser is a tiny helper to avoid importing io.NopCloser in many tests.
func ioNopCloser(r io.Reader) io.ReadCloser { return io.NopCloser(r) }
