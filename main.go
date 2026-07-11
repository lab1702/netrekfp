package main

import (
	"embed"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	if dir := os.Getenv("NETREKFP_SHOTDIR"); dir != "" {
		// dev only: lets the page dump its framebuffer for headless inspection
		http.HandleFunc("/debug/shot", func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			_ = os.WriteFile(filepath.Join(dir, "shot.png"), b, 0644)
		})
	}

	go func() {
		for range time.Tick(100 * time.Millisecond) {
			game.Tick()
			srv.broadcast()
		}
	}()

	log.Printf("netrekfp listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
