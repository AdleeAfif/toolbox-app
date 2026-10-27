package main

import (
	"log"
	"net/http"

	"url-tool/internal/server"
)

func main() {
	addr := ":8080"
	log.Printf("listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, server.New()))
}
