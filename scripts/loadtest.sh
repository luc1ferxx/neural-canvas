#!/usr/bin/env bash
#
# Load test the read and authentication paths with vegeta.
#
# One script, two callers, deliberately. Locally it produces the numbers quoted
# in the README, at a rate a laptop can actually drive. In CI it runs against the
# docker compose stack as a regression gate at a much lower rate. Two scripts
# would drift, and then the number in the README would stop describing the thing
# CI protects.
#
# What is measured, and why these three:
#
#   /healthz   No auth, no I/O, no allocation to speak of. It is the floor: HTTP
#              parsing, routing, middleware, metrics recording. Any latency the
#              other scenarios show above this is work, not overhead.
#   /search    The real read path, and the one the frontend calls on every page
#              load: JWT signature check, then a per-request Elasticsearch get to
#              see whether the session was revoked, then the search query itself.
#              Two Elasticsearch round trips per request.
#   /signin    bcrypt. Expected to be orders of magnitude slower than everything
#              else, and expected to stay that way -- that cost is the entire
#              point of a password hash. Measured so the number is known rather
#              than assumed, and driven at a low rate because it is CPU-bound and
#              saturating it would only measure the queue.
#
# /generate is not load tested. It is capped at 20 per user per day by the quota,
# so a sustained rate against it measures the quota rejection path rather than
# the generation path -- and against the real provider it would spend money.
#
# Thresholds: the success ratio is asserted strictly, because a dropped
# connection or a 5xx under load is a real defect at any rate. Latency is
# asserted loosely and only as a ceiling, because a shared CI runner's timings
# are not comparable to a laptop's and a tight bound there would fail for
# reasons that have nothing to do with the code.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
METRICS_URL="${METRICS_URL:-http://localhost:9090/metrics}"
OUT_DIR="${OUT_DIR:-./loadtest-results}"

# Defaults are the local ones -- the rates the README's numbers were taken at, on
# the hardware the README names. CI overrides all of them downward: a shared
# two-core runner cannot drive these, and a rate the runner cannot generate
# measures the load generator rather than the service.
#
# 3000/s for the two read scenarios is deliberately below the measured saturation
# point (~8500/s on that laptop, collapsing at 12800/s) so the reported latency is
# a service's, not a queue's.
DURATION="${DURATION:-30s}"
HEALTH_RATE="${HEALTH_RATE:-3000}"
SEARCH_RATE="${SEARCH_RATE:-3000}"
# bcrypt is CPU-bound by design, so this is driven at a rate that measures the
# hash cost rather than the depth of the queue in front of it.
SIGNIN_RATE="${SIGNIN_RATE:-10}"
# Enough that the query matches a real result set. Still small: these numbers
# describe an index of tens of documents, and say nothing about how the query
# behaves over millions.
SEED_POSTS="${SEED_POSTS:-30}"

# Ceilings, not targets. See the note above.
MAX_P99_MS="${MAX_P99_MS:-2000}"
MAX_SIGNIN_P99_MS="${MAX_SIGNIN_P99_MS:-5000}"

fail() { echo "::error::$*" >&2; exit 1; }
note() { echo "==> $*"; }

for tool in vegeta jq curl; do
  command -v "$tool" >/dev/null || fail "$tool is not installed"
done

mkdir -p "$OUT_DIR"

note "waiting for $BASE_URL to be ready"
ready=""
for _ in $(seq 1 60); do
  # || true because a refused connection exits non-zero, and under set -e that
  # would end the run with curl's exit code and no explanation.
  if [ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/readyz" || true)" = "200" ]; then
    ready=yes
    break
  fi
  sleep 1
done
[ -n "$ready" ] || fail "$BASE_URL/readyz never returned 200"

# ---------------------------------------------------------------------------
# Fixture: a user, a token, and enough posts that /search returns a real result
# set rather than an empty one.
# ---------------------------------------------------------------------------

user="load-$(date +%s)-$$"
pass="load-test-password"

note "creating the fixture user"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/signup" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$user\",\"password\":\"$pass\",\"age\":30,\"gender\":\"other\"}" || true)
[ "$code" = "200" ] || fail "signup returned $code"

# The signin body is written to a file because vegeta reads request bodies from
# disk, and the same file is reused for the signin scenario below.
printf '{"username":"%s","password":"%s"}' "$user" "$pass" > "$OUT_DIR/signin-body.json"

# token.json, not signin.json: the signin scenario below writes its vegeta report
# to "$OUT_DIR/$name.json", so naming this after the endpoint would have it
# silently overwritten partway through the run. It cost a confusing debugging
# session -- a token read back out of the file afterwards was the string "null",
# because by then the file was a latency report.
code=$(curl -s -o "$OUT_DIR/token.json" -w '%{http_code}' -X POST "$BASE_URL/signin" \
  -H 'Content-Type: application/json' -d @"$OUT_DIR/signin-body.json" || true)
[ "$code" = "200" ] || fail "signin returned $code"
token=$(jq -r .token "$OUT_DIR/token.json")
[ -n "$token" ] && [ "$token" != "null" ] || fail "no token in the signin response"

# A 1x1 PNG. Uploading is how the index gets seeded: /upload has no quota, and it
# exercises the same storage-write and index-write path a generation would.
printf '%s' \
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFAAH/q842iQAAAABJRU5ErkJggg==' \
  | base64 --decode > "$OUT_DIR/seed.png"

note "seeding $SEED_POSTS posts"
for i in $(seq 1 "$SEED_POSTS"); do
  code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/upload" \
    -H "Authorization: Bearer $token" \
    -F "media_file=@$OUT_DIR/seed.png;type=image/png" \
    -F "message=a lighthouse in a storm number $i" || true)
  [ "$code" = "200" ] || fail "seed upload $i returned $code"
done

# Elasticsearch indexes asynchronously: a document is durable when the write
# returns but is not searchable until the next refresh, which is once per second
# by default. Without this the search scenario would run against an index that
# does not have the fixtures in it yet, and would report the latency of returning
# nothing.
note "waiting for the index to refresh"
sleep 2
found=$(curl -s -H "Authorization: Bearer $token" \
  "$BASE_URL/search?keywords=lighthouse&size=50" | jq 'length')
[ "$found" -gt 0 ] || fail "the seeded posts are not searchable (search returned $found)"
note "search returns $found posts"

# ---------------------------------------------------------------------------
# Scenarios
# ---------------------------------------------------------------------------

printf 'GET %s/healthz\n' "$BASE_URL" > "$OUT_DIR/healthz.targets"

cat > "$OUT_DIR/search.targets" <<EOF
GET $BASE_URL/search?keywords=lighthouse&size=10
Authorization: Bearer $token
EOF

cat > "$OUT_DIR/signin.targets" <<EOF
POST $BASE_URL/signin
Content-Type: application/json
@$OUT_DIR/signin-body.json
EOF

# ms extracts a nanosecond latency from vegeta's JSON report as milliseconds with
# one decimal. jq is doing the arithmetic rather than bash, which has no floats.
# Bracket rather than dot notation because vegeta's percentile keys start with a
# digit ("99th"), which jq will not accept after a dot.
ms() { jq -r "(.latencies[\"$1\"] / 1000000 * 10 | round / 10)" "$2"; }

run_scenario() {
  local name="$1" rate="$2" ceiling_ms="$3"

  note "$name: ${rate}/s for $DURATION"
  vegeta attack \
    -targets="$OUT_DIR/$name.targets" \
    -rate="$rate/1s" \
    -duration="$DURATION" \
    -timeout=30s \
    > "$OUT_DIR/$name.bin"
  vegeta report -type=json "$OUT_DIR/$name.bin" > "$OUT_DIR/$name.json"

  local success p99 requests throughput
  success=$(jq -r .success "$OUT_DIR/$name.json")
  requests=$(jq -r .requests "$OUT_DIR/$name.json")
  throughput=$(jq -r '(.throughput * 10 | round / 10)' "$OUT_DIR/$name.json")
  p99=$(ms 99th "$OUT_DIR/$name.json")

  printf '    requests   %s\n' "$requests"
  printf '    throughput %s/s\n' "$throughput"
  printf '    success    %s\n' "$success"
  printf '    mean       %s ms\n' "$(ms mean "$OUT_DIR/$name.json")"
  printf '    p50        %s ms\n' "$(ms 50th "$OUT_DIR/$name.json")"
  printf '    p95        %s ms\n' "$(ms 95th "$OUT_DIR/$name.json")"
  printf '    p99        %s ms\n' "$p99"
  printf '    max        %s ms\n' "$(ms max "$OUT_DIR/$name.json")"
  printf '    statuses   %s\n' "$(jq -c .status_codes "$OUT_DIR/$name.json")"

  # Strict: anything other than every request succeeding is a defect, whatever
  # the rate was.
  if [ "$success" != "1" ]; then
    jq -r '.errors[]?' "$OUT_DIR/$name.json" | sort -u | head -5 | sed 's/^/    /'
    fail "$name: success ratio is $success, want 1 (see the statuses and errors above)"
  fi

  # Loose: a ceiling that only a real collapse crosses.
  awk -v got="$p99" -v want="$ceiling_ms" 'BEGIN { exit !(got <= want) }' \
    || fail "$name: p99 is ${p99}ms, over the ${ceiling_ms}ms ceiling"
}

run_scenario healthz "$HEALTH_RATE" "$MAX_P99_MS"
run_scenario search "$SEARCH_RATE" "$MAX_P99_MS"
run_scenario signin "$SIGNIN_RATE" "$MAX_SIGNIN_P99_MS"

# ---------------------------------------------------------------------------
# A markdown table, so the README's numbers are copied from a run rather than
# typed from memory.
# ---------------------------------------------------------------------------

{
  echo "| Scenario | Rate | Requests | Success | p50 | p95 | p99 | max |"
  echo "|---|---|---|---|---|---|---|---|"
  for name in healthz search signin; do
    f="$OUT_DIR/$name.json"
    printf '| `%s` | %s/s | %s | %s%% | %s ms | %s ms | %s ms | %s ms |\n' \
      "$name" \
      "$(jq -r '(.rate | round)' "$f")" \
      "$(jq -r .requests "$f")" \
      "$(jq -r '(.success * 100 | round)' "$f")" \
      "$(ms 50th "$f")" "$(ms 95th "$f")" "$(ms 99th "$f")" "$(ms max "$f")"
  done
} | tee "$OUT_DIR/summary.md"

# The metrics the service reported about itself during the run, next to what the
# client measured. Disagreement between the two is worth seeing: it means either
# the instrumentation or the load generator is not measuring what it claims.
if metrics=$(curl -s --fail "$METRICS_URL" 2>/dev/null); then
  echo
  note "what the service recorded about itself"
  echo "$metrics" | awk '
    /^http_request_duration_seconds_count[{]/ { print "    " $0 }
    /^http_requests_in_flight /               { print "    " $0 }
  ' | sort
fi

note "results in $OUT_DIR"
