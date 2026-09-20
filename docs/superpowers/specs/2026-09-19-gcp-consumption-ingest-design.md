# Streaming XML consumption ingest on GCP

Date: 2026-09-19
Status: approved design, awaiting implementation plan

## 1. Goal

Consume energy-consumption XML published on a Pub/Sub topic, parse it with bounded
memory and no intermediate DOM, and land canonical interval readings in BigQuery, where
they can be aggregated per metering point and period.

The work is done when:

1. A message published to the upstream topic results in its readings being queryable in
   BigQuery within seconds, without operator action.
2. A malformed or unparseable payload never blocks the subscription, and its raw bytes
   remain retrievable for inspection.
3. Redelivery of the same message, which Pub/Sub guarantees will happen eventually,
   does not produce duplicate readings for a consumer of the published view.
4. Parsing a 10 MB document uses memory proportional to one batch of readings, not to
   the document, and the cost of that parse is visible in a checked-in benchmark.
5. The existing tasks API is unaffected in latency, deployment and configuration.

## 2. Non-goals

- The AWS mirror of this path (SNS/SQS, Timestream). The GCP root moves first; the
  `deploy/terraform` contract stays symmetrical so it can follow later.
- A GKE overlay for ingest. Cloud Run only, until real volumes justify otherwise.
- Payloads above Pub/Sub's 10 MB message ceiling. See §15 for the claim-check variant
  that becomes necessary if that changes.
- Replay and backfill tooling beyond re-publishing a quarantined file by hand.
- Dashboards, BI or any read API over the readings. BigQuery IAM is the read surface.
- Changes to the tasks resource.

## 3. Decisions taken

| Decision | Chosen | Rejected, and why |
| --- | --- | --- |
| Ingress | Pub/Sub **push** to Cloud Run | Pull consumer batches across messages, but needs a standing instance. One file already holds thousands of readings, so the file is the batch. |
| Consumer host | **Second Cloud Run service, same image** | One service with an extra route shares a concurrency setting that cannot suit both a CPU-bound parse and a JSON CRUD API. |
| Store | **BigQuery** | Cloud SQL is not a timeseries engine past a few hundred million rows; Bigtable bills per node around the clock; self-hosted Timescale means operating a database. |
| Streaming ETL | Hand-written Go consumer | Dataflow adds a second toolchain and a standing worker bill to a repo whose premise is one Go binary. |
| Duplicate handling | **Append-only rows + dedup view** | Exactly-once writes via committed streams, or a scheduled `MERGE`, add machinery for a problem a window function solves at read time. |
| Permanent failures | **200 + quarantine to GCS** | Dead-lettering alone loses the payload into a topic nobody reads, after N pointless retries. |

## 4. Architecture

```
publisher ──▶ Pub/Sub topic (upstream, may be someone else's project)
                │
                └─ push subscription (ours, OIDC-signed)
                     │
                     ──▶ POST /internal/pubsub/consumption
                     │     Cloud Run service `experiment-go-ingest`
                     │     (same image as the API, different config)
                     │
                     │     1. verify OIDC token: audience + service account
                     │     2. decode envelope, base64 → []byte
                     │     3. registry picks a Parser from root element + namespace
                     │     4. stream-parse, emitting batches of readings
                     │     5. append each batch to BigQuery (Storage Write API)
                     │     6. 204 │ 5xx transient │ 200 + quarantine on permanent
                     │
                     └─ dead-letter topic after 5 failed deliveries
```

Constraints this has to hold to:

- A Pub/Sub message caps at **10 MB**; base64 in the push envelope inflates it by ~4/3,
  giving a request body up to **~13.4 MB**, comfortably inside Cloud Run's 32 MB limit.
- The push **ack deadline is raised to 60 s** (default 10 s) and the Cloud Run request
  timeout is set above it. The handler replies only after the last append commits.
- The ingest service runs **CPU 2, concurrency 8**, because parsing is CPU-bound. The
  API service keeps its own settings.
- Cloud Run bills CPU only while a request is in flight, which is exactly when parsing
  happens, so the service still scales to zero between bursts.

## 5. Domain model and contracts

New package `internal/consumption`, layered exactly as `internal/task` is: transport
knows Gin, the service knows rules, the sink knows BigQuery, and none of them knows the
other two.

```go
// Reading is one metered interval, normalised away from whatever the source called it.
type Reading struct {
	MeteringPointID string        // who
	Start           time.Time     // interval start, UTC
	Resolution      time.Duration // PT15M, PT1H, ...
	Value           float64
	Unit            string // kWh, MWh
	Quality         string // measured | estimated | missing
	Direction       string // consumption | production
	SourceMessageID string // Pub/Sub message id: dedup key and audit trail
	IngestedAt      time.Time
}
```

`Quality` and `Direction` are mandatory in the model on purpose. Every metering standard
distinguishes measured from estimated values and consumption from production; a pipeline
that flattens either produces numbers nobody can defend later.

```go
// Parser turns one document into batches of readings. The callback keeps memory
// proportional to a batch rather than to the document.
type Parser interface {
	// Parse consumes the document whose root element has already been read
	// from d. It calls emit once per batch; implementations must not retain
	// the slice after emit returns, and must stop and return emit's error.
	Parse(ctx context.Context, d *xml.Decoder, root xml.StartElement, emit func([]Reading) error) error
}

// Sink stores a batch. Implementations classify their errors (see §8).
type Sink interface {
	Write(ctx context.Context, rows []Reading) error
}
```

The registry (§6) is what reads the document's root element; it hands the parser the
same decoder and that already-read `xml.StartElement`, so no bytes are ever decoded
twice.

Sentinel errors, mapped to HTTP in §9:

- `ErrUnknownFormat` — no parser registered for this root element and namespace.
- `ErrMalformed` — the document is not well-formed, or a required field is missing.
- `ErrInvalidReading` — a reading violates a domain rule (blank metering point, zero
  resolution, non-finite value).

`Service` owns the orchestration — select parser, parse, validate each batch, write —
and is the only place that knows all three contracts. It is constructed with a
`Parser` registry and a `Sink`, so its tests need neither GCP nor a network.

## 6. Format registry

The concrete standard is not yet known, so it is isolated behind the `Parser` contract
from the first commit rather than discovered later.

```go
// Registry maps a document's root element to the parser that understands it.
// It is a value the Service holds, not package state, so tests construct their
// own with a stub parser in it.
type Registry struct{ ... }

func (r *Registry) Register(namespaceURI, localName string, p Parser)

// For inspects the first StartElement and returns the matching parser.
func (r *Registry) For(d *xml.Decoder) (Parser, error) // ErrUnknownFormat when nothing matches
```

Dispatch reads only the first `StartElement` of the stream, then hands the same decoder
to the chosen parser, so no bytes are read twice.

**The reference implementation is ENTSO-E ESMP** (`*_MarketDocument` →
`TimeSeries` → `Period` → `Point`), chosen because its structure — a repeating point
list under a period with an ISO-8601 resolution — is representative of every candidate
format, and because it is publicly documented, so the golden file in the repository can
be realistic. When the real standard is confirmed, adding it is one new file under
`internal/consumption/xmlfmt/`, one golden file and one `Register` call. No other
package changes. If the confirmed standard turns out to be ENTSO-E ESMP, the reference
implementation is the implementation.

## 7. Parsing

`xml.Decoder` in a `Token()` loop, calling `DecodeElement` only on the repeating point
elements. The document is never materialised as a tree. Readings accumulate into a slice
that is handed to `emit` when it reaches `INGEST_BATCH_ROWS` (default 5000) and again at
the end; the slice is reused between batches via its capacity, and the sink must not
retain it.

Because this parses untrusted input from outside the trust boundary:

- The handler rejects a body larger than 16 MB before decoding.
- A parse stops with `ErrMalformed` past 5 000 000 readings from one document, so a
  crafted file cannot drive unbounded work.
- A fuzz target (`go test -fuzz=FuzzParse`) runs the registry and reference parser over
  mutated input; it must not panic and must not hang. Go's `encoding/xml` does not
  expand external or nested entities, so entity-expansion attacks are out of scope, but
  the fuzz target is what keeps that assumption honest.

A checked-in benchmark (`BenchmarkParse`) over a multi-megabyte golden file makes the
throughput and allocation profile visible, so a regression shows up as a number.

## 8. BigQuery

Dataset in `europe-north1`, matching the rest of the infrastructure.

```sql
CREATE TABLE readings (
  metering_point_id STRING    NOT NULL,
  interval_start    TIMESTAMP NOT NULL,
  resolution_sec    INT64     NOT NULL,
  value             FLOAT64   NOT NULL,
  unit              STRING    NOT NULL,
  quality           STRING    NOT NULL,
  direction         STRING    NOT NULL,
  source_message_id STRING    NOT NULL,
  ingested_at       TIMESTAMP NOT NULL
)
PARTITION BY DATE(interval_start)
CLUSTER BY metering_point_id, direction;
```

Partitioning by interval day and clustering by metering point is what makes "this meter,
this month" scan megabytes instead of the table.

**Writes** go through the Storage Write API (`managedwriter`, default stream) in
appends of one batch. The default stream is at-least-once, which is the right trade:
retries are cheap and duplicates are resolved on read.

**Duplicates are expected, not exceptional.** Pub/Sub redelivers, and corrected readings
are re-sent by design. Rows are therefore append-only and readers use a view:

```sql
CREATE VIEW readings_current AS
SELECT * EXCEPT(rn) FROM (
  SELECT *, ROW_NUMBER() OVER (
    PARTITION BY metering_point_id, interval_start, direction
    ORDER BY ingested_at DESC) AS rn
  FROM readings
) WHERE rn = 1;
```

A later correction wins because it is newer. Nothing is ever updated in place, so the
history of what arrived when survives.

**Error classification** is the sink's job, because only it can tell the difference:
`codes.Unavailable`, `DeadlineExceeded`, `ResourceExhausted` and `Internal` are
transient; `InvalidArgument`, `NotFound` and `PermissionDenied` are permanent. The
service propagates the distinction; §9 maps it to a status code.

## 9. HTTP surface

`POST /internal/pubsub/consumption`, registered only when `ENABLE_INGEST_ENDPOINT` is
true — so the API service does not expose it at all. Adding it follows the repository's
`rest-endpoint` skill: handler in `internal/httpapi`, rules in the service, storage
behind the sink.

Request body is the standard push envelope:

```json
{"message": {"data": "<base64>", "messageId": "...", "attributes": {}, "publishTime": "..."},
 "subscription": "projects/.../subscriptions/..."}
```

Authentication is two layers. The subscription signs an OIDC token for a dedicated
service account; only that account holds `roles/run.invoker` on the ingest service, so
Cloud Run rejects anonymous calls at the edge. The handler then validates the token
itself — signature, `aud` equal to `PUBSUB_AUDIENCE`, and email equal to
`PUBSUB_PUSH_SERVICE_ACCOUNT` — so an accidental `allUsers` binding does not turn the
endpoint into an open XML sink.

| Failure | Reply | Effect |
| --- | --- | --- |
| Missing or invalid OIDC token | 401 | rejected; sustained 401s are the alert |
| Body over 16 MB | 413 | logged, not quarantined — the body is never read, so there is nothing to store. Pub/Sub retries and dead-letters it, which is the right outcome for what can only be a publisher defect. |
| Envelope or base64 unparseable | 200 + quarantine | permanent — retrying cannot help |
| `ErrUnknownFormat` / `ErrMalformed` | 200 + quarantine | permanent |
| `ErrInvalidReading` | 200 + quarantine | permanent, counted separately |
| Sink transient error | 503 | Pub/Sub retries with backoff; DLQ after 5 attempts |
| Context deadline mid-append | 503 | retried; the view absorbs the partial rows |
| Success | 204 | acked |

Replying **200 to a permanent failure is deliberate**: it ends the redelivery loop while
the evidence is preserved at
`gs://<name>-quarantine/YYYY/MM/DD/<message_id>.xml`, with the reason and the parser's
position recorded in object metadata and in the log line. Error response bodies never
echo payload content.

One structured log line per message, through the existing request-ID middleware:
`message_id, format, bytes, readings, parse_ms, write_ms, outcome`. The count of
quarantined messages is the signal worth alerting on.

## 10. Configuration

`internal/config` gains the settings below, validated with the same fail-fast rule the
rest of the configuration uses: when `ENABLE_INGEST_ENDPOINT` is true, the others must
be present and valid or the process exits before serving.

| Setting | Default | Notes |
| --- | --- | --- |
| `ENABLE_INGEST_ENDPOINT` | `false` | Registers the push route. On for the ingest service only. |
| `PUBSUB_AUDIENCE` | — | Expected `aud` claim; the ingest service URL. |
| `PUBSUB_PUSH_SERVICE_ACCOUNT` | — | Expected token email claim. |
| `BQ_PROJECT` | — | Defaults to the runtime project when empty. |
| `BQ_DATASET` / `BQ_TABLE` | — / `readings` | Destination table. |
| `QUARANTINE_BUCKET` | — | GCS bucket for permanently failed payloads. |
| `INGEST_BATCH_ROWS` | `5000` | Rows per Storage Write append. |

**One change to existing behaviour:** configuration currently fails fast unless a
database is configured, but the ingest service has no reason to reach Postgres. The
database becomes optional — absent configuration yields a nil pool, which `/readyz`
already handles by reporting ready without a dependency check. The API service is
unaffected: it still fails fast, because it still requires a database.

## 11. Terraform

Two new modules under `deploy/terraform/gcp/modules/`, following the existing contract
(inputs named like their siblings, outputs consumed by the root):

**`messaging/`** — the push subscription with `oidc_token` configured for the ingest
service account and the audience; `ack_deadline_seconds = 60`; a retry policy with
exponential backoff from 10 s to 600 s; a dead-letter topic and its own subscription
with `max_delivery_attempts = 5`; and the IAM that makes it work, including the Pub/Sub
service agent's publisher rights on the dead-letter topic and subscriber rights on the
subscription. The upstream topic is a `data` source when it belongs to another system
and a resource when created for development, selected by a variable.

**`warehouse/`** — the dataset, the partitioned and clustered table, the
`readings_current` view, and `roles/bigquery.dataEditor` for the ingest service account
scoped to the dataset.

**Root wiring** — a second instance of the existing `serverless` module for
`experiment-go-ingest`, sharing `var.image` with the API and overriding CPU, concurrency
and configuration; the quarantine bucket with a lifecycle rule; and the ingest service
account with `roles/storage.objectCreator` on that bucket. Everything new is behind a
`create_ingest` toggle, matching the existing `create_db` and `create_k8s` pattern, so
the current deployments are untouched when it is off.

## 12. Make targets and documentation

- `make ingest-deploy` — Terraform apply with the ingest toggle on.
- `make ingest-smoke` — publish a golden file to the topic, then poll BigQuery until the
  expected row count appears, with a timeout and a non-zero exit.
- `make bq-readings` — a convenience query against `readings_current`.
- `README.md` — a section on the ingest path, its configuration, and the endpoint table
  entry; the layout block gains `internal/consumption/`.
- `deploy/README.md` — a phase covering the subscription, the dataset and the costs.

## 13. Testing and acceptance

| Layer | How |
| --- | --- |
| Parser | Golden XML in `testdata/` with expected readings; malformed variants asserting `ErrMalformed`; `BenchmarkParse`; `FuzzParse` |
| Registry | Dispatch on root element and namespace; unknown root yields `ErrUnknownFormat` |
| Service | Stub parser and stub sink; asserts batching, validation and error propagation |
| Handler | Stub service, golden envelopes, table-driven status mapping; token validation faked through an interface — no GCP in unit tests |
| Sink | Unit-tested through its interface; an opt-in `-tags integration` test appends to a real development dataset and reads the rows back |
| End to end | `make ingest-smoke` against a deployed service |

Acceptance is the five statements in §1, each demonstrated: the smoke target covers 1,
a deliberately corrupt file covers 2, publishing the same message twice and querying the
view covers 3, the benchmark covers 4, and the API service's unchanged Terraform plan
plus its passing tests cover 5.

## 14. Rollout order

1. Domain model, contracts, registry, reference parser, tests, benchmark, fuzz target.
   No cloud dependency; fully testable locally.
2. BigQuery sink and configuration, with the integration test.
3. Push handler, token validation, quarantine writer, status mapping.
4. Terraform modules and root wiring, behind `create_ingest`.
5. Deploy, `make ingest-smoke`, documentation.

Each step leaves the repository green and deployable; nothing before step 4 changes what
runs in production today.

## 15. Risks and notes

- **The 10 MB ceiling.** Inline payloads are a hard limit of this design. If files grow
  past it, the publisher switches to a GCS pointer and only envelope decoding changes —
  the parser already takes an `io.Reader`, so a `storage.Reader` substitutes for a
  `bytes.Reader` with no other edits.
- **The format is unconfirmed.** Mitigated by the registry, at the cost of a reference
  implementation that may be replaced. The blast radius is one file and one test.
- **Cold starts.** A scale-to-zero ingest service pays a cold start on the first message
  of a burst. Pub/Sub's retry absorbs it; if it shows up as latency that matters,
  `min_instances = 1` is a one-line change with a standing cost.
- **`encoding/xml` is not the fastest XML parser in Go.** It is the one in the standard
  library, it streams, and the benchmark will say whether it is the bottleneck. Replacing
  it is a change behind the `Parser` contract, so the decision can wait for evidence.
- **Quarantine growth.** The bucket has a lifecycle rule (90 days) so a persistent
  upstream defect cannot accumulate cost indefinitely.
- **Cost.** BigQuery storage for interval data is measured in cents per million rows;
  the risk is query cost, not ingest. Partitioning and clustering are the control, and
  the dataset is not exposed to ad-hoc consumers by this design.
