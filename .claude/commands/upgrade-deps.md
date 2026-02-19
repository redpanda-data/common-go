Upgrade all Go module dependencies in this monorepo.

For each module directory containing a go.mod file, in dependency order (leaf modules first, modules with more dependencies last):

1. Run `go get -u all` to upgrade all dependencies
2. If `go get -u all` fails with "does not contain package" errors:
   - Identify the stale transitive dep (e.g. sentry-go, k8s.io/api, otel/sdk)
   - Run `go get <that-dep>@latest` first to resolve it
   - Retry `go get -u all`
3. Run `go mod tidy`
4. Run `go build ./...` — if build fails due to API changes in upgraded deps, fix the code
5. Run `go vet ./...`
6. Run `go test ./...` — note any pre-existing failures vs new regressions

Process modules in parallel where possible (independent modules can be upgraded concurrently).

Common issues to watch for:
- `github.com/getsentry/sentry-go` internal packages removed in newer versions (fix: `go get github.com/getsentry/sentry-go@latest` first)
- `k8s.io/api`, `k8s.io/client-go`, `k8s.io/component-base` removing packages across minor versions (fix: pin all k8s deps to `@latest` first)
- `go.opentelemetry.io/otel/sdk` internal packages removed (fix: `go get go.opentelemetry.io/otel/sdk@latest` first)
- `go.opentelemetry.io/collector/pdata` internal packages removed (fix: `go get go.opentelemetry.io/collector/pdata@latest` first)
- k8s modules (kube, otelutil, rp-controller-utils, rp-controller-gen) use coordinated k8s.io versions — keep them in sync

After all modules are upgraded, show a summary table of each module's status (pass/fail/pre-existing issues).