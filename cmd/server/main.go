package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"taskbridge/internal/api"
	"taskbridge/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "server listen address")
	flag.Parse()

	memoryStore := store.NewMemoryStore()
	server := api.NewServer(memoryStore)

	fmt.Printf("TaskBridge server listening on %s\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, server.Routes()))
}
