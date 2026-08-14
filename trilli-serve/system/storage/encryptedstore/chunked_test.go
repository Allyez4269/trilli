package encryptedstore

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"
)

// TestChunkedEncryptionRoundTrips proves resumable chunked uploads preserve the
// exact TLE1 on-disk format: encrypting a file as [EncryptReader(chunk0)] +
// [EncryptReaderNoHeader(chunkN)]… (each non-final chunk a whole multiple of
// FrameChunkSize) and concatenating the pieces yields a blob that the normal
// decoder reads back byte-for-byte identical to the original plaintext — so
// existing files and downloads are unaffected.
func TestChunkedEncryptionRoundTrips(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	// 5 full frames + a partial tail — exercises full frames AND a short final frame.
	plain := make([]byte, 5*FrameChunkSize+12345)
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}

	// Baseline: straight-through encryption must decrypt to plain.
	er, err := EncryptReader(bytes.NewReader(plain), dek)
	if err != nil {
		t.Fatal(err)
	}
	straight := encAll(t, er)
	if got := decAll(t, straight, dek); !bytes.Equal(got, plain) {
		t.Fatalf("straight-through decrypt mismatch (got %d bytes, want %d)", len(got), len(plain))
	}

	// Chunk size = 2 frames (128 KiB), a legal multiple of FrameChunkSize.
	chunkPlain := 2 * FrameChunkSize
	var chunked bytes.Buffer
	for i, off := 0, 0; off < len(plain); i, off = i+1, off+chunkPlain {
		end := off + chunkPlain
		if end > len(plain) {
			end = len(plain)
		}
		var (
			r   *encReader
			err error
		)
		if i == 0 {
			r, err = EncryptReader(bytes.NewReader(plain[off:end]), dek) // header on first chunk only
		} else {
			r, err = EncryptReaderNoHeader(bytes.NewReader(plain[off:end]), dek)
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		chunked.Write(b)
	}

	// The concatenated chunked ciphertext must decrypt to the SAME plaintext.
	if got := decAll(t, chunked.Bytes(), dek); !bytes.Equal(got, plain) {
		t.Fatalf("chunked decrypt mismatch (got %d bytes, want %d)", len(got), len(plain))
	}

	// Wrong key must fail (authentication), i.e. the format is genuinely encrypted.
	bad := make([]byte, 32)
	if d, err := DecryptReader(bytes.NewReader(chunked.Bytes()), bad, nil); err == nil {
		if _, err := io.ReadAll(d); err == nil {
			t.Fatal("expected decrypt to fail with the wrong key")
		}
	}
}

func encAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decAll(t *testing.T, ct, dek []byte) []byte {
	t.Helper()
	d, err := DecryptReader(bytes.NewReader(ct), dek, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
