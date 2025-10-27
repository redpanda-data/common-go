// Copyright 2021 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/foxcpp/go-mockdns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// none of these tests in this package should be called in parallel
// due to needing to check that all responses are closed in this
// global tracker

var responsesPendingClosure atomic.Int64

type trackedReadCloser struct {
	io.ReadCloser
	onClose func()
}

func (c *trackedReadCloser) Close() error {
	c.onClose()
	return c.ReadCloser.Close()
}

func trackResponses(resp *http.Response) *http.Response {
	responsesPendingClosure.Add(1)

	resp.Body = &trackedReadCloser{ReadCloser: resp.Body, onClose: func() {
		responsesPendingClosure.Add(-1)
	}}

	return resp
}

func init() {
	responseWrapper = trackResponses
}

type testCall struct {
	brokerID int
	req      *http.Request
}
type reqsTest struct {
	// setup
	name     string
	nNodes   int
	leaderID int
	// action
	action func(*testing.T, *AdminAPI) error
	// assertions
	all      []string
	any      []string
	leader   []string
	none     []string
	handlers map[string]http.HandlerFunc
}

func TestAdminAPI(t *testing.T) {
	tests := []reqsTest{
		{
			name:     "delete user in 1 node cluster",
			nNodes:   1,
			leaderID: 0,
			action:   func(_ *testing.T, a *AdminAPI) error { return a.DeleteUser(context.Background(), "Milo") },
			leader:   []string{"/v1/security/users/Milo"},
			none:     []string{"/v1/partitions/redpanda/controller/0", "/v1/node_config"},
		},
		{
			name:     "delete user in 3 node cluster",
			nNodes:   3,
			leaderID: 1,
			action:   func(_ *testing.T, a *AdminAPI) error { return a.DeleteUser(context.Background(), "Lola") },
			all:      []string{"/v1/node_config"},
			any:      []string{"/v1/partitions/redpanda/controller/0"},
			leader:   []string{"/v1/security/users/Lola"},
		},
		{
			name:     "create user in 3 node cluster",
			nNodes:   3,
			leaderID: 1,
			action: func(_ *testing.T, a *AdminAPI) error {
				return a.CreateUser(context.Background(), "Joss", "momorocks", ScramSha256)
			},
			all:    []string{"/v1/node_config"},
			any:    []string{"/v1/partitions/redpanda/controller/0"},
			leader: []string{"/v1/security/users"},
		},
		{
			name:     "list users in 3 node cluster",
			nNodes:   3,
			leaderID: 1,
			handlers: map[string]http.HandlerFunc{
				"/v1/security/users": func(rw http.ResponseWriter, _ *http.Request) {
					rw.Write([]byte(`["Joss", "lola", "jeff", "tobias"]`))
				},
			},
			action: func(t *testing.T, a *AdminAPI) error {
				users, err := a.ListUsers(context.Background())
				require.NoError(t, err)
				require.Len(t, users, 4)
				return nil
			},
			any:  []string{"/v1/security/users"},
			none: []string{"/v1/partitions/redpanda/controller/0"},
		},
		{
			name:     "request failures ensure response body closure",
			nNodes:   3,
			leaderID: 1,
			handlers: map[string]http.HandlerFunc{
				"/v1/security/users": func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusBadRequest)
				},
			},
			action: func(t *testing.T, a *AdminAPI) error {
				_, err := a.ListUsers(context.Background())
				require.Error(t, err)
				return nil
			},
			any:  []string{"/v1/security/users"},
			none: []string{"/v1/partitions/redpanda/controller/0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls := []string{}
			calls := []testCall{}
			mutex := sync.Mutex{}
			tServers := []*httptest.Server{}
			for i := 0; i < tt.nNodes; i++ {
				ts := httptest.NewServer(handlerForNode(t, i, tt, &calls, &mutex))
				tServers = append(tServers, ts)
				urls = append(urls, ts.URL)
			}

			defer func() {
				for _, ts := range tServers {
					ts.Close()
				}
			}()

			adminClient, err := NewAdminAPI(urls, new(NopAuth), nil)
			require.NoError(t, err)
			err = tt.action(t, adminClient)
			require.NoError(t, err)
			for _, path := range tt.all {
				checkCallToAllNodes(t, calls, path, tt.nNodes)
			}
			for _, path := range tt.any {
				checkCallToAnyNode(t, calls, path, tt.nNodes)
			}
			for _, path := range tt.leader {
				checkCallToLeader(t, calls, path, tt.leaderID)
			}
			for _, path := range tt.none {
				checkCallNone(t, calls, path, tt.nNodes)
			}

			require.EqualValues(t, 0, responsesPendingClosure.Load(), "Not all requests were closed!")
		})
	}
}

func handlerForNode(
	t *testing.T, nodeID int, tt reqsTest, calls *[]testCall, mutex *sync.Mutex,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t.Logf("~~~ NodeID: %d, path: %s", nodeID, r.URL.Path)
		mutex.Lock()
		*calls = append(*calls, testCall{nodeID, r})
		mutex.Unlock()

		if h, ok := tt.handlers[r.URL.Path]; ok {
			h(w, r)
			return
		}

		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/node_config"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`{"node_id": %d}`, nodeID))) //nolint:gocritic // original rpk code
		case strings.HasPrefix(r.URL.Path, "/v1/partitions/redpanda/controller/0"):
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(fmt.Sprintf(`{"leader_id": %d}`, tt.leaderID))) //nolint:gocritic // original rpk code
		case strings.HasPrefix(r.URL.Path, "/v1/security/users"):
			if nodeID == tt.leaderID {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
		default:
			require.Failf(t, "unexpected url path: %s", r.URL.Path)
		}
	}
}

func checkCallToAllNodes(
	t *testing.T, calls []testCall, path string, nNodes int,
) {
	for i := 0; i < nNodes; i++ {
		if len(callsForPathAndNodeID(calls, path, i)) == 0 {
			require.Fail(t, fmt.Sprintf("path (%s) was expected to be called in all nodes but it wasn't called in node (%d)", path, i))
			return
		}
	}
}

func checkCallToAnyNode(
	t *testing.T, calls []testCall, path string, nNodes int,
) {
	for i := 0; i < nNodes; i++ {
		if len(callsForPathAndNodeID(calls, path, i)) > 0 {
			return
		}
	}
	require.Fail(t, fmt.Sprintf("path (%s) was expected to be called in any node but it wasn't called", path))
}

func checkCallToLeader(
	t *testing.T, calls []testCall, path string, leaderID int,
) {
	if len(callsForPathAndNodeID(calls, path, leaderID)) == 0 {
		require.Fail(t, fmt.Sprintf("path (%s) was expected to be called in the leader node but it wasn't", path))
	}
}

func checkCallNone(t *testing.T, calls []testCall, path string, nNodes int) {
	for i := 0; i < nNodes; i++ {
		if len(callsForPathAndNodeID(calls, path, i)) > 0 {
			require.Fail(t, fmt.Sprintf("path (%s) was expected to not be called but it was called in node (%d)", path, i))
		}
	}
}

func callsForPathAndNodeID(
	calls []testCall, path string, nodeID int,
) []testCall {
	return callsForPath(callsForNodeID(calls, nodeID), path)
}

func callsForPath(calls []testCall, path string) []testCall {
	filtered := []testCall{}
	for _, call := range calls {
		if call.req.URL.Path == path {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

func callsForNodeID(calls []testCall, nodeID int) []testCall {
	filtered := []testCall{}
	for _, call := range calls {
		if call.brokerID == nodeID {
			filtered = append(filtered, call)
		}
	}
	return filtered
}

func TestAdminAddressesFromK8SDNS(t *testing.T) {
	schemes := []string{"http", "https"}

	for _, scheme := range schemes {
		t.Run(scheme, func(t *testing.T) {
			adminAPIURL := scheme + "://" + "redpanda-api.cluster.local:19644"

			adminAPIHostURL, err := url.Parse(adminAPIURL)
			require.NoError(t, err)

			srv, err := mockdns.NewServer(map[string]mockdns.Zone{
				"_admin._tcp." + adminAPIHostURL.Hostname() + ".": {
					SRV: []net.SRV{
						{
							Target: "rp-id123-0.rp-id123.redpanda.svc.cluster.local.",
							Port:   9644,
							Weight: 33,
						},
						{
							Target: "rp-id123-1.rp-id123.redpanda.svc.cluster.local.",
							Port:   9644,
							Weight: 33,
						},
						{
							Target: "rp-id123-2.rp-id123.redpanda.svc.cluster.local.",
							Port:   9644,
							Weight: 33,
						},
					},
				},
			}, false)
			require.NoError(t, err)

			defer srv.Close()

			srv.PatchNet(net.DefaultResolver)
			defer mockdns.UnpatchNet(net.DefaultResolver)

			brokerURLs, err := AdminAddressesFromK8SDNS(adminAPIURL)
			assert.NoError(t, err)
			require.Len(t, brokerURLs, 3)
			assert.Equal(t, scheme+"://"+"rp-id123-0.rp-id123.redpanda.svc.cluster.local.:9644", brokerURLs[0])
			assert.Equal(t, scheme+"://"+"rp-id123-1.rp-id123.redpanda.svc.cluster.local.:9644", brokerURLs[1])
			assert.Equal(t, scheme+"://"+"rp-id123-2.rp-id123.redpanda.svc.cluster.local.:9644", brokerURLs[2])
		})
	}
}

func TestUpdateAPIUrlsFromKubernetesDNS(t *testing.T) {
	schemes := []string{"http", "https"}

	for _, scheme := range schemes {
		t.Run(scheme, func(t *testing.T) {
			adminAPIURL := scheme + "://" + "redpanda-api.cluster.local:19644"

			adminAPIHostURL, err := url.Parse(adminAPIURL)
			require.NoError(t, err)

			srv, err := mockdns.NewServer(map[string]mockdns.Zone{
				"_admin._tcp." + adminAPIHostURL.Hostname() + ".": {
					SRV: []net.SRV{
						{
							Target: "rp-id123-0.rp-id123.redpanda.svc.cluster.local.",
							Port:   9644,
							Weight: 33,
						},
						{
							Target: "rp-id123-1.rp-id123.redpanda.svc.cluster.local.",
							Port:   9644,
							Weight: 33,
						},
						{
							Target: "rp-id123-2.rp-id123.redpanda.svc.cluster.local.",
							Port:   9644,
							Weight: 33,
						},
					},
				},
			}, false)
			require.NoError(t, err)

			defer srv.Close()

			srv.PatchNet(net.DefaultResolver)
			defer mockdns.UnpatchNet(net.DefaultResolver)

			cl, err := NewClient([]string{adminAPIURL}, nil, nil, false)
			require.NoError(t, err)
			require.NotNil(t, cl)
			assert.Len(t, cl.urls, 1)

			err = cl.UpdateAPIUrlsFromKubernetesDNS()
			require.NoError(t, err)
			require.NotNil(t, cl)
			assert.Len(t, cl.urls, 3)
			assert.Equal(t, scheme+"://"+"rp-id123-0.rp-id123.redpanda.svc.cluster.local.:9644", cl.urls[0])
			assert.Equal(t, scheme+"://"+"rp-id123-1.rp-id123.redpanda.svc.cluster.local.:9644", cl.urls[1])
			assert.Equal(t, scheme+"://"+"rp-id123-2.rp-id123.redpanda.svc.cluster.local.:9644", cl.urls[2])
		})
	}
}

func TestDialerPassing(t *testing.T) {
	urls := []string{}
	for id := 0; id < 3; id++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/v1/node_config"):
				w.Write([]byte(fmt.Sprintf(`{"node_id": %d}`, id))) //nolint:gocritic // original rpk code
			case strings.HasPrefix(r.URL.Path, "/v1/partitions/redpanda/controller/0"):
				w.Write([]byte(`{"leader_id": 0}`))
			default:
				require.Failf(t, "unexpected url path: %s", r.URL.Path)
			}
		}))

		t.Cleanup(server.Close)
		urls = append(urls, server.URL)
	}

	var dialed atomic.Int64
	adminClient, err := NewAdminAPIWithDialer(urls, new(NopAuth), nil, func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialed.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	})
	require.NoError(t, err)

	err = adminClient.eachBroker(func(client *AdminAPI) error {
		_, err := client.GetNodeConfig(context.Background())
		if err != nil {
			return err
		}
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, int64(3), dialed.Load())
}

func TestIdleConnectionClosure(t *testing.T) {
	clients := 1000
	numRequests := 10

	urls := []string{}
	for id := 0; id < 3; id++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, "/v1/node_config"):
				w.Write([]byte(fmt.Sprintf(`{"node_id": %d}`, id))) //nolint:gocritic // original rpk code
			case strings.HasPrefix(r.URL.Path, "/v1/partitions/redpanda/controller/0"):
				w.Write([]byte(`{"leader_id": 0}`))
			default:
				require.Failf(t, "unexpected url path: %s", r.URL.Path)
			}
		}))

		t.Cleanup(server.Close)

		urls = append(urls, server.URL)
	}

	// tracker to make sure we close all of our connections
	var mutex sync.RWMutex
	conns := []*wrappedConnection{}

	for i := 0; i < clients; i++ {
		// initialize a new client and do some requests
		adminClient, err := NewAdminAPIWithDialer(urls, new(NopAuth), nil, func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			mutex.Lock()
			defer mutex.Unlock()

			wrapped := &wrappedConnection{Conn: conn}
			conns = append(conns, wrapped)
			return wrapped, nil
		})
		require.NoError(t, err)

		for i := 0; i < numRequests; i++ {
			_, err = adminClient.GetLeaderID(context.Background())
			require.NoError(t, err)
		}

		adminClient.Close()
	}

	mutex.RLock()
	defer mutex.RUnlock()

	closed := 0
	for _, conn := range conns {
		if conn.closed {
			closed++
		}
	}
	require.Equal(t, closed, len(conns), "Not all connections were closed")
}

type wrappedConnection struct {
	net.Conn
	closed bool
}

func (w *wrappedConnection) Close() error {
	w.closed = true
	return w.Conn.Close()
}

func TestNewHostClientIndex(t *testing.T) {
	addrs := []string{"http://host0:9644", "http://host1:9644"}

	cl, err := NewHostClient(addrs, nil, new(NopAuth), false, "1")
	require.NoError(t, err)

	require.Equal(t, 1, len(cl.urls))
	assert.Equal(t, "http://host1:9644", cl.urls[0])
}

func TestSendOneStream(t *testing.T) {
	testData := "test response data"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/test-endpoint", r.URL.Path)
		assert.Equal(t, http.MethodGet, r.Method)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testData))
	}))
	defer server.Close()

	client, err := NewAdminAPI([]string{server.URL}, new(NopAuth), nil)
	require.NoError(t, err)

	resp, err := client.SendOneStream(context.Background(), http.MethodGet, "/test-endpoint", nil, false)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	// Verify we can read the response
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, testData, string(data))

	// Verify response metadata
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/plain", resp.Header.Get("Content-Type"))
}

func TestSendOneStreamWithMultipleEndpoints(t *testing.T) {
	client, err := NewAdminAPI([]string{"http://host1:9644", "http://host2:9644"}, new(NopAuth), nil)
	require.NoError(t, err)

	resp, err := client.SendOneStream(context.Background(), http.MethodGet, "/test", nil, false)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "unable to issue a single-admin-endpoint request to 2 admin endpoints")
}

func TestSendOneStreamWithRequestBody(t *testing.T) {
	requestBody := map[string]string{"key": "value"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/test-post", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"key":"value"}`, string(body))

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	}))
	defer server.Close()

	client, err := NewAdminAPI([]string{server.URL}, new(NopAuth), nil)
	require.NoError(t, err)

	resp, err := client.SendOneStream(context.Background(), http.MethodPost, "/test-post", requestBody, false)
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "created", string(data))
}
