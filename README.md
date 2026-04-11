# traefik-real-ip46

A Traefik middleware plugin that converts NAT64-mapped IPv6 addresses to their embedded IPv4 addresses in request headers.

When traffic passes through a NAT46 gateway, IPv4 clients appear with addresses like `64:ff9b::c0a8:101` (encoding `192.168.1.1`). This plugin detects those addresses and rewrites the headers back to plain IPv4, so services see the real client address.

## How it works

For a configured `/96` prefix (per [RFC 6052](https://www.rfc-editor.org/rfc/rfc6052)), the IPv4 address is embedded in the last 32 bits of the IPv6 address. For example:

```
64:ff9b::c0a8:0101  →  192.168.1.1
64:ff9b::0808:0808  →  8.8.8.8
```

The plugin checks each configured header, converts any matching addresses in place, and leaves non-matching addresses untouched.

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
```

### Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `headers` | `[]string` | `["X-Real-IP", "X-Forwarded-For"]` | Headers to inspect and rewrite |
| `prefixes` | `[]string` | `["64:ff9b::/96"]` | NAT64 prefixes to match against. Must be `/96` IPv6 CIDRs. |
| `debug` | `bool` | `false` | Log per-request decisions to stdout explaining why each address was or was not converted. |

Multiple prefixes are supported, for example when both the [well-known prefix](https://www.rfc-editor.org/rfc/rfc6052#section-2.1) and a network-specific prefix are in use:

```yaml
prefixes:
  - "64:ff9b::/96"
  - "64:ff9b:1::/96"
```

`X-Forwarded-For` can contain multiple comma-separated addresses — all matching entries are converted.

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
```

**3a. Apply globally via entrypoints in Helm values:**

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
go test ./...
go vet ./...
```
