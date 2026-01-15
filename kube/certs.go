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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// much of this is an adapted version of https://github.com/open-policy-agent/cert-controller

const (
	certName                          = "tls.crt"
	keyName                           = "tls.key"
	caCertName                        = "ca.crt"
	caKeyName                         = "ca.key"
	defaultCAName                     = "Redpanda Kubernetes CA"
	defaultCAOrganization             = "Engineering"
	defaultControllerName             = "cert-rotator"
	defaultRotationCheckFrequency     = 12 * time.Hour
	defaultCaCertValidityDuration     = 10 * 365 * 24 * time.Hour
	defaultServerCertValidityDuration = 1 * 365 * 24 * time.Hour
	defaultLookaheadInterval          = 90 * 24 * time.Hour
)

// WebhookType it the type of webhook, either validating/mutating webhook, a CRD conversion webhook, or an extension API server.
type WebhookType int

const (
	// Validating indicates the webhook is a ValidatingWebhook.
	Validating WebhookType = iota
	// Mutating indicates the webhook is a MutatingWebhook.
	Mutating
	// CRDConversion indicates the webhook is a conversion webhook.
	CRDConversion
)

var gvks = map[WebhookType]schema.GroupVersionKind{
	Validating:    {Group: "admissionregistration.k8s.io", Version: "v1", Kind: "ValidatingWebhookConfiguration"},
	Mutating:      {Group: "admissionregistration.k8s.io", Version: "v1", Kind: "MutatingWebhookConfiguration"},
	CRDConversion: {Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"},
}

type leaderElectedRunnable[T manager.Runnable] struct {
	name     string
	runnable T
}

func wrapRunnableWithLeaderElection[T manager.Runnable](name string, runnable T) *leaderElectedRunnable[T] {
	return &leaderElectedRunnable[T]{
		name:     name,
		runnable: runnable,
	}
}

func (l *leaderElectedRunnable[T]) Start(ctx context.Context) error {
	logger := Logger(ctx).WithName(fmt.Sprintf("CertRotator[%s]", l.name))
	logger.V(3).Info("running")
	defer logger.V(3).Info("stopping")

	return l.runnable.Start(ctx)
}

func (*leaderElectedRunnable[T]) NeedLeaderElection() bool {
	return true
}

// WebhookInfo is used by the rotator to receive info about resources to be updated with certificates.
type WebhookInfo struct {
	// Name is the name of the webhook for a validating or mutating webhook, or the CRD name in case of a CRD conversion webhook
	Name     string
	Type     WebhookType
	Versions []string
}

func (w WebhookInfo) gvk() schema.GroupVersionKind {
	return gvks[w.Type]
}

// AddRotator adds the CertRotator and ReconcileWH to the manager.
func AddRotator(mgr manager.Manager, cr *CertRotator) error {
	if err := cr.validate(); err != nil {
		return err
	}

	cache, err := addNamespacedCache(mgr, cr.SecretKey.Namespace)
	if err != nil {
		return fmt.Errorf("creating namespaced cache: %w", err)
	}

	ctl, err := FromRESTConfig(mgr.GetConfig(), Options{
		Options: client.Options{
			Scheme: mgr.GetScheme(),
		},
	})
	if err != nil {
		return fmt.Errorf("initializing client: %w", err)
	}

	cr.reader = cache
	cr.writer = ctl
	if err := mgr.Add(wrapRunnableWithLeaderElection("Runnable", cr)); err != nil {
		return err
	}

	return addController(mgr, &webhookReconciler{
		rotator: cr,
		scheme:  mgr.GetScheme(),
	})
}

func addNamespacedCache(mgr manager.Manager, namespace string) (cache.Cache, error) {
	var namespaces map[string]cache.Config
	if namespace != "" {
		namespaces = map[string]cache.Config{
			namespace: {},
		}
	}

	c, err := cache.New(mgr.GetConfig(), cache.Options{
		Scheme:            mgr.GetScheme(),
		Mapper:            mgr.GetRESTMapper(),
		DefaultNamespaces: namespaces,
	})
	if err != nil {
		return nil, err
	}
	if err := mgr.Add(wrapRunnableWithLeaderElection("Cache", c)); err != nil {
		return nil, fmt.Errorf("registering namespaced cache: %w", err)
	}
	return c, nil
}

// CertRotatorConfig holds the configuration for a CertRotator.
type CertRotatorConfig struct {
	// SecretKey is the secret where the CA and certificate are written.
	SecretKey types.NamespacedName
	// DNSName is the name written to the generated certificate.
	DNSName string
	// Webhooks is the list of webhooks that this rotator manages.
	Webhooks []WebhookInfo

	// optional fields

	// URL field to set on the webhook client configuration.
	URL *string
	// Service field to set on the webhook client configuration.
	Service *apiextensionsv1.ServiceReference
	// CAName overrides the default CA name leveraged in the generated certificate.
	CAName string
	// CAOrganization overrides the default organization leveraged in the generated certificate.
	CAOrganization string
	// ExtraDNSNames adds additional names to the certificate which is generated
	ExtraDNSNames []string
	// CACertDuration sets how long a CA cert will be valid for.
	CACertDuration time.Duration
	// ServerCertDuration sets how long a server cert will be valid for.
	ServerCertDuration time.Duration
	// RotationCheckFrequency sets how often the rotation is executed
	RotationCheckFrequency time.Duration
	// LookaheadInterval sets how long before the certificate is renewed
	LookaheadInterval time.Duration

	// ControllerName allows registering multiple cert-rotator controllers.
	// Use the default value unless rotating multiple certificate secrets.
	ControllerName string
}

func (c CertRotatorConfig) validate() error {
	if c.SecretKey.Namespace == "" {
		return errors.New("invalid namespace for secret")
	}
	if c.DNSName == "" {
		return errors.New("dns name required")
	}
	return nil
}

// CertRotator contains cert artifacts and a channel to close when the certs are ready.
type CertRotator struct {
	CertRotatorConfig

	reader             cache.Cache
	writer             *Ctl
	currentCA          atomic.Value
	currentCertificate atomic.Value
}

// NewCertRotator initializes a CertRotator with the given configuration.
func NewCertRotator(config CertRotatorConfig) *CertRotator {
	if config.ControllerName == "" {
		config.ControllerName = defaultControllerName
	}
	if config.CAName == "" {
		config.CAName = defaultCAName
	}
	if config.CAOrganization == "" {
		config.CAOrganization = defaultCAOrganization
	}
	if config.CACertDuration == time.Duration(0) {
		config.CACertDuration = defaultCaCertValidityDuration
	}
	if config.ServerCertDuration == time.Duration(0) {
		config.ServerCertDuration = defaultServerCertValidityDuration
	}
	if config.LookaheadInterval == time.Duration(0) {
		config.LookaheadInterval = defaultLookaheadInterval
	}
	if config.RotationCheckFrequency == time.Duration(0) {
		config.RotationCheckFrequency = defaultRotationCheckFrequency
	}

	return &CertRotator{
		CertRotatorConfig: config,
	}
}

// Start starts the CertRotator runnable to rotate certs and ensure the certs are ready.
func (cr *CertRotator) Start(ctx context.Context) (err error) {
	if cr.reader == nil {
		return errors.New("nil reader")
	}

	logger := Logger(ctx).WithName("CertRotator[Start]")

	if !cr.reader.WaitForCacheSync(ctx) {
		return errors.New("failed waiting for reader to sync")
	}

	if err := cr.refreshCertificates(ctx); err != nil {
		return err
	}

	ticker := time.NewTicker(cr.RotationCheckFrequency)

tickerLoop:
	for {
		select {
		case <-ticker.C:
			if err := cr.refreshCertificates(ctx); err != nil {
				logger.Error(err, "error rotating certs")
			}
		case <-ctx.Done():
			break tickerLoop
		}
	}

	ticker.Stop()
	return nil
}

// GetCertificate fetches the currently loaded certificate, or an error if none exists yet.
func (cr *CertRotator) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert, err := cr.getCurrentCertificate()
	if err != nil {
		return nil, err
	}
	c, err := tls.X509KeyPair(cert.CertPEM, cert.KeyPEM)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (cr *CertRotator) getCurrentCertificate() (*certificateArtifacts, error) {
	cert := cr.currentCertificate.Load()
	if cert == nil {
		return nil, errors.New("certificate not yet loaded")
	}
	return cert.(*certificateArtifacts), nil //nolint:revive // this is always a certificateArtifacts
}

func (cr *CertRotator) getCurrentCA() (*certificateArtifacts, error) {
	ca := cr.currentCA.Load()
	if ca == nil {
		return nil, errors.New("ca not yet loaded")
	}
	return ca.(*certificateArtifacts), nil //nolint:revive // this is always a certificateArtifacts
}

func (cr *CertRotator) cacheArtifacts(secret *corev1.Secret) error {
	caArtifacts, certArtifacts, err := buildArtifactsFromSecret(secret)
	if err != nil {
		return err
	}
	cr.currentCA.Store(caArtifacts)
	cr.currentCertificate.Store(certArtifacts)
	return nil
}

func (cr *CertRotator) refreshCertificates(ctx context.Context) error {
	logger := Logger(ctx).WithName("CertRotator[Refresh]")

	secret := &corev1.Secret{}
	if err := cr.reader.Get(ctx, cr.SecretKey, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("acquiring secret to update certificates: %w", err)
		}
	}

	if !cr.validCA(secret) {
		logger.V(3).Info("refreshing CA and Cert")
		if err := cr.refreshCA(ctx, secret); err != nil {
			return err
		}
		return cr.cacheArtifacts(secret)
	}

	caArtifacts, _, err := buildArtifactsFromSecret(secret)
	if err != nil {
		return err
	}

	if !cr.validServerCert(secret) {
		logger.V(3).Info("refreshing Cert")
		if err := cr.refreshCert(ctx, secret, caArtifacts); err != nil {
			return err
		}
		return cr.cacheArtifacts(secret)
	}
	return nil
}

func (cr *CertRotator) refreshCA(ctx context.Context, secret *corev1.Secret) error {
	now := time.Now()
	begin := now.Add(-1 * time.Hour)

	caArtifacts, err := cr.createCA(begin, now.Add(cr.CACertDuration))
	if err != nil {
		return err
	}

	return cr.refreshCert(ctx, secret, caArtifacts)
}

func (cr *CertRotator) refreshCert(ctx context.Context, secret *corev1.Secret, caArtifacts *certificateArtifacts) error {
	now := time.Now()
	begin := now.Add(-1 * time.Hour)
	certificateArtifacts, err := cr.createSignedCert(caArtifacts, begin, now.Add(cr.ServerCertDuration))
	if err != nil {
		return err
	}
	return cr.writeSecret(ctx, caArtifacts, certificateArtifacts, secret)
}

func injectCert(url *string, service *apiextensionsv1.ServiceReference, updatedResource *unstructured.Unstructured, certPem []byte, webhook WebhookInfo) error {
	switch webhook.Type {
	case Validating, Mutating:
		return injectCertToWebhook(url, service, updatedResource, certPem)
	case CRDConversion:
		return injectCertToConversionWebhook(webhook.Versions, url, service, updatedResource, certPem)
	default:
		return errors.New("incorrect webhook type")
	}
}

func injectCertToWebhook(url *string, service *apiextensionsv1.ServiceReference, wh *unstructured.Unstructured, certPem []byte) error {
	webhooks, found, err := unstructured.NestedSlice(wh.Object, "webhooks")
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	for i, h := range webhooks {
		hook, ok := h.(map[string]any)
		if !ok {
			return fmt.Errorf("webhook %d is not well-formed", i)
		}
		if err := unstructured.SetNestedField(hook, base64.StdEncoding.EncodeToString(certPem), "clientConfig", "caBundle"); err != nil {
			return err
		}

		if url != nil {
			if err := unstructured.SetNestedField(hook, "clientConfig", "url"); err != nil {
				return err
			}
		}
		if service != nil {
			if err := unstructured.SetNestedField(hook, "clientConfig", "service"); err != nil {
				return err
			}
		}
		webhooks[i] = hook
	}
	return unstructured.SetNestedSlice(wh.Object, webhooks, "webhooks")
}

func ensureNestedField(obj map[string]any, fields ...string) error {
	_, found, err := unstructured.NestedMap(obj, fields...)
	if err != nil {
		return err
	}
	if !found {
		if err := unstructured.SetNestedField(obj, map[string]any{}, fields...); err != nil {
			return err
		}
	}
	return nil
}

func injectCertToConversionWebhook(versions []string, url *string, service *apiextensionsv1.ServiceReference, crd *unstructured.Unstructured, certPem []byte) error {
	if err := ensureNestedField(crd.Object, "spec", "conversion"); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(crd.Object, string(apiextensionsv1.WebhookConverter), "spec", "conversion", "strategy"); err != nil {
		return err
	}
	if err := ensureNestedField(crd.Object, "spec", "conversion", "webhook"); err != nil {
		return err
	}
	if err := unstructured.SetNestedStringSlice(crd.Object, versions, "spec", "conversion", "webhook", "conversionReviewVersions"); err != nil {
		return err
	}
	if err := ensureNestedField(crd.Object, "spec", "conversion", "webhook", "clientConfig"); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(crd.Object, base64.StdEncoding.EncodeToString(certPem), "spec", "conversion", "webhook", "clientConfig", "caBundle"); err != nil {
		return err
	}
	if url != nil {
		if err := unstructured.SetNestedField(crd.Object, *url, "spec", "conversion", "webhook", "clientConfig", "url"); err != nil {
			return err
		}
	}
	if service != nil {
		if err := unstructured.SetNestedField(crd.Object, service, "spec", "conversion", "webhook", "clientConfig", "service"); err != nil {
			return err
		}
	}
	return nil
}

func (cr *CertRotator) writeSecret(ctx context.Context, caArtifacts, certArtifacts *certificateArtifacts, secret *corev1.Secret) error {
	secret.Name = cr.SecretKey.Name
	secret.Namespace = cr.SecretKey.Namespace

	populateSecret(caArtifacts, certArtifacts, secret)
	return cr.writer.Apply(ctx, secret, client.FieldOwner(cr.ControllerName), client.ForceOwnership)
}

// certificateArtifacts stores cert artifacts.
type certificateArtifacts struct {
	Cert    *x509.Certificate
	Key     *rsa.PrivateKey
	CertPEM []byte
	KeyPEM  []byte
}

func populateSecret(caArtifacts, certArtifacts *certificateArtifacts, secret *corev1.Secret) {
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[caCertName] = caArtifacts.CertPEM
	secret.Data[caKeyName] = caArtifacts.KeyPEM
	secret.Data[certName] = certArtifacts.CertPEM
	secret.Data[keyName] = certArtifacts.KeyPEM
}

func buildArtifactsFromData(data map[string][]byte, certName, keyName string) (*certificateArtifacts, error) {
	certPEM, ok := data[certName]
	if !ok {
		return nil, fmt.Errorf("cert secret is not well-formed, missing %s", certName)
	}
	keyPEM, ok := data[keyName]
	if !ok {
		return nil, fmt.Errorf("cert secret is not well-formed, missing %s", keyName)
	}
	certDER, _ := pem.Decode(certPEM)
	if certDER == nil {
		return nil, errors.New("bad cert")
	}
	cert, err := x509.ParseCertificate(certDER.Bytes)
	if err != nil {
		return nil, fmt.Errorf("while parsing CA cert: %w", err)
	}
	keyDER, _ := pem.Decode(keyPEM)
	if keyDER == nil {
		return nil, errors.New("bad cert")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyDER.Bytes)
	if err != nil {
		return nil, fmt.Errorf("while parsing key: %w", err)
	}
	return &certificateArtifacts{
		Cert:    cert,
		CertPEM: certPEM,
		Key:     key,
		KeyPEM:  keyPEM,
	}, nil
}

func buildArtifactsFromSecret(secret *corev1.Secret) (caArtifacts *certificateArtifacts, certArtifacts *certificateArtifacts, _ error) {
	caArtifacts, err := buildArtifactsFromData(secret.Data, caCertName, caKeyName)
	if err != nil {
		return nil, nil, err
	}
	certArtifacts, err = buildArtifactsFromData(secret.Data, certName, keyName)
	if err != nil {
		return nil, nil, err
	}
	return caArtifacts, certArtifacts, nil
}

func createCertificate(template, parent *x509.Certificate, signer *rsa.PrivateKey) (*certificateArtifacts, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	if signer == nil {
		signer = key
	}
	if parent == nil {
		parent = template
	}

	der, err := x509.CreateCertificate(rand.Reader, template, parent, key.Public(), signer)
	if err != nil {
		return nil, fmt.Errorf("creating certificate: %w", err)
	}
	certPEM, keyPEM, err := pemEncode(der, key)
	if err != nil {
		return nil, fmt.Errorf("encoding PEM: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}

	return &certificateArtifacts{Cert: cert, Key: key, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

func (cr *CertRotator) createCA(begin, end time.Time) (*certificateArtifacts, error) {
	return createCertificate(&x509.Certificate{
		SerialNumber: big.NewInt(0),
		Subject: pkix.Name{
			CommonName:   cr.CAName,
			Organization: []string{cr.CAOrganization},
		},
		DNSNames: []string{
			cr.CAName,
		},
		NotBefore:             begin,
		NotAfter:              end,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}, nil, nil)
}

func (cr *CertRotator) createSignedCert(ca *certificateArtifacts, begin, end time.Time) (*certificateArtifacts, error) {
	dnsNames := []string{}
	ipSANS := []net.IP{}
	if ip := net.ParseIP(cr.DNSName); ip != nil {
		ipSANS = append(ipSANS, ip)
	} else {
		dnsNames = append(dnsNames, cr.DNSName)
	}

	caDER, _ := pem.Decode(ca.CertPEM)
	if caDER == nil {
		return nil, errors.New("bad CA cert")
	}
	parent, err := x509.ParseCertificate(caDER.Bytes)
	if err != nil {
		return nil, err
	}

	return createCertificate(&x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: cr.DNSName,
		},
		DNSNames:              append(dnsNames, cr.ExtraDNSNames...),
		IPAddresses:           ipSANS,
		NotBefore:             begin,
		NotAfter:              end,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}, parent, ca.Key)
}

func pemEncode(certificateDER []byte, key *rsa.PrivateKey) ([]byte, []byte, error) { //nolint:revive // return types are fine and internal
	certBuf := &bytes.Buffer{}
	if err := pem.Encode(certBuf, &pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}); err != nil {
		return nil, nil, fmt.Errorf("encoding cert: %w", err)
	}
	keyBuf := &bytes.Buffer{}
	if err := pem.Encode(keyBuf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		return nil, nil, fmt.Errorf("encoding key: %w", err)
	}
	return certBuf.Bytes(), keyBuf.Bytes(), nil
}

func (cr *CertRotator) lookaheadTime() time.Time {
	return time.Now().Add(cr.LookaheadInterval)
}

func (cr *CertRotator) validServerCert(secret *corev1.Secret) bool {
	if secret == nil || secret.Data == nil {
		return false
	}

	caCert, cert, key := secret.Data[caCertName], secret.Data[certName], secret.Data[keyName]

	valid, err := validCert(caCert, cert, key, cr.DNSName, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, cr.lookaheadTime())
	if err != nil {
		fmt.Println(err)
		return false
	}
	return valid
}

func (cr *CertRotator) validCA(secret *corev1.Secret) bool {
	if secret == nil || secret.Data == nil {
		return false
	}

	cert, key := secret.Data[caCertName], secret.Data[caKeyName]
	valid, err := validCert(cert, cert, key, cr.CAName, nil, cr.lookaheadTime())
	if err != nil {
		return false
	}
	return valid
}

func validCert(caCert, cert, key []byte, dnsName string, keyUsages []x509.ExtKeyUsage, at time.Time) (bool, error) {
	if len(caCert) == 0 || len(cert) == 0 || len(key) == 0 {
		return false, errors.New("empty cert")
	}

	pool := x509.NewCertPool()
	caDer, _ := pem.Decode(caCert)
	if caDer == nil {
		return false, errors.New("bad CA cert")
	}
	cac, err := x509.ParseCertificate(caDer.Bytes)
	if err != nil {
		return false, fmt.Errorf("parsing CA cert: %w", err)
	}
	pool.AddCert(cac)

	_, err = tls.X509KeyPair(cert, key)
	if err != nil {
		return false, fmt.Errorf("building key pair: %w", err)
	}

	b, _ := pem.Decode(cert)
	if b == nil {
		return false, errors.New("bad private key")
	}

	crt, err := x509.ParseCertificate(b.Bytes)
	if err != nil {
		return false, fmt.Errorf("parsing cert: %w", err)
	}

	_, err = crt.Verify(x509.VerifyOptions{
		DNSName:     dnsName,
		Roots:       pool,
		CurrentTime: at,
		KeyUsages:   keyUsages,
	})
	if err != nil {
		return false, fmt.Errorf("verifying cert: %w", err)
	}
	return true, nil
}

func reconcileSecretAndWebhookMapFunc(webhook WebhookInfo, r *webhookReconciler) func(ctx context.Context, object *unstructured.Unstructured) []reconcile.Request {
	return func(_ context.Context, object *unstructured.Unstructured) []reconcile.Request {
		hookKey := types.NamespacedName{Name: webhook.Name}
		if object.GetNamespace() != hookKey.Namespace {
			return nil
		}
		if object.GetName() != hookKey.Name {
			return nil
		}
		return []reconcile.Request{{NamespacedName: r.rotator.SecretKey}}
	}
}

func addController(mgr manager.Manager, r *webhookReconciler) error {
	c, err := controller.NewUnmanaged(r.rotator.ControllerName, controller.Options{Reconciler: r, NeedLeaderElection: ptr.To(true)})
	if err != nil {
		return err
	}

	if err := c.Watch(source.Kind(r.rotator.reader, &corev1.Secret{}, &handler.TypedEnqueueRequestForObject[*corev1.Secret]{})); err != nil {
		return fmt.Errorf("watching Secrets: %w", err)
	}

	for _, webhook := range r.rotator.Webhooks {
		hook := &unstructured.Unstructured{}
		hook.SetGroupVersionKind(webhook.gvk())
		if err := c.Watch(source.Kind(r.rotator.reader, hook, handler.TypedEnqueueRequestsFromMapFunc(reconcileSecretAndWebhookMapFunc(webhook, r)))); err != nil {
			return fmt.Errorf("watching webhook %s: %w", webhook.Name, err)
		}
	}

	return mgr.Add(c)
}

var _ reconcile.Reconciler = &webhookReconciler{}

type webhookReconciler struct {
	rotator *CertRotator
	scheme  *runtime.Scheme
}

// Reconcile reads that state of the cluster for a validatingwebhookconfiguration
// object and makes sure the most recent CA cert is included.
func (r *webhookReconciler) Reconcile(ctx context.Context, request reconcile.Request) (_ reconcile.Result, err error) {
	logger := Logger(ctx).WithName(fmt.Sprintf("%s[Reconcile]", r.rotator.ControllerName))
	if request.NamespacedName != r.rotator.SecretKey {
		return reconcile.Result{}, nil
	}

	if !r.rotator.reader.WaitForCacheSync(ctx) {
		if errors.Is(err, context.Canceled) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, errors.New("cache not ready")
	}

	secret := &corev1.Secret{}
	if err := r.rotator.reader.Get(ctx, request.NamespacedName, secret); err != nil {
		if apierrors.IsNotFound(err) || errors.Is(err, context.Canceled) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !secret.GetDeletionTimestamp().IsZero() {
		// object is being deleted, return early
		return reconcile.Result{}, nil
	}

	if err := r.rotator.refreshCertificates(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "error refreshing certificates")
		return reconcile.Result{}, err
	}

	ca, err := r.rotator.getCurrentCA()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "error loading CA")
		return reconcile.Result{}, err
	}
	if err := r.ensureCerts(ctx, ca.CertPEM); err != nil {
		if errors.Is(err, context.Canceled) {
			return reconcile.Result{}, nil
		}
		logger.Error(err, "error ensuring certificates injected")
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *webhookReconciler) ensureCerts(ctx context.Context, certPem []byte) error {
	var multierr error
	logger := Logger(ctx).WithName("CertRotator[EnsureCerts]")

	for _, webhook := range r.rotator.Webhooks {
		gvk := webhook.gvk()
		log := logger.WithValues("name", webhook.Name, "gvk", gvk)
		updatedResource := &unstructured.Unstructured{}
		updatedResource.SetGroupVersionKind(gvk)
		if err := r.rotator.reader.Get(ctx, types.NamespacedName{Name: webhook.Name}, updatedResource); err != nil {
			if apierrors.IsNotFound(err) {
				log.Error(err, "Webhook not found. Unable to update certificate.")
				continue
			}
			multierr = errors.Join(multierr, err)
			if !errors.Is(err, context.Canceled) {
				log.Error(err, "Error getting webhook for certificate update.")
			}
			continue
		}
		if !updatedResource.GetDeletionTimestamp().IsZero() {
			log.Info("Webhook is being deleted. Unable to update certificate")
			continue
		}

		if err := injectCert(r.rotator.URL, r.rotator.Service, updatedResource, certPem, webhook); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Error(err, "Unable to inject cert to webhook.")
			}
			multierr = errors.Join(multierr, err)
			continue
		}
		if err := r.rotator.writer.Apply(ctx, updatedResource, client.FieldOwner(r.rotator.ControllerName), client.ForceOwnership); err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Error(err, "Error updating webhook with certificate")
			}
			multierr = errors.Join(multierr, err)
			continue
		}
	}

	return multierr
}
