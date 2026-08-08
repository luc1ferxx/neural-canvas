package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/constants"
	"github.com/luc1ferxx/neural-canvas/backend/model"
	"github.com/luc1ferxx/neural-canvas/backend/store"
)

// These tests run against a real Elasticsearch. They are skipped unless
// ES_TEST_URL is set, so `go test ./...` stays hermetic:
//
//	ES_TEST_URL=http://127.0.0.1:9200 go test ./service/... -run Integration -v
//
// They exist because the interesting failure modes here are not compile errors.
// A mapping can be rejected, a Painless script can be invalid, "index": false
// silently makes a filter match nothing, and a reindex can drop fields. None of
// that is visible without a cluster.

// legacyPostMapping is the pre-migration mapping, where "type" was declared
// unsearchable. Recreated here so the migration can be tested end to end.
const legacyPostMapping = `{
    "mappings": {
        "properties": {
            "id":      { "type": "keyword" },
            "user":    { "type": "keyword" },
            "message": { "type": "text" },
            "url":     { "type": "keyword", "index": false },
            "type":    { "type": "keyword", "index": false }
        }
    }
}`

func setupIntegration(t *testing.T) context.Context {
	t.Helper()

	esURL := os.Getenv("ES_TEST_URL")
	if esURL == "" {
		t.Skip("ES_TEST_URL not set; skipping Elasticsearch integration tests")
	}

	t.Setenv("ES_URL", esURL)
	t.Setenv("ES_USERNAME", "test")
	t.Setenv("ES_PASSWORD", "test")
	t.Setenv("GCS_BUCKET", "test-bucket")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("JWT_SECRET", "0123456789012345678901234567890123456789")
	t.Setenv("ALLOWED_ORIGINS", "http://localhost:3000")

	if err := config.Load(); err != nil {
		t.Fatalf("config.Load(): %v", err)
	}
	ctx := t.Context()
	if err := store.InitElasticsearchBackend(ctx); err != nil {
		t.Fatalf("InitElasticsearchBackend(): %v", err)
	}
	return ctx
}

// uniqueName keeps documents from colliding across tests and across runs.
func uniqueName(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
}

// TestIntegrationMappingsAreAccepted is the cheapest useful check: if any mapping
// in elasticsearch.go were malformed, index creation would fail here.
func TestIntegrationMappingsAreAccepted(t *testing.T) {
	ctx := setupIntegration(t)

	for _, index := range []string{
		constants.USER_INDEX,
		constants.POST_INDEX,
		constants.LOGIN_ATTEMPT_INDEX,
	} {
		exists, err := store.ESBackend.IndexExists(ctx, index)
		if err != nil {
			t.Fatalf("IndexExists(%q): %v", index, err)
		}
		if !exists {
			t.Errorf("index %q was not created at startup", index)
		}
	}
}

func TestIntegrationSignupThenImmediateLogin(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("racer")
	password := "correct-horse"

	if err := AddUser(ctx, &model.User{Username: username, Password: password}); err != nil {
		t.Fatalf("AddUser(): %v", err)
	}

	// No sleep. This used to be a race: the user document was not searchable for
	// up to a refresh interval, so a login immediately after signup could fail.
	ok, err := CheckUser(ctx, username, password)
	if err != nil {
		t.Fatalf("CheckUser(): %v", err)
	}
	if !ok {
		t.Error("a freshly registered user could not log in immediately")
	}
}

func TestIntegrationPasswordIsHashedNotStored(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("hashed")
	password := "plaintext-should-never-appear"

	if err := AddUser(ctx, &model.User{Username: username, Password: password}); err != nil {
		t.Fatalf("AddUser(): %v", err)
	}

	stored, found, err := getUser(ctx, username)
	if err != nil || !found {
		t.Fatalf("getUser(): found=%v err=%v", found, err)
	}

	if stored.Password == password {
		t.Fatal("the password was stored in plaintext")
	}
	if len(stored.Password) < 50 || stored.Password[0] != '$' {
		t.Errorf("stored credential does not look like a bcrypt hash: %q", stored.Password)
	}

	if ok, _ := CheckUser(ctx, username, "wrong-password"); ok {
		t.Error("CheckUser accepted a wrong password")
	}
	if ok, _ := CheckUser(ctx, username, password); !ok {
		t.Error("CheckUser rejected the correct password")
	}
}

func TestIntegrationDuplicateSignupRejected(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("dupe")
	user := model.User{Username: username, Password: "some-password"}

	if err := AddUser(ctx, &user); err != nil {
		t.Fatalf("first AddUser(): %v", err)
	}
	if err := AddUser(ctx, &user); !errors.Is(err, ErrUserExists) {
		t.Errorf("second AddUser() = %v, want ErrUserExists", err)
	}
}

// TestIntegrationLoginThrottle exercises the Painless script. A syntax error or a
// wrong field name there would not surface anywhere else.
func TestIntegrationLoginThrottle(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("throttled")

	allowed, err := LoginAllowed(ctx, username)
	if err != nil {
		t.Fatalf("LoginAllowed() on a fresh name: %v", err)
	}
	if !allowed {
		t.Fatal("a username with no history should be allowed")
	}

	for i := 1; i <= MaxLoginFailures; i++ {
		if err := RecordLoginFailure(ctx, username); err != nil {
			t.Fatalf("RecordLoginFailure() #%d: %v", i, err)
		}
	}

	allowed, err = LoginAllowed(ctx, username)
	if err != nil {
		t.Fatalf("LoginAllowed() after %d failures: %v", MaxLoginFailures, err)
	}
	if allowed {
		t.Errorf("still allowed after %d failures; the lockout does not work", MaxLoginFailures)
	}

	// A successful sign-in clears the count.
	if err := ClearLoginFailures(ctx, username); err != nil {
		t.Fatalf("ClearLoginFailures(): %v", err)
	}
	allowed, err = LoginAllowed(ctx, username)
	if err != nil {
		t.Fatalf("LoginAllowed() after clear: %v", err)
	}
	if !allowed {
		t.Error("the counter was not cleared after a successful sign-in")
	}
}

// TestIntegrationThrottleIncrementsAtomically checks the script actually
// accumulates rather than overwriting, which is the whole reason it is a script.
func TestIntegrationThrottleIncrementsAtomically(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("counter")

	for i := 0; i < 3; i++ {
		if err := RecordLoginFailure(ctx, username); err != nil {
			t.Fatalf("RecordLoginFailure() #%d: %v", i+1, err)
		}
	}

	var attempt loginAttempt
	found, err := store.ESBackend.GetDocument(
		ctx, constants.LOGIN_ATTEMPT_INDEX, username, &attempt)
	if err != nil || !found {
		t.Fatalf("GetDocument(): found=%v err=%v", found, err)
	}

	if attempt.Failures != 3 {
		t.Errorf("failures = %d, want 3: the script overwrote instead of incrementing",
			attempt.Failures)
	}
	if attempt.FirstAttempt == 0 {
		t.Error("firstAttempt was not recorded, so the window can never expire")
	}
}

// TestIntegrationTokenRevocation covers the logout path end to end.
func TestIntegrationTokenRevocation(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("revoked")
	if err := AddUser(ctx, &model.User{Username: username, Password: "some-password"}); err != nil {
		t.Fatalf("AddUser(): %v", err)
	}

	validAfter, err := TokensValidAfter(ctx, username)
	if err != nil {
		t.Fatalf("TokensValidAfter(): %v", err)
	}
	if validAfter != 0 {
		t.Errorf("a new account should have no revocation cutoff, got %d", validAfter)
	}

	issuedAt := time.Now().Unix()

	if err := RevokeTokens(ctx, username); err != nil {
		t.Fatalf("RevokeTokens(): %v", err)
	}

	validAfter, err = TokensValidAfter(ctx, username)
	if err != nil {
		t.Fatalf("TokensValidAfter() after revoke: %v", err)
	}
	if validAfter == 0 {
		t.Fatal("the revocation cutoff was not persisted")
	}
	if issuedAt >= validAfter {
		t.Errorf("a token issued at %d would survive a revocation cutoff of %d",
			issuedAt, validAfter)
	}

	// Revoking must not damage the credential: the partial update has to leave
	// the password hash alone.
	if ok, err := CheckUser(ctx, username, "some-password"); err != nil || !ok {
		t.Errorf("the password stopped working after revocation: ok=%v err=%v", ok, err)
	}
}

func TestIntegrationRevokingUnknownUserIsHarmless(t *testing.T) {
	ctx := setupIntegration(t)

	if err := RevokeTokens(ctx, uniqueName("ghost")); err != nil {
		t.Errorf("RevokeTokens() on a missing user returned %v, want nil", err)
	}
}

// TestIntegrationConcurrentSignupCreatesOneAccount is the race that read-then-write
// could not close.
//
// The old AddUser called getUser and then indexed. Elasticsearch has no unique
// constraint, and the index operation was an upsert, so N concurrent signups for
// the same name all saw "not found", all proceeded, and the last write won. The
// earlier registrant was left with someone else's password on their account.
//
// Exactly one caller must succeed and the rest must see ErrUserExists. The stored
// password must be the winner's, and the account must actually be usable -- a
// half-created user that nobody can log into would satisfy a weaker assertion.
func TestIntegrationConcurrentSignupCreatesOneAccount(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("racecondition")

	// This is a probabilistic guard, not a proof, and it is worth being precise
	// about which. Measured against a deliberately reverted AddUser, it detected
	// the overwrite in 5 of 6 runs at 8 contenders and 5 of 6 again at 24 --
	// raising concurrency does not help, because what decides it is whether
	// Elasticsearch's realtime GET happens to pick up the first write before the
	// others read. So roughly one run in six would let a regression through here.
	//
	// The deterministic half of the guarantee is
	// TestIntegrationCreateDocumentRejectsADuplicate, which pins the primitive this
	// depends on: a create-only write over an existing id must fail. That one
	// cannot pass if the atomicity is lost.
	const attempts = 8

	// Each contender uses a distinguishable password, so it is possible to tell
	// which one landed rather than merely counting outcomes.
	passwords := make([]string, attempts)
	for i := range passwords {
		passwords[i] = fmt.Sprintf("password-from-caller-%d", i)
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		created int
		exists  int
		other   []error
	)

	// A start gate, so the goroutines contend rather than being serialised by
	// however long each one takes to spin up.
	gate := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(pw string) {
			defer wg.Done()
			<-gate

			err := AddUser(ctx, &model.User{Username: username, Password: pw})

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case errors.Is(err, ErrUserExists):
				exists++
			default:
				other = append(other, err)
			}
		}(passwords[i])
	}

	close(gate)
	wg.Wait()

	for _, err := range other {
		t.Errorf("unexpected error from AddUser: %v", err)
	}

	if created != 1 {
		t.Errorf("%d callers created the account, want exactly 1: "+
			"concurrent signups are overwriting each other", created)
	}
	if exists != attempts-1 {
		t.Errorf("%d callers saw ErrUserExists, want %d", exists, attempts-1)
	}

	// Exactly one of the passwords must work, and the account must be usable.
	working := 0
	for _, pw := range passwords {
		ok, err := CheckUser(ctx, username, pw)
		if err != nil {
			t.Fatalf("CheckUser(): %v", err)
		}
		if ok {
			working++
		}
	}
	if working != 1 {
		t.Errorf("%d of the %d passwords work, want exactly 1: the account is not "+
			"in a single consistent state", working, attempts)
	}
}

// TestIntegrationCreateDocumentRejectsADuplicate covers the store primitive
// directly, separately from the signup flow that uses it.
func TestIntegrationCreateDocumentRejectsADuplicate(t *testing.T) {
	ctx := setupIntegration(t)

	id := uniqueName("dupe-doc")
	doc := model.Post{Id: id, User: "someone", Message: "first", Type: "image",
		Url: "https://example.invalid/a.png"}

	if err := store.ESBackend.CreateDocument(ctx, &doc, constants.POST_INDEX, id); err != nil {
		t.Fatalf("first CreateDocument(): %v", err)
	}

	second := doc
	second.Message = "second"
	err := store.ESBackend.CreateDocument(ctx, &second, constants.POST_INDEX, id)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateDocument() = %v, want store.ErrConflict", err)
	}

	// The original must be untouched: a rejected create must not be a partial write.
	var stored model.Post
	found, err := store.ESBackend.GetDocument(ctx, constants.POST_INDEX, id, &stored)
	if err != nil || !found {
		t.Fatalf("GetDocument(): found=%v err=%v", found, err)
	}
	if stored.Message != "first" {
		t.Errorf("message = %q, want %q: the rejected create modified the document",
			stored.Message, "first")
	}
}

// TestIntegrationGenerationQuotaStopsSpending is the test that matters for cost.
// /generate bills OpenAI per call, so an unmetered endpoint lets one account
// drain the balance for every user.
func TestIntegrationGenerationQuotaStopsSpending(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("spender")

	remaining, err := GenerationsRemaining(ctx, username)
	if err != nil {
		t.Fatalf("GenerationsRemaining() on a fresh user: %v", err)
	}
	if remaining != MaxGenerationsPerDay {
		t.Errorf("a fresh user has %d generations, want %d", remaining, MaxGenerationsPerDay)
	}

	for i := 1; i <= MaxGenerationsPerDay; i++ {
		if err := ReserveGeneration(ctx, username); err != nil {
			t.Fatalf("ReserveGeneration() #%d of %d: %v", i, MaxGenerationsPerDay, err)
		}
	}

	// The allowance is spent; the next attempt must be refused rather than
	// forwarded to a paid API.
	if err := ReserveGeneration(ctx, username); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("ReserveGeneration() past the limit = %v, want ErrQuotaExceeded", err)
	}

	remaining, err = GenerationsRemaining(ctx, username)
	if err != nil {
		t.Fatalf("GenerationsRemaining(): %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining = %d after exhausting the quota, want 0", remaining)
	}
}

// TestIntegrationQuotaCountsAreIsolatedPerUser guards the obvious way to get this
// wrong: one shared counter, so the first heavy user locks out everyone.
func TestIntegrationQuotaCountsAreIsolatedPerUser(t *testing.T) {
	ctx := setupIntegration(t)

	heavy := uniqueName("heavy")
	light := uniqueName("light")

	for i := 0; i < MaxGenerationsPerDay; i++ {
		if err := ReserveGeneration(ctx, heavy); err != nil {
			t.Fatalf("ReserveGeneration(heavy) #%d: %v", i+1, err)
		}
	}

	if err := ReserveGeneration(ctx, heavy); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("the heavy user was not stopped: %v", err)
	}
	if err := ReserveGeneration(ctx, light); err != nil {
		t.Errorf("a different user was blocked by someone else's usage: %v", err)
	}
}

// TestIntegrationSigningInDoesNotResetTheSpendingQuota is why the quota has its
// own index rather than sharing the login-throttle one. With a shared index, a
// successful sign-in -- which clears login failures -- would also hand back a
// fresh allowance, making the cap trivially bypassable by logging out and in.
func TestIntegrationSigningInDoesNotResetTheSpendingQuota(t *testing.T) {
	ctx := setupIntegration(t)

	username := uniqueName("cycler")

	for i := 0; i < MaxGenerationsPerDay; i++ {
		if err := ReserveGeneration(ctx, username); err != nil {
			t.Fatalf("ReserveGeneration() #%d: %v", i+1, err)
		}
	}

	// Simulate a fresh sign-in.
	if err := ClearLoginFailures(ctx, username); err != nil {
		t.Fatalf("ClearLoginFailures(): %v", err)
	}

	if err := ReserveGeneration(ctx, username); !errors.Is(err, ErrQuotaExceeded) {
		t.Errorf("signing in restored the generation quota (got %v); the cap can be "+
			"bypassed by signing out and back in", err)
	}
}

// indexPost writes a post directly, bypassing SavePost so the test needs no GCS.
func indexPost(ctx context.Context, t *testing.T, index string, p model.Post) {
	t.Helper()
	if err := store.ESBackend.SaveToES(ctx, &p, index, p.Id); err != nil {
		t.Fatalf("SaveToES(): %v", err)
	}
}

// TestIntegrationTypeFilterIsAppliedByElasticsearch is the test that proves the
// migration was necessary. On the legacy mapping this filter matches nothing.
func TestIntegrationTypeFilterIsAppliedByElasticsearch(t *testing.T) {
	ctx := setupIntegration(t)

	author := uniqueName("author")
	for i := 0; i < 3; i++ {
		indexPost(ctx, t, constants.POST_INDEX, model.Post{
			Id: uniqueName("img"), User: author, Message: "a picture", Type: "image",
			Url: "https://example.invalid/i.png",
		})
	}
	for i := 0; i < 2; i++ {
		indexPost(ctx, t, constants.POST_INDEX, model.Post{
			Id: uniqueName("vid"), User: author, Message: "a clip", Type: "video",
			Url: "https://example.invalid/v.mp4",
		})
	}

	images, err := SearchPosts(ctx, PostQuery{User: author, Type: "image", Size: 50})
	if err != nil {
		t.Fatalf("SearchPosts(image): %v", err)
	}
	if len(images) != 3 {
		t.Errorf("got %d images, want 3", len(images))
	}
	for _, p := range images {
		if p.Type != "image" {
			t.Errorf("image query returned a %q post", p.Type)
		}
	}

	videos, err := SearchPosts(ctx, PostQuery{User: author, Type: "video", Size: 50})
	if err != nil {
		t.Fatalf("SearchPosts(video): %v", err)
	}
	if len(videos) != 2 {
		t.Errorf("got %d videos, want 2", len(videos))
	}

	both, err := SearchPosts(ctx, PostQuery{User: author, Size: 50})
	if err != nil {
		t.Fatalf("SearchPosts(all): %v", err)
	}
	if len(both) != 5 {
		t.Errorf("got %d posts with no type filter, want 5", len(both))
	}
}

// TestIntegrationPaginationReturnsMoreThanTen is the regression guard for the
// silent Elasticsearch default that capped every search at 10 hits.
func TestIntegrationPaginationReturnsMoreThanTen(t *testing.T) {
	ctx := setupIntegration(t)

	author := uniqueName("prolific")
	const total = 25
	for i := 0; i < total; i++ {
		indexPost(ctx, t, constants.POST_INDEX, model.Post{
			Id: uniqueName("p"), User: author, Message: "post", Type: "image",
			Url: "https://example.invalid/p.png",
		})
	}

	posts, err := SearchPosts(ctx, PostQuery{User: author, Size: 50})
	if err != nil {
		t.Fatalf("SearchPosts(): %v", err)
	}
	if len(posts) != total {
		t.Errorf("got %d posts, want %d (the old default capped this at 10)", len(posts), total)
	}

	firstPage, err := SearchPosts(ctx, PostQuery{User: author, From: 0, Size: 10})
	if err != nil {
		t.Fatalf("SearchPosts(page 1): %v", err)
	}
	secondPage, err := SearchPosts(ctx, PostQuery{User: author, From: 10, Size: 10})
	if err != nil {
		t.Fatalf("SearchPosts(page 2): %v", err)
	}
	if len(firstPage) != 10 || len(secondPage) != 10 {
		t.Errorf("page sizes = %d and %d, want 10 and 10", len(firstPage), len(secondPage))
	}
	if len(firstPage) > 0 && len(secondPage) > 0 && firstPage[0].Id == secondPage[0].Id {
		t.Error("the second page repeated the first; `from` is not being applied")
	}
}

// TestIntegrationLegacyMappingCannotFilterByType demonstrates the problem the
// reindex solves, then verifies the migration fixes it.
func TestIntegrationLegacyMappingCannotFilterByType(t *testing.T) {
	ctx := setupIntegration(t)

	legacy := uniqueName("legacy_post_")
	if err := store.ESBackend.EnsureIndex(ctx, legacy, legacyPostMapping); err != nil {
		t.Fatalf("create legacy index: %v", err)
	}
	t.Cleanup(func() {
		_ = store.ESBackend.DeleteIndex(context.Background(), legacy)
	})

	author := uniqueName("legacyauthor")
	indexPost(ctx, t, legacy, model.Post{
		Id: uniqueName("l"), User: author, Message: "old post", Type: "image",
		Url: "https://example.invalid/old.png",
	})

	// Confirm the document is there when not filtering by type.
	all, err := store.ESBackend.ReadFromESPaged(
		ctx, buildPostQuery(PostQuery{User: author}), legacy, 0, 50)
	if err != nil {
		t.Fatalf("read legacy index: %v", err)
	}
	if all.TotalHits() != 1 {
		t.Fatalf("legacy index holds %d matching posts, want 1", all.TotalHits())
	}

	// Filtering by type against the legacy mapping does not merely return
	// nothing: Elasticsearch rejects the query outright with a 400, because the
	// field is not indexed and therefore not searchable at all.
	//
	// That is why the migration uses a new index name rather than trying to
	// filter the old one. It also means a deployment that pointed at the legacy
	// index would fail every search, not degrade quietly -- see the startup
	// warning in store.InitElasticsearchBackend.
	filtered, err := store.ESBackend.ReadFromESPaged(
		ctx, buildPostQuery(PostQuery{User: author, Type: "image"}), legacy, 0, 50)
	if err == nil && filtered.TotalHits() > 0 {
		t.Errorf("the legacy mapping unexpectedly served a type filter (%d hits); "+
			"if that were possible the migration would be unnecessary", filtered.TotalHits())
	}
	if err != nil {
		t.Logf("confirmed: the legacy mapping cannot serve a type filter (%v)", err)
	}

	// Now migrate and confirm the same filter works.
	target := uniqueName("migrated_post_")
	if err := store.ESBackend.EnsureIndex(ctx, target, store.PostMapping()); err != nil {
		t.Fatalf("create target index: %v", err)
	}
	t.Cleanup(func() {
		_ = store.ESBackend.DeleteIndex(context.Background(), target)
	})

	copied, err := store.ESBackend.Reindex(ctx, legacy, target)
	if err != nil {
		t.Fatalf("Reindex(): %v", err)
	}
	if copied != 1 {
		t.Errorf("Reindex copied %d documents, want 1", copied)
	}

	migrated, err := store.ESBackend.ReadFromESPaged(
		ctx, buildPostQuery(PostQuery{User: author, Type: "image"}), target, 0, 50)
	if err != nil {
		t.Fatalf("read migrated index: %v", err)
	}
	if migrated.TotalHits() != 1 {
		t.Errorf("after reindex the type filter matched %d posts, want 1: "+
			"the migration did not make type searchable", migrated.TotalHits())
	}

	// The copied document must keep its fields.
	posts := getPostFromSearchResult(migrated)
	if len(posts) == 1 {
		if posts[0].Type != "image" {
			t.Errorf("migrated post type = %q, want image", posts[0].Type)
		}
		if posts[0].Url == "" {
			t.Error("migrated post lost its url")
		}
	}
}
