# Social AI

A full-stack application combining AI image generation with social sharing.

## Project Structure

```
.
├── social-ai/         # React frontend (Create React App)
│   ├── src/
│   ├── public/
│   ├── .env.example
│   └── package.json
│
├── socialai/          # Go backend (App Engine flexible)
│   ├── handler/       # HTTP handlers, routing, auth middleware
│   ├── service/       # business logic
│   ├── backend/       # Elasticsearch + Google Cloud Storage clients
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

**Backend** — copy `socialai/.env.example` to `socialai/.env` and fill it in.
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

**Frontend** — copy `social-ai/.env.example` to `social-ai/.env.local`. Only
non-secret values belong there: Create React App inlines every `REACT_APP_*`
variable into the production bundle, where anyone can read it.

## Backend (socialai)

Go 1.25+. Elasticsearch for users and posts, Google Cloud Storage for media.

```bash
cd socialai
cp .env.example .env      # then edit
set -a && . ./.env && set +a
go run .
```

```bash
go build ./...            # compile
go vet ./...              # static checks
go test ./...             # tests
```

### Endpoints

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/signup` | — | Create an account |
| `POST` | `/signin` | — | Exchange credentials for a 24h JWT |
| `POST` | `/generate` | JWT | Generate an image from a prompt, store it, return the post |
| `POST` | `/upload` | JWT | Upload media and create a post |
| `GET` | `/search` | JWT | Search posts by `user` or `keywords` |
| `DELETE` | `/post/{id}` | JWT | Delete one of your own posts and its stored media |

`GET /search` accepts `from` and `size` for paging, defaulting to `from=0` and
`size=50` with a ceiling of 200. Invalid values are clamped rather than
rejected. Leaving `size` unset previously fell through to Elasticsearch's
default of 10, which silently truncated every result set.

`DELETE /post/{id}` only removes a post whose `user` matches the caller, and
returns 404 when nothing matches — a missing post and someone else's post are
indistinguishable, so the API does not confirm that another user's post id
exists. The media object is deleted before the index entry, because bucket
objects are world-readable and a leftover file would stay fetchable by URL.

### Authentication

Passwords are stored as bcrypt hashes; the plaintext is never persisted and
never part of a query. Sign-in looks the user up by username and compares the
hash in application code.

Failed sign-ins are throttled per username (5 attempts per 15 minutes). The
counter is in process memory, so with more than one instance the effective limit
is per-instance — move it to a shared store if the instance count grows.

### Media handling

Uploads are capped at 32 MiB and typed by inspecting their leading bytes, not by
filename extension. Only JPEG, PNG, GIF, WebP, MP4, WebM and AVI are accepted.

Objects in the bucket are world-readable, so script-capable formats are refused
outright: an accepted `.svg` or `.html` would be stored XSS served from the
app's own storage domain. Note this is stricter than before — QuickTime `.mov`,
`.flv` and `.wmv` are no longer accepted, because Go's content sniffer cannot
positively identify them.

## Frontend (social-ai)

```bash
cd social-ai
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

Tracked but not yet addressed:

- Type tabs filter client side within one page. The `type` field is mapped
  `"index": false`, so Elasticsearch cannot filter on it; making the tabs
  server-side paged requires indexing that field and reindexing existing posts.
  With the default page size of 50 this is no longer visible in practice, but it
  is not correct for large collections.
- Logout and the 401 handler both clear the token client side. The JWT itself
  stays valid for the remainder of its 24 hours, because there is no server-side
  revocation list.
- The sign-in throttle counts attempts in process memory, so with more than one
  instance the effective limit is per-instance.
- QuickTime `.mov`, `.flv` and `.wmv` uploads are rejected: Go's content sniffer
  cannot positively identify them, and the filename extension is not trusted.
  Supporting them needs explicit ftyp brand parsing.
- No CI. `go build ./...`, `go vet ./...`, `go test ./...`, `npm ci` and
  `npm test` all pass locally and would make a reasonable first workflow.

## License

MIT
