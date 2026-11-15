package selfupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func trusted(t *testing.T, name string) string {
	return strings.TrimSpace(string(fixture(t, name)))
}

// The fixtures were produced by the real `ssh-keygen -Y sign`.
func TestVerifyRealSSHKeygenSignatures(t *testing.T) {
	msg := fixture(t, "sample.txt")
	key := trusted(t, "test_key.pub")
	for _, sig := range []string{"sample.txt.sig", "sample.sha256.sig"} {
		if err := VerifySSHSig(key, Namespace, fixture(t, sig), msg); err != nil {
			t.Fatalf("%s: %v", sig, err)
		}
	}
	// CRLF armor (e.g. a checkout with autocrlf) still verifies.
	crlf := strings.ReplaceAll(string(fixture(t, "sample.txt.sig")), "\n", "\r\n")
	if err := VerifySSHSig(key, Namespace, []byte(crlf), msg); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejects(t *testing.T) {
	msg := fixture(t, "sample.txt")
	key := trusted(t, "test_key.pub")
	good := fixture(t, "sample.txt.sig")
	cases := map[string]struct {
		key  string
		ns   string
		sig  []byte
		msg  []byte
		want string
	}{
		"tampered message": {key, Namespace, good, append([]byte("x"), msg...), "does not match"},
		"wrong namespace":  {key, Namespace, fixture(t, "sample.wrongns.sig"), msg, "namespace"},
		"expected ns diff": {key, "other", good, msg, "namespace"},
		"other signer":     {key, Namespace, fixture(t, "sample.otherkey.sig"), msg, "untrusted key"},
		"other trusted":    {trusted(t, "other_key.pub"), Namespace, good, msg, "untrusted key"},
		"garbage":          {key, Namespace, []byte("hello"), msg, "not an SSH signature"},
		"empty":            {key, Namespace, nil, msg, "not an SSH signature"},
		"bad base64":       {key, Namespace, []byte("-----BEGIN SSH SIGNATURE-----\n!!!\n-----END SSH SIGNATURE-----\n"), msg, "base64"},
		"bad magic":        {key, Namespace, armor([]byte("NOTSIG" + strings.Repeat("a", 40))), msg, "magic"},
		"truncated":        {key, Namespace, armor(decode(t, good)[:50]), msg, "malformed"},
		"bad trusted key":  {"ssh-rsa AAAA", Namespace, good, msg, "ssh-ed25519"},
	}
	for name, c := range cases {
		err := VerifySSHSig(c.key, c.ns, c.sig, c.msg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, c.want)
		}
	}
	if err := VerifySSHSig(key, Namespace, fixture(t, "sample.wrongns.sig"), msg); !errors.Is(err, ErrSignature) {
		t.Errorf("want ErrSignature, got %v", err)
	}
}

func decode(t *testing.T, armored []byte) []byte {
	t.Helper()
	raw, err := decodeArmor(armored)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func armor(raw []byte) []byte {
	return []byte(sigArmorBegin + "\n" + base64.StdEncoding.EncodeToString(raw) + "\n" + sigArmorEnd + "\n")
}

// sign builds an SSHSIG blob the way ssh-keygen does, with tweakable fields.
func sign(priv ed25519.PrivateKey, pubBlob []byte, version uint32, ns, hashAlg string, msg []byte) []byte {
	h := sha512.Sum512(msg)
	signed := appendString([]byte(sigMagic), []byte(ns))
	signed = appendString(signed, nil)
	signed = appendString(signed, []byte(hashAlg))
	signed = appendString(signed, h[:])
	inner := appendString(appendString(nil, []byte(keyType)), ed25519.Sign(priv, signed))
	raw := []byte(sigMagic)
	raw = append(raw, byte(version>>24), byte(version>>16), byte(version>>8), byte(version))
	raw = appendString(raw, pubBlob)
	raw = appendString(raw, []byte(ns))
	raw = appendString(raw, nil)
	raw = appendString(raw, []byte(hashAlg))
	raw = appendString(raw, inner)
	return armor(raw)
}

func newKey(t *testing.T) (ed25519.PrivateKey, []byte, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	blob := appendString(appendString(nil, []byte(keyType)), pub)
	return priv, blob, keyType + " " + base64.StdEncoding.EncodeToString(blob) + " test"
}

func TestVerifySyntheticSignatures(t *testing.T) {
	priv, blob, line := newKey(t)
	msg := []byte("payload")
	if err := VerifySSHSig(line, Namespace, sign(priv, blob, 1, Namespace, "sha512", msg), msg); err != nil {
		t.Fatal(err)
	}
	for name, sig := range map[string][]byte{
		"version 2":      sign(priv, blob, 2, Namespace, "sha512", msg),
		"unknown hash":   sign(priv, blob, 1, Namespace, "md5", msg),
		"namespace":      sign(priv, blob, 1, "git", "sha512", msg),
		"sha1 as sha256": sign(priv, blob, 1, Namespace, "sha1", msg),
	} {
		if err := VerifySSHSig(line, Namespace, sig, msg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
