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
	candidates := []string{token, token + ".private", token + ".meta.json"}
	if temps, err := filepath.Glob(filepath.Join(filepath.Dir(token), ".token-*")); err == nil {
		candidates = append(candidates, temps...)
	}
	var out []string
	for _, path := range candidates {
		if _, err := os.Lstat(path); err == nil {
			out = append(out, path)
		}
	}
	return out
}

func sameDir(first, second string) bool {
	first, second = filepath.Clean(first), filepath.Clean(second)
	if first == second {
		return true
	}
	resolvedFirst, e1 := filepath.EvalSymlinks(first)
	resolvedSecond, e2 := filepath.EvalSymlinks(second)
	return e1 == nil && e2 == nil && resolvedFirst == resolvedSecond
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
func checkDataDir(path, home, defaultDir string, protect []string) dataVerdict {
	verdict := dataVerdict{dirName: path}
	refuse := func(why string) dataVerdict { verdict.reason = why; return verdict }
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return refuse("la ruta no es absoluta")
	}
	path = filepath.Clean(path)
	verdict.dirName = path
	verdict.custom = !sameDir(path, defaultDir)
	if filepath.Dir(path) == path {
		return refuse("es la raíz del disco")
	}
	if home != "" && sameDir(path, home) {
		return refuse("es tu carpeta personal")
	}
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if home != "" && inside(path, home) {
		return refuse("contiene tu carpeta personal")
	}
	for _, protected := range protect {
		if protected != "" && inside(path, filepath.Clean(protected)) {
			return refuse("contiene el programa o su configuración")
		}
	}
	if userFolderNames[strings.ToLower(filepath.Base(path))] {
		return refuse("parece una carpeta personal del sistema (" + filepath.Base(path) + ")")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return verdict
	}
	verdict.exists = true
	if err != nil {
		return refuse("no se puede leer")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return refuse("es un enlace simbólico")
	}
	if !info.IsDir() {
		return refuse("no es una carpeta")
	}
	if marker, err := os.Lstat(filepath.Join(path, dataMarker)); err == nil && marker.IsDir() {
		verdict.ok = true
		return verdict
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return refuse("no se puede leer")
	}
	if len(entries) == 0 {
		verdict.ok, verdict.empty = true, true
		return verdict
	}
	return refuse("no contiene la marca " + dataMarker + "; no parece una carpeta de apuntes creada por este programa")
}
