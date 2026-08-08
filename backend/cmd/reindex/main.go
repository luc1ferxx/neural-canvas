// Command reindex migrates posts from the legacy "post" index into the
// versioned index that indexes the "type" field.
//
// The original mapping declared "type" with "index": false, which makes it
// unsearchable. Elasticsearch does not allow that parameter to change on an
// existing field, so the only way to filter posts by type server-side is to
// create a new index and copy the documents across. The values survive the copy
// because an unindexed field is still stored in _source.
//
// Usage:
//
//	set -a && . ./.env && set +a
//	go run ./cmd/reindex
//
// The command is safe to run more than once: it refuses to copy into a
// non-empty destination unless -force is given.
//
// Output here is fmt.Print rather than slog, deliberately. The server's log is
// machine-readable because a collector consumes it; this is a one-shot command
// whose consumer is a person watching a terminal, and rendering a progress report
// as JSON entries would make it harder to read, not easier to query.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/luc1ferxx/neural-canvas/backend/config"
	"github.com/luc1ferxx/neural-canvas/backend/constants"
	"github.com/luc1ferxx/neural-canvas/backend/store"
)

func main() {
	force := flag.Bool("force", false,
		"copy even if the destination index already contains documents")
	flag.Parse()

	if err := config.Load(); err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	// Ctrl-C aborts the copy. Elasticsearch stops the underlying reindex task when
	// the connection goes away, so a cancelled run leaves the destination partially
	// filled rather than corrupt -- and a re-run copies by id, so it converges.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := store.InitElasticsearchBackend(ctx); err != nil {
		log.Fatalf("elasticsearch: %v", err)
	}

	es := store.ESBackend
	src := constants.POST_INDEX_LEGACY
	dst := constants.POST_INDEX

	srcExists, err := es.IndexExists(ctx, src)
	if err != nil {
		log.Fatalf("check %q: %v", src, err)
	}
	if !srcExists {
		fmt.Printf("Legacy index %q does not exist. Nothing to migrate.\n", src)
		return
	}

	srcCount, err := es.CountDocuments(ctx, src)
	if err != nil {
		log.Fatalf("count %q: %v", src, err)
	}

	// InitElasticsearchBackend already created dst with the current mapping.
	dstCount, err := es.CountDocuments(ctx, dst)
	if err != nil {
		log.Fatalf("count %q: %v", dst, err)
	}

	fmt.Printf("source      %-10s %d documents\n", src, srcCount)
	fmt.Printf("destination %-10s %d documents\n", dst, dstCount)

	if srcCount == 0 {
		fmt.Println("Source is empty. Nothing to migrate.")
		return
	}
	if dstCount > 0 && !*force {
		fmt.Printf("\nDestination already holds %d documents; refusing to copy.\n", dstCount)
		fmt.Println("Re-run with -force if you intend to merge into it.")
		fmt.Println("Documents are copied by id, so a repeat run overwrites rather than duplicates.")
		return
	}

	fmt.Printf("\nCopying %s -> %s ...\n", src, dst)
	// Reindex sets no deadline of its own, because only the caller knows how much
	// data there is. An hour is generous for this dataset and still bounded.
	reindexCtx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()

	copied, err := es.Reindex(reindexCtx, src, dst)
	if err != nil {
		log.Fatalf("reindex: %v", err)
	}

	finalCount, err := es.CountDocuments(ctx, dst)
	if err != nil {
		log.Fatalf("count %q after copy: %v", dst, err)
	}

	fmt.Printf("Copied %d documents. %q now holds %d.\n", copied, dst, finalCount)

	if finalCount < srcCount {
		log.Fatalf("destination has %d documents but source had %d: copy is incomplete",
			finalCount, srcCount)
	}

	fmt.Printf("\nDone. Verify the app, then delete the legacy index when you are confident:\n")
	fmt.Printf("  curl -XDELETE \"$ES_URL/%s\" -u \"$ES_USERNAME:$ES_PASSWORD\"\n", src)
}
