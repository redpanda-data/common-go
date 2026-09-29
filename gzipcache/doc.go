// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package gzipcache provides a gzip compressor for connect-go that compresses
// each distinct payload once.
//
// A server that sends the same message on many streams (a watch or pub/sub
// fanout) pays for one deflate per stream with connect's built-in gzip. The
// compressor in this package keys every payload by its content: the first
// send compresses it, concurrent sends of the same bytes wait for that result,
// and later sends copy the cached frame. A cache hit is verified byte for
// byte, so a hash collision never serves the wrong payload.
//
// connect drives a compressor as Reset(dst), one Write with the whole
// message, Close. Any other call sequence falls back to plain gzip, which is
// slower but always correct.
package gzipcache
