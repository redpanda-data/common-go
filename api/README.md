# API

The API package contains code that can be shared among multiple projects that are involved 
in serving or consuming any public or internal Redpanda API. Redpanda uses connectrpc
and therefore this project is heavily built around that.

## Package errors

The errors package contains several helpers for constructing new connectrpc errors,
as well as a helper for writing connect.Errors via HTTP.

**Creating new Connect errors:**

```go
err := apierrors.NewConnectError(
	connect.CodeUnimplemented,
	errors.New("the redpanda admin api must be configured to use this endpoint"),
	apierrors.NewErrorInfo(
            apierrors.DomainDataplane,
            v1alpha1.Reason_REASON_FEATURE_NOT_CONFIGURED.String(),
        ),
	apierrors.NewHelp(apierrors.NewHelpLink("Redpanda Console Configuration Reference", "https://docs.redpanda.com/current/reference/console/config/")),
)
```

## Package grpcgateway

**gRPC Gateway setup**

```go
gwMux := runtime.NewServeMux(
	runtime.WithForwardResponseOption(apierrors.GetHTTPResponseModifier()),
	runtime.WithErrorHandler(apierrors.NiceHTTPErrorHandler),
	runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.HTTPBodyMarshaler{
		Marshaler: apierrors.ProtoJSONMarshaler
    }),
    runtime.WithUnescapingMode(runtime.UnescapingModeAllExceptReserved),
)
```

## Package interceptor

Package interceptors provides interceptors that may be useful to any API that utilizes
connectrpc.

**Observer interceptor**

The observer interceptor observes a request throughout it's lifecycle, collects a bunch
of metadata and stats and calls the provided callback when the server finished processing
the request. This interceptor defines both an HTTP middleware and a connectrpc
interceptor, so that it can gather information at both stages of the request lifecycle.

This can be useful for access logs and monitoring.

```go
observerInterceptor := redpandainterceptor.NewObserver(func(_ context.Context, rm *redpandainterceptor.RequestMetadata) {
    api.Logger.Info("",
        zap.String("duration", rm.Duration().String()),
        zap.String("procedure", rm.Procedure()),
        zap.String("request_uri", rm.RequestURI()),
        zap.String("protocol", rm.Protocol()),
        zap.String("status_code", rm.StatusCode()),
        zap.Int64("bytes_read", rm.BytesReceived()),
        zap.Int64("bytes_sent", rm.BytesSent()),
        zap.String("peer", rm.PeerAddress()))
})

// You must mount both HTTP middleware and connectrpc interceptors
r.Use(observerInterceptor.WrapHandler)
userSvcPath, userSvcHandler := dataplanev1alpha1connect.NewUserServiceHandler(userSvc, connect.WithInterceptors(observerInterceptor))
```

## Package metrics

The metrics package offers features that streamline the process of instrumenting your API server.
It works alongside the observerInterceptor that is implemented as part of the interceptor package.

```go
apiProm, err := metrics.NewPrometheus(
    metrics.WithRegistry(prometheus.DefaultRegisterer),
    metrics.WithDynamicLabel("test", func(ctx context.Context, metadata *redpandainterceptor.RequestMetadata) string {
        return "val"
    }),
)
if err != nil {
    api.Logger.Fatal("failed to create prometheus adapter", zap.Error(err))
}

// Mount observer interceptor before
observerInterceptor := redpandainterceptor.NewObserver(apiProm.ObserverAdapter())

// You must mount both HTTP middleware and connectrpc interceptors
r.Use(observerInterceptor.WrapHandler)
userSvcPath, userSvcHandler := dataplanev1alpha1connect.NewUserServiceHandler(userSvc, connect.WithInterceptors(observerInterceptor))
```

## Package pagination

The package pagination provides functions for handling paginated Redpanda API
responses on the server-side.

**Serving paginated responses**

```go
topics := []Topics{} // Some collection which we want to paginate

// Sort the collection by the desired key
sort.SliceStable(topics, func(i, j int) bool {
    return topics[i].Name < topics[j].Name
})

// Chop the slice into pages, continue at the page that is
// encoded in the provided token.
page, nextPageToken, err := pagination.SliceToPaginatedWithToken(topics, int(req.Msg.PageSize), req.Msg.GetPageToken(), "name", func(x *v1alpha1.ListTopicsResponse_Topic) string {
    return x.GetName()
})
if err != nil {
    return nil, apierrors.NewConnectError(
        connect.CodeInternal,
        fmt.Errorf("failed to apply pagination: %w", err),
        apierrors.NewErrorInfo(v1alpha1.Reason_REASON_CONSOLE_ERROR.String()),
    )
}
```