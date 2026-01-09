## Kube

The `kube` package has some niceties around interacting with Kubernetes as a more powerful alternative to the controller-runtime clients.

## Features

1. Operational fetching of kubernetes objects via generics rather than having to initialize objects independently
2. A server-side apply wrapper of `client.Client` for consistency
3. Various "Waiter" routines that allow you to apply some object and then wait for a condition to become true (very useful in tests)
4. Port-forwarding and Exec implementations to ease forwarding into pod ports and execing commands on them programmatically
5. A Dialer implementation that leverages Kubernetes lookups based on quasi-service/pod-based hostnames and can port-forward into a pod from a local network (useful for tests where needing to do validations against listeners running inside pods, also heavily leveraged in the operator tests)
6. An environment variable expander that allows us to mimic the expansion behavior into environment variables based on Secret or ConfigMap values from a pod's `EnvFrom` definitions
7. Wrappers arounds encoding/decoding YAML similar to how `kubectl` allows for framed manifest deserialization into `client.Object`s
8. A "Syncer" that server-side applies an arbitrary group of `client.Object` values and tracks when they should be GC'd because a subsequent render causes them to become no longer needed.