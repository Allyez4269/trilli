package encryptedstore

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

func mustDEK(t *testing.T) []byte {
	t.Helper()
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatalf("dek: %v", err)
	}
	return dek
}

func roundTrip(t *testing.T, dek []byte, plaintext []byte) {
	t.Helper()
	enc, err := EncryptReader(bytes.NewReader(plaintext), dek)
	if err != nil {
		t.Fatalf("EncryptReader: %v", err)
	}
	ct, err := io.ReadAll(enc)
	if err != nil {
		t.Fatalf("read ciphertext: %v", err)
	}
	if !HasMagic(ct) {
		t.Fatalf("ciphertext missing TLE1 magic")
	}
	// Only meaningful for non-trivial inputs: a handful of random bytes can
	// coincidentally appear in the ciphertext, so guard the leak check.
	if len(plaintext) >= 64 && bytes.Contains(ct, plaintext) {
		t.Fatalf("plaintext leaked into ciphertext")
	}
	if got := enc.PlaintextBytes(); got != int64(len(plaintext)) {
		t.Fatalf("PlaintextBytes=%d want %d", got, len(plaintext))
	}
	dec, err := DecryptReader(bytes.NewReader(ct), dek, nil)
	if err != nil {
		t.Fatalf("DecryptReader: %v", err)
	}
	out, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read plaintext: %v", err)
	}
	if !bytes.Equal(out, plaintext) {
		t.Fatalf("round-trip mismatch: got %d bytes want %d", len(out), len(plaintext))
	}
}

func TestRoundTripSizes(t *testing.T) {
	dek := mustDEK(t)
	sizes := []int{
		0, 1, 100,
		chunkSize - 1, chunkSize, chunkSize + 1,
		2 * chunkSize, 3*chunkSize + 123,
		1 << 20, // 1 MiB
	}
	for _, n := range sizes {
		pt := make([]byte, n)
		if _, err := rand.Read(pt); err != nil {
			t.Fatal(err)
		}
		roundTrip(t, dek, pt)
	}
}

func TestWrongKeyFails(t *testing.T) {
	dek := mustDEK(t)
	other := mustDEK(t)
	pt := make([]byte, 5000)
	_, _ = rand.Read(pt)

	enc, _ := EncryptReader(bytes.NewReader(pt), dek)
	ct, _ := io.ReadAll(enc)

	dec, _ := DecryptReader(bytes.NewReader(ct), other, nil)
	if _, err := io.ReadAll(dec); err == nil {
		t.Fatal("expected decryption with the wrong key to fail, got nil error")
	}
}

func TestTamperFails(t *testing.T) {
	dek := mustDEK(t)
	pt := make([]byte, 5000)
	_, _ = rand.Read(pt)
	enc, _ := EncryptReader(bytes.NewReader(pt), dek)
	ct, _ := io.ReadAll(enc)

	ct[len(ct)-1] ^= 0xFF // flip a byte in the last tag/ciphertext

	dec, _ := DecryptReader(bytes.NewReader(ct), dek, nil)
	if _, err := io.ReadAll(dec); err == nil {
		t.Fatal("expected tampered ciphertext to fail authentication")
	}
}

func TestConsumedHeaderPath(t *testing.T) {
	dek := mustDEK(t)
	pt := []byte("hello trilli lockbox")
	enc, _ := EncryptReader(bytes.NewReader(pt), dek)
	ct, _ := io.ReadAll(enc)

	// Simulate the decorator having peeked the 5-byte header off the stream.
	hdr := ct[:headerLen]
	rest := ct[headerLen:]
	dec, err := DecryptReader(bytes.NewReader(rest), dek, hdr)
	if err != nil {
		t.Fatalf("DecryptReader with consumed header: %v", err)
	}
	out, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(out, pt) {
		t.Fatalf("mismatch: %q", out)
	}
}
