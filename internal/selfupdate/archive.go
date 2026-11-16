package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

const maxBinaryBytes = 100 << 20

// AssetName mirrors name_template in .goreleaser.yaml. version has no "v".
func AssetName(version, goos, goarch string) (string, error) {
	var osName, archName string
	switch goos {
	case "darwin":
		osName = "macos"
		switch goarch {
		case "arm64":
			archName = "apple-silicon"
		case "amd64":
			archName = "intel"
		}
	case "linux", "windows":
		osName = goos
		switch goarch {
		case "amd64":
			archName = "x64"
		case "arm64":
			archName = "arm64"
		}
	}
	if osName == "" || archName == "" {
		return "", fmt.Errorf("no hay versiones publicadas para %s/%s", goos, goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", BinaryName, version, osName, archName, ext), nil
}

// checksumFor finds the sha256 of exactly name in a checksums.txt body
// ("<hex>  <filename>" per line).
func checksumFor(checksums []byte, name string) (string, error) {
	found := ""
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("checksums.txt lista %s más de una vez", name)
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("checksum de %s con formato inválido", name)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("checksum de %s con formato inválido", name)
		}
		found = strings.ToLower(fields[0])
	}
	if found == "" {
		return "", fmt.Errorf("checksums.txt no incluye %s", name)
	}
	return found, nil
}

func verifyChecksum(data []byte, want string) error {
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return errors.New("el checksum SHA-256 del archivo descargado no coincide")
	}
	return nil
}

// extractBinary returns the content of the single top-level entry called bin.
// Entries are matched by exact name and never written by name, so crafted
// paths ("../x", absolute paths) cannot escape anything: they are just ignored.
func extractBinary(archive []byte, zipped bool, bin string) ([]byte, error) {
	var data []byte
	var err error
	if zipped {
		data, err = fromZip(archive, bin)
	} else {
		data, err = fromTarGz(archive, bin)
	}
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("el archivo no contiene %s", bin)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s está vacío en el archivo", bin)
	}
	return data, nil
}

func entryMatches(name, bin string) bool {
	return !strings.ContainsAny(name, "\\") && path.Clean(name) == bin
}

func readCapped(reader io.Reader) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, maxBinaryBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > maxBinaryBytes {
		return nil, errors.New("el ejecutable del archivo supera el tamaño máximo permitido")
	}
	return content, nil
}

func fromTarGz(archive []byte, bin string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, errors.New("archivo tar.gz inválido")
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, errors.New("archivo tar.gz inválido")
		}
		if h.Typeflag != tar.TypeReg || !entryMatches(h.Name, bin) {
			continue
		}
		if h.Size > maxBinaryBytes {
			return nil, errors.New("el ejecutable del archivo supera el tamaño máximo permitido")
		}
		return readCapped(tr)
	}
}

func fromZip(archive []byte, bin string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, errors.New("archivo zip inválido")
	}
	for _, zipFile := range zr.File {
		if !zipFile.Mode().IsRegular() || !entryMatches(zipFile.Name, bin) {
			continue
		}
		if zipFile.UncompressedSize64 > maxBinaryBytes {
			return nil, errors.New("el ejecutable del archivo supera el tamaño máximo permitido")
		}
		reader, err := zipFile.Open()
		if err != nil {
			return nil, errors.New("archivo zip inválido")
		}
		defer reader.Close()
		return readCapped(reader)
	}
	return nil, nil
}
