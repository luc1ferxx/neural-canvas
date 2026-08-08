# Social AI

A full-stack application combining AI image generation with social sharing.

## Project Structure

```
.
├── frontend/          # React frontend (Create React App)
│   ├── src/
│   ├── public/
│   ├── .env.example
│   └── package.json
│
├── backend/           # Go backend (App Engine flexible)
│   ├── handler/       # HTTP handlers, routing, auth middleware
│   ├── service/       # business logic
│   ├── store/         # Elasticsearch + Google Cloud Storage clients
│   ├── media/         # upload content-type validation
│   ├── config/        # environment-driven configuration
│   ├── .env.example
│   └── go.mod
│
└── README.md
```

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
| `OPENAI_API_KEY` | Server-side only, never exposed to the browser |
| `ALLOWED_ORIGINS` | Comma-separated frontend origins; `*` is rejected |
| `PORT` | Optional, App Engine sets it; defaults to 8080 |

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
errors compared with `==` rather than `errors.Is` — including one in the GCS
delete path, where a wrapped `ErrObjectNotExist` would have made an
already-deleted object look like a failure and left the post undeletable — plus
a server started without a header-read timeout. Install it with:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2
```

Integration tests run against a real Elasticsearch and skip unless `ES_TEST_URL`
is set. They cover what compiling cannot: that the mappings are accepted, that
the throttle script increments rather than overwrites, that an unindexed field
genuinely cannot be filtered on, and that the reindex preserves fields.

```bash
ES_TEST_URL=http://127.0.0.1:9200 go test -run Integration -v ./...
```

CI runs them with an Elasticsearch service container.

### Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
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

### Authentication

Passwords are stored as bcrypt hashes; the plaintext is never persisted and
never part of a query. Sign-in looks the user up by document id, which is
realtime in Elasticsearch, so a freshly registered account can log in
immediately.

Signing out is enforced server-side. A JWT is self-contained, so clearing it in
the browser alone left it valid for the remainder of its 24 hours. `POST
/signout` records a `tokensValidAfter` timestamp on the user, and any token
issued before it is refused on the next request. The state lives in
Elasticsearch, so a sign-out applies across instances.

Failed sign-ins are throttled per username: 5 attempts per 15 minutes. Counters
live in Elasticsearch and are incremented by a Painless script, so the increment
is atomic and the limit is shared rather than per-instance.

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
- The GCS write, the DALL-E call and the delete of a stored object have never run
  against live services here: they need real credentials. Everything else is
  covered by the unit and integration suites.

## License

MIT
