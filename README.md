# Social AI

A full-stack application combining AI image generation with social sharing.

## Project Structure

```
.
├── frontend/          # React frontend (Create React App)
│   ├── src/
│   ├── public/
│   ├── Dockerfile     # build with npm, serve with nginx
│   ├── nginx.conf
│   ├── .env.example
│   └── package.json
│
├── backend/           # Go backend
│   ├── handler/       # HTTP handlers, routing, auth middleware
│   ├── service/       # business logic
│   ├── store/         # Elasticsearch + Google Cloud Storage clients
│   ├── media/         # upload content-type validation
│   ├── config/        # environment-driven configuration
│   ├── logging/       # structured logging, request ids
│   ├── Dockerfile     # static binary, non-root, distroless-ish alpine
│   ├── .env.example
│   └── go.mod
│
├── docker-compose.yml # the whole stack, offline
└── README.md
```

## Running it offline

The whole stack runs with no cloud account, no credentials and no billing:

```bash
docker compose up --build
open http://localhost:3000
```

That starts the same Elasticsearch 7.17.24 the tests and the deployment use — with
security enabled, so the credential path is exercised rather than bypassed — a
Google Cloud Storage emulator, the API, and the frontend behind nginx.

The only thing replaced by a substitute is the DALL-E call, via
`IMAGE_PROVIDER=stub`, which renders a placeholder PNG derived from a hash of the
prompt. That is the one step that costs money per request; everything after it is
the production path unmodified — the same content sniffing, the same storage
write, the same public-read ACL, the same index write. The placeholder is a real
encoded PNG for that reason: the bytes have to survive the same validation as a
real upload.

Nothing is persisted. Both stores are deliberately throwaway *together*: the
storage emulator holds objects in memory, so if Elasticsearch kept its indices the
gallery would come back listing posts whose images no longer exist. `docker compose
down` resets everything.

## Configuration

No credentials live in source. Both halves read configuration from the
environment and ship a `.env.example` listing what is required.

**Backend** — copy `backend/.env.example` to `backend/.env` and fill it in.
The server validates everything at startup and exits with a single explanatory
message if anything is missing, too short, or unsafe:

| Variable | Notes |
|---|---|
| `ES_URL` | Use `https` outside local dev; basic auth over plain http sends the password in cleartext |
| `ES_USERNAME`, `ES_PASSWORD` | Elasticsearch credentials |
| `GCS_BUCKET` | Bucket for uploaded and generated media |
| `JWT_SECRET` | HS256 signing key, minimum 32 chars (`openssl rand -base64 48`) |
| `OPENAI_API_KEY` | Server-side only, never exposed to the browser. Required only when `IMAGE_PROVIDER=openai` |
| `IMAGE_PROVIDER` | Optional. `openai` (default) or `stub`. A named switch, not inferred from a missing key |
| `ALLOWED_ORIGINS` | Comma-separated frontend origins; `*` is rejected |
| `PORT` | Optional; defaults to 8080 |
| `LOG_LEVEL` | Optional. `debug`, `info` (default), `warn`, `error`. An unrecognised value is rejected |
| `STORAGE_EMULATOR_HOST` | Optional. Points the storage client at an emulator. When set, the server also creates the bucket at startup |

**Frontend** — copy `frontend/.env.example` to `frontend/.env.local`. Only
non-secret values belong there: Create React App inlines every `REACT_APP_*`
variable into the production bundle, where anyone can read it.

## Backend

Go 1.25+. Elasticsearch for users and posts, Google Cloud Storage for media.

```bash
cd backend
cp .env.example .env      # then edit
set -a && . ./.env && set +a
go run .
```

```bash
go build ./...            # compile
go vet ./...              # static checks
golangci-lint run ./...   # linters; config in .golangci.yml
go test ./...             # unit tests
```

`go vet` is the floor. The linter set in `.golangci.yml` caught three sentinel
errors compared with `==` rather than `errors.Is`. One of them, in the GCS delete
path, turned out to be a live bug rather than a latent one: the client returns
`ErrObjectNotExist` wrapped, so the comparison was never true and deleting a post
whose image had already gone failed every time. It also caught a server started
without a header-read timeout. Install it with:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
```

Integration tests run against real services and skip unless the corresponding
variable is set. They cover what compiling cannot: that the mappings are accepted,
that the throttle script increments rather than overwrites, that an unindexed
field genuinely cannot be filtered on, that the reindex preserves fields, and that
an object written to storage comes back with the bytes, the content type and the
public-read ACL it was given.

```bash
# Against the compose stack, which already runs both:
docker compose up -d elasticsearch fake-gcs

cd backend
ES_TEST_URL=http://127.0.0.1:9200 \
ES_TEST_USERNAME=elastic ES_TEST_PASSWORD=local-dev-password \
GCS_TEST_EMULATOR=localhost:4443 \
  go test -run Integration -v ./...
```

Credentials are overridable because a cluster that actually checks them is the
only way to exercise that half of the config; with `xpack.security` off, any
username and password work.

CI runs the same suite against both service containers, and a separate job builds
the compose stack and drives a request through it end to end — signup, signin,
generate, fetch the image URL anonymously, delete, then confirm the object is gone.
That job exists because a compose file cannot be verified by reading it: a wrong
image tag, a healthcheck calling a binary the image does not ship, or an emulator
default that turns out to be load-bearing all look exactly like a working file.

### Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/healthz` | — | Liveness. Is this process running |
| `GET` | `/readyz` | — | Readiness. Can this process serve traffic |
| `POST` | `/signup` | — | Create an account |
| `POST` | `/signin` | — | Exchange credentials for a 24h JWT |
| `POST` | `/signout` | JWT | Revoke every token issued to the caller so far |
| `POST` | `/generate` | JWT | Generate an image from a prompt, store it, return the post |
| `POST` | `/upload` | JWT | Upload media and create a post |
| `GET` | `/search` | JWT | Search posts by `user` or `keywords`, filter by `type` |
| `DELETE` | `/post/{id}` | JWT | Delete one of your own posts and its stored media |

`GET /search` accepts `from` and `size` for paging, defaulting to `from=0` and
`size=50` with a ceiling of 200. Invalid values are clamped rather than
rejected. It also accepts `type=image` or `type=video`; an unrecognised value is
ignored. Filtering happens in Elasticsearch, so each gallery tab is its own
request rather than a client-side slice of one page.

`DELETE /post/{id}` only removes a post whose `user` matches the caller, and
returns 404 when nothing matches — a missing post and someone else's post are
indistinguishable, so the API does not confirm that another user's post id
exists. The media object is deleted before the index entry, because bucket
objects are world-readable and a leftover file would stay fetchable by URL.

### Errors

Every failure has the same shape:

```json
{
  "error": {
    "code": "rate_limited",
    "message": "Too many failed sign-in attempts, try again later",
    "request_id": "9f2c41a8bd3e5107"
  }
}
```

`code` is stable and meant to be branched on. `message` is prose and may be
reworded at any time, so matching on it is a bug waiting to happen. This replaced
35 `text/plain` responses — a JSON client could not parse them, and status codes
alone cannot separate "username taken" from "password too short", both 400.

Internal detail never reaches the client. A wrapped Elasticsearch error or an
OpenAI quota message goes to the log with the request id attached; the client gets
a generic message and the same id.

### Observability

Logs are JSON on stdout via `log/slog`, using the field names Cloud Logging reads
(`severity`, `message`, `timestamp`) rather than slog's defaults — with the
defaults, every entry arrives at severity "default" and filtering by severity in
the log viewer returns nothing.

Each request gets an id, taken from an inbound `X-Request-Id` when there is one so
it survives the hop from a load balancer, and generated otherwise. It is attached
to every log line the request produces, echoed in the response header, and
included in error bodies. That is what makes a user's report actionable: the id
they can see is the id in the log.

An inbound id is accepted only if it is alphanumeric with dashes and underscores,
at most 64 characters. An id containing a newline could forge log entries in a
line-oriented log, and an unbounded one could flood every line the request emits.

Access log level tracks the outcome: 5xx at error, 4xx at warn, the rest at info,
so a spike of 401s from a stale frontend does not look like an outage. Probes are
not logged; they run every few seconds forever.

`LOG_LEVEL=debug` adds per-request detail — rejected tokens, unparseable bodies,
stored objects. Useful locally, too noisy and too revealing for production.

### Health probes

Two endpoints, because they answer different questions and a single one cannot.

`/healthz` is liveness and checks nothing but itself. If it reported unhealthy
whenever Elasticsearch was unreachable, an orchestrator would respond to an
Elasticsearch outage by killing and restarting every instance — which does not
repair Elasticsearch, and turns a recoverable dependency blip into a restart loop.
Liveness asks "is this process wedged", and only a restart fixes that.

`/readyz` is readiness and pings Elasticsearch, answering 503 when it cannot be
reached. That takes the instance out of the load balancer's rotation without
killing it, so it rejoins by itself once the dependency recovers. 503 rather than
500 because it tells a load balancer to look elsewhere instead of retrying here.

### Lifecycle

The server traps `SIGTERM` and `SIGINT`, stops accepting connections, and gives
in-flight handlers 25 seconds to finish — under App Engine's own grace period, so
it has time to matter. Before this the process was killed outright, and a 32 MiB
upload became a connection reset with no status code to interpret.

Every outbound call takes a context derived from the request, so a client that
hangs up cancels the Elasticsearch query it was waiting on, and each call has its
own deadline (`backend/store/timeouts.go`). The bounds differ by two orders of
magnitude because a keyword lookup and a 32 MiB upload are not the same operation.

One deliberate exception: once `/generate` has called DALL-E, OpenAI has been
billed, so the GCS upload and index write that follow run under
`context.WithoutCancel`. A client hanging up during a 30-second wait is ordinary,
and cancelling there would spend the money and throw the image away.

### Authentication

Passwords are stored as bcrypt hashes; the plaintext is never persisted and
never part of a query. Sign-in looks the user up by document id, which is
realtime in Elasticsearch, so a freshly registered account can log in
immediately. `POST /signin` returns `{"token": "..."}`.

Signing out is enforced server-side. A JWT is self-contained, so clearing it in
the browser alone left it valid for the remainder of its 24 hours. `POST
/signout` records a `tokensValidAfter` timestamp on the user, and any token
issued before it is refused on the next request. The state lives in
Elasticsearch, so a sign-out applies across instances.

Failed sign-ins are throttled per username: 5 attempts per 15 minutes. Counters
live in Elasticsearch and are incremented by a Painless script, so the increment
is atomic and the limit is shared rather than per-instance.

Signups enforce uniqueness with a create-only write (`op_type=create`) rather than
a read followed by a write. Elasticsearch has no unique constraint, so the old
check-then-index left a window: measured against the reverted implementation, up
to 6 of 8 concurrent signups for the same name all "succeeded", and the last write
won — leaving the earlier registrants holding a password that had been replaced by
someone else's.

### Cost control

`/generate` is the only endpoint that spends money: DALL-E bills per image. It is
capped at 20 generations per user per rolling 24 hours, on the same Elasticsearch
counter as the login throttle but in its own index — sharing one would mean a
successful sign-in, which clears login failures, also handed back a fresh spending
allowance.

The count is incremented *before* the call, which is the opposite of the login
throttle. A failure occurring after OpenAI has been reached has still been billed,
so counting only successes would let a caller whose prompt reliably fails
downstream spend without limit. This over-charges by the number of genuine internal
failures, which is the cheaper mistake.

It is check-then-increment, so a simultaneous burst can overshoot by roughly the
concurrency. The increment itself is atomic, so the overshoot is bounded and small.
For a spending cap, "20, occasionally 22" is a different thing from "unbounded".

If the quota store is unreachable the request is refused, not allowed through. That
is also the opposite of the login throttle, which fails open: failing open on a
brute-force limiter still leaves the attacker facing the credential check, whereas
failing open on a spending limit turns an Elasticsearch outage into an unbounded
OpenAI bill.

### Media handling

Uploads are capped at 32 MiB and typed by inspecting their leading bytes, not by
filename extension. Accepted: JPEG, PNG, GIF, WebP, MP4, WebM, AVI, QuickTime,
FLV and WMV. The last three are detected from explicit container signatures,
since Go's `http.DetectContentType` has no entry for them.

Objects in the bucket are world-readable, so script-capable formats are refused
outright: an accepted `.svg` or `.html` would be stored XSS served from the
app's own storage domain.

### Migrating an existing deployment

The post index is versioned. The original mapping declared `type` with
`"index": false`, which makes the field unsearchable — Elasticsearch rejects a
filter on it with a 400 rather than returning nothing — and that parameter
cannot be changed on an existing field. So filtering by type server-side
required a new index.

After deploying, copy the old documents across:

```bash
cd backend
set -a && . ./.env && set +a
go run ./cmd/reindex
```

The values survive the copy because an unindexed field is still stored in
`_source`. The command refuses to write into a non-empty destination unless
given `-force`, and the server prints a prominent warning at startup while the
migration is outstanding, so an empty gallery is not mistaken for data loss.

Delete the legacy index once you are satisfied:

```bash
curl -XDELETE "$ES_URL/post" -u "$ES_USERNAME:$ES_PASSWORD"
```

## Frontend

```bash
cd frontend
npm install
cp .env.example .env.local   # then edit
npm start
```

```bash
npm run build             # production build
npm test                  # tests
```

Image generation calls the backend's `POST /generate`, which invokes DALL-E,
stores the result, and returns the saved post. The frontend holds no API key.

## Features

- User authentication (signup/login) with bcrypt-hashed passwords
- AI image generation via DALL-E, executed server-side
- Image upload and gallery management
- Search by keyword or user
- Responsive design

## Technologies

- **Frontend**: React 18, Material-UI, antd, Axios, React Router
- **Backend**: Go, gorilla/mux, Elasticsearch, Google Cloud Storage
- **Auth**: JWT (HS256), bcrypt
- **API**: RESTful endpoints

## Known gaps

- A revoked session is enforced with one Elasticsearch get per authenticated
  request. That is a sub-millisecond lookup at this scale; a short-lived cache
  would trade immediate revocation for throughput if it ever stops being cheap.
- `/signout` revokes every token for that user, not just the one presented.
  Signing out of one browser signs out of all of them.
- Search paging is offset-based (`from`/`size`), which degrades past roughly
  10,000 results. A `search_after` cursor would be needed beyond that.
- The DALL-E call itself has never been exercised in this repo's tests: it costs
  money per request, so `IMAGE_PROVIDER=stub` covers the pipeline around it and the
  call itself is checked by hand. The storage write, ACL and delete now run against
  an emulator in CI.
- Elasticsearch is the only datastore, including for users. It is not a relational
  database and does not pretend to be one: uniqueness needs `op_type=create`, a
  write is not immediately visible without `refresh=wait_for`, and there are no
  transactions. Those constraints are worked around explicitly rather than
  ignored, but a relational store for accounts would be the right answer at any
  real scale.

## License

MIT
