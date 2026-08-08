package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"socialai/media"
	"socialai/model"
	"socialai/service"

	"github.com/gorilla/mux"
	"github.com/pborman/uuid"
)

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one upload request")

	username, ok := usernameFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Cap the request before touching the body. Without this a single upload
	// can exhaust memory, since multipart parsing buffers as it reads.
	r.Body = http.MaxBytesReader(w, r.Body, media.MaxUploadBytes)

	file, _, err := r.FormFile("media_file")
	if err != nil {
		http.Error(w, "Media file is not available or exceeds the size limit", http.StatusBadRequest)
		fmt.Printf("Media file is not available %v\n", err)
		return
	}
	defer file.Close()

	// Type comes from the bytes, not from the filename extension. A client can
	// name an HTML page "cat.jpg", and every object in the bucket is public.
	postType, mime, body, err := media.Sniff(file)
	if err != nil {
		var unsupported *media.ErrUnsupported
		if errors.As(err, &unsupported) {
			http.Error(w,
				fmt.Sprintf("Unsupported media type (%s). Allowed: JPEG, PNG, GIF, WebP, MP4, WebM, AVI.", unsupported.MIME),
				http.StatusUnsupportedMediaType)
			return
		}
		http.Error(w, "Failed to inspect media file", http.StatusInternalServerError)
		fmt.Printf("Failed to inspect media file %v\n", err)
		return
	}

	p := model.Post{
		Id:      uuid.New(),
		User:    username,
		Message: r.FormValue("message"),
		Type:    postType,
	}

	if err := service.SavePost(&p, body, mime); err != nil {
		http.Error(w, "Failed to save post to backend", http.StatusInternalServerError)
		fmt.Printf("Failed to save post to backend %v\n", err)
		return
	}

	fmt.Println("Post is saved successfully.")
	// Return the post so the client can render it without refetching.
	writeJSON(w, http.StatusOK, p)
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one request for search")

	user := r.URL.Query().Get("user")
	keywords := r.URL.Query().Get("keywords")
	from, size := parsePagination(r.URL.Query())

	var posts []model.Post
	var err error
	if user != "" {
		posts, err = service.SearchPostsByUser(user, from, size)
	} else {
		posts, err = service.SearchPostsByKeywords(keywords, from, size)
	}
	if err != nil {
		http.Error(w, "Failed to read post from backend", http.StatusInternalServerError)
		fmt.Printf("Failed to read post from backend %v.\n", err)
		return
	}

	if posts == nil {
		posts = []model.Post{}
	}

	js, err := json.Marshal(posts)
	if err != nil {
		http.Error(w, "Failed to parse posts into JSON format", http.StatusInternalServerError)
		fmt.Printf("Failed to parse posts into JSON format %v.\n", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(js)
}

// deleteHandler removes one of the caller's own posts.
//
// The route for this existed only as a commented-out line, so the frontend's
// delete button called an endpoint that always 404'd.
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Received one delete request")

	username, ok := usernameFromContext(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id := mux.Vars(r)["id"]
	if id == "" {
		http.Error(w, "Post id is required", http.StatusBadRequest)
		return
	}

	// Ownership is enforced in the service by matching id AND user, so one user
	// cannot delete another's post.
	if err := service.DeletePost(id, username); err != nil {
		if errors.Is(err, service.ErrPostNotFound) {
			http.Error(w, "Post not found", http.StatusNotFound)
			return
		}
		http.Error(w, "Failed to delete post", http.StatusInternalServerError)
		fmt.Printf("Failed to delete post %v\n", err)
		return
	}

	fmt.Printf("Post %s deleted by %s\n", id, username)
	writeJSON(w, http.StatusOK, map[string]string{"id": id})
}
