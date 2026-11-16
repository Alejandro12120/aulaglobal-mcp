package store

import (
	"aulaglobal-mcp/internal/aulaglobal"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type manifestEntry struct {
	CourseID     int64  `json:"course_id,omitempty"`
	CourseFolder string `json:"course_folder,omitempty"`
	Path         string `json:"path"`
	Size         *int64 `json:"size"`
	Modified     int64  `json:"timemodified"`
}

func (s *Store) atomic(path string, content []byte) error {
	if err := s.Root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), ".partial-"+hex.EncodeToString(nonce[:]))
	temp, err := s.Root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer s.Root.Remove(tmp)
	defer temp.Close()
	if _, err = temp.Write(content); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	return s.Root.Rename(tmp, path)
}
func Path(file aulaglobal.File) string {
	course := file.CourseName
	if course == "" {
		course = fmtCourse(file.CourseID)
	}
	parts := []string{Sanitize(course), Sanitize(file.Section)}
	subdir := file.Filepath
	if file.LocalSubdir != "" {
		subdir = file.LocalSubdir
	}
	for _, part := range strings.FieldsFunc(subdir, func(r rune) bool { return r == '/' || r == '\\' }) {
		parts = append(parts, Sanitize(part))
	}
	return filepath.ToSlash(filepath.Join(append(parts, Sanitize(file.Filename))...))
}
func fmtCourse(id int64) string { encoded, _ := json.Marshal(id); return "course-" + string(encoded) }
func (s *Store) paths(files []aulaglobal.File) (map[string]string, error) {
	manifest := map[string]manifestEntry{}
	content, err := s.cacheRead(".aulaglobal-cache/manifest.json")
	if err == nil {
		if json.Unmarshal(content, &manifest) != nil {
			return nil, errors.New("invalid download manifest")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	folders := map[int64]string{}
	// Sort legacy entries as well: a course must never depend on map iteration order.
	ids := make([]string, 0, len(manifest))
	for id := range manifest {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		entry := manifest[id]
		if entry.CourseID != 0 && entry.CourseFolder != "" {
			if Sanitize(entry.CourseFolder) != entry.CourseFolder {
				return nil, errors.New("invalid course folder")
			}
			folders[entry.CourseID] = entry.CourseFolder
		}
	}
	used := map[string]string{}
	for id, entry := range manifest {
		entry.Path = filepath.ToSlash(strings.ReplaceAll(entry.Path, "\\", "/"))
		if valid(entry.Path) != nil {
			return nil, errors.New("invalid manifest path")
		}
		manifest[id] = entry
		used[entry.Path] = id
	}
	sorted := append([]aulaglobal.File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Handle < sorted[j].Handle })
	out := map[string]string{}
	for _, file := range sorted {
		id := file.Handle
		// Recover a pre-upgrade course folder from an already assigned handle.
		if folders[file.CourseID] == "" {
			for _, candidate := range sorted {
				if candidate.CourseID == file.CourseID && manifest[candidate.Handle].Path != "" {
					folders[file.CourseID] = strings.Split(filepath.ToSlash(manifest[candidate.Handle].Path), "/")[0]
					break
				}
			}
		}
		if folders[file.CourseID] == "" {
			name := file.CourseName
			if name == "" {
				name = fmtCourse(file.CourseID)
			}
			folders[file.CourseID] = Sanitize(name)
		}
		file.CourseName = folders[file.CourseID]
		entryPath := manifest[id].Path
		if entryPath == "" {
			entryPath = filepath.ToSlash(Path(file))

			base := entryPath
			for attempt := 0; ; attempt++ {
				owner, occupied := used[entryPath]
				_, err := s.Root.Stat(entryPath)
				if (!occupied || owner == id) && (occupied || os.IsNotExist(err)) {
					break
				}
				if err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				ext := filepath.Ext(base)
				suffix := fmt.Sprintf("%d", attempt+2)
				entryPath = strings.TrimSuffix(base, ext) + " (" + suffix + ")" + ext
			}

		}
		used[entryPath] = id
		manifest[id] = manifestEntry{Path: entryPath, Size: file.Size, Modified: file.Modified, CourseID: file.CourseID, CourseFolder: file.CourseName}
		out[id] = entryPath
	}
	content, err = json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	return out, s.atomic(".aulaglobal-cache/manifest.json", content)
}
