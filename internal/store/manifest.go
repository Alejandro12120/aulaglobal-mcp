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

func (s *Store) atomic(path string, b []byte) error {
	if err := s.Root.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return e
	}
	tmp := filepath.Join(filepath.Dir(path), ".partial-"+hex.EncodeToString(nonce[:]))
	f, e := s.Root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer s.Root.Remove(tmp)
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return s.Root.Rename(tmp, path)
}
func Path(f aulaglobal.File) string {
	course := f.CourseName
	if course == "" {
		course = fmtCourse(f.CourseID)
	}
	parts := []string{Sanitize(course), Sanitize(f.Section)}
	subdir := f.Filepath
	if f.LocalSubdir != "" {
		subdir = f.LocalSubdir
	}
	for _, part := range strings.FieldsFunc(subdir, func(r rune) bool { return r == '/' || r == '\\' }) {
		parts = append(parts, Sanitize(part))
	}
	return filepath.Join(append(parts, Sanitize(f.Filename))...)
}
func fmtCourse(id int64) string { b, _ := json.Marshal(id); return "course-" + string(b) }
func (s *Store) paths(files []aulaglobal.File) (map[string]string, error) {
	m := map[string]manifestEntry{}
	b, e := s.cacheRead(".aulaglobal-cache/manifest.json")
	if e == nil {
		if json.Unmarshal(b, &m) != nil {
			return nil, errors.New("invalid download manifest")
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	folders := map[int64]string{}
	// Sort legacy entries as well: a course must never depend on map iteration order.
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := m[id]
		if v.CourseID != 0 && v.CourseFolder != "" {
			if Sanitize(v.CourseFolder) != v.CourseFolder {
				return nil, errors.New("invalid course folder")
			}
			folders[v.CourseID] = v.CourseFolder
		}
	}
	used := map[string]string{}
	for id, v := range m {
		if valid(v.Path) != nil {
			return nil, errors.New("invalid manifest path")
		}
		used[v.Path] = id
	}
	sorted := append([]aulaglobal.File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Handle < sorted[j].Handle })
	out := map[string]string{}
	for _, f := range sorted {
		id := f.Handle
		// Recover a pre-upgrade course folder from an already assigned handle.
		if folders[f.CourseID] == "" {
			for _, candidate := range sorted {
				if candidate.CourseID == f.CourseID && m[candidate.Handle].Path != "" {
					folders[f.CourseID] = strings.Split(filepath.ToSlash(m[candidate.Handle].Path), "/")[0]
					break
				}
			}
		}
		if folders[f.CourseID] == "" {
			name := f.CourseName
			if name == "" {
				name = fmtCourse(f.CourseID)
			}
			folders[f.CourseID] = Sanitize(name)
		}
		f.CourseName = folders[f.CourseID]
		p := m[id].Path
		if p == "" {
			p = Path(f)

			base := p
			for n := 0; ; n++ {
				owner, occupied := used[p]
				_, err := s.Root.Stat(p)
				if (!occupied || owner == id) && (occupied || os.IsNotExist(err)) {
					break
				}
				if err != nil && !os.IsNotExist(err) {
					return nil, err
				}
				ext := filepath.Ext(base)
				suffix := fmt.Sprintf("%d", n+2)
				p = strings.TrimSuffix(base, ext) + " (" + suffix + ")" + ext
			}

		}
		used[p] = id
		m[id] = manifestEntry{Path: p, Size: f.Size, Modified: f.Modified, CourseID: f.CourseID, CourseFolder: f.CourseName}
		out[id] = p
	}
	b, e = json.Marshal(m)
	if e != nil {
		return nil, e
	}
	return out, s.atomic(".aulaglobal-cache/manifest.json", b)
}
