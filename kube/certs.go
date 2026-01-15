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
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

var (
	_ manager.Runnable               = &CertRotator{}
	_ manager.LeaderElectionRunnable = &CertRotator{}
	_ manager.Runnable               = leaderElectedController{}
	_ manager.LeaderElectionRunnable = leaderElectedController{}
	_ manager.Runnable               = &leaderElectedCache{}
	_ manager.LeaderElectionRunnable = &leaderElectedCache{}

	gvks = map[WebhookType]schema.GroupVersionKind{
		Validating:    {Group: "admissionregistration.k8s.io", Version: "v1", Kind: "ValidatingWebhookConfiguration"},
		Mutating:      {Group: "admissionregistration.k8s.io", Version: "v1", Kind: "MutatingWebhookConfiguration"},
		CRDConversion: {Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition"},
	}
)

type leaderElectedController struct {
	controller.Controller
}

func (leaderElectedController) NeedLeaderElection() bool {
	return true
}

type leaderElectedCache struct {
	cache.Cache
}

func (*leaderElectedCache) NeedLeaderElection() bool {
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
	if mgr == nil || cr == nil {
		return errors.New("nil arguments")
	}
	ns := cr.SecretKey.Namespace
	if ns == "" {
		return errors.New("invalid namespace for secret")
	}
	if cr.DNSName == "" {
		return errors.New("dns name required")
	}

	cache, err := addNamespacedCache(mgr, ns)
	if err != nil {
		return fmt.Errorf("creating namespaced cache: %w", err)
	}

	cr.reader = cache
	cr.writer = mgr.GetClient() // TODO make overrideable
	cr.certsMounted = make(chan struct{})
	cr.certsNotMounted = make(chan struct{})
	cr.caNotInjected = make(chan struct{})
	if err := mgr.Add(cr); err != nil {
		return err
	}
	if cr.ControllerName == "" {
		cr.ControllerName = defaultControllerName
	}

	if cr.CAName == "" {
		cr.CAName = defaultCAName
	}

	if cr.CAOrganization == "" {
		cr.CAOrganization = defaultCAOrganization
	}

	if cr.CACertDuration == time.Duration(0) {
		cr.CACertDuration = defaultCaCertValidityDuration
	}

	if cr.ServerCertDuration == time.Duration(0) {
		cr.ServerCertDuration = defaultServerCertValidityDuration
	}

	if cr.LookaheadInterval == time.Duration(0) {
		cr.LookaheadInterval = defaultLookaheadInterval
	}

	if cr.RotationCheckFrequency == time.Duration(0) {
		cr.RotationCheckFrequency = defaultRotationCheckFrequency
	}

	reconciler := &webhookReconciler{
		rotator:             cr,
		cache:               cache,
		writer:              mgr.GetClient(),
		scheme:              mgr.GetScheme(),
		secretKey:           cr.SecretKey,
		url:                 cr.URL,
		service:             cr.Service,
		webhooks:            cr.Webhooks,
		refreshCertIfNeeded: cr.refreshCertIfNeeded,
	}
	return addController(mgr, reconciler, cr.ControllerName)
}

// addNamespacedCache will add a new namespace-scoped cache.Cache to the provided manager.
// Informers in the new cache will be scoped to the provided namespace for namespaced resources,
// but will still have cluster-wide visibility into cluster-scoped resources.
// The cache will be started by the manager when it starts, and consumers should synchronize on
// it using WaitForCacheSync().
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
	if err := mgr.Add(&leaderElectedCache{Cache: c}); err != nil {
		return nil, fmt.Errorf("registering namespaced cache: %w", err)
	}
	return c, nil
}

// SyncingReader is a reader that needs syncing prior to being usable.
type SyncingReader interface {
	client.Reader
	WaitForCacheSync(ctx context.Context) bool
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

// CertRotator contains cert artifacts and a channel to close when the certs are ready.
type CertRotator struct {
	CertRotatorConfig

	reader SyncingReader
	writer client.Client

	certsMounted    chan struct{}
	certsNotMounted chan struct{}
	wasCAInjected   atomic.Bool
	caNotInjected   chan struct{}
	isReady         chan struct{}

	currentCertificate atomic.Value
}

// NewCertRotator initializes a CertRotator with the given configuration.
func NewCertRotator(config CertRotatorConfig) *CertRotator {
	return &CertRotator{
		CertRotatorConfig: config,
		isReady:           make(chan struct{}),
	}
}

// NeedLeaderElection marks this as requiring leader election for the manager.
func (*CertRotator) NeedLeaderElection() bool {
	return true
}

// IsReady returns a channel that is closed when the CertRotator has injected bundles.
func (cr *CertRotator) IsReady() <-chan struct{} {
	return cr.isReady
}

// Start starts the CertRotator runnable to rotate certs and ensure the certs are ready.
func (cr *CertRotator) Start(ctx context.Context) (err error) {
	if cr.reader == nil {
		return errors.New("nil reader")
	}

	logger := Logger(ctx).WithName(fmt.Sprintf("%s[Start]", cr.ControllerName))
	logger.V(3).Info("starting rotator")

	if !cr.reader.WaitForCacheSync(ctx) {
		return errors.New("failed waiting for reader to sync")
	}

	// explicitly rotate immediately so that the certificate
	// can be bootstrapped, otherwise manager exits before a cert
	// is written
	if _, err := cr.refreshCertIfNeeded(ctx); err != nil {
		return err
	}

	// Once the certs are ready, close the channel.
	go cr.ensureReady(ctx)

	ticker := time.NewTicker(cr.RotationCheckFrequency)

tickerLoop:
	for {
		select {
		case <-ticker.C:
			if _, err := cr.refreshCertIfNeeded(ctx); err != nil {
				logger.Error(err, "error rotating certs")
			}
		case <-ctx.Done():
			break tickerLoop
		case <-cr.certsNotMounted:
			return errors.New("could not mount certs")
		case <-cr.caNotInjected:
			return errors.New("could not inject certs to webhooks")
		}
	}

	ticker.Stop()
	return nil
}

// GetCertificate fetches the currently loaded certificate, or an error if none exists yet.
func (cr *CertRotator) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cert := cr.currentCertificate.Load()
	if cert == nil {
		return nil, errors.New("certificate not yet loaded")
	}
	artifacts := cert.(*KeyPairArtifacts) //nolint:revive // this is always a KeyPairArtifacts
	c, err := tls.X509KeyPair(artifacts.CertPEM, artifacts.KeyPEM)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// refreshCertIfNeeded returns true if the CA was rotated
// and if there's any error when rotating the CA or refreshing the certs.
func (cr *CertRotator) refreshCertIfNeeded(ctx context.Context) (bool, error) {
	var rotatedCA bool
	logger := Logger(ctx).WithName(fmt.Sprintf("%s[Refresh]", cr.ControllerName))

	logger.V(3).Info("refreshing cert if needed")

	refreshFn := func() (bool, error) {
		secret := &corev1.Secret{}
		if err := cr.reader.Get(ctx, cr.SecretKey, secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return false, fmt.Errorf("acquiring secret to update certificates: %w", err)
			}
		}
		defer func() {
			artifacts, err := buildArtifactsFromSecret(secret)
			if err == nil {
				logger.V(3).Info("caching artifacts")
				cr.currentCertificate.Store(artifacts)
			}
		}()
		if secret.Data == nil || !cr.validCACert(secret.Data[caCertName], secret.Data[caKeyName]) {
			if err := cr.refreshCerts(ctx, true, secret); err != nil {
				logger.Error(err, "refreshing ca cert")
				return false, nil
			}
			rotatedCA = true
			return true, nil
		}
		// make sure our reconciler is initialized on startup (either this or the above refreshCerts() will call this)
		if !cr.validServerCert(secret.Data[caCertName], secret.Data[certName], secret.Data[keyName]) {
			if err := cr.refreshCerts(ctx, false, secret); err != nil {
				logger.Error(err, "refreshing cert")
				return false, nil
			}
			return true, nil
		}
		return true, nil
	}
	if err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 10 * time.Millisecond,
		Factor:   2,
		Jitter:   1,
		Steps:    10,
	}, refreshFn); err != nil {
		return rotatedCA, err
	}
	return rotatedCA, nil
}

func (cr *CertRotator) refreshCerts(ctx context.Context, refreshCA bool, secret *corev1.Secret) error {
	var caArtifacts *KeyPairArtifacts
	now := time.Now()
	begin := now.Add(-1 * time.Hour)
	if refreshCA {
		end := now.Add(cr.CACertDuration)
		var err error
		caArtifacts, err = cr.CreateCACert(begin, end)
		if err != nil {
			return err
		}
	} else {
		var err error
		caArtifacts, err = buildArtifactsFromSecret(secret)
		if err != nil {
			return err
		}
	}
	end := now.Add(cr.ServerCertDuration)
	cert, key, err := cr.createCertPEM(caArtifacts, begin, end)
	if err != nil {
		return err
	}
	return cr.writeSecret(ctx, cert, key, caArtifacts, secret)
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

func (cr *CertRotator) writeSecret(ctx context.Context, cert, key []byte, caArtifacts *KeyPairArtifacts, secret *corev1.Secret) error {
	secret.Name = cr.SecretKey.Name
	secret.Namespace = cr.SecretKey.Namespace

	populateSecret(cert, key, certName, keyName, caArtifacts, secret)
	toUpdate := secret.DeepCopy()
	_, err := controllerutil.CreateOrUpdate(ctx, cr.writer, secret, func() error {
		secret.Data = toUpdate.Data
		return nil
	})

	return err
}

// KeyPairArtifacts stores cert artifacts.
type KeyPairArtifacts struct {
	Cert    *x509.Certificate
	Key     *rsa.PrivateKey
	CertPEM []byte
	KeyPEM  []byte
}

func populateSecret(cert, key []byte, certName string, keyName string, caArtifacts *KeyPairArtifacts, secret *corev1.Secret) {
	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[caCertName] = caArtifacts.CertPEM
	secret.Data[caKeyName] = caArtifacts.KeyPEM
	secret.Data[certName] = cert
	secret.Data[keyName] = key
}

func buildArtifactsFromSecret(secret *corev1.Secret) (*KeyPairArtifacts, error) {
	caPem, ok := secret.Data[caCertName]
	if !ok {
		return nil, fmt.Errorf("cert secret is not well-formed, missing %s", caCertName)
	}
	keyPem, ok := secret.Data[caKeyName]
	if !ok {
		return nil, fmt.Errorf("cert secret is not well-formed, missing %s", caKeyName)
	}
	caDer, _ := pem.Decode(caPem)
	if caDer == nil {
		return nil, errors.New("bad CA cert")
	}
	caCert, err := x509.ParseCertificate(caDer.Bytes)
	if err != nil {
		return nil, fmt.Errorf("while parsing CA cert: %w", err)
	}
	keyDer, _ := pem.Decode(keyPem)
	if keyDer == nil {
		return nil, errors.New("bad CA cert")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyDer.Bytes)
	if err != nil {
		return nil, fmt.Errorf("while parsing CA key: %w", err)
	}
	return &KeyPairArtifacts{
		Cert:    caCert,
		CertPEM: caPem,
		KeyPEM:  keyPem,
		Key:     key,
	}, nil
}

// CreateCACert creates the self-signed CA cert and private key that will
// be used to sign the server certificate.
func (cr *CertRotator) CreateCACert(begin, end time.Time) (*KeyPairArtifacts, error) {
	templ := &x509.Certificate{
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
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generating key: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, templ, templ, key.Public(), key)
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

	return &KeyPairArtifacts{Cert: cert, Key: key, CertPEM: certPEM, KeyPEM: keyPEM}, nil
}

func (cr *CertRotator) createCertPEM(ca *KeyPairArtifacts, begin, end time.Time) ([]byte, []byte, error) { //nolint:revive // return types are fine and internal
	dnsNames := []string{cr.DNSName}
	dnsNames = append(dnsNames, cr.ExtraDNSNames...)
	templ := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: cr.DNSName,
		},
		DNSNames:              dnsNames,
		NotBefore:             begin,
		NotAfter:              end,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generating key: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, templ, ca.Cert, key.Public(), ca.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("creating certificate: %w", err)
	}
	certPEM, keyPEM, err := pemEncode(der, key)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding PEM: %w", err)
	}
	return certPEM, keyPEM, nil
}

// pemEncode takes a certificate and encodes it as PEM.
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

func (cr *CertRotator) validServerCert(caCert, cert, key []byte) bool {
	valid, err := validCert(caCert, cert, key, cr.DNSName, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, cr.lookaheadTime())
	if err != nil {
		return false
	}
	return valid
}

func (cr *CertRotator) validCACert(cert, key []byte) bool {
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

	opt := x509.VerifyOptions{
		DNSName:     dnsName,
		Roots:       pool,
		CurrentTime: at,
		KeyUsages:   keyUsages,
	}

	_, err = crt.Verify(opt)
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
		return []reconcile.Request{{NamespacedName: r.secretKey}}
	}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler.
func addController(mgr manager.Manager, r *webhookReconciler, controllerName string) error {
	// Create a new controller
	c, err := controller.NewUnmanaged(controllerName, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	err = c.Watch(
		source.Kind(r.cache, &corev1.Secret{}, &handler.TypedEnqueueRequestForObject[*corev1.Secret]{}),
	)
	if err != nil {
		return fmt.Errorf("watching Secrets: %w", err)
	}

	for _, webhook := range r.webhooks {
		wh := &unstructured.Unstructured{}
		wh.SetGroupVersionKind(webhook.gvk())
		err = c.Watch(
			source.Kind(r.cache, wh, handler.TypedEnqueueRequestsFromMapFunc(reconcileSecretAndWebhookMapFunc(webhook, r))),
		)
		if err != nil {
			return fmt.Errorf("watching webhook %s: %w", webhook.Name, err)
		}
	}

	return mgr.Add(leaderElectedController{c})
}

var _ reconcile.Reconciler = &webhookReconciler{}

type webhookReconciler struct {
	rotator *CertRotator

	writer              client.Client
	cache               cache.Cache
	scheme              *runtime.Scheme
	url                 *string
	service             *apiextensionsv1.ServiceReference
	secretKey           types.NamespacedName
	webhooks            []WebhookInfo
	refreshCertIfNeeded func(context.Context) (bool, error)
}

// Reconcile reads that state of the cluster for a validatingwebhookconfiguration
// object and makes sure the most recent CA cert is included.
func (r *webhookReconciler) Reconcile(ctx context.Context, request reconcile.Request) (_ reconcile.Result, err error) {
	logger := Logger(ctx).WithName(fmt.Sprintf("%s[Reconcile]", r.rotator.ControllerName))
	logger.V(3).Info("reconciling", "namespace", request.Namespace, "name", request.Name)

	if request.NamespacedName != r.secretKey {
		return reconcile.Result{}, nil
	}

	if !r.cache.WaitForCacheSync(ctx) {
		return reconcile.Result{}, errors.New("cache not ready")
	}

	secret := &corev1.Secret{}
	if err := r.cache.Get(ctx, request.NamespacedName, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if !secret.GetDeletionTimestamp().IsZero() {
		// object is being deleted, return early
		return reconcile.Result{}, nil
	}

	rotatedCA, err := r.refreshCertIfNeeded(ctx)
	if err != nil {
		logger.Error(err, "error rotating certs on secret reconcile")
		return reconcile.Result{}, err
	}

	// if we did rotate the CA, the secret is stale so let's return
	if rotatedCA {
		return reconcile.Result{}, nil
	}

	artifacts, err := buildArtifactsFromSecret(secret)
	if err != nil {
		logger.Error(err, "secret is not well-formed, cannot update webhook configurations")
		return reconcile.Result{}, nil
	}

	// Ensure certs on webhooks
	if err := r.ensureCerts(ctx, artifacts.CertPEM); err != nil {
		return reconcile.Result{}, err
	}

	// Set CAInjected if the reconciler has not exited early.
	r.rotator.wasCAInjected.Store(true)
	return reconcile.Result{}, nil
}

// ensureCerts returns an arbitrary error if multiple errors are encountered,
// while all the errors are logged.
// This is important to allow the controller to reconcile the secret. If an error
// is returned, request will be requeued, and the controller will attempt to reconcile
// the secret again.
// When an error is encountered for when processing a webhook, the error is logged, but
// following webhooks are also attempted to be updated. If multiple errors occur for different
// webhooks, only the last one will be returned. This is ok, as the returned error is only meant
// to indicate that reconciliation failed. The information about all the errors is passed not
// by the returned error, but rather in the logged errors.
func (r *webhookReconciler) ensureCerts(ctx context.Context, certPem []byte) error {
	var anyError error
	logger := Logger(ctx).WithName(fmt.Sprintf("%s[EnsureCerts]", r.rotator.ControllerName))

	for _, webhook := range r.webhooks {
		gvk := webhook.gvk()
		log := logger.WithValues("name", webhook.Name, "gvk", gvk)
		updatedResource := &unstructured.Unstructured{}
		updatedResource.SetGroupVersionKind(gvk)
		if err := r.cache.Get(ctx, types.NamespacedName{Name: webhook.Name}, updatedResource); err != nil {
			if apierrors.IsNotFound(err) {
				log.Error(err, "Webhook not found. Unable to update certificate.")
				continue
			}
			anyError = err
			log.Error(err, "Error getting webhook for certificate update.")
			continue
		}
		if !updatedResource.GetDeletionTimestamp().IsZero() {
			log.Info("Webhook is being deleted. Unable to update certificate")
			continue
		}

		log.V(1).Info("Ensuring CA cert", "name", webhook.Name, "gvk", gvk)
		if err := injectCert(r.url, r.service, updatedResource, certPem, webhook); err != nil {
			log.Error(err, "Unable to inject cert to webhook.")
			anyError = err
			continue
		}
		if err := r.writer.Update(ctx, updatedResource); err != nil {
			log.Error(err, "Error updating webhook with certificate")
			anyError = err
			continue
		}
	}

	return anyError
}

func (cr *CertRotator) ensureReady(ctx context.Context) {
	logger := Logger(ctx).WithName(fmt.Sprintf("%s[Ready]", cr.ControllerName))

	checkFn := func() (bool, error) {
		return cr.wasCAInjected.Load(), nil
	}
	if err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 1 * time.Second,
		Factor:   2,
		Jitter:   1,
		Steps:    10,
	}, checkFn); err != nil {
		logger.Error(err, "max retries for checking CA injection")
		close(cr.caNotInjected)
		return
	}
	logger.V(1).Info("CA certs are injected to webhooks")
	close(cr.isReady)
}
