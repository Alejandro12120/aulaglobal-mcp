//go:build ignore

// mkrelease crea una versión falsa con el mismo formato que GoReleaser (ver
// .goreleaser.yaml): un archivo por plataforma, checksums.txt y
// checksums.txt.sig firmado con ssh-keygen. Solo para scripts/test-install.sh.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

var labels = []string{
	"macos_apple-silicon", "macos_intel", "linux_x64", "linux_arm64", "windows_x64", "windows_arm64",
}

func main() {
	out := flag.String("out", "", "carpeta raíz del servidor (se crea releases/download/vX.Y.Z)")
	version := flag.String("version", "", "versión sin v")
	key := flag.String("key", "", "clave privada ssh para firmar")
	stubVersion := flag.String("stub-version", "", "versión que imprime el programa falso (por defecto -version)")
	var real, extra multi
	flag.Var(&real, "payload", "etiqueta=ruta: programa real para esa plataforma (repetible)")
	flag.Var(&extra, "extra", "archivo adicional publicado y listado en checksums.txt (repetible)")
	flag.Parse()
	if *out == "" || *version == "" || *key == "" {
		log.Fatal("faltan -out, -version o -key")
	}
	if *stubVersion == "" {
		*stubVersion = *version
	}
	stub := []byte(fmt.Sprintf(`#!/bin/sh
# Programa falso para pruebas del instalador.
case "${1:-}" in
  --version|version) echo %s ;;
  setup)
    echo "STUB-SETUP-RAN $*"
    if [ -t 0 ]; then echo "STUB-STDIN-IS-TTY"; else echo "STUB-STDIN-NOT-TTY"; fi
    ;;
  *) echo "stub $*" ;;
esac
`, *stubVersion))
	payload := map[string][]byte{}
	for _, r := range real {
		l, p, ok := strings.Cut(r, "=")
		if !ok {
			log.Fatalf("-payload %q: usa etiqueta=ruta", r)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		payload[l] = b
	}
	dir := filepath.Join(*out, "releases", "download", "v"+*version)
	must(os.MkdirAll(dir, 0o755))

	sums := map[string]string{}
	add := func(name string, data []byte) {
		must(os.WriteFile(filepath.Join(dir, name), data, 0o644))
		h := sha256.Sum256(data)
		sums[name] = hex.EncodeToString(h[:])
	}
	files := map[string][]byte{
		"README.md":        []byte("readme\n"),
		"LICENSE":          []byte("licence\n"),
		"docs/TECNICO.md": []byte("tecnico\n"),
	}
	for _, l := range labels {
		bin := payload[l]
		if bin == nil {
			bin = stub
		}
		name := fmt.Sprintf("aulaglobal-mcp_%s_%s", *version, l)
		if strings.HasPrefix(l, "windows") {
			add(name+".zip", mkzip(bin, files))
		} else {
			add(name+".tar.gz", mktar(bin, files))
		}
	}
	for _, e := range extra {
		b, err := os.ReadFile(e)
		if err != nil {
			log.Fatal(err)
		}
		add(filepath.Base(e), b)
	}
	names := make([]string, 0, len(sums))
	for n := range sums {
		names = append(names, n)
	}
	sort.Strings(names)
	var sb strings.Builder
	for _, n := range names {
		fmt.Fprintf(&sb, "%s  %s\n", sums[n], n)
	}
	cs := filepath.Join(dir, "checksums.txt")
	must(os.WriteFile(cs, []byte(sb.String()), 0o644))
	cmd := exec.Command("ssh-keygen", "-Y", "sign", "-f", *key, "-n", "aulaglobal-mcp-update", cs)
	cmd.Stderr = os.Stderr
	must(cmd.Run())
}

func mktar(bin []byte, files map[string][]byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name string, mode int64, data []byte) {
		must(tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(data)
		must(err)
	}
	write("aulaglobal-mcp", 0o755, bin)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		write(n, 0o644, files[n])
	}
	must(tw.Close())
	must(gz.Close())
	return buf.Bytes()
}

func mkzip(bin []byte, files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, data []byte) {
		w, err := zw.Create(name)
		must(err)
		_, err = w.Write(data)
		must(err)
	}
	write("aulaglobal-mcp.exe", bin)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		write(n, files[n])
	}
	must(zw.Close())
	return buf.Bytes()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
