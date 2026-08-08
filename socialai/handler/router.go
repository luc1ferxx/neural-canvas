package handler

import (
	"net/http"

	jwtMiddleware "github.com/auth0/go-jwt-middleware"
	jwt "github.com/form3tech-oss/jwt-go"

	"socialai/config"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

func InitRouter() http.Handler {
	jwtMiddleware := jwtMiddleware.New(jwtMiddleware.Options{
		ValidationKeyGetter: func(token *jwt.Token) (interface{}, error) {
			return config.C.JWTSecret, nil
		},
		SigningMethod: jwt.SigningMethodHS256,
	})

	router := mux.NewRouter()

	router.Handle("/upload", jwtMiddleware.Handler(http.HandlerFunc(uploadHandler))).Methods("POST")
	router.Handle("/search", jwtMiddleware.Handler(http.HandlerFunc(searchHandler))).Methods("GET")
	router.Handle("/generate", jwtMiddleware.Handler(http.HandlerFunc(generateHandler))).Methods("POST")

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
