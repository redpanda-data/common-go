package kube

import (
	"context"
	"net"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	utilversion "k8s.io/apimachinery/pkg/util/version"
	"k8s.io/apiserver/pkg/endpoints/openapi"
	registryrest "k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/util/compatibility"
	"k8s.io/client-go/rest"
	basecompatibility "k8s.io/component-base/compatibility"
	"k8s.io/component-base/featuregate"
	baseversion "k8s.io/component-base/version"
	"k8s.io/kube-openapi/pkg/common"
	ctrl "sigs.k8s.io/controller-runtime"
)

type APIServerBuilder struct {
	manager ctrl.Manager
	address net.IP
	port    int
	rotator *CertRotator
	storage map[string]registryrest.Storage
}

func NewAPIServerManagedBy(mgr ctrl.Manager) *APIServerBuilder {
	return &APIServerBuilder{
		manager: mgr,
		address: net.ParseIP("0.0.0.0"),
		port:    8443,
		storage: make(map[string]registryrest.Storage),
	}
}

func (sb *APIServerBuilder) WithBind(address net.IP, port int) *APIServerBuilder {
	sb.address = address
	sb.port = port
	return sb
}

func (sb *APIServerBuilder) WithRotator(rotator *CertRotator) *APIServerBuilder {
	sb.rotator = rotator
	return sb
}

func (sb *APIServerBuilder) WithStorage(name string, storage registryrest.Storage) *APIServerBuilder {
	sb.storage[name] = storage
	return sb
}

func (sb *APIServerBuilder) Complete(groupVersion schema.GroupVersion, api common.GetOpenAPIDefinitions, title, version string) error {
	scheme := sb.manager.GetScheme()
	codecs := serializer.NewCodecFactory(scheme)
	serverConfig := genericapiserver.NewRecommendedConfig(codecs)
	serverConfig.ClientConfig = sb.manager.GetConfig()
	secure := genericoptions.NewSecureServingOptions().WithLoopback()
	secure.BindPort = sb.port
	secure.BindNetwork = "tcp"
	secure.BindAddress = sb.address
	loopbackConfig := &rest.Config{}
	serving := &genericapiserver.SecureServingInfo{}

	if sb.rotator == nil {
		if err := secure.MaybeDefaultWithSelfSignedCerts("127.0.0.1", nil, nil); err != nil {
			return err
		}
	}
	if err := secure.ApplyTo(&serving, &loopbackConfig); err != nil {
		return err
	}
	if sb.rotator != nil {
		serving.Cert = sb.rotator
		serving.SNICerts = append(serving.SNICerts, sb.rotator)
	}
	serverConfig.Config.SecureServing = serving
	serverConfig.LoopbackClientConfig = loopbackConfig

	serverConfig.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(api, openapi.NewDefinitionNamer(scheme))
	serverConfig.OpenAPIConfig.Info.Title = title
	serverConfig.OpenAPIConfig.Info.Version = version
	serverConfig.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(api, openapi.NewDefinitionNamer(scheme))
	serverConfig.OpenAPIV3Config.Info.Title = title
	serverConfig.OpenAPIV3Config.Info.Version = version

	_, gate := compatibility.DefaultComponentGlobalsRegistry.ComponentGlobalsOrRegister(
		title, basecompatibility.NewEffectiveVersionFromString(version, "", ""),
		featuregate.NewVersionedFeatureGate(utilversion.MustParse(version)),
	)
	utilruntime.Must(gate.AddVersioned(map[featuregate.Feature]featuregate.VersionedSpecs{}))
	utilruntime.Must(compatibility.DefaultComponentGlobalsRegistry.SetEmulationVersionMapping(title, basecompatibility.DefaultKubeComponent, func(from *utilversion.Version) *utilversion.Version {
		return utilversion.MustParse(baseversion.DefaultKubeBinaryVersion)
	}))
	serverConfig.EffectiveVersion = compatibility.DefaultComponentGlobalsRegistry.EffectiveVersionFor(title)

	server, err := serverConfig.Complete().New(title, genericapiserver.NewEmptyDelegate())
	if err != nil {
		return err
	}
	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(groupVersion.Group, scheme, runtime.NewParameterCodec(scheme), codecs)
	apiGroupInfo.VersionedResourcesStorageMap[groupVersion.Version] = sb.storage
	if err := server.InstallAPIGroup(&apiGroupInfo); err != nil {
		return err
	}

	return sb.manager.Add(&apiServer{
		genericserver: server,
	})
}

type apiServer struct {
	genericserver *genericapiserver.GenericAPIServer
}

func (s *apiServer) Start(ctx context.Context) error {
	return s.genericserver.PrepareRun().RunWithContext(ctx)
}

func (*apiServer) NeedLeaderElection() bool {
	return true
}
