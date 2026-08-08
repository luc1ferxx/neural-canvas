package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

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
}

// InitGCSBackend returns an error rather than panicking so startup failures are
// reportable.
func InitGCSBackend(ctx context.Context) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("create GCS client: %w", err)
	}

	GCSBackend = &GoogleCloudStorageBackend{
		client: client,
		bucket: config.C.GCSBucket,
	}
	return nil
}

// SaveToGCS stores r and returns its media link.
//
// contentType must be a value already validated against the media allowlist.
// Setting it explicitly matters: if the object has no content type, GCS sniffs
// one at serve time, which reintroduces exactly the confusion the upload-side
// validation exists to prevent.
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
