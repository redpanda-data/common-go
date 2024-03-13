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