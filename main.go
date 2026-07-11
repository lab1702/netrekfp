package main

import (
	"compress/gzip"
	"embed"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed web
var webFS embed.FS

func main() {
	addr := flag.String("addr", ":9701", "listen address")
	flag.Parse()

	game := NewGame()
	srv := &Server{game: game, clients: map[*Client]bool{}}

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", gzipHandler(http.FileServer(http.FS(sub))))
	http.HandleFunc("/ws", srv.handleWS)
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

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

type gzipWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g *gzipWriter) WriteHeader(code int) {
	g.Header().Del("Content-Length") // no longer valid for the compressed body
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	g.Header().Del("Content-Length")
	return g.gz.Write(b)
}

func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		next.ServeHTTP(&gzipWriter{w, gz}, r)
	})
}
