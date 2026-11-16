package selfupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// ErrSignature is wrapped by every signature verification failure.
var ErrSignature = errors.New("invalid signature")

const (
	sigArmorBegin = "-----BEGIN SSH SIGNATURE-----"
	sigArmorEnd   = "-----END SSH SIGNATURE-----"
	sigMagic      = "SSHSIG"
	keyType       = "ssh-ed25519"
)

func sigErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSignature, fmt.Sprintf(format, args...))
}

type sshReader struct{ rest []byte }

func (r *sshReader) u32() (uint32, bool) {
	if len(r.rest) < 4 {
		return 0, false
	}
	length := binary.BigEndian.Uint32(r.rest)
	r.rest = r.rest[4:]
	return length, true
}

func (r *sshReader) str() ([]byte, bool) {
	length, ok := r.u32()
	if !ok || uint64(length) > uint64(len(r.rest)) {
		return nil, false
	}
	field := r.rest[:length]
	r.rest = r.rest[length:]
	return field, true
}

func appendString(dst, field []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(field)))
	return append(dst, field...)
}

// parsePublicKey reads an authorized_keys style ssh-ed25519 line and returns
// the wire-format blob plus the raw key.
func parsePublicKey(line string) ([]byte, ed25519.PublicKey, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != keyType {
		return nil, nil, errors.New("trusted key must be an ssh-ed25519 public key")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return nil, nil, errors.New("trusted key is not valid base64")
	}
	r := sshReader{blob}
	t, ok1 := r.str()
	keyBytes, ok2 := r.str()
	if !ok1 || !ok2 || string(t) != keyType || len(keyBytes) != ed25519.PublicKeySize || len(r.rest) != 0 {
		return nil, nil, errors.New("trusted key blob is malformed")
	}
	return blob, ed25519.PublicKey(keyBytes), nil
}

func decodeArmor(armored []byte) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(armored)), "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != sigArmorBegin || strings.TrimSpace(lines[len(lines)-1]) != sigArmorEnd {
		return nil, sigErr("not an SSH signature")
	}
	var b strings.Builder
	for _, line := range lines[1 : len(lines)-1] {
		b.WriteString(strings.TrimSpace(line))
	}
	raw, err := base64.StdEncoding.DecodeString(b.String())
	if err != nil {
		return nil, sigErr("armor is not valid base64")
	}
	return raw, nil
}

// VerifySSHSig checks an OpenSSH SSHSIG signature (ssh-keygen -Y sign) over
// message. The signature must come from exactly trustedKey (an ssh-ed25519
// authorized_keys line) and carry the given namespace.
func VerifySSHSig(trustedKey, namespace string, armored, message []byte) error {
	trustedBlob, pub, err := parsePublicKey(trustedKey)
	if err != nil {
		return err
	}
	raw, err := decodeArmor(armored)
	if err != nil {
		return err
	}
	if len(raw) < len(sigMagic) || string(raw[:len(sigMagic)]) != sigMagic {
		return sigErr("bad magic")
	}
	r := sshReader{raw[len(sigMagic):]}
	version, ok := r.u32()
	if !ok || version != 1 {
		return sigErr("unsupported version")
	}
	publicKey, ok1 := r.str()
	namespaceBytes, ok2 := r.str()
	reserved, ok3 := r.str()
	hashAlg, ok4 := r.str()
	sig, ok5 := r.str()
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || len(r.rest) != 0 {
		return sigErr("malformed structure")
	}
	if !bytes.Equal(publicKey, trustedBlob) {
		return sigErr("signed with an untrusted key")
	}
	if string(namespaceBytes) != namespace {
		return sigErr("wrong namespace")
	}
	var digest []byte
	switch string(hashAlg) {
	case "sha512":
		digestHash := sha512.Sum512(message)
		digest = digestHash[:]
	case "sha256":
		digestHash := sha256.Sum256(message)
		digest = digestHash[:]
	default:
		return sigErr("unsupported hash algorithm")
	}
	sigReader := sshReader{sig}
	format, ok1 := sigReader.str()
	value, ok2 := sigReader.str()
	if !ok1 || !ok2 || len(sigReader.rest) != 0 || string(format) != keyType || len(value) != ed25519.SignatureSize {
		return sigErr("unsupported signature format")
	}
	signed := []byte(sigMagic)
	signed = appendString(signed, namespaceBytes)
	signed = appendString(signed, reserved)
	signed = appendString(signed, hashAlg)
	signed = appendString(signed, digest)
	if !ed25519.Verify(pub, signed, value) {
		return sigErr("signature does not match")
	}
	return nil
}
