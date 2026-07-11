package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	game := NewGame()
	srv := &Server{game: game, clients: map[*Client]bool{}}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", http.FileServer(http.FS(sub)))
	http.HandleFunc("/ws", srv.handleWS)

	go func() {
		for range time.Tick(100 * time.Millisecond) {
			game.Tick()
			srv.broadcast()
		}
	}()

	log.Printf("netrekfp listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
