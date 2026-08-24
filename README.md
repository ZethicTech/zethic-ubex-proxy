# fc-proxy

Sits between Postman and Flexcube. Testers swap the base URL and send plain
JSON — the proxy handles encryption, the bearer token, and the unique ids, and
logs every call to a local SQLite file it can export as CSV.

## Build for Windows

On any machine with Go 1.24+:

```sh
make windows
```

Produces `build/fc-proxy.exe` (~10 MB) plus `build/fc-proxy.env.example`.
Pure Go, no cgo — the exe is self-contained, nothing to install on the target.

Without make:

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o fc-proxy.exe ./cmd/fc-proxy
```

`make build` for a native binary, `make test` for the tests.

## Layout

```
cmd/fc-proxy/        the CLI: command dispatch, serve, export
internal/config/     env + flag resolution
internal/proxy/      the interception handler, session context, token cache
internal/ledger/     SQLite log and CSV export
internal/encryption/ the AES-CBC scheme, pinned by test vectors
```

## Setup on the jump server

1. Copy `fc-proxy.exe` and `fc-proxy.env.example` into a folder.
2. Rename the example to `fc-proxy.env` and fill in the Flexcube base URL,
   `API_SECRET`, the SANGAM client id/secret, and the session defaults.
3. Run it:

```
fc-proxy.exe serve
```

The env file is read from the binary's folder automatically. The ledger is
created next to it. Flags: `-port`, `-base-url`, `-db`, `-env`, `-insecure`,
`-quiet`.

## Usage

Point Postman at the proxy instead of Flexcube — the path stays identical:

```
Before:  POST https://flexcube.internal.example/prov/v1/query/fcdepositdetails
After:   POST http://localhost:9090/prov/v1/query/fcdepositdetails
```

Send plain JSON:

```json
{ "ServiceCode": "S001", "AccountNumber": "1234567890" }
```

The proxy wraps it in `Data` with a generated `SessionContext`, encrypts it,
attaches the token and fresh `x-session-id` / `x-message-id`, and returns the
response decrypted. A 401 refreshes the token and retries once.

### Overrides

Everything has a default in the env file. `ServiceCode` can be set from the
payload (as above); everything else is an `X-Proxy-*` header, consumed by the
proxy and never forwarded:

| Header | Effect |
| --- | --- |
| `X-Proxy-Service-Code` | `SessionContext.ServiceCode` |
| `X-Proxy-Bank-Code` / `-Branch` | `BankCode` / `TransactionBranch` |
| `X-Proxy-User-Id` / `-User-No` | `UserId` / `UserNo` |
| `X-Proxy-Session-Channel` | `SessionContext.Channel` |
| `X-Proxy-External-Ref` | pin `ExternalReferenceNo` instead of generating one |
| `X-Proxy-Channel` | drives `x-user-id: <PREFIX>_<CHANNEL>` |
| `X-Proxy-Encrypt-Body` / `-Encrypt-Query` | `false` to send in the clear |
| `X-Proxy-Decrypt-Response` | `false` to get raw ciphertext back |
| `X-Proxy-Session-Context` | `false` to skip the wrap + injection |

Hand-written `SessionContext` beats payload `ServiceCode`, beats header, beats
default. Other Postman headers pass through untouched.

Each response carries `X-Proxy-Ledger-Id`, `-Duration-Ms`, `-Session-Id`,
`-Message-Id`, `-External-Ref`, `-Target-Url`, `-Decrypted`, `-Retried`.

### Exporting the log

Every call is recorded with timestamps, the URL, the payload at all three stages
(as typed / after injection / encrypted), both header sets, status, duration and
any error.

```
fc-proxy.exe export                                    # today
fc-proxy.exe export -date 2026-08-21
fc-proxy.exe export -from 2026-08-01 -to 2026-08-21 -out august.csv
```

Dates are local, both ends inclusive.

## Defaults

Every value committed here - bank code, branch, test user, service code,
`x-user-id` prefix, `x-channel-id` - is a placeholder. The real ones go in
`fc-proxy.env` on the machine that runs the proxy; that file is gitignored and
must stay out of the repo.

## Gotchas

- **Response stays ciphertext (`decrypt_ok = 0`)** — wrong `API_SECRET`, or it
  isn't hex-encoded. `FC_PROXY_SECRET_RAW` takes it as literal text instead.
- **Everything 401s** — outside `APP_ENV=production`/`uat` the proxy sends a
  static dev token and never calls OAuth. On uat/prod, check the client creds.
- **Cert errors** — self-signed internal host; run with `-insecure`.
- **Debugging a failed call** — take `X-Proxy-Ledger-Id` from the response and
  read that row out of the ledger.
