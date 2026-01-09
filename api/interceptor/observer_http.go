// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package interceptor

import (
	"io"
	"net/http"
)

type bodyReader struct {
	base      io.ReadCloser
	bytesRead *int64
}

func (r bodyReader) Read(p []byte) (int, error) {
	n, err := r.base.Read(p)
	*r.bytesRead += int64(n)
	return n, err
}

func (r bodyReader) Close() error {
	return r.base.Close()
}

type responseWriteFlusher interface {
	http.ResponseWriter
	http.Flusher
}

type responseController struct {
	base responseWriteFlusher
	rm   *RequestMetadata
}

var _ responseWriteFlusher = (*responseController)(nil)

func (r responseController) Header() http.Header {
	return r.base.Header()
}

func (r responseController) Write(p []byte) (int, error) {
	n, err := r.base.Write(p)
	r.rm.bytesSent += int64(n)
	return n, err
}

func (r responseController) WriteHeader(statusCode int) {
	r.rm.httpStatusCode = statusCode
	r.base.WriteHeader(statusCode)
}

func (r responseController) Flush() {
	r.base.Flush()
}

func (r responseController) Unwrap() http.ResponseWriter {
	return r.base
}
