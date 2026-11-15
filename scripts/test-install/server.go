//go:build ignore

// server imita lo mínimo de github.com/<repo>/releases que usan los
// instaladores: /releases/latest responde con una redirección a
// /releases/tag/<etiqueta> y /releases/download/... sirve archivos estáticos.
// Solo para pruebas locales (scripts/test-install.sh); escucha en loopback.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	dir := flag.String("dir", "", "carpeta que contiene releases/")
	addr := flag.String("addr", "127.0.0.1:8099", "dirección de escucha")
	latest := flag.String("latest", "", "archivo con la etiqueta de la última versión (vX.Y.Z)")
	flag.Parse()
	if *dir == "" || *latest == "" {
		log.Fatal("faltan -dir o -latest")
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(*latest)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		tag := strings.TrimSpace(string(b))
		http.Redirect(w, r, "http://"+r.Host+"/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/latest/download/", func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(*latest)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		tag := strings.TrimSpace(string(b))
		name := strings.TrimPrefix(r.URL.Path, "/releases/latest/download/")
		http.Redirect(w, r, "http://"+r.Host+"/releases/download/"+tag+"/"+name, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "release page")
	})
	mux.Handle("/", http.FileServer(http.Dir(*dir)))
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(os.Stderr, "%s %s\n", r.Method, r.URL.Path)
		mux.ServeHTTP(w, r)
	})
	log.Fatal(http.ListenAndServe(*addr, logged))
}
