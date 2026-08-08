package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Config holds every value that used to be a hardcoded constant. Nothing in
// here may be committed: Load reads it from the environment and refuses to
// start if anything required is missing.
type Config struct {
	ESURL      string
	ESUsername string
	ESPassword string

	GCSBucket string

	JWTSecret []byte
	OpenAIKey string

	AllowedOrigins []string
	Port           string

	// LogLevel is the minimum severity that gets emitted. Defaults to info.
	LogLevel slog.Level
}

// C is the process-wide config, valid only after a successful Load.
var C Config

// minJWTSecretLen is 32 bytes because the tokens are HS256; a short secret is
// brute-forceable offline from a single captured token.
const minJWTSecretLen = 32

// Load populates C from the environment. It returns a single error listing
// everything that is wrong so a misconfigured deploy fails once, loudly,
// instead of panicking later on the first request.
func Load() error {
	var missing []string
	require := func(key string) string {
		v := strings.TrimSpace(os.Getenv(key))
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	C.ESURL = require("ES_URL")
	C.ESUsername = require("ES_USERNAME")
	C.ESPassword = require("ES_PASSWORD")
	C.GCSBucket = require("GCS_BUCKET")
	C.OpenAIKey = require("OPENAI_API_KEY")
	secret := require("JWT_SECRET")

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s (see .env.example)",
			strings.Join(missing, ", "))
	}

	if len(secret) < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters, got %d",
			minJWTSecretLen, len(secret))
	}
	C.JWTSecret = []byte(secret)

	if !strings.HasPrefix(C.ESURL, "https://") {
		// Basic auth over plain HTTP puts the Elasticsearch password on the
		// wire in cleartext. Allowed for local development only.
		fmt.Fprintf(os.Stderr,
			"WARNING: ES_URL is not https, Elasticsearch credentials will be sent in cleartext (%s)\n",
			C.ESURL)
	}

	C.Port = strings.TrimSpace(os.Getenv("PORT"))
	if C.Port == "" {
		C.Port = "8080"
	}

	origins := strings.TrimSpace(os.Getenv("ALLOWED_ORIGINS"))
	if origins == "" {
		origins = "http://localhost:3000"
	}
	for _, o := range strings.Split(origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			C.AllowedOrigins = append(C.AllowedOrigins, o)
		}
	}
	for _, o := range C.AllowedOrigins {
		if o == "*" {
			return fmt.Errorf(`ALLOWED_ORIGINS must not contain "*": these endpoints are authenticated, ` +
				`list your frontend origins explicitly`)
		}
	}

	// slog.Level parses its own text, so "debug", "info", "warn" and "error" are
	// accepted without a lookup table here. An unrecognised value is rejected
	// rather than silently defaulting, because a typo would otherwise be
	// discovered only by noticing that expected log lines never appear.
	C.LogLevel = slog.LevelInfo
	if raw := strings.TrimSpace(os.Getenv("LOG_LEVEL")); raw != "" {
		if err := C.LogLevel.UnmarshalText([]byte(raw)); err != nil {
			return fmt.Errorf("LOG_LEVEL %q is not one of debug, info, warn, error", raw)
		}
	}

	return nil
}
