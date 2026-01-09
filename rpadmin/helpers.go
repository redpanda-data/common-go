// Copyright 2026 Redpanda Data, Inc.
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
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
)

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

	_, records, err := net.DefaultResolver.LookupSRV(context.Background(), "admin", "tcp", adminURL.Hostname())
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

	return a.initURLs(urls)
}

// SetAuth sets the auth in the client after its initialization.
func (a *AdminAPI) SetAuth(auth Auth) {
	a.auth = auth
}
