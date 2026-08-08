package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/luc1ferxx/neural-canvas/backend/logging"
	"github.com/luc1ferxx/neural-canvas/backend/media"
	"github.com/luc1ferxx/neural-canvas/backend/model"
	"github.com/luc1ferxx/neural-canvas/backend/service"

	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
)

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())

	username, ok := usernameFromContext(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
		return
	}

	// Cap the request before touching the body. Without this a single upload
	// can exhaust memory, since multipart parsing buffers as it reads.
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxUploadBytes)

	file, _, err := r.FormFile("media_file")
	if err != nil {
		log.Debug("media file unavailable", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest,
			"Media file is not available or exceeds the size limit")
		return
	}
	// Close on a read is discarded deliberately: it reports nothing actionable.
	// The write-side Close in store.SaveToGCS is checked, because that is where
	// a Close error means the upload did not land.
	defer func() { _ = file.Close() }()

	// Type comes from the bytes, not from the filename extension. A client can
	// name an HTML page "cat.jpg", and every object in the bucket is public.
	postType, mime, body, err := media.Sniff(file)
	if err != nil {
		var unsupported *media.ErrUnsupported
		if errors.As(err, &unsupported) {
			writeError(w, r, http.StatusUnsupportedMediaType, codeUnsupportedType,
				fmt.Sprintf("Unsupported media type (%s). Allowed: JPEG, PNG, GIF, WebP, MP4, WebM, AVI.",
					unsupported.MIME))
			return
		}
		log.Error("could not inspect media file", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal,
			"Failed to inspect media file")
		return
	}

	p := model.Post{
		Id:      uuid.New(),
		User:    username,
		Message: r.FormValue("message"),
		Type:    postType,
	}

	if err := service.SavePost(r.Context(), &p, body, mime); err != nil {
		log.Error("could not save post",
			slog.String("post_id", p.Id), slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal,
			"Failed to save post")
		return
	}

	log.Info("post created",
		slog.String("post_id", p.Id),
		slog.String("username", username),
		slog.String("type", postType))
	// Return the post so the client can render it without refetching.
	writeJSON(w, http.StatusOK, p)
}

// validPostTypes bounds the type filter. An unrecognised value is ignored
// rather than rejected, so a stale client cannot break search.
var validPostTypes = map[string]bool{"image": true, "video": true}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())

	q := r.URL.Query()
	from, size := parsePagination(q)

	postType := q.Get("type")
	if !validPostTypes[postType] {
		postType = ""
	}

	posts, err := service.SearchPosts(r.Context(), service.PostQuery{
		User:     q.Get("user"),
		Keywords: q.Get("keywords"),
		Type:     postType,
		From:     from,
		Size:     size,
	})
	if err != nil {
		log.Error("could not read posts", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal,
			"Failed to read posts")
		return
	}

	if posts == nil {
		posts = []model.Post{}
	}

	js, err := json.Marshal(posts)
	if err != nil {
		log.Error("could not encode posts", slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal,
			"Failed to encode posts")
		return
	}

	log.Debug("search served", slog.Int("results", len(posts)), slog.String("type", postType))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(js)
}

// deleteHandler removes one of the caller's own posts.
//
// The route for this existed only as a commented-out line, so the frontend's
// delete button called an endpoint that always 404'd.
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	log := logging.FromContext(r.Context())

	username, ok := usernameFromContext(r)
	if !ok {
		writeError(w, r, http.StatusUnauthorized, codeUnauthorized, "Unauthorized")
		return
	}

	id := mux.Vars(r)["id"]
	if id == "" {
		writeError(w, r, http.StatusBadRequest, codeInvalidRequest, "Post id is required")
		return
	}

	// Ownership is enforced in the service by matching id AND user, so one user
	// cannot delete another's post.
	if err := service.DeletePost(r.Context(), id, username); err != nil {
		if errors.Is(err, service.ErrPostNotFound) {
			writeError(w, r, http.StatusNotFound, codeNotFound, "Post not found")
			return
		}
		log.Error("could not delete post",
			slog.String("post_id", id), slog.String("cause", err.Error()))
		writeError(w, r, http.StatusInternalServerError, codeInternal, "Failed to delete post")
		return
	}

	log.Info("post deleted", slog.String("post_id", id), slog.String("username", username))
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}
