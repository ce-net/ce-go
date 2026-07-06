# ce-go — the CE mesh client for Go

A dependency-free (stdlib-only) Go client for a local CE node. It lets a Go ceapp talk to its
node exactly like the Rust (`ce-rs`), TypeScript (`@ce-net/sdk`), and Python (`ce.py`) clients do:
publish/subscribe telemetry, directed request/reply, and a `Serve` loop — all over the node's
local HTTP API. No third-party modules, no build step beyond `go build`.

```go
import ce "github.com/ce-net/ce-go"
```

## Quickstart

```go
ctx := context.Background()
c := ce.Connect()                                    // http://127.0.0.1:8844, token auto-discovered
c.WaitReady(ctx, 30*time.Second)                     // don't race a node started at boot

c.Publish(ctx, "building/temp", []byte("21.5"))      // fan out to every subscriber

msgs, _ := c.Subscribe(ctx, "building/temp")         // stream readings from the mesh
for m := range msgs {
    fmt.Println(m.Sender, m.Text())
}
```

Be a mesh responder (a capability provider):

```go
c.Serve(ctx, []string{"demo/echo"}, func(m ce.Message) ([]byte, error) {
    return []byte("echo: " + m.Text()), nil          // return the reply; nil = don't reply
})
```

Call one, reliably:

```go
reply, _ := c.Request(ctx, peerNodeID, "demo/echo", []byte("hello"), 10*time.Second)
```

Run the full example against a live local node (`ce start --no-economy`):

```sh
go run ./examples/quickstart
```

## Surface (Tier A — the app / mesh tier)

| Method | Node endpoint |
|---|---|
| `Connect(opts...)` / `WithBaseURL` / `WithToken` / `WithHTTPClient` | — |
| `Status(ctx)` · `NodeID(ctx)` · `WaitReady(ctx, timeout)` | `GET /status` |
| `Publish(ctx, topic, payload)` | `POST /mesh/publish` |
| `SubscribeTopic(ctx, topic)` | `POST /mesh/subscribe` |
| `Subscribe(ctx, topics...)` → `<-chan Message` | `GET /mesh/messages/stream` (SSE) |
| `Messages(ctx, subscribe...)` → `<-chan Message` | `GET /mesh/messages/stream` (SSE) |
| `Send(ctx, to, topic, payload)` | `POST /mesh/send` |
| `Request(ctx, to, topic, payload, timeout)` → `[]byte` | `POST /mesh/request` |
| `Reply(ctx, token, payload)` | `POST /mesh/reply` |
| `Serve(ctx, topics, Handler)` | subscribe → stream → reply loop |

This is the surface a ceapp reaches for first. The full node tier grows on top of it.

## Surface (Tier B — the full node tier)

| Area | Methods | Node endpoint(s) |
|---|---|---|
| Status | `Status` (with `Balance` + breakdown), `EconomyEnabled` | `GET /status` |
| Money | `Amount` (`FromCredits`/`ParseCredits`/`Credits`/`Add`/`Cmp`, base-unit-string JSON) | — |
| Blobs | `PutBlob` / `GetBlob`, `CID` | `POST /blobs`, `GET /blobs/:hash` |
| Objects | `PutObject` / `GetObject` (chunked, CID-verified) | many `/blobs` + manifest |
| Jobs | `Bid` · `Jobs` · `Job` · `Kill` | `/jobs*` |
| Capacity | `Atlas` · `Beacon` | `GET /atlas`, `GET /beacon` |
| Names/discovery | `ClaimName` · `ResolveName` · `AdvertiseService` · `FindService` · `AdvertiseTag`/`FindTag` | `/names/*`, `/discovery/*` |
| Economy | `Transfer` · `Channels`/`OpenChannel`/`SignReceipt`/`CloseChannel`/`ExpireChannel` · `History` | `/transfer`, `/channels/*`, `/history/:id` |
| Streams | `Blocks` · `Transactions` · `SignalStream` · `Signals` | `/blocks/stream`, `/transactions/stream`, `/signals*` |

**Money is always integer base units** (`1 credit = 10^18`), carried on the wire as a decimal
string — never a float, never a JSON number (values exceed 2^53). `Amount` uses `math/big`.

**Content addressing is portable.** `CID(data)` is the lowercase hex SHA-256 the node returns from
`POST /blobs`; `PutObject` chunks at 1 MiB into a wire-stable manifest, so an object CID computed by
any CE SDK refers to the same bytes.

**Economy is optional.** On a personal-mesh node, economic calls return a 503 — check
`ce.IsEconomyDisabled(err)` and degrade rather than treating it as a hard failure. (Economy is being
extracted into an adapter and will eventually leave the substrate entirely.)

```sh
go run ./examples/tierb    # live check: status, blob/object round-trips, discovery, economy-degrade
```

Full rationale and the two-tier model: [DESIGN.md](DESIGN.md).

## Conventions (identical across every CE SDK)

- **Auth**: `Authorization: Bearer <api-token>`. The token comes from `$CE_API_TOKEN`, else the
  node's `api.token` in the CE data dir (per-OS, resolved the same way the `ce` binary does). The
  SDK holds no keys and does no crypto — the node signs and verifies.
- **Endpoint**: `$CE_NODE_URL`, default `http://127.0.0.1:8844`.
- **Payloads** are `[]byte` on the wire (hex-encoded in the JSON body; the SDK handles it).
- **Errors** surface as `*ce.Error` carrying the node's status and body detail.
- **Streams** reconnect automatically with backoff; cancel the `context.Context` to stop.

## Design & ideas

The client is a thin, I/O-bound marshaller over the node's HTTP API — the heavy work (mesh routing,
NAT, crypto, containers) happens in the Rust node, so the client's language is a free,
performance-neutral choice.

- **[DESIGN.md](DESIGN.md)** — the decisions behind ce-go and the polyglot model (why stdlib-only,
  why no crypto, the wire conventions, substrate-only `Status`), and how to work on it.
- **[IDEAS.md](IDEAS.md)** — five non-obvious things this system makes possible (a mesh standard
  library, self-distributing software, a heterogeneous notebook, an ambient capability fabric,
  conformance-as-trust) — why you'd want them and how they work on these primitives.

ce-go is one of four CE SDKs (Rust `ce-rs`, TypeScript `@ce-net/sdk`, Python `ce.py`, Go `ce-go`),
kept behaviorally identical by the [`ce-conformance`](https://github.com/ce-net/ce-conformance) kit.

## Test

```sh
go test ./...
```

The suite drives the full Tier-A surface against an in-process fake node (`net/http/httptest`) —
publish/send hex round-trips, request/reply decoding, the SSE-parse → handler → reply loop, and
error propagation. It is the local seed of the language-agnostic SDK conformance kit.
