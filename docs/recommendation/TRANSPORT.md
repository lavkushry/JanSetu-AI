# Authenticated recommendation transport

Go and Rust support mutual TLS on the existing gRPC boundary. Go trusts an explicitly supplied server CA bundle and verifies the configured DNS name against the ranker certificate. Rust trusts an explicitly supplied client CA bundle and requires a valid client certificate. Use a dedicated client CA that issues only authorized JanSetu API identities: possession of any valid client-auth certificate signed by that CA grants access to the ranker. This is service authentication; Go still owns all viewer consent, eligibility and final hydration checks.

## Configuration

| Variable | Go API | Rust ranker |
| --- | --- | --- |
| `JANSETU_RECOMMENDATION_TLS_CA_FILE` | Server CA PEM bundle | Dedicated API client CA PEM bundle |
| `JANSETU_RECOMMENDATION_TLS_CERT_FILE` | API client certificate PEM chain | Ranker server certificate PEM chain |
| `JANSETU_RECOMMENDATION_TLS_KEY_FILE` | API private key PEM | Ranker private key PEM |
| `JANSETU_RECOMMENDATION_TLS_SERVER_NAME` | Required server DNS SAN, independent of the dial address | Unused |

Go requires all four settings together; Rust requires all three file settings together. Incomplete configurations, unreadable files, malformed credentials and mismatched private keys stop startup. Neither process silently falls back to plaintext when credentials are configured. The Go client requires TLS 1.3 and uses only the supplied CA bundle, not operating-system roots. Rust bounds the TLS handshake to three seconds; application ranking still has its existing 120 ms deadline. Authentication/connection errors follow the existing authorized chronological feed fallback; no TLS retry downgrades the connection.

Without credentials, the existing isolated local/test plaintext setup remains available. Rust refuses plaintext when `JANSETU_ENV` is anything other than absent/empty, `local` or `test`. The Go application's existing local/test deployment gate is retained. Setting up TLS does not enable production intake or increase recommendation rollout.

Certificates must have the appropriate server-auth/client-auth extended key usage. Keep CA signing keys outside both services. Load leaf certificates and keys through read-only secret files, never committed files or image layers. Credentials are read at startup; replacing files alone does not rotate existing processes/connections. Restart both peers during rotation and retain the previous CA in a bounded overlap bundle while replacing instances. This implementation does not provide CRL/OCSP revocation or hot reload: use short-lived certificates and terminate connections/replace instances when revoking access. The [shared rollback](ROLLOUT.md) can suspend ranking during replacement.

## Optional Compose overlay

The base Compose file keeps the isolated pilot configuration. To configure mutual TLS, supply absolute file paths for `JANSETU_RECOMMENDATION_SERVER_CA_PATH`, `JANSETU_RECOMMENDATION_API_CERT_PATH`, `JANSETU_RECOMMENDATION_API_KEY_PATH`, `JANSETU_RECOMMENDATION_CLIENT_CA_PATH`, `JANSETU_RECOMMENDATION_SERVER_CERT_PATH` and `JANSETU_RECOMMENDATION_SERVER_KEY_PATH`, plus `JANSETU_RECOMMENDATION_TLS_SERVER_NAME`. Then validate the combined configuration:

```sh
docker compose -f compose.yaml -f infra/recommendation/compose.mtls.yaml config --quiet
```

The [overlay](../../infra/recommendation/compose.mtls.yaml) mounts only each peer's required CA/certificate/key. Local Compose file-backed secrets retain host ownership/permissions; ensure API files are readable by UID 65532 and ranker files by UID 10001, with private keys restricted to the intended service. Managed deployments should provision equivalent identity-scoped secrets. The overlay adds no host listener and preserves the existing API/web bindings. No active pilot migration or deployment is performed by the transport proof.

## Reproducible proof

Run `make recommendation-transport-proof`. Go generates ephemeral ECDSA certificates with separate client and server CAs, starts actual Rust processes on loopback ports, and sends the production protobuf through the production Go client under the race detector. It verifies a valid ranked response, then rejects an unknown server CA, wrong server name, untrusted/expired/wrong-purpose client certificates, an expired server certificate, absent client credentials and plaintext clients. A TLS client cannot downgrade to a plaintext Rust endpoint. A valid request still works after the rejected connections.

The proof also checks startup rejection for partial credentials, malformed CA material, missing key files and mismatched server keys. Certificates and process logs stay in test temporary directories and are removed with the test. CI runs the proof as a required part of `Runnable core`; ordinary unit tests skip process startup unless `JANSETU_RECOMMENDATION_TEST_BINARY` points to a built ranker. This establishes interoperability and access checks, not certificate provisioning, sustained TLS load capacity or operational rotation readiness.
