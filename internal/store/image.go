package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// MaxImageBytes is the largest image Image returns. Base64 adds a third, so this
// keeps the encoded image under 5 MB, a common client limit.
const MaxImageBytes = 3<<20 + 3<<18 // 3.75 MiB

// Image reads a downloaded image so an assistant can look at it. The type comes
// from the content, never from the extension, and only formats that clients
// render are accepted.
func (s *Store) Image(ctx context.Context, path string) ([]byte, string, error) {
	if err := valid(path); err != nil {
		return nil, "", err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	// Lstat does not follow the last element, so a symlink is refused outright.
	if info, err := s.Root.Lstat(path); err != nil {
		return nil, "", err
	} else if !info.Mode().IsRegular() {
		return nil, "", errors.New("regular file required")
	}
	file, err := s.Root.Open(path)
	if err != nil {
		return nil, "", errors.New("cannot open file inside download root")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("regular file required")
	}
	if info.Size() > MaxImageBytes {
		return nil, "", fmt.Errorf("image too large: %d bytes, limit %d bytes", info.Size(), MaxImageBytes)
	}
	// The file could grow after the Stat, so the read is bounded too.
	data, err := io.ReadAll(io.LimitReader(file, MaxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > MaxImageBytes {
		return nil, "", fmt.Errorf("image too large: over %d bytes", MaxImageBytes)
	}
	switch mime := http.DetectContentType(data); mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return data, mime, nil
	default:
		return nil, "", fmt.Errorf("not a supported image (png, jpeg, gif or webp); detected %s", mime)
	}
}
