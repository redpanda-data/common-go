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

package kube

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type (
	// Config is an alias for clientcmdapi.Config
	// to reduce import verbosity.
	Config = clientcmdapi.Config
	// RESTConfig is an alias for rest.Config
	// to reduce import verbosity.
	RESTConfig = rest.Config
)

// WriteToFile is an alias for clientcmd.WriteToFile
// to reduce import verbosity.
var WriteToFile = clientcmd.WriteToFile

// RestToConfig converts a rest.Config to a clientcmdapi.Config.
func RestToConfig(cfg *rest.Config) clientcmdapi.Config {
	// Thanks to: https://github.com/kubernetes/client-go/issues/711#issuecomment-1666075787

	clusters := make(map[string]*clientcmdapi.Cluster)
	clusters["default-cluster"] = &clientcmdapi.Cluster{
		Server:                   cfg.Host,
		CertificateAuthority:     cfg.CAFile,
		CertificateAuthorityData: cfg.CAData,
	}

	contexts := make(map[string]*clientcmdapi.Context)
	contexts["default-context"] = &clientcmdapi.Context{
		Cluster:  "default-cluster",
		AuthInfo: "default-user",
	}

	authinfos := make(map[string]*clientcmdapi.AuthInfo)
	authinfos["default-user"] = &clientcmdapi.AuthInfo{
		Token:                 cfg.BearerToken,
		TokenFile:             cfg.BearerTokenFile,
		ClientCertificateData: cfg.CertData,
		ClientCertificate:     cfg.CertFile,
		ClientKeyData:         cfg.KeyData,
		ClientKey:             cfg.KeyFile,
	}

	return clientcmdapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		Clusters:       clusters,
		Contexts:       contexts,
		CurrentContext: "default-context",
		AuthInfos:      authinfos,
	}
}

// ConfigToRest converts a clientcmdapi.Config to a rest.Config.
func ConfigToRest(cfg Config) (*RESTConfig, error) {
	clientConfig := clientcmd.NewNonInteractiveClientConfig(cfg, cfg.CurrentContext, &clientcmd.ConfigOverrides{}, nil)
	return clientConfig.ClientConfig()
}
