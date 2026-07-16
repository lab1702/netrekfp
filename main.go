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
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			game.Tick()
			srv.broadcast()
		}
	}()

	log.Printf("netrekfp listening on %s", *addr)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           http.DefaultServeMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(httpServer.ListenAndServe())
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
		defer func() { _ = gz.Close() }()
		next.ServeHTTP(&gzipWriter{w, gz}, r)
	})
}
