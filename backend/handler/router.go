package handler

import (
	"net/http"

	jwtMiddleware "github.com/auth0/go-jwt-middleware"
	jwt "github.com/form3tech-oss/jwt-go"

	"github.com/luc1ferxx/neural-canvas/backend/config"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

func InitRouter() http.Handler {
	jwtMw := jwtMiddleware.New(jwtMiddleware.Options{
		ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
			return config.C.JWTSecret, nil
		},
		SigningMethod: jwt.SigningMethodHS256,
	})

	// protected requires a validly signed token AND a session that has not been
	// revoked by a sign-out. Both checks belong on every authenticated route, so
	// they are composed here rather than repeated per handler.
	protected := func(h http.HandlerFunc) http.Handler {
		return jwtMw.Handler(requireLiveSession(h))
	}

	router := mux.NewRouter()

	router.Handle("/upload", protected(uploadHandler)).Methods("POST")
	router.Handle("/search", protected(searchHandler)).Methods("GET")
	router.Handle("/generate", protected(generateHandler)).Methods("POST")
	router.Handle("/post/{id}", protected(deleteHandler)).Methods("DELETE")
	router.Handle("/signout", protected(signoutHandler)).Methods("POST")

	router.Handle("/signup", http.HandlerFunc(signupHandler)).Methods("POST")
	router.Handle("/signin", http.HandlerFunc(signinHandler)).Methods("POST")

	// Explicit origins from config, never "*". These endpoints require an
	// Authorization header, so a wildcard would let any site drive the API on
	// behalf of a user whose token it managed to obtain.
	originsOk := handlers.AllowedOrigins(config.C.AllowedOrigins)
	headersOk := handlers.AllowedHeaders([]string{"Authorization", "Content-Type"})
	methodsOk := handlers.AllowedMethods([]string{"GET", "POST", "DELETE"})

	return handlers.CORS(originsOk, headersOk, methodsOk)(router)
}
