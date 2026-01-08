// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

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
