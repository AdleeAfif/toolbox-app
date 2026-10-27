package main

import (
	"log"
	"net/http"

	"toolbox-app/internal/server"
)

func main() {
	addr := ":8080"
	log.Printf("listening on http://localhost%s", addr)
	log.Fatal(http.ListenAndServe(addr, server.New()))
}
