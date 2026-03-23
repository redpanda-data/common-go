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

package kube_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/redpanda-data/common-go/kube"
)

func TestDialer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := k3s.Run(ctx, "rancher/k3s:v1.27.1-k3s1")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	config, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(config)
	require.NoError(t, err)
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	client, err := client.New(restcfg, client.Options{Scheme: s})
	require.NoError(t, err)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "name",
			Namespace: "default",
			Labels: map[string]string{
				"service": "label",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image: "caddy",
					Name:  "caddy",
					Command: []string{
						"caddy",
					},
					Args: []string{
						"file-server",
						"--domain",
						// use localhost so we don't reach out to an
						// ACME server
						"localhost",
					},
					Ports: []corev1.ContainerPort{{
						Name:          "http",
						ContainerPort: 80,
					}, {
						Name:          "https",
						ContainerPort: 443,
					}},
				},
			},
		},
	}
	err = client.Create(ctx, pod)
	require.NoError(t, err)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "service",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"service": "label",
			},
			Ports: []corev1.ServicePort{{
				Name: "http",
				Port: 8080,
			}},
		},
	}
	err = client.Create(ctx, service)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		var ready corev1.Pod
		err := client.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &ready)
		if err != nil {
			return false
		}

		return ready.Status.Phase == corev1.PodRunning
	}, 30*time.Second, 10*time.Millisecond)

	dialer := kube.NewPodDialer(restcfg)
	// Set the `ServerName` to match what Caddy generates for the localhost domain,
	// otherwise it fails due to an SNI mismatch.
	tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			DialContext: func(ctx context.Context, network string, address string) (net.Conn, error) {
				conn, err := dialer.DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}

				// add in some deadline calls to make sure we don't panic/error

				if err := conn.SetReadDeadline(time.Now().Add(1 * time.Minute)); err != nil {
					return nil, err
				}

				if err := conn.SetWriteDeadline(time.Now().Add(1 * time.Minute)); err != nil {
					return nil, err
				}

				if err := conn.SetDeadline(time.Now().Add(1 * time.Minute)); err != nil {
					return nil, err
				}

				return conn, nil
			},
		},
	}

	for _, host := range []string{
		// http service-based DNS
		"http://name.service.default.svc.cluster.local",
		"http://name.service.default.svc",
		"http://name.service.default",
		// https pod-based DNS
		"http://name.default",
		"http://name",
		// https service-based DNS
		"https://name.service.default.svc.cluster.local",
		"https://name.service.default.svc",
		"https://name.service.default",
		// https pod-based DNS
		"https://name.default",
		"https://name",
		// trailing dots
		"http://name.service.default.svc.cluster.local.",
	} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			// Test the pooling behavior of HTTPClient by making requests to
			// the same hosts a few times.
			for range 5 {
				func() {
					// Run in a closure so we have a context scoped to the life
					// time of each request we make, which is distinct from the
					// lifetime of the connection due to http.Transport's
					// connection pooling.
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()

					req, err := http.NewRequestWithContext(ctx, http.MethodGet, host, http.NoBody)
					require.NoError(t, err)

					resp, err := httpClient.Do(req)
					require.NoError(t, err)

					defer resp.Body.Close()

					_, err = io.ReadAll(resp.Body)
					require.NoError(t, err)

					require.Equal(t, http.StatusOK, resp.StatusCode)
				}()
			}
		})
	}

	t.Run("roundTripperFor-v1.27", func(t *testing.T) {
		const canary = "You've used the customized dialer!"

		// Reuse the existing kube-apiserver so the initial request succeeds
		// and goes on to the SPDY upgrade process.
		cfg := rest.CopyConfig(restcfg)

		// Prior to our own implementation of roundTripperFor, our custom
		// dialer would only get called once. Though that's a bit difficult to
		// showcase with a test...
		var calls int32
		cfg.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			switch atomic.AddInt32(&calls, 1) {
			case 0:
				return (&net.Dialer{}).DialContext(ctx, network, address)
			case 1:
				return nil, errors.New(canary)
			default:
				t.Fatal("shouldn't get called more than twice")
				panic("unreachable")
			}
		}

		dialer := kube.NewPodDialer(cfg)

		_, err := dialer.DialContext(context.Background(), "tcp", "name:80")
		require.Error(t, err)
		require.Contains(t, err.Error(), canary)
	})
}

// setupK3sCluster is a helper that starts a k3s container with the given image,
// creates a caddy pod, waits for it to be running, and returns the rest config,
// client, and pod.
func setupK3sCluster(ctx context.Context, t *testing.T, image string) (*rest.Config, *corev1.Pod) {
	t.Helper()

	container, err := k3s.Run(ctx, image)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	config, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)
	restcfg, err := clientcmd.RESTConfigFromKubeConfig(config)
	require.NoError(t, err)
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	c, err := client.New(restcfg, client.Options{Scheme: s})
	require.NoError(t, err)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "caddy-test",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Image:   "caddy",
					Name:    "caddy",
					Command: []string{"caddy"},
					Args: []string{
						"file-server",
						"--domain",
						"localhost",
					},
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 80},
						{Name: "https", ContainerPort: 443},
					},
				},
			},
		},
	}
	require.NoError(t, c.Create(ctx, pod))

	require.Eventually(t, func() bool {
		var ready corev1.Pod
		if err := c.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, &ready); err != nil {
			return false
		}
		return ready.Status.Phase == corev1.PodRunning && ready.Status.PodIP != ""
	}, 60*time.Second, 100*time.Millisecond)

	// Re-fetch to get the pod IP.
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: pod.Name, Namespace: pod.Namespace}, pod))

	return restcfg, pod
}

// TestDialerDashedIPResolution_K8s132 demonstrates that on Kubernetes 1.32+,
// pod-IP-based hostnames with dashes (e.g. "10-42-0-7") require the
// resolveIPToPod fix in PodDialer. Without the fix, the dialer would attempt
// to look up a pod literally named "10-42-0-7" which doesn't exist.
func TestDialerDashedIPResolution_K8s132(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	restcfg, pod := setupK3sCluster(ctx, t, "rancher/k3s:v1.32.13-k3s1")

	// Convert the pod IP (e.g. "10.42.0.7") to the dashed format
	// that Kafka brokers advertise in K8s 1.32+ (e.g. "10-42-0-7").
	dashedIP := strings.ReplaceAll(pod.Status.PodIP, ".", "-")
	t.Logf("Pod %s/%s has IP %s (dashed: %s)", pod.Namespace, pod.Name, pod.Status.PodIP, dashedIP)

	dialer := kube.NewPodDialer(restcfg)
	tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			DialContext:     dialer.DialContext,
		},
	}

	// Dial using the dashed-IP format, simulating what a Kafka broker
	// would advertise in K8s 1.32+. The fix resolves this dashed IP
	// back to the actual pod via the K8s API.
	for _, scheme := range []string{"http", "https"} {
		t.Run(fmt.Sprintf("%s/dashed-ip", scheme), func(t *testing.T) {
			url := fmt.Sprintf("%s://%s", scheme, dashedIP)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			require.NoError(t, err)

			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	// Also verify that regular pod-name dialing still works on 1.32+.
	for _, host := range []string{
		fmt.Sprintf("http://%s", pod.Name),
		fmt.Sprintf("http://%s.%s", pod.Name, pod.Namespace),
	} {
		t.Run(host, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, host, http.NoBody)
			require.NoError(t, err)

			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

// TestDialerDashedIPResolution_K8s131 demonstrates that on Kubernetes 1.31.x
// and lower, the standard pod-name-based dialing works without needing dashed-IP
// resolution. This serves as a baseline showing the behavior prior to 1.32.
func TestDialerDashedIPResolution_K8s131(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	restcfg, pod := setupK3sCluster(ctx, t, "rancher/k3s:v1.31.6-k3s1")

	t.Logf("Pod %s/%s has IP %s", pod.Namespace, pod.Name, pod.Status.PodIP)

	dialer := kube.NewPodDialer(restcfg)
	tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			DialContext:     dialer.DialContext,
		},
	}

	// On 1.31.x, pod-name-based dialing works normally. Dashes in pod
	// names are just literal characters, not IP-derived hostnames.
	for _, host := range []string{
		fmt.Sprintf("http://%s", pod.Name),
		fmt.Sprintf("http://%s.%s", pod.Name, pod.Namespace),
		fmt.Sprintf("https://%s", pod.Name),
		fmt.Sprintf("https://%s.%s", pod.Name, pod.Namespace),
	} {
		t.Run(host, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, host, http.NoBody)
			require.NoError(t, err)

			resp, err := httpClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}

	// Also verify that the dashed-IP resolution works on 1.31.x too
	// (the fix is backwards compatible).
	dashedIP := strings.ReplaceAll(pod.Status.PodIP, ".", "-")
	t.Run(fmt.Sprintf("http/dashed-ip-%s", dashedIP), func(t *testing.T) {
		url := fmt.Sprintf("http://%s", dashedIP)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		require.NoError(t, err)

		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}
