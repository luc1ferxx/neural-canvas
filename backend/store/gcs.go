package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/logging"

	"cloud.google.com/go/storage"
)

var (
	GCSBackend *GoogleCloudStorageBackend
)

type GoogleCloudStorageBackend struct {
	client *storage.Client
	bucket string
	// emulated records that STORAGE_EMULATOR_HOST was set when the client was
	// built, which changes what operations are available.
	emulated bool
}

// emulatorHostEnv is Google's own variable for pointing a client at a local
// implementation. The client library honours it directly, which is why nothing
// here has to special-case an endpoint.
const emulatorHostEnv = "STORAGE_EMULATOR_HOST"

// InitGCSBackend returns an error rather than panicking so startup failures are
// reportable.
func InitGCSBackend(ctx context.Context) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create GCS client: %w", err)
	}

	emulated := os.Getenv(emulatorHostEnv) != ""
	if emulated {
		slog.Warn("using a storage emulator; stored objects are not durable",
			slog.String("host", os.Getenv(emulatorHostEnv)))
	}

	GCSBackend = &GoogleCloudStorageBackend{
		client:   client,
		bucket:   config.C.GCSBucket,
		emulated: emulated,
	}

	if emulated {
		// Only against an emulator. In a real project the bucket is created out of
		// band, with lifecycle rules, versioning and IAM that belong in
		// infrastructure config rather than in application startup -- and a server
		// that creates its own bucket needs permission to create buckets, which is
		// far more than it should hold. Against an emulator there is nothing to
		// manage and nothing to protect, and creating it here is what lets the
		// compose stack come up with one command instead of a documented curl.
		if err := GCSBackend.ensureBucket(ctx); err != nil {
			return err
		}
	}

	return nil
}

// ensureBucket creates the configured bucket if it is absent.
func (backend *GoogleCloudStorageBackend) ensureBucket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, gcsDeleteTimeout)
	defer cancel()

	bucket := backend.client.Bucket(backend.bucket)
	if _, err := bucket.Attrs(ctx); err == nil {
		return nil
	} else if !errors.Is(err, storage.ErrBucketNotExist) {
		return fmt.Errorf("check bucket %q: %w", backend.bucket, err)
	}

	// projectID is ignored by the emulator; it is required by the signature.
	if err := bucket.Create(ctx, "local-dev", nil); err != nil {
		return fmt.Errorf("create bucket %q: %w", backend.bucket, err)
	}
	slog.Info("created emulator bucket", slog.String("bucket", backend.bucket))
	return nil
}

// SaveToGCS stores r and returns its media link.
//
// contentType must be a value already validated against the media allowlist.
// Setting it explicitly matters. Leaving it empty does not leave the object
// untyped: the client library sniffs one with net/http.DetectContentType and
// stores that instead. So the choice is not "typed or untyped", it is "the type
// that passed validation" versus "a second, independent guess at the type" --
// and the browser would be served the guess. Two guesses that can disagree about
// the same bytes is the confusion the upload-side validation exists to remove.
//
// Objects are left world-readable: these are images meant to be shared, and the
// frontend renders them directly from this URL.
func (backend *GoogleCloudStorageBackend) SaveToGCS(ctx context.Context, r io.Reader, objectName, contentType string) (string, error) {
	// Derived from the caller's context, so a client that hangs up mid-upload
	// abandons the transfer instead of streaming the rest into the bucket on its
	// behalf. Returning without Close never finalizes the object, and cancel
	// releases the stream.
	ctx, cancel := context.WithTimeout(ctx, gcsUploadTimeout)
	defer cancel()

	object := backend.client.Bucket(backend.bucket).Object(objectName)

	wc := object.NewWriter(ctx)
	wc.ContentType = contentType
	wc.CacheControl = "public, max-age=31536000"

	if _, err := io.Copy(wc, r); err != nil {
		return "", fmt.Errorf("write object %q: %w", objectName, err)
	}

	if err := wc.Close(); err != nil {
		return "", fmt.Errorf("finalize object %q: %w", objectName, err)
	}

	// Runs against the emulator too. This started out guarded by the emulated flag
	// on the assumption that a local implementation would have no IAM to set --
	// but fake-gcs-server implements object ACLs, and a probe confirmed the rule
	// really is stored as allUsers/READER. Skipping it would have left the one
	// permission the frontend depends on as the only step the offline stack could
	// not check, in exchange for a branch that existed to dodge an error that
	// never happened. The failure is fatal because an object that silently stayed
	// private renders as a broken image with nothing logged anywhere.
	if err := object.ACL().Set(ctx, storage.AllUsers, storage.RoleReader); err != nil {
		return "", fmt.Errorf("set public read on %q: %w", objectName, err)
	}

	attrs, err := object.Attrs(ctx)
	if err != nil {
		return "", fmt.Errorf("read attrs of %q: %w", objectName, err)
	}

	// Debug, not info: the handler already logs one line per created post with
	// the id. At info this doubled every upload's output for no extra fact.
	logging.FromContext(ctx).Debug("object stored",
		slog.String("object", objectName), slog.String("content_type", contentType))
	return attrs.MediaLink, nil
}

// DeleteFromGCS removes an object. A missing object is not an error: the caller
// is deleting a post, and an already-absent file means that goal is met.
//
// This matters for the delete flow: every object is world-readable, so leaving
// the file behind would keep a "deleted" image publicly fetchable by its URL.
func (backend *GoogleCloudStorageBackend) DeleteFromGCS(ctx context.Context, objectName string) error {
	ctx, cancel := context.WithTimeout(ctx, gcsDeleteTimeout)
	defer cancel()

	err := backend.client.Bucket(backend.bucket).Object(objectName).Delete(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete object %q: %w", objectName, err)
	}
	return nil
}
