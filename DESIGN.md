# ce-go — design & decisions

This is the "why" behind ce-go. The [README](README.md) is the "what / how to use"; this is the
rationale, the constraints, and the tradeoffs — so you can extend the SDK without re-deriving them.
ce-go is one of four CE SDKs (Rust `ce-rs`, TypeScript `@ce-net/sdk`, Python `ce.py`, Go `ce-go`);
they are kept behaviorally identical by the [`ce-conformance`](https://github.com/ce-net/ce-conformance)
kit, not by shared code.

## The one idea

CE is one product — the node client — expressed in many languages. Each SDK is a **thin HTTP+SSE
wrapper over the local node API** (`127.0.0.1:8844`). The node (Rust) does all the heavy work
(libp2p, NAT traversal, crypto, capability verification, blobs, containers). The SDK just marshals
JSON and hex, and waits on I/O. That is why any language works, and why ce-go is stdlib-only.

## Decisions

**Stdlib only, one dependency-free module.** No third-party deps. `net/http` + `encoding/json` +
`bufio` for SSE is all a thin client needs. This keeps ce-go trivially vendorable (drop it in, no
`go get` supply chain), portable to constrained targets, and honest about what an SDK is — a
marshaller, not a framework.

**Idiomatic Go, not a transpile.** ce-go returns `(T, error)`, takes `context.Context`, and delivers
streams as `<-chan Message`. It is NOT a mechanical port of ce-rs. The point of polyglot SDKs is that
a Go developer feels at home; a transpiled lowest-common-denominator client would defeat that. Go's
idioms are maximally different from Rust/Python/TS, which is exactly why porting cleanly to Go proved
the node contract is language-neutral and not accidentally Rust-shaped.

**The SDK holds no keys and does no crypto.** It authenticates to its LOCAL node with a bearer
`api.token` (from `$CE_API_TOKEN` or the data-dir `api.token`) and carries capability tokens as
opaque strings. The node signs and verifies everything. This is the single most important decision:
it means a new-language SDK never re-implements Ed25519, `ce-cap`, or the wallet format, so the
security-critical surface exists in exactly one place. Do not add crypto to ce-go.

**Wire conventions are the real contract** (mirror them exactly across SDKs):
- Payloads are bytes, hex-encoded as `payload_hex`. Never base64.
- Money is integer base units as a decimal **string** (`Amount`), never a float or JSON number —
  values exceed JSON's 2^53. `1 credit = 10^18` base units; `Amount` uses `math/big`.
- `reply_token` is a u64. Go decodes it into `*uint64` losslessly (this matters — see the ce-ts
  history where JS rounded it past 2^53 and broke request/reply).
- SSE for streams, with one shared parser (`scanSSE`/`sseOnce`) reused by every stream so there is a
  single SSE implementation, not one per endpoint.

**Content addressing must be portable.** `CID(data)` is the lowercase hex SHA-256 the node returns
from `POST /blobs`. `PutObject` chunks at exactly 1 MiB into a **wire-stable manifest**
(`{"kind":"ce-object-v1","chunk_size":1048576,"total_size":N,"chunks":[...]}`, compact JSON, field
order fixed) so an object CID computed by ce-go equals the one computed by ce-rs/ce-ts/ce-py for the
same bytes. This is enforced by the `object_cid` conformance scenario against a pinned constant. If
you touch `data.go`, you must not change the byte layout of the manifest.

**`Status` is substrate-only.** `Status` is `{NodeID, PeerID, ListenPort, Economy}`. The core node
is chain-free — height, balance, and the rest of the ledger are **economy-adapter** concepts, not
substrate, so they are deliberately NOT on `Status`. When `EconomyEnabled()` is false, the economic
endpoints (transfer/jobs/channels/names/history) return a 503 `*Error` (`IsEconomyDisabled`). The
economy adapter declares its own typed API for its own types — do not re-add ledger fields here.

**Two tiers.** Tier A (`status` + `/mesh/*`: publish/subscribe/send/request/reply/serve) is the
surface a ceapp actually reaches for and the minimum any SDK must have. Tier B (blobs/objects, money,
jobs, names/discovery, economy, typed streams) grows on top. Both are covered by the conformance kit.

## Tradeoffs / things left open

- **No `ce-lane` fast path yet.** The local hot path could bypass HTTP via the Rust `ce-lane` shm
  transport (bound over a C ABI via cgo). Not built; the pure-HTTP client is fully functional. For
  bulk/high-rate data, measure before assuming HTTP overhead is fine.
- **Economic methods still live here.** Per directive, they will move to the economy adapter's own
  SDK; the ledger *types* are already gone, the *methods* (Transfer/Channels/History) remain for now.
- **No version negotiation.** Forward/backward compatibility is by convention (missing fields decode
  to zero, extra fields ignored) — discipline, not a mechanism.

## How to work on it

```sh
go build ./... && go vet ./... && go test ./...        # stdlib-only, fast
go run ./examples/quickstart                            # against a live node (ce start)
```
Then run the cross-language kit — the real gate:
```sh
cd ../ce-conformance && ./run.sh                        # ce-go must stay green in the matrix
```
Reproduce-first for bugs: add the failing test before the fix. Tests use `net/http/httptest` fakes;
end-to-end confidence comes from the conformance kit against a real node.

See [`IDEAS.md`](IDEAS.md) for what this system makes possible.
