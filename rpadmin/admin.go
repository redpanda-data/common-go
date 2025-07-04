// Copyright 2021 Redpanda Data, Inc.
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
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/sethgrid/pester"
	"go.uber.org/zap"

	commonnet "github.com/redpanda-data/common-go/net"
)

// ErrNoAdminAPILeader happens when there's no leader for the Admin API.
var ErrNoAdminAPILeader = errors.New("no Admin API leader found")

// ErrNoSRVRecordsFound happens when we try to deduce Admin API URLs
// from Kubernetes SRV DNS records, but no records were returned by
// the DNS query.
var ErrNoSRVRecordsFound = errors.New("not SRV DNS records found")

// HTTPResponseError is the error response.
type HTTPResponseError struct {
	Method   string
	URL      string
	Response *http.Response
	Body     []byte
}

type (
	// Auth affixes auth to an http request.
	Auth interface{ apply(req *http.Request) }
	// Opt is an option to configure an admin client.
	Opt interface{ apply(*pester.Client) }
	// BasicAuth options struct.
	BasicAuth struct {
		Username string
		Password string
	}
	// BearerToken options struct.
	BearerToken struct {
		Token string
	}
	// NopAuth options struct.
	NopAuth struct{}
)

// responseWrapper functions as a testing mechanism for ensuring
// that we actually close all of our network connections
var responseWrapper = func(r *http.Response) *http.Response { return r }

func (a *BasicAuth) apply(req *http.Request) {
	req.SetBasicAuth(a.Username, a.Password)
}

func (a *BearerToken) apply(req *http.Request) {
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", a.Token))
}

func (*NopAuth) apply(*http.Request) {}

// GenericErrorBody is the JSON decodable body that is produced by generic error
// handling in the admin server when a seastar http exception is thrown.
type GenericErrorBody struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

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

// DialContextFunc implements a dialing function that returns a net.Conn
type DialContextFunc = func(ctx context.Context, network, addr string) (net.Conn, error)

// NewAdminAPIWithDialer creates a new Redpanda Admin API client with a specified dialer function.
func NewAdminAPIWithDialer(urls []string, auth Auth, tlsConfig *tls.Config, dialer DialContextFunc, opts ...Opt) (*AdminAPI, error) {
	return newAdminAPI(urls, auth, tlsConfig, dialer, false, opts...)
}

// NewAdminAPI creates a new Redpanda Admin API client.
func NewAdminAPI(urls []string, auth Auth, tlsConfig *tls.Config) (*AdminAPI, error) {
	return newAdminAPI(urls, auth, tlsConfig, nil, false)
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

	for _, opt := range opts {
		opt.apply(client)
	}
	transport := defaultTransport()

	a := &AdminAPI{
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

	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}
	if dialer != nil {
		transport.DialContext = dialer
	}

	a.retryClient.Transport = transport
	a.oneshotClient.Transport = transport

	if err := a.initURLs(urls, tlsConfig, forCloud); err != nil {
		return nil, err
	}

	return a, nil
}

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

func (a *AdminAPI) initURLs(urls []string, tlsConfig *tls.Config, forCloud bool) error {
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
			if tlsConfig != nil {
				scheme = schemeHTTPS
			}
		case schemeHTTPS:
		default:
			return fmt.Errorf("unrecognized scheme %q in host %q", scheme, u)
		}
		full := fmt.Sprintf("%s://%s", scheme, host)
		if forCloud {
			full += "/api" // our cloud paths are prefixed with "/api"
		}
		a.urls[i] = full
	}

	return nil
}

// SetAuth sets the auth in the client.
func (a *AdminAPI) SetAuth(auth Auth) {
	a.auth = auth
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
	// Shuffle the list of URLs
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
		url := shuffled[i] + path

		// If err is set, we are retrying after a failure on the previous node
		if err != nil {
			zap.L().Warn(fmt.Sprintf("Request error, trying another node: %s", err.Error()))
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
		res, err = a.sendAndReceive(ctx, method, url, body, retryable)
		if err == nil {
			// Success, return the result from this node.
			return maybeUnmarshalRespInto(method, url, res, into)
		}
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
	a.urlsMutex.RLock()
	if len(a.urls) != 1 {
		return fmt.Errorf("unable to issue a single-admin-endpoint request to %d admin endpoints", len(a.urls))
	}
	url := a.urls[0] + path
	a.urlsMutex.RUnlock()
	res, err := a.sendAndReceive(ctx, method, url, body, retryable)
	if err != nil {
		return err
	}
	return maybeUnmarshalRespInto(method, url, res, into)
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
func (a *AdminAPI) sendAndReceive(
	ctx context.Context, method, url string, body any, retryable bool,
) (*http.Response, error) {
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
		return nil, err
	}

	a.auth.apply(req)

	const applicationJSON = "application/json"
	req.Header.Set("Content-Type", applicationJSON)
	req.Header.Set("Accept", applicationJSON)

	// Issue request to the appropriate client, depending on retry behaviour
	var res *http.Response
	bearer := strings.Contains(req.Header.Get("Authorization"), "Bearer ")
	basic := strings.Contains(req.Header.Get("Authorization"), "Basic ")
	zap.L().Debug("Sending request", zap.String("method", method), zap.String("url", url), zap.Bool("bearer", bearer), zap.Bool("basic", basic))
	if retryable {
		res, err = a.retryClient.Do(req)
	} else {
		res, err = a.oneshotClient.Do(req)
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
			return nil, fmt.Errorf("%s to server %s expected a tls connection: %w", method, url, err)
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
			return nil, fmt.Errorf("request %s %s failed: %s, unable to read body: %w", method, url, status, err)
		}
		return nil, &HTTPResponseError{Response: res, Body: resBody, Method: method, URL: url}
	}

	return res, nil
}

// DecodeGenericErrorBody decodes generic error body.
func (he HTTPResponseError) DecodeGenericErrorBody() (GenericErrorBody, error) {
	var resp GenericErrorBody
	err := json.Unmarshal(he.Body, &resp)
	return resp, err
}

// Error returns string representation of the error.
func (he HTTPResponseError) Error() string {
	return fmt.Sprintf("request %s %s failed: %s, body: %q\n",
		he.Method, he.URL, http.StatusText(he.Response.StatusCode), he.Body)
}

type clientOpt struct{ fn func(*pester.Client) }

func (opt clientOpt) apply(cl *pester.Client) { opt.fn(cl) }

// ClientTimeout sets the client timeout, overriding the default of 10s.
func ClientTimeout(t time.Duration) Opt {
	return clientOpt{func(cl *pester.Client) {
		cl.Timeout = t
	}}
}

// MaxRetries sets the client maxRetries, overriding the default of 3.
func MaxRetries(r int) Opt {
	return clientOpt{func(cl *pester.Client) {
		cl.MaxRetries = r
	}}
}

// AdminAddressesFromK8SDNS attempts to deduce admin API URLs
// based on Kubernetes DNS resolution.
// https://github.com/kubernetes/dns/blob/master/docs/specification.md
// Assume that Admin API URL configured is a Kubernetes Service URL.
// This Admin API URL is passed in as the function argument.
// Since it's a Kubernetes service, Kubernetes DNS creates a DNS SRV record
// for the admin port mapping.
// We can query the DNS record to get the target host names and ports.
// To check if a workload is running inside a kubernetes pod test for
// KUBERNETES_SERVICE_HOST or KUBERNETES_SERVICE_PORT env vars.
func AdminAddressesFromK8SDNS(adminAPIURL string) ([]string, error) {
	adminURL, err := url.Parse(adminAPIURL)
	if err != nil {
		return nil, err
	}

	_, records, err := net.LookupSRV("admin", "tcp", adminURL.Hostname())
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return nil, ErrNoSRVRecordsFound
	}

	// targets may be in the form
	// redpanda-1.redpanda.redpanda.svc.cluster.local.
	// take advantage of ordinals and order them accordingly
	sort.Slice(records, func(i, j int) bool {
		return records[i].Target < records[j].Target
	})

	urls := make([]string, 0, len(records))

	proto := "http://"
	if adminURL.Scheme == schemeHTTPS {
		proto = "https://"
	}

	for _, r := range records {
		urls = append(urls, proto+r.Target+":"+strconv.Itoa(int(r.Port)))
	}

	return urls, nil
}

// UpdateAPIUrlsFromKubernetesDNS updates the client's internal URLs to admin addresses from Kubernetes DNS.
// See AdminAddressesFromK8SDNS.
func (a *AdminAPI) UpdateAPIUrlsFromKubernetesDNS() error {
	a.urlsMutex.RLock()
	if len(a.urls) == 0 {
		return errors.New("at least one url is required for the admin api")
	}

	baseURL := a.urls[0]
	a.urlsMutex.RUnlock()

	urls, err := AdminAddressesFromK8SDNS(baseURL)
	if err != nil {
		return err
	}

	return a.initURLs(urls, a.tlsConfig, a.forCloud)
}

// Close closes all idle connections of the underlying transport
// this should be called when an admin client is no longer in-use
// in order to not leak connections from the underlying transport
// pool.
func (a *AdminAPI) Close() {
	a.transport.CloseIdleConnections()
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
