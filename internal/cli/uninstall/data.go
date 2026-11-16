package uninstall

import (
	"os"
	"path/filepath"
	"strings"
)

// dataMarker is the folder inside a notes folder that marks it as created by
// this program.
const dataMarker = ".aulaglobal-cache"

// tokenFiles lists what setup stores for a token: the token, its private
// companion, its metadata and the temporaries an interrupted setup leaves.
func tokenFiles(token string) []string {
	cands := []string{token, token + ".private", token + ".meta.json"}
	if temps, err := filepath.Glob(filepath.Join(filepath.Dir(token), ".token-*")); err == nil {
		cands = append(cands, temps...)
	}
	var out []string
	for _, p := range cands {
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, e1 := filepath.EvalSymlinks(a)
	rb, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && ra == rb
}

type dataVerdict struct {
	exists  bool
	ok      bool   // may be deleted
	empty   bool   // delete with Remove, not RemoveAll
	custom  bool   // not the default location
	reason  string // why it is refused
	dirName string
}

var userFolderNames = map[string]bool{
	"documents": true, "documentos": true, "desktop": true, "escritorio": true, "downloads": true, "descargas": true,
	"pictures": true, "imágenes": true, "imagenes": true, "music": true, "música": true, "musica": true,
	"videos": true, "vídeos": true, "movies": true, "library": true, "appdata": true, "dropbox": true,
	"onedrive": true, "google drive": true, "icloud drive": true, "public": true, "applications": true,
}

// checkDataDir decides whether p may be deleted as the materials folder.
// protect lists paths that must not be inside it (program, config, cache).
func checkDataDir(p, home, def string, protect []string) dataVerdict {
	v := dataVerdict{dirName: p}
	refuse := func(why string) dataVerdict { v.reason = why; return v }
	if strings.TrimSpace(p) == "" || !filepath.IsAbs(p) {
		return refuse("la ruta no es absoluta")
	}
	p = filepath.Clean(p)
	v.dirName = p
	v.custom = !sameDir(p, def)
	if filepath.Dir(p) == p {
		return refuse("es la raíz del disco")
	}
	if home != "" && sameDir(p, home) {
		return refuse("es tu carpeta personal")
	}
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if home != "" && inside(p, home) {
		return refuse("contiene tu carpeta personal")
	}
	for _, q := range protect {
		if q != "" && inside(p, filepath.Clean(q)) {
			return refuse("contiene el programa o su configuración")
		}
	}
	if userFolderNames[strings.ToLower(filepath.Base(p))] {
		return refuse("parece una carpeta personal del sistema (" + filepath.Base(p) + ")")
	}
	fi, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return v
	}
	v.exists = true
	if err != nil {
		return refuse("no se puede leer")
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return refuse("es un enlace simbólico")
	}
	if !fi.IsDir() {
		return refuse("no es una carpeta")
	}
	if m, err := os.Lstat(filepath.Join(p, dataMarker)); err == nil && m.IsDir() {
		v.ok = true
		return v
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return refuse("no se puede leer")
	}
	if len(entries) == 0 {
		v.ok, v.empty = true, true
		return v
	}
	return refuse("no contiene la marca " + dataMarker + "; no parece una carpeta de apuntes creada por este programa")
}
