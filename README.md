# traefik-real-ip46

A Traefik middleware plugin that converts NAT64-mapped IPv6 addresses to their embedded IPv4 addresses in request headers and the request's client address.

When IPv4 clients reach an IPv6-only Traefik through a NAT64/SIIT translator, they appear with addresses like `64:ff9b::c000:201` (encoding `192.0.2.1`). This plugin detects those addresses and rewrites them back to plain IPv4, so services and later middlewares see the real client address.

## How it works

For a configured `/96` prefix (per [RFC 6052](https://www.rfc-editor.org/rfc/rfc6052)), the IPv4 address is embedded in the last 32 bits of the IPv6 address. For example:

```
64:ff9b::c000:0201  →  192.0.2.1
64:ff9b::0808:0808  →  8.8.8.8
```

The plugin checks each configured header (every line, if a header appears more than once), converts any matching addresses in place, and leaves non-matching addresses untouched. By default it also rewrites the request's `RemoteAddr`, so the `X-Forwarded-For` entry Traefik adds when forwarding the request carries the IPv4 address too.

It also handles:

- **IPv4-mapped addresses** such as `::ffff:192.0.2.1`, which are always converted to plain IPv4 (no prefix needed).
- **Brackets and ports**: `[64:ff9b::c000:201]:443` becomes `192.0.2.1:443`. An IPv6 zone such as `%eth0` is dropped.
- **Non-routable results are skipped**: a NAT64 address that would turn into an unspecified (`0.0.0.0`), loopback, link-local, multicast or broadcast IPv4 address is left as it is, so it can't fool IP-based middlewares. Private ranges such as `10.0.0.0/8` are still converted.

The RFC 7239 `Forwarded` header is not supported.

### Where this fits in Traefik's request flow

- Before any middleware runs, Traefik's entrypoint sets `X-Real-Ip` from the client address (if it is not already set) and merges repeated `X-Forwarded-For` lines into one. So the plugin usually sees a single `X-Forwarded-For` line; handling several lines is a safety net.
- Traefik adds the client address to `X-Forwarded-For` when it forwards the request, after the middlewares. With `rewriteRemoteAddr` on, that entry is IPv4 too.
- Middlewares that look at the client address, such as `ipAllowList` or `rateLimit`, see the IPv4 address only if they come **after** this plugin in the chain.
- Traefik's access log records the client address at the entrypoint, so it still shows the original IPv6 address.

## Installation

Enable the plugin in Traefik's static configuration:

```yaml
# traefik.yml
experimental:
  plugins:
    traefik-real-ip46:
      moduleName: github.com/olemathias/traefik-real-ip46
      version: v0.1.0
```

For the Helm chart, see [Kubernetes](#kubernetes-traefik-helm-chart) below.

## Configuration

```yaml
http:
  middlewares:
    real-ip46:
      plugin:
        traefik-real-ip46:
          headers:
            - X-Real-IP
            - X-Forwarded-For
          prefixes:
            - "64:ff9b::/96"
          rewriteRemoteAddr: true
          debug: false
```

Put this middleware first in the chain so that IP-based middlewares after it see the IPv4 address.

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `headers` | `[]string` | `["X-Real-IP", "X-Forwarded-For"]` | Headers to inspect and rewrite |
| `prefixes` | `[]string` | `["64:ff9b::/96"]` | NAT64 prefixes to match against. Must be `/96` IPv6 CIDRs. |
| `rewriteRemoteAddr` | `bool` | `true` | Also convert the request's `RemoteAddr`. |
| `debug` | `bool` | `false` | Log why each address was or was not converted. Lines go straight to stdout, so they don't follow Traefik's log format or level. Note: this logs client IP addresses. |

Multiple prefixes are supported, for example when both the [well-known prefix](https://www.rfc-editor.org/rfc/rfc6052#section-2.1) and a network-specific prefix are in use. Note that RFC 6052 only allows public IPv4 addresses under the well-known prefix `64:ff9b::/96`; translators that map private IPv4 ranges use a network-specific prefix:

```yaml
prefixes:
  - "64:ff9b::/96"
  - "64:ff9b:1::/96"
```

`X-Forwarded-For` can contain multiple comma-separated addresses — all matching entries are converted.

Lists can also be written as one comma-separated string (for example `prefixes: "64:ff9b::/96, 64:ff9b:1::/96"`); spaces around entries are ignored.

## Kubernetes (Traefik Helm chart)

**1. Enable the plugin in your Helm values:**

```yaml
# values.yaml
experimental:
  plugins:
    traefik-real-ip46:
      moduleName: github.com/olemathias/traefik-real-ip46
      version: v0.1.0
```

**2. Create the `Middleware` resource:**

```yaml
apiVersion: traefik.io/v1alpha1
kind: Middleware
metadata:
  name: real-ip46
  namespace: traefik
spec:
  plugin:
    traefik-real-ip46:
      headers:
        - X-Real-IP
        - X-Forwarded-For
      prefixes:
        - "64:ff9b::/96"
      rewriteRemoteAddr: true
```

**3. Apply globally via entrypoints in Helm values:**

The middleware reference format is `{namespace}-{name}@kubernetescrd`.

```yaml
# values.yaml
ports:
  websecure:
    http:
      middlewares:
        - traefik-real-ip46@kubernetescrd
```

## Development

```sh
gofmt -l .
go vet ./...
go test ./...
```

CI also runs the tests under [Yaegi](https://github.com/traefik/yaegi), the interpreter Traefik uses to load plugins. To do the same locally, the repo must sit in a GOPATH layout (`$GOPATH/src/github.com/olemathias/traefik-real-ip46`):

```sh
go install github.com/traefik/yaegi/cmd/yaegi@v0.16.1
GO111MODULE=off yaegi test -v .
```

## License

MIT. See [LICENSE](LICENSE).
