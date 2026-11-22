package server

import (
	"io/fs"
	"net/http"
	"probex/web"
)

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method not allowed", 405)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
	files, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		http.Error(w, "embedded files unavailable", 500)
		return
	}
	http.FileServer(http.FS(files)).ServeHTTP(w, r)
}
