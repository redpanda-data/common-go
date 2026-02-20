// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package rpadmin provides a client to interact with Redpanda's admin server.
package rpadmin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	urlpkg "net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/hashicorp/go-multierror"
	"github.com/sethgrid/pester"
	"go.uber.org/zap"

	commonnet "github.com/redpanda-data/common-go/net"
)

// DialContextFunc implements a dialing function that returns a net.Conn
type DialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// AdminAPI is a client to interact with Redpanda's admin server.
type AdminAPI struct {
	urlsMutex           sync.RWMutex
	urls                []string
	brokerIDToUrlsMutex sync.Mutex
	brokerIDToUrls      map[int]string
	dialer              DialContextFunc
	transport           *http.Transport
	retryClient         *pester.Client
	oneshotClient       *http.Client
	auth                Auth
	tlsConfig           *tls.Config
	forCloud            bool
	opts                []Opt
}

// NewAdminAPIClient creates a new Redpanda Admin API client, to set additional
// options such as Auth and TLS, use Opt.
func NewAdminAPIClient(urls []string, opts ...Opt) (*AdminAPI, error) {
	return newAdminAPI(urls, &NopAuth{}, nil, nil, false, opts...)
}

// NewAdminAPI creates a new Redpanda Admin API client.
func NewAdminAPI(urls []string, auth Auth, tlsConfig *tls.Config) (*AdminAPI, error) {
	return newAdminAPI(urls, auth, tlsConfig, nil, false)
}

// NewClient returns an AdminAPI client that talks to each of the admin api addresses passed in.
func NewClient(addrs []string, tls *tls.Config, auth Auth, forCloud bool, opts ...Opt) (*AdminAPI, error) {
	return newAdminAPI(addrs, auth, tls, nil, forCloud, opts...)
}

// NewHostClient returns an AdminAPI that talks to the given host, which is
// either an int index into the rpk.admin_api section of the config, or a
// hostname.
func NewHostClient(addrs []string, tls *tls.Config, auth Auth, forCloud bool, host string) (*AdminAPI, error) {
	if host == "" {
		return nil, errors.New("invalid empty admin host")
	}

	i, err := strconv.Atoi(host)
	if err == nil {
		if i < 0 || i >= len(addrs) {
			return nil, fmt.Errorf("admin host %d is out of allowed range [0, %d)", i, len(addrs))
		}
		addrs = []string{addrs[i]}
	} else {
		addrs = []string{host} // trust input is hostname (validate below)
	}

	return newAdminAPI(addrs, auth, tls, nil, forCloud)
}

// NewAdminAPIWithDialer creates a new Redpanda Admin API client with a
// specified dialer function.
func NewAdminAPIWithDialer(urls []string, auth Auth, tlsConfig *tls.Config, dialer DialContextFunc, opts ...Opt) (*AdminAPI, error) {
	return newAdminAPI(urls, auth, tlsConfig, dialer, false, opts...)
}

// ForBroker returns a new admin client with the same configuration as the initial
// client, but that talks to a single broker with the given id.
func (a *AdminAPI) ForBroker(ctx context.Context, id int) (*AdminAPI, error) {
	url, err := a.BrokerIDToURL(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.newAdminForSingleHost(url)
}

// ForHost returns a new admin client with the same configuration as the initial
// client, but that talks to a single broker at the given url.
func (a *AdminAPI) ForHost(url string) (*AdminAPI, error) {
	return a.newAdminForSingleHost(url)
}

// Close closes all idle connections of the underlying transport
// this should be called when an admin client is no longer in-use
// in order to not leak connections from the underlying transport
// pool.
func (a *AdminAPI) Close() {
	a.transport.CloseIdleConnections()
}

// Do sends an HTTP request to one of the admin API URLs, retrying on
// failure. The request must have a relative URL, the host and scheme
// will be set by this method as well as the authentication mechanism
// set on the AdminAPI.Auth Opt.
func (a *AdminAPI) Do(req *http.Request) (*http.Response, error) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // old rpk code.

	a.urlsMutex.RLock()
	shuffled := make([]string, len(a.urls))
	copy(shuffled, a.urls)
	rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	a.urlsMutex.RUnlock()

	// After a 503 or 504, wait a little for an election
	const unavailableBackoff = 1500 * time.Millisecond

	var err error
	for i := range shuffled {
		parsedURL, parseErr := urlpkg.Parse(shuffled[i])
		if parseErr != nil {
			return nil, fmt.Errorf("unable to parse url %q: %v", shuffled[i], parseErr)
		}

		clonedReq := req.Clone(req.Context())
		if clonedReq.GetBody != nil {
			cBody, err := clonedReq.GetBody()
			if err != nil {
				return nil, fmt.Errorf("unable to re use request body on a different node: %v", err)
			}
			clonedReq.Body = cBody
		}

		if parsedURL.Path == "/api" && a.forCloud {
			clonedReq.URL.Path = parsedURL.Path + clonedReq.URL.Path
		}

		clonedReq.URL.Host = parsedURL.Host
		clonedReq.URL.Scheme = parsedURL.Scheme
		// ensure that we have prefixed path
		if !strings.HasPrefix(clonedReq.URL.Path, "/") {
			clonedReq.URL.Path = "/" + clonedReq.URL.Path
		}

		// If err is set, we are retrying after a failure on the previous node
		if err != nil {
			var httpErr *HTTPResponseError
			if errors.As(err, &httpErr) {
				status := httpErr.Response.StatusCode

				// The node was up but told us the cluster
				// wasn't ready: wait before retry.
				if status == 503 || status == 504 {
					time.Sleep(unavailableBackoff)
				}
			}
		}

		// Where there are multiple nodes, disable the HTTP request retry in favour of our
		// own retry across the available nodes
		retryable := len(shuffled) == 1

		var res *http.Response
		res, err = a.sendReqAndReceive(clonedReq, retryable)
		if err == nil {
			return res, nil
		}
	}

	return nil, err
}

func newAdminAPI(urls []string, auth Auth, tlsConfig *tls.Config, dialer DialContextFunc, forCloud bool, opts ...Opt) (*AdminAPI, error) {
	// General purpose backoff, includes 503s and other errors
	const retryBackoffMs = 1500

	if len(urls) == 0 {
		return nil, errors.New("at least one url is required for the admin api")
	}

	// In situations where a request can't be executed immediately (e.g. no
	// controller leader) the admin API does not block, it returns 503.
	// Use a retrying HTTP client to handle that gracefully.
	client := pester.New()

	// Backoff is the default redpanda raft election timeout: this enables us
	// to cleanly retry on 503s due to leadership changes in progress.
	client.Backoff = func(_ int) time.Duration {
		maxJitter := 100
		delayMs := retryBackoffMs + rng(maxJitter)
		return time.Duration(delayMs) * time.Millisecond
	}

	// This happens to be the same as the pester default, but make it explicit:
	// a raft election on a 3 node group might take 3x longer if it has
	// to repeat until the lowest-priority voter wins.
	client.MaxRetries = 3

	client.LogHook = func(e pester.ErrEntry) {
		// Only log from here when retrying: a final error propagates to caller
		if e.Err != nil && e.Retry <= client.MaxRetries {
			zap.L().Sugar().Debugf("Retrying %s for error: %s\n", e.Verb, e.Err.Error())
		}
	}

	client.Timeout = 10 * time.Second

	transport := defaultTransport()

	adminAPI := &AdminAPI{
		urls:           make([]string, len(urls)),
		retryClient:    client,
		oneshotClient:  &http.Client{Timeout: client.Timeout},
		auth:           auth,
		transport:      transport,
		tlsConfig:      tlsConfig,
		brokerIDToUrls: make(map[int]string),
		forCloud:       forCloud,
		dialer:         dialer,
		opts:           opts,
	}

	for _, opt := range opts {
		opt.apply(adminAPI)
	}

	if adminAPI.tlsConfig != nil {
		transport.TLSClientConfig = adminAPI.tlsConfig
	}
	if adminAPI.dialer != nil {
		transport.DialContext = adminAPI.dialer
	}

	adminAPI.retryClient.Transport = transport
	adminAPI.oneshotClient.Transport = transport

	if err := adminAPI.initURLs(urls); err != nil {
		return nil, err
	}

	return adminAPI, nil
}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

func (a *AdminAPI) initURLs(urls []string) error {
	a.urlsMutex.Lock()
	defer a.urlsMutex.Unlock()

	if len(a.urls) != len(urls) {
		a.urls = make([]string, len(urls))
	}

	for i, u := range urls {
		scheme, host, err := commonnet.ParseHostMaybeScheme(u)
		if err != nil {
			return err
		}
		switch scheme {
		case "", schemeHTTP:
			scheme = schemeHTTP
			if a.tlsConfig != nil {
				scheme = schemeHTTPS
			}
		case schemeHTTPS:
		default:
			return fmt.Errorf("unrecognized scheme %q in host %q", scheme, u)
		}
		full := fmt.Sprintf("%s://%s", scheme, host)
		if a.forCloud {
			full += "/api" // our cloud paths are prefixed with "/api"
		}
		a.urls[i] = full
	}

	return nil
}

func (a *AdminAPI) newAdminForSingleHost(host string) (*AdminAPI, error) {
	return newAdminAPI([]string{host}, a.auth, a.tlsConfig, a.dialer, a.forCloud, a.opts...)
}

func (a *AdminAPI) urlsWithPath(path string) []string {
	a.urlsMutex.RLock()
	defer a.urlsMutex.RUnlock()

	urls := make([]string, len(a.urls))
	for i := 0; i < len(a.urls); i++ {
		urls[i] = fmt.Sprintf("%s%s", a.urls[i], path)
	}

	return urls
}

// rng is a package-scoped, mutex guarded, seeded *rand.Rand.
var rng = func() func(int) int {
	var mu sync.Mutex
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // old rpk code.
	return func(n int) int {
		mu.Lock()
		defer mu.Unlock()
		return rng.Intn(n)
	}
}()

func (a *AdminAPI) mapBrokerIDsToURLs(ctx context.Context) {
	err := a.eachBroker(func(aa *AdminAPI) error {
		nc, err := aa.GetNodeConfig(ctx)
		if err != nil {
			return err
		}

		a.urlsMutex.RLock()
		url := aa.urls[0]
		a.urlsMutex.RUnlock()

		a.brokerIDToUrlsMutex.Lock()
		a.brokerIDToUrls[nc.NodeID] = url
		a.brokerIDToUrlsMutex.Unlock()
		return nil
	})
	if err != nil {
		zap.L().Sugar().Warn(fmt.Sprintf("failed to map brokerID to URL for 1 or more brokers: %v", err))
	}
}

// GetLeaderID returns the broker ID of the leader of the Admin API.
func (a *AdminAPI) GetLeaderID(ctx context.Context) (*int, error) {
	pa, err := a.GetPartition(ctx, "redpanda", "controller", 0)
	if pa.LeaderID == -1 {
		return nil, ErrNoAdminAPILeader
	}
	if err != nil {
		return nil, err
	}
	return &pa.LeaderID, nil
}

// sendAny sends a single request to one of the client's urls and unmarshals
// the body into into, which is expected to be a pointer to a struct.
//
// On errors, this function will keep trying all the nodes we know about until
// one of them succeeds, or we run out of nodes.  In the latter case, we will return
// the error from the last node we tried.
func (a *AdminAPI) sendAny(ctx context.Context, method, path string, body, into any) error {
	req, err := prepareRequest(ctx, method, path, body)
	if err != nil {
		return err
	}

	res, err := a.Do(req)
	if err == nil {
		// Success, return the result from this node.
		return maybeUnmarshalRespInto(method, res.Request.URL.String(), res, into)
	}

	// Fall through: all nodes failed.
	return err
}

// sendToLeader sends a single request to the leader of the Admin API for Redpanda >= 21.11.1
// otherwise, it broadcasts the request.
func (a *AdminAPI) sendToLeader(ctx context.Context, method, path string, body, into any) error {
	const (
		// When there is no leader, we wait long enough for an election to complete
		noLeaderBackoff = 1500 * time.Millisecond

		// When there is a stale leader, we might have to wait long enough for
		// an election to start *and* for the resulting election to complete
		staleLeaderBackoff = 9000 * time.Millisecond
	)
	// If there's only one broker, let's just send the request to it
	if len(a.urls) == 1 {
		return a.sendOne(ctx, method, path, body, into, true)
	}

	retries := 3
	var leaderID *int
	var leaderURL string
	for leaderID == nil || leaderURL == "" {
		var err error
		leaderID, err = a.GetLeaderID(ctx)
		if errors.Is(err, ErrNoAdminAPILeader) { //nolint:gocritic // original rpk code
			// No leader?  We might have contacted a recently-started node
			// who doesn't know yet, or there might be an election pending,
			// or there might be no quorum.  In any case, retry in the hopes
			// the cluster will get out of this state.
			retries--
			if retries == 0 {
				return err
			}
			time.Sleep(noLeaderBackoff)
		} else if err != nil {
			// Unexpected error, do not retry promptly.
			return err
		} else {
			// Got a leader ID, check if it's resolvable
			leaderURL, err = a.BrokerIDToURL(ctx, *leaderID)
			if err != nil && len(a.brokerIDToUrls) == 0 {
				// Could not map any IDs: probably this is an old redpanda
				// with no node_config endpoint.  Fall back to broadcast.
				return a.sendAll(ctx, method, path, body, into)
			} else if err == nil {
				break
			}
			// Have ID mapping for some nodes but not the one that is
			// allegedly the leader.  This leader ID is probably stale,
			// e.g. if it just died a moment ago.  Try again.  This is
			// a long timeout, because it's the sum of the time for nodes
			// to start an election, followed by the worst cast number of
			// election rounds
			retries--
			if retries == 0 {
				return err
			}
			time.Sleep(staleLeaderBackoff)
		}
	}

	aLeader, err := a.newAdminForSingleHost(leaderURL)
	if err != nil {
		return err
	}
	return aLeader.sendOne(ctx, method, path, body, into, true)
}

// BrokerIDToURL resolves the URL of the broker with the given ID.
func (a *AdminAPI) BrokerIDToURL(ctx context.Context, brokerID int) (string, error) {
	if url, ok := a.getURLFromBrokerID(brokerID); ok {
		return url, nil
	} else { //nolint:revive // old rpk code.
		// Try once to map again broker IDs to URLs
		a.mapBrokerIDsToURLs(ctx)
		if url, ok := a.getURLFromBrokerID(brokerID); ok {
			return url, nil
		}
	}
	return "", fmt.Errorf("failed to map brokerID %d to URL", brokerID)
}

func (a *AdminAPI) getURLFromBrokerID(brokerID int) (string, bool) {
	a.brokerIDToUrlsMutex.Lock()
	url, ok := a.brokerIDToUrls[brokerID]
	a.brokerIDToUrlsMutex.Unlock()
	return url, ok
}

// sendOne sends a request with sendAndReceive and unmarshals the body into
// into, which is expected to be a pointer to a struct
//
// Set `retryable` to true if the API endpoint might have transient errors, such
// as temporarily having no leader for a raft group.  Set it to false if the endpoint
// should always work while the node is up, e.g. GETs of node-local state.
func (a *AdminAPI) sendOne(
	ctx context.Context, method, path string, body, into any, retryable bool,
) error {
	res, err := a.SendOneStream(ctx, method, path, body, retryable)
	if err != nil {
		return err
	}
	return maybeUnmarshalRespInto(method, res.Request.URL.String(), res, into)
}

// SendOneStream sends a request to a single admin endpoint and returns the raw
// HTTP response. This is useful for streaming responses, custom unmarshaling,
// file downloads, or any scenario where you need direct access to the response
// body.
//
// The caller is responsible for closing the response body.
func (a *AdminAPI) SendOneStream(ctx context.Context, method, path string, body any, retryable bool) (*http.Response, error) {
	a.urlsMutex.RLock()
	if len(a.urls) != 1 {
		return nil, fmt.Errorf("unable to issue a single-admin-endpoint request to %d admin endpoints", len(a.urls))
	}
	url := a.urls[0] + path
	a.urlsMutex.RUnlock()
	return a.sendAndReceive(ctx, method, url, body, retryable)
}

// sendAll sends a request to all URLs in the admin client. The first successful
// response will be unmarshalled into `into` if it is non-nil.
//
// As of v21.11.1, the Redpanda admin API redirects requests to the leader based
// on certain assumptions about all nodes listening on the same admin port, and
// that the admin API is available on the same IP address as the internal RPC
// interface.
// These limitations come from the fact that nodes don't currently share info
// with each other about where they're actually listening for the admin API.
//
// Unfortunately these assumptions do not match all environments in which
// Redpanda is deployed, hence, we need to reintroduce the sendAll method and
// broadcast on writes to the Admin API.
func (a *AdminAPI) sendAll(rootCtx context.Context, method, path string, body, into any) error {
	var (
		resURL string
		res    *http.Response
		grp    multierror.Group

		// When one request is successful, we want to cancel all other
		// outstanding requests. We do not cancel the successful
		// request's context, because the context is used all the way
		// through reading a response body.
		cancels      []func()
		cancelExcept = func(except int) {
			for i, cancel := range cancels {
				if i != except {
					cancel()
				}
			}
		}

		mutex sync.Mutex
	)

	for i, url := range a.urlsWithPath(path) {
		ctx, cancel := context.WithCancel(rootCtx)
		myURL := url
		except := i
		cancels = append(cancels, cancel)
		grp.Go(func() error {
			myRes, err := a.sendAndReceive(ctx, method, myURL, body, false)
			if err != nil {
				return err
			}
			cancelExcept(except) // kill all other requests

			// Only one request should be successful, but for
			// paranoia, we guard keeping the first successful
			// response.
			mutex.Lock()
			defer mutex.Unlock()

			if resURL != "" {
				// close the response body for my response since it won't be read
				// in the unmarshaling code
				myRes.Body.Close()
				return nil
			}

			resURL, res = myURL, myRes

			return nil
		})
	}

	err := grp.Wait()
	if res != nil {
		return maybeUnmarshalRespInto(method, resURL, res, into)
	}
	return err
}

// eachBroker creates a single host AdminAPI for each of the brokers and calls `fn`
// for each of them in a go routine.
func (a *AdminAPI) eachBroker(fn func(aa *AdminAPI) error) error {
	var grp multierror.Group
	a.urlsMutex.RLock()
	for _, url := range a.urls {
		aURL := url
		grp.Go(func() error {
			aa, err := a.newAdminForSingleHost(aURL)
			if err != nil {
				return err
			}
			return fn(aa)
		})
	}
	a.urlsMutex.RUnlock()

	return grp.Wait().ErrorOrNil()
}

// responseWrapper functions as a testing mechanism for ensuring
// that we actually close all of our network connections
var responseWrapper = func(r *http.Response) *http.Response { return r }

// Unmarshals a response body into `into`, if it is non-nil.
//
// * If into is a *[]byte, the raw response put directly into `into`.
// * If into is a *string, the raw response put directly into `into` as a string.
// * Otherwise, the response is json unmarshalled into `into`.
func maybeUnmarshalRespInto(method, url string, resp *http.Response, into any) error {
	defer resp.Body.Close()
	if into == nil {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unable to read %s %s response body: %w", method, url, err)
	}
	switch t := into.(type) {
	case *[]byte:
		*t = body
	case *string:
		*t = string(body)
	default:
		if err := json.Unmarshal(body, into); err != nil {
			return fmt.Errorf("unable to decode %s %s response body: %w", method, url, err)
		}
	}
	return nil
}

// sendAndReceive sends a request and returns the response. If body is
// non-nil, this json encodes the body and sends it with the request.
// If the body is already an io.Reader, the reader is used directly
// without marshaling.
func (a *AdminAPI) sendAndReceive(ctx context.Context, method, url string, body any, retryable bool) (*http.Response, error) {
	req, err := prepareRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	return a.sendReqAndReceive(req, retryable)
}

func prepareRequest(ctx context.Context, method, url string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		// We might be passing io reader already as body, e.g: license file.
		if v, ok := body.(io.Reader); ok {
			r = v
		} else {
			bs, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("unable to encode request body for %s %s: %w", method, url, err) // should not happen
			}
			r = bytes.NewBuffer(bs)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, fmt.Errorf("unable to create request for %s %s: %w", method, url, err) // should not happen
	}

	const applicationJSON = "application/json"
	req.Header.Set("Content-Type", applicationJSON)
	req.Header.Set("Accept", applicationJSON)
	return req, nil
}

func (a *AdminAPI) sendReqAndReceive(req *http.Request, retryable bool) (*http.Response, error) {
	a.auth.apply(req)

	// Issue request to the appropriate client, depending on retry behaviour
	var res *http.Response
	bearer := strings.Contains(req.Header.Get("Authorization"), "Bearer ")
	basic := strings.Contains(req.Header.Get("Authorization"), "Basic ")
	zap.L().Debug("Sending request", zap.String("method", req.Method), zap.String("url", req.URL.String()), zap.Bool("bearer", bearer), zap.Bool("basic", basic))
	var err error
	if retryable {
		res, err = a.retryClient.Do(req)
	} else {
		res, err = a.oneshotClient.Do(req) //nolint:gosec // G704: req URL is constructed internally, not from user input
	}

	if res != nil {
		// this is mainly used to ensure we're cleaning up all of our responses
		res = responseWrapper(res)
	}

	if err != nil {
		// When the server expects a TLS connection, but the TLS config isn't
		// set/ passed, The client returns an error like
		// Get "http://localhost:9644/v1/security/users": EOF
		// which doesn't make it obvious to the user what's going on.
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s to server %s expected a tls connection: %w", req.Method, req.URL.String(), err)
		}
		return nil, err
	}

	if res.StatusCode/100 != 2 {
		// we read the body just below, so this response is now
		// junked and we need to close it.
		defer res.Body.Close()

		resBody, err := io.ReadAll(res.Body)
		status := http.StatusText(res.StatusCode)
		if err != nil {
			return nil, fmt.Errorf("request %s %s failed: %s, unable to read body: %w", req.Method, req.URL.String(), status, err)
		}
		respErr := &HTTPResponseError{Response: res, Body: resBody, Method: req.Method, URL: req.URL.String()}
		// We use the error writer to detect whether the request was made using
		// the Connect protocol (AdminV2); If it was, we return a Connect error
		// for better handling upstream.
		isProto := connect.NewErrorWriter(connect.WithRequireConnectProtocolHeader()).IsSupported(req)
		if isProto {
			return nil, connect.NewError(connectCodeFromBody(respErr.Body), respErr)
		}
		return nil, respErr
	}

	return res, nil
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// connect error codes come in the `code` field of the JSON body, e.g:
// { "code": "not_found", "message": "Failed to find foo" }.
func connectCodeFromBody(body []byte) connect.Code {
	var resp GenericConnectError
	if err := json.Unmarshal(body, &resp); err != nil {
		return connect.CodeUnknown
	}
	var code connect.Code
	err := code.UnmarshalText([]byte(resp.Code))
	if err != nil {
		return connect.CodeUnknown
	}
	return code
}
