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

func sigErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrSignature, fmt.Sprintf(format, a...))
}

type sshReader struct{ b []byte }

func (r *sshReader) u32() (uint32, bool) {
	if len(r.b) < 4 {
		return 0, false
	}
	n := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return n, true
}

func (r *sshReader) str() ([]byte, bool) {
	n, ok := r.u32()
	if !ok || uint64(n) > uint64(len(r.b)) {
		return nil, false
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s, true
}

func appendString(dst, s []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(s)))
	return append(dst, s...)
}

// parsePublicKey reads an authorized_keys style ssh-ed25519 line and returns
// the wire-format blob plus the raw key.
func parsePublicKey(line string) ([]byte, ed25519.PublicKey, error) {
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != keyType {
		return nil, nil, errors.New("trusted key must be an ssh-ed25519 public key")
	}
	blob, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return nil, nil, errors.New("trusted key is not valid base64")
	}
	r := sshReader{blob}
	t, ok1 := r.str()
	k, ok2 := r.str()
	if !ok1 || !ok2 || string(t) != keyType || len(k) != ed25519.PublicKeySize || len(r.b) != 0 {
		return nil, nil, errors.New("trusted key blob is malformed")
	}
	return blob, ed25519.PublicKey(k), nil
}

func decodeArmor(armored []byte) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(armored)), "\r\n", "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != sigArmorBegin || strings.TrimSpace(lines[len(lines)-1]) != sigArmorEnd {
		return nil, sigErr("not an SSH signature")
	}
	var b strings.Builder
	for _, l := range lines[1 : len(lines)-1] {
		b.WriteString(strings.TrimSpace(l))
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
	pk, ok1 := r.str()
	ns, ok2 := r.str()
	reserved, ok3 := r.str()
	hashAlg, ok4 := r.str()
	sig, ok5 := r.str()
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 || len(r.b) != 0 {
		return sigErr("malformed structure")
	}
	if !bytes.Equal(pk, trustedBlob) {
		return sigErr("signed with an untrusted key")
	}
	if string(ns) != namespace {
		return sigErr("wrong namespace")
	}
	var digest []byte
	switch string(hashAlg) {
	case "sha512":
		h := sha512.Sum512(message)
		digest = h[:]
	case "sha256":
		h := sha256.Sum256(message)
		digest = h[:]
	default:
		return sigErr("unsupported hash algorithm")
	}
	sr := sshReader{sig}
	format, ok1 := sr.str()
	value, ok2 := sr.str()
	if !ok1 || !ok2 || len(sr.b) != 0 || string(format) != keyType || len(value) != ed25519.SignatureSize {
		return sigErr("unsupported signature format")
	}
	signed := []byte(sigMagic)
	signed = appendString(signed, ns)
	signed = appendString(signed, reserved)
	signed = appendString(signed, hashAlg)
	signed = appendString(signed, digest)
	if !ed25519.Verify(pub, signed, value) {
		return sigErr("signature does not match")
	}
	return nil
}
