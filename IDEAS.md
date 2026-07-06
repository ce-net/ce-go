# What you can build with this — five ideas that don't exist yet

The polyglot SDKs (`ce-rs`, `ce-ts`, `ce-py`, `ce-go`) are small. What they unlock is not. Each SDK
is a thin client over a mesh node that already does the hard parts — routing, NAT traversal, crypto,
capability auth, content-addressed storage — so an app author writes a plain function and never
touches transport, discovery, serialization, or infrastructure. And because every language speaks the
same mesh, a capability written once is callable from all of them.

This document is deliberately not a tutorial. It's five things you *couldn't build before*, why
you'd want them, how they actually work on these primitives, the decisions behind the design, and the
questions they leave open. They are grounded in what exists (`serve`/`request`, `publish`/`subscribe`,
`PutObject`/`CID`, caps) — not vaporware — but they aim past the demos.

The through-line: **stop deploying services to machines; start growing a standard library on a mesh.**

---

## 1. The mesh standard library — functions that live nowhere and everywhere

**What it is.** You write a function once — `resizeImage`, `transcribe`, `route`, `settle` — and
`serve` it on a topic. Anyone on the mesh `request`s it, from any language, with no knowledge of
where it runs. There is no HTTP server, no API gateway, no service mesh, no Kubernetes, no per-service
client SDK to generate. A Rust image-resize capability is called identically by a Python cron script,
a Go daemon, and a browser TypeScript app. Install a new capability-providing ceapp and the mesh's
"standard library" grows by one function — for every language at once.

**Why you'd want it.** The entire microservice tax — gateways, load balancers, service discovery,
sidecars, mTLS, OpenAPI-per-service, a cluster to run it on — exists to let one function call another
across a network. Here that's a language primitive: `request(provider, topic, payload)`. The
*transparency invariant* means the caller never learns (or cares) what language or machine answered.
Capabilities compound: every one you add makes the whole mesh strictly more capable, and higher-order
capabilities are just functions that call lower ones.

**How it works.** `serve(ctx, topics, handler)` subscribes and answers directed `request`s;
`request(ctx, to, topic, payload, timeout)` is the reliable RPC; `advertise`/`FindService` (+ `locate`
in ce-rs) is discovery. The node handles routing, NAT hole-punching, and cap verification; the SDK is
a marshaller. Payloads are opaque bytes, so any serialization (JSON, protobuf, raw) works, and a
provider can be rewritten in another language without touching a single caller.

```go
// provider (Go) — anywhere on the mesh
c.Serve(ctx, []string{"cap/thumbnail"}, func(m ce.Message) ([]byte, error) {
    return thumbnail(m.Payload), nil    // a plain function
})
```
```python
# caller (Python) — anywhere else, no address, no HTTP
thumb = ce.connect().request(provider_id, "cap/thumbnail", jpeg_bytes)
```

**Decisions.** Request/reply (not gossip) is the reliable primitive — never build must-arrive
semantics on pub/sub. Opaque byte payloads keep the mesh language- and schema-agnostic. Providers are
replaceable and cap-gated: holding a capability lets you call; the node verifies, the provider
authorizes.

**Open questions.** Today the call is untyped bytes; the *typed* capability contract across languages
(a manifest that generates typed stubs) is the natural next layer — and it almost certainly must be
*generated*, because hand-writing N-language clients for every capability doesn't scale (see idea 5).
Versioning, discovery ranking (trust/latency/capacity via `ce-lb`/`locate`), and how a capability
advertises its schema are the frontier.

---

## 2. Software that ships itself — distribution with no registry and no host

**What it is.** You publish a program — a WASM capability, a script, a cross-compiled binary — as a
**content-addressed blob**. Its CID (a SHA-256) is simultaneously its name, its integrity check, and
its trust anchor. Any peer fetches it by CID, verifies it by hash + author signature + capability, and
runs it. There is no npm registry, no Docker Hub, no CDN, no app store, no central host anyone can
poison or take down. Devices distribute software to each other, peer to peer.

**Why you'd want it.** You trust the *hash*, never the *source* — so no relay can substitute bytes,
there's no supply-chain injection point, and there's no honeypot to attack. It works on a LAN, offline,
or air-gapped. A capable machine can cross-compile a capability *for* a constrained device (an ESP32
can't build for itself) and hand it over the mesh; a CID-keyed cache dedupes it fleet-wide. This is
"software distribution" as a mesh primitive instead of a company you depend on.

**How it works.** `PutObject(bytes)` chunks the artifact at 1 MiB into a **wire-stable manifest** and
returns the object CID; `GetObject(cid)` reassembles it, verifying every chunk against its hash. The
manifest is byte-identical across every SDK — the `object_cid` conformance scenario pins a constant
that Go, Python, TS, and Rust all reproduce for the same input — so a Go publisher and a Python
consumer agree on the CID with no coordination. Distribution is therefore language-agnostic by
construction.

**Decisions.** Content address = trust (the same idea as idea 5, applied to bytes). Portable,
byte-stable manifest so the CID is universal. Chunking gives dedup and partial fetch. Publishing is
itself an app over node primitives (blob write + sign), not a privileged feature.

**Open questions.** The keystone gap is cross-node *fetch-by-CID over the DHT* — the substrate
primitive that makes "any holder can serve" real (in progress in the node). Reproducible builds plus a
builder quorum give trust in a binary with *no* central build authority — a deep, mostly-unsolved
problem this design invites. What's the GC / pinning policy for a fleet's blob store?

---

## 3. The heterogeneous notebook — one document, many languages, many machines

**What it is.** A notebook (or pipeline) where each step runs in the *best language on the best
machine*, and the steps share data as content-addressed blobs. Ingest and parse in Rust (fast), model
in Python (pandas/torch on the GPU box), visualize in a browser TypeScript cell — all in one document,
with no Docker, no Airflow, no Spark cluster, no object-store credentials. The mesh is the kernel;
CIDs are the shared memory between cells.

**Why you'd want it.** Data and ML work today is a pile of glue: Python here, a scheduler there, an
object store with IAM, a notebook that only runs one language, a cluster to rent. Here it collapses to
one substrate. A cell passes a CID to the next cell; the next cell — in a different language on a
different machine — reads the same bytes through the same SDK. Immutable, cacheable, deduplicated
intermediates fall out for free because everything is content-addressed. Polyglot pipelines with zero
infrastructure.

**How it works.** Each cell is a capability (or a `script`-tier ceapp) using its language's SDK.
Intermediate results are `PutObject` → CID → the downstream cell does `GetObject(cid)`. Placement is
`locate`/atlas-driven (run the GPU cell on the GPU node, the ingest cell near the data). The notebook
orchestrator itself can be written in any language — it just sequences capability calls and threads
CIDs.

**Decisions.** Content-addressed intermediates make the pipeline a reproducible DAG of hashes (a
build graph you can cache and re-run incrementally). Language-per-step is a feature, not a
compromise. The orchestrator is not privileged — it's another mesh client.

**Open questions.** Reactive re-execution (a changed input CID invalidates exactly its downstream);
provenance and lineage (the CID DAG *is* the audit trail); who schedules and how placement
negotiates cost/latency/GPU (`ce-sched`). Live collaboration on the same notebook (idea 4's CRDTs).

---

## 4. Ambient capability fabric — your building, robot, or fleet as one computer

**What it is.** Every physical device is an addressable capability provider. A temperature sensor on
an Arduino's Linux MPU runs a Python `climate.read` capability; a Go controller on your laptop calls
it; a browser TypeScript dashboard subscribes to its stream — transparently, as you roam, scoped by
organization (the Arduino org trusts its own boards for full admin between them, but your laptop gets
zero admin over the boards, and vice-versa). Your house, your robot, your drone fleet becomes a single
computer you call functions on.

**Why you'd want it.** It replaces the whole IoT stack — an MQTT broker, a per-vendor cloud, bespoke
firmware protocols, a phone app that only talks to one brand. There is no cloud dependency; it's
LAN-first and works when the internet is down. The *same app* runs on the sensor, the laptop, and the
relay (the transparency invariant), because adapters fix each environment's protocol, not the app.
Adding a sensor means installing a capability ceapp — and the whole mesh gains a new function.

**How it works.** `script`-tier ceapps (often Python, drop in `ce.py`) `provide` capabilities,
`publish` telemetry, and `serve` control requests. Organization roots (`ce-cap`) scope trust — a
device trusts N org roots and can belong to N orgs/workspaces, so "home", "work", and "ai-drone-fleet"
are separate trust domains. Roaming survives because the node auto-dials a relay circuit and
hole-punches (DCUtR). Polyglot in the small: the sensor is Python, the controller is Go, the UI is TS
— one mesh.

**Decisions.** Capabilities are keyed by chip/org, not hardcoded into the OS (the substrate must not
learn "capability" as a concept — it's an app). Fleet nodes run economy-free (`--no-economy`). It's
mesh-only — no `ce tunnel`, no netcat/python hacks — so the reliability contract is the mesh's. A
liveness/staleness contract keeps feeds from silently freezing (a real failure mode we hit).

**Open questions.** The per-chip *blueprint generator* (given board + module + wiring, emit the exact
constraint-correct plan). Reliability guarantees strong enough for a 4K camera feed as you walk a
building. The "feels like one machine" DX bar for install and debug on embedded targets.

---

## 5. Correctness as a public, runnable, cross-language contract

**What it is.** The [`ce-conformance`](https://github.com/ce-net/ce-conformance) kit — the thing that
keeps these four SDKs identical — is really a new *social* primitive, not just internal QA. It's a
language-neutral behavioral contract plus a thin per-implementation runner: any language community, or
any competing implementation of a capability's protocol, *self-certifies by passing it*. "Conformant"
becomes a badge you earn with a green run, not a trust relationship you negotiate or a code review
someone grants you. Push the idea further: every capability ships its own conformance suite, and any
implementation in any language, by anyone, that passes it is interoperable *by construction*.

**Why you'd want it.** This is how you grow an ecosystem of independent, competing implementations
with **no central gatekeeper**. It flips "trust the vendor" into "trust the passing test" — the same
move as content-addressing (idea 2), applied to *behavior* instead of *bytes*. A green conformance run
IS the trust. It's how the mesh can have five SDKs today and fifty tomorrow without a committee
approving each one, and how two people can independently implement the same capability and know they
interoperate before they ever talk.

**How it works.** A language-neutral `SCENARIOS.md` (the contract) + a tiny runner per implementation
that drives *only* that implementation's public API and prints `CONF <id> PASS/FAIL` + a driver that
builds a cross-implementation matrix and gates on any failure. Pinned cross-implementation invariants
(like the `object_cid` constant every SDK must reproduce) make interop provable, not asserted. In
practice it has already found and driven fixes for two real interop bugs across languages — drift
becomes a failing test the moment you add a runner.

**Decisions.** Behavioral equivalence over shared code (each implementation stays idiomatic — a
transpiled monoculture would be worse and unadoptable). Runnable, not prose — the contract executes.
Economy-agnostic core scenarios so the substrate contract never depends on optional layers. Pinned
invariants for the things that *must* match byte-for-byte.

**Open questions.** Conformance as a *marketplace filter* (only listed if green). Versioned
conformance — which node version does a green run attest to? "Who certifies the certifier" — the kit
itself is code that can drift. And the biggest one: if *every* capability and adapter ships a
conformance suite, does the capability system *generate* both the suites and the typed clients from a
single declared manifest? That's where ideas 1, 2, and 5 converge — a mesh where declaring a
capability gives you, for free and in every language, a typed client, a distributable artifact, and a
proof of interop.

---

## The shape of it

Four of these five are things the current industry solves with a *stack of separate products* (a
service mesh, a registry + CDN, a data platform, an IoT cloud, a certification body). Here each is a
consequence of two ideas: **the SDK is thin because the node is the operating system**, and **behavior
is unified by a runnable contract, not by shared code.** That's why polyglot isn't a nicety — it's the
mechanism. The mesh doesn't care what language you speak; it only cares that you pass.
