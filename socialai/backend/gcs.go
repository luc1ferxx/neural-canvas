package backend

import (
	"context"
	"fmt"
	"io"

	"socialai/config"

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
func InitGCSBackend() error {
	client, err := storage.NewClient(context.Background())
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
func (backend *GoogleCloudStorageBackend) SaveToGCS(r io.Reader, objectName, contentType string) (string, error) {
	// A cancelable context is how an in-flight upload is abandoned: returning
	// without Close never finalizes the object, and cancel releases the stream.
	ctx, cancel := context.WithCancel(context.Background())
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

	fmt.Printf("File is saved to GCS: %s\n", attrs.MediaLink)
	return attrs.MediaLink, nil
}

// DeleteFromGCS removes an object. A missing object is not an error: the caller
// is deleting a post, and an already-absent file means that goal is met.
//
// This matters for the delete flow: every object is world-readable, so leaving
// the file behind would keep a "deleted" image publicly fetchable by its URL.
func (backend *GoogleCloudStorageBackend) DeleteFromGCS(objectName string) error {
	ctx := context.Background()

	err := backend.client.Bucket(backend.bucket).Object(objectName).Delete(ctx)
	if err == storage.ErrObjectNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete object %q: %w", objectName, err)
	}
	return nil
}
