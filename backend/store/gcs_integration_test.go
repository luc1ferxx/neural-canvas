package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/luc1ferxx/neural-canvas/backend/config"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// These run against a GCS emulator (fsouza/fake-gcs-server) and are skipped
// unless GCS_TEST_EMULATOR is set:
//
//	GCS_TEST_EMULATOR=localhost:4443 go test -run Integration ./store/...
//
// They exist because the storage write and delete were the only paths in this
// project that had never been executed against anything at all -- not a unit
// test, not an integration test, not a manual run. Running them found that the
// sentinel comparison a linter had flagged in DeleteFromGCS was not a latent
// risk but a live bug: the client returns ErrObjectNotExist wrapped, in a
// *fmt.wrapErrors, so the old err == storage.ErrObjectNotExist was never true and
// deleting a post whose image was already gone failed every time.
//
// They live in package store, not package service, so that verifying what was
// actually stored can use the private client instead of forcing a
// production-facing accessor to exist purely for tests.
//
// Every assertion here was mutation-tested: the corresponding line in gcs.go was
// broken on purpose and the test had to go red. Three mutations survived the first
// version of this file. The content-type test was satisfied by the client library
// sniffing the same answer it had been asked to assert. The bucket-creation test
// passed on a bucket it had not created -- first one an earlier test in the same
// run had created, then, once that was fixed, the previous run's leftover, because
// the emulator outlives the test binary. And the public-read ACL had no assertion
// at all, because the code was skipping it whenever it was talking to an emulator.
// Each is addressed below, and each is the reason for the comment above it rather
// than a claim of coverage.

// setupGCS points the storage client at the emulator and gives the calling test
// its own bucket, deleted first.
//
// Both halves of that exist to stop a test passing on a bucket it did not create.
// A shared bucket is created once, by whichever test happens to run first, and
// every later test -- including the one asserting that startup creates it -- then
// finds it already there. And because the emulator is a long-lived process shared
// by every run against it, even a per-test bucket survives into the next run, so
// deleting it up front is what makes the second run as meaningful as the first.
//
// This is not theorised. The mutation that removes bucket creation entirely stayed
// green until both halves were in place.
func setupGCS(t *testing.T) context.Context {
	t.Helper()

	host := os.Getenv("GCS_TEST_EMULATOR")
	if host == "" {
		t.Skip("GCS_TEST_EMULATOR not set; skipping storage integration tests")
	}

	bucket := bucketNameFor(t.Name())

	t.Setenv("STORAGE_EMULATOR_HOST", host)
	t.Setenv("ES_URL", "http://127.0.0.1:9200")
	t.Setenv("ES_USERNAME", "test")
	t.Setenv("ES_PASSWORD", "test")
	t.Setenv("GCS_BUCKET", bucket)
	t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")
	t.Setenv("IMAGE_PROVIDER", config.ProviderStub)
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")

	if err := config.Load(); err != nil {
		t.Fatalf("config.Load(): %v", err)
	}

	ctx := t.Context()
	dropBucket(ctx, t, bucket)

	if err := InitGCSBackend(ctx); err != nil {
		t.Fatalf("InitGCSBackend(): %v", err)
	}
	return ctx
}

// dropBucket removes a bucket and everything in it, using its own client so it
// runs before the code under test is initialised. A bucket that is not there is
// the desired state, not a failure.
func dropBucket(ctx context.Context, t *testing.T, name string) {
	t.Helper()

	client, err := storage.NewClient(ctx)
	if err != nil {
		t.Fatalf("storage.NewClient(): %v", err)
	}
	defer func() { _ = client.Close() }()

	bucket := client.Bucket(name)

	it := bucket.Objects(ctx, nil)
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			if errors.Is(err, storage.ErrBucketNotExist) {
				return
			}
			t.Fatalf("list objects in %q: %v", name, err)
		}
		if err := bucket.Object(attrs.Name).Delete(ctx); err != nil &&
			!errors.Is(err, storage.ErrObjectNotExist) {
			t.Fatalf("delete leftover object %q: %v", attrs.Name, err)
		}
	}

	if err := bucket.Delete(ctx); err != nil && !errors.Is(err, storage.ErrBucketNotExist) {
		t.Fatalf("delete bucket %q: %v", name, err)
	}
}

// bucketNameFor turns a test name into a legal bucket name: lowercase, and only
// letters, digits and dashes.
func bucketNameFor(testName string) string {
	var b strings.Builder
	b.WriteString("nc-")
	for _, r := range strings.ToLower(testName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return b.String()
}

// readObject fetches an object's bytes and content type straight from storage,
// bypassing the code under test.
func readObject(ctx context.Context, t *testing.T, name string) ([]byte, string) {
	t.Helper()

	object := GCSBackend.client.Bucket(GCSBackend.bucket).Object(name)
	attrs, err := object.Attrs(ctx)
	if err != nil {
		t.Fatalf("Attrs(%q): %v", name, err)
	}

	reader, err := object.NewReader(ctx)
	if err != nil {
		t.Fatalf("NewReader(%q): %v", name, err)
	}
	defer func() { _ = reader.Close() }()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read %q: %v", name, err)
	}
	return data, attrs.ContentType
}

// TestIntegrationSaveToGCSStoresTheBytes covers the upload itself.
func TestIntegrationSaveToGCSStoresTheBytes(t *testing.T) {
	ctx := setupGCS(t)

	// A real PNG header, so this is not merely storing arbitrary bytes.
	content := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0x42}, 512)...)
	name := "upload-object"

	link, err := GCSBackend.SaveToGCS(ctx, bytes.NewReader(content), name, "image/png")
	if err != nil {
		t.Fatalf("SaveToGCS(): %v", err)
	}
	if link == "" {
		t.Error("SaveToGCS returned an empty media link; the post would have no image URL")
	}

	stored, _ := readObject(ctx, t, name)
	if !bytes.Equal(stored, content) {
		t.Errorf("stored %d bytes, wrote %d: the object was truncated or altered",
			len(stored), len(content))
	}
}

// TestIntegrationSaveToGCSDeclaresTheValidatedType checks that the content type
// stored is the one the caller passed.
//
// The payload is deliberately bytes that sniff as something else. Asserting
// image/png on PNG bytes proves nothing: the client library sniffs the type when
// none is declared, so it would answer image/png whether or not SaveToGCS ever
// set it -- which is exactly how the first version of this test passed with the
// assignment deleted. Declaring a type the content would never be guessed as is
// the only way to tell "we stored the validated type" apart from "something
// guessed the same answer".
func TestIntegrationSaveToGCSDeclaresTheValidatedType(t *testing.T) {
	ctx := setupGCS(t)

	// DetectContentType calls a run of NUL bytes application/octet-stream.
	content := bytes.Repeat([]byte{0x00}, 1024)
	name := "declared-type-object"

	if _, err := GCSBackend.SaveToGCS(
		ctx, bytes.NewReader(content), name, "image/png"); err != nil {
		t.Fatalf("SaveToGCS(): %v", err)
	}

	_, contentType := readObject(ctx, t, name)
	if contentType != "image/png" {
		t.Errorf("content type = %q, want image/png: the stored type is a sniff of the "+
			"bytes rather than the type that passed validation", contentType)
	}
}

// TestIntegrationSaveToGCSMakesTheObjectPublic covers the ACL.
//
// This is the permission the whole gallery depends on: the frontend renders every
// image straight from its bucket URL with no credentials, so an object that
// stayed private is a broken image with a 200-shaped API response behind it.
func TestIntegrationSaveToGCSMakesTheObjectPublic(t *testing.T) {
	ctx := setupGCS(t)

	name := "public-object"
	if _, err := GCSBackend.SaveToGCS(
		ctx, strings.NewReader("payload"), name, "image/png"); err != nil {
		t.Fatalf("SaveToGCS(): %v", err)
	}

	rules, err := GCSBackend.client.Bucket(GCSBackend.bucket).Object(name).ACL().List(ctx)
	if err != nil {
		t.Fatalf("ACL().List(): %v", err)
	}

	for _, rule := range rules {
		if rule.Entity == storage.AllUsers && rule.Role == storage.RoleReader {
			return
		}
	}
	t.Errorf("no allUsers/READER rule in %v: the image would not load in a browser", rules)
}

// TestIntegrationSaveToGCSOverwritesCleanly checks a repeated object name does not
// leave a mix of the two payloads.
func TestIntegrationSaveToGCSOverwritesCleanly(t *testing.T) {
	ctx := setupGCS(t)

	name := "overwrite-object"
	long := bytes.Repeat([]byte("A"), 4096)
	short := []byte("B")

	if _, err := GCSBackend.SaveToGCS(ctx, bytes.NewReader(long), name, "image/png"); err != nil {
		t.Fatalf("first SaveToGCS(): %v", err)
	}
	if _, err := GCSBackend.SaveToGCS(ctx, bytes.NewReader(short), name, "image/png"); err != nil {
		t.Fatalf("second SaveToGCS(): %v", err)
	}

	stored, _ := readObject(ctx, t, name)
	if !bytes.Equal(stored, short) {
		t.Errorf("stored %d bytes, want %d: the second write did not replace the first",
			len(stored), len(short))
	}
}

// TestIntegrationDeleteFromGCSRemovesTheObject is the half that matters most.
//
// Objects are world-readable, so a delete that reported success without removing
// the file would leave a "deleted" image permanently fetchable by anyone holding
// the URL -- and the API would have told the user it was gone.
func TestIntegrationDeleteFromGCSRemovesTheObject(t *testing.T) {
	ctx := setupGCS(t)

	name := "delete-object"
	if _, err := GCSBackend.SaveToGCS(
		ctx, strings.NewReader("payload"), name, "image/png"); err != nil {
		t.Fatalf("SaveToGCS(): %v", err)
	}

	// Confirm it is really there first; otherwise the assertion below would pass
	// against a delete that does nothing.
	object := GCSBackend.client.Bucket(GCSBackend.bucket).Object(name)
	if _, err := object.Attrs(ctx); err != nil {
		t.Fatalf("the object was not stored: %v", err)
	}

	if err := GCSBackend.DeleteFromGCS(ctx, name); err != nil {
		t.Fatalf("DeleteFromGCS(): %v", err)
	}

	if _, err := object.Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("after delete, Attrs() = %v, want ErrObjectNotExist: the object is "+
			"still fetchable by URL", err)
	}
}

// TestIntegrationDeleteMissingObjectIsNotAnError is the behaviour that makes
// deleting a post idempotent, and the one the == comparison really was breaking:
// the client wraps its sentinel, so that comparison never matched and this case
// returned an error to a caller whose goal was already met.
func TestIntegrationDeleteMissingObjectIsNotAnError(t *testing.T) {
	ctx := setupGCS(t)

	if err := GCSBackend.DeleteFromGCS(ctx, "an-object-that-was-never-created"); err != nil {
		t.Errorf("DeleteFromGCS() on a missing object = %v, want nil: deleting a post "+
			"whose file is already gone must still succeed", err)
	}
}

// TestIntegrationDeleteIsIdempotent covers the retry case directly: the same
// delete twice must not start failing on the second attempt.
func TestIntegrationDeleteIsIdempotent(t *testing.T) {
	ctx := setupGCS(t)

	name := "idempotent-delete"
	if _, err := GCSBackend.SaveToGCS(
		ctx, strings.NewReader("payload"), name, "image/png"); err != nil {
		t.Fatalf("SaveToGCS(): %v", err)
	}

	if err := GCSBackend.DeleteFromGCS(ctx, name); err != nil {
		t.Fatalf("first DeleteFromGCS(): %v", err)
	}
	if err := GCSBackend.DeleteFromGCS(ctx, name); err != nil {
		t.Errorf("second DeleteFromGCS() = %v, want nil", err)
	}
}

// TestIntegrationBucketIsCreatedAgainstAnEmulator covers the startup step that
// makes the compose stack come up with one command instead of a documented curl.
//
// It is not redundant with the upload tests even though they would also fail
// without a bucket: fake-gcs-server rejects a write to a bucket that does not
// exist with a 404 rather than creating one, so this pins the specific reason --
// startup created it -- instead of leaving a stack that cannot store anything to
// be diagnosed from an upload failure.
func TestIntegrationBucketIsCreatedAgainstAnEmulator(t *testing.T) {
	ctx := setupGCS(t)

	if !GCSBackend.emulated {
		t.Fatal("STORAGE_EMULATOR_HOST was set but the backend did not notice")
	}

	if _, err := GCSBackend.client.Bucket(GCSBackend.bucket).Attrs(ctx); err != nil {
		t.Fatalf("the bucket was not created at startup: %v", err)
	}

	// And it must be safe to run again, since every process start calls it.
	if err := GCSBackend.ensureBucket(ctx); err != nil {
		t.Errorf("ensureBucket() on an existing bucket = %v, want nil", err)
	}
}
