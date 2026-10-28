package main

import (
	"log"
	"net/http"
	"time"

	"toolbox-app/internal/server"
)

func main() {
	addr := ":8080"
	log.Printf("listening on http://localhost%s", addr)
	// WriteTimeout covers a 20 MB upload being compressed on the Pi.
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
