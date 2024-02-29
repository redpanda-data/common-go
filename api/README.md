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
