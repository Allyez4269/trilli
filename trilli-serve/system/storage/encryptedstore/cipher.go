// Package encryptedstore wraps a storage.Store so blob bytes are encrypted with
// a per-tenant key BEFORE they reach the hyperscaler and decrypted on the way
// back. Azure (or anyone browsing the account) sees only ciphertext; only
// Trilli, holding the keys, can read the data.
//
// File format "TLE1" (Trilli Lockbox v1): a 5-byte header (magic "TLE1" + a
// 1-byte version) followed by a sequence of frames. Each frame is a fresh
// 12-byte random nonce followed by an AES-256-GCM sealed chunk of up to 64 KiB
// of plaintext (ciphertext = plaintext + 16-byte tag). A random nonce per chunk
// means we never have to reason about nonce reuse across the many files sharing
// one tenant key. Chunking keeps encryption and decryption fully streaming, so
// large uploads/downloads don't buffer in memory.
package encryptedstore

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

const (
	magic      = "TLE1"
	version    = byte(1)
	headerLen  = len(magic) + 1 // magic + version
	chunkSize  = 64 * 1024      // plaintext bytes per frame
	nonceSize  = 12             // AES-GCM standard nonce
	tagSize    = 16             // AES-GCM tag
	frameMax   = chunkSize + tagSize
)

// ErrCorrupt is returned when a TLE1 stream is malformed or fails authentication.
var ErrCorrupt = errors.New("encryptedstore: corrupt or tampered ciphertext")

// header is the constant 5-byte prefix every TLE1 blob starts with.
func header() []byte { return append([]byte(magic), version) }

// HasMagic reports whether b begins with the TLE1 header (used to tell an
// encrypted blob from a legacy plaintext one).
func HasMagic(b []byte) bool {
	return len(b) >= headerLen && string(b[:len(magic)]) == magic && b[len(magic)] == version
}

func newGCM(dek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("encryptedstore: cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("encryptedstore: gcm: %w", err)
	}
	return gcm, nil
}

// --- encrypt ---

type encReader struct {
	src   io.Reader
	gcm   cipher.AEAD
	buf   bytes.Buffer
	chunk []byte
	wrote bool // header emitted?
	eof   bool
	plain int64 // plaintext bytes consumed (for quota accounting)
}

// EncryptReader returns a reader that yields the TLE1-encrypted form of src.
func EncryptReader(src io.Reader, dek []byte) (*encReader, error) {
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	return &encReader{src: src, gcm: gcm, chunk: make([]byte, chunkSize)}, nil
}

// FrameChunkSize is the plaintext size of a full frame (64 KiB). For resumable
// chunked uploads, every chunk EXCEPT the last must be a whole multiple of this
// so the concatenated frames contain no short frame before the true EOF (the
// decoder treats any short frame as end-of-stream).
const FrameChunkSize = chunkSize

// EncryptReaderNoHeader is EncryptReader without the 5-byte TLE1 header — used to
// encrypt a NON-first chunk of a file whose header was emitted with the first
// chunk. Concatenating EncryptReader(chunk0) + EncryptReaderNoHeader(chunk1) + …
// (in order) yields a valid TLE1 blob, provided every chunk but the last is a
// whole multiple of FrameChunkSize plaintext.
func EncryptReaderNoHeader(src io.Reader, dek []byte) (*encReader, error) {
	e, err := EncryptReader(src, dek)
	if err != nil {
		return nil, err
	}
	e.wrote = true // header already written by the first chunk
	return e, nil
}

// PlaintextBytes is the number of source bytes consumed so far — read it after
// the stream is fully consumed to get the true plaintext size.
func (e *encReader) PlaintextBytes() int64 { return e.plain }

func (e *encReader) Read(p []byte) (int, error) {
	for e.buf.Len() == 0 && !e.eof {
		if !e.wrote {
			e.buf.Write(header())
			e.wrote = true
		}
		n, rerr := io.ReadFull(e.src, e.chunk)
		if n > 0 {
			e.plain += int64(n)
			nonce := make([]byte, nonceSize)
			if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
				return 0, fmt.Errorf("encryptedstore: nonce: %w", err)
			}
			ct := e.gcm.Seal(nil, nonce, e.chunk[:n], nil)
			e.buf.Write(nonce)
			e.buf.Write(ct)
		}
		switch {
		case rerr == io.EOF || rerr == io.ErrUnexpectedEOF:
			e.eof = true
		case rerr != nil:
			return 0, rerr
		}
	}
	return e.buf.Read(p)
}

// --- decrypt ---

type decReader struct {
	src     io.Reader
	gcm     cipher.AEAD
	buf     bytes.Buffer
	checked bool
	eof     bool
	frame   []byte
}

// DecryptReader returns a reader that yields the plaintext of a TLE1 stream.
// The header may already have been consumed from src by a caller probing for
// the magic; pass it as consumedHeader so it can be validated/skipped.
func DecryptReader(src io.Reader, dek []byte, consumedHeader []byte) (*decReader, error) {
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	d := &decReader{src: src, gcm: gcm, frame: make([]byte, frameMax)}
	if consumedHeader != nil {
		if !HasMagic(consumedHeader) {
			return nil, ErrCorrupt
		}
		d.checked = true
	}
	return d, nil
}

func (d *decReader) Read(p []byte) (int, error) {
	for d.buf.Len() == 0 && !d.eof {
		if !d.checked {
			hdr := make([]byte, headerLen)
			if _, err := io.ReadFull(d.src, hdr); err != nil {
				return 0, ErrCorrupt
			}
			if !HasMagic(hdr) {
				return 0, ErrCorrupt
			}
			d.checked = true
		}
		nonce := make([]byte, nonceSize)
		nn, nerr := io.ReadFull(d.src, nonce)
		if nn == 0 && (nerr == io.EOF) {
			d.eof = true // clean end of stream
			break
		}
		if nerr != nil {
			return 0, ErrCorrupt // truncated nonce
		}
		cn, cerr := io.ReadFull(d.src, d.frame)
		if cn == 0 {
			return 0, ErrCorrupt // nonce with no ciphertext
		}
		pt, oerr := d.gcm.Open(nil, nonce, d.frame[:cn], nil)
		if oerr != nil {
			return 0, ErrCorrupt // failed authentication = tampered/wrong key
		}
		d.buf.Write(pt)
		if cerr == io.EOF || cerr == io.ErrUnexpectedEOF {
			d.eof = true // last (short) chunk
		}
	}
	n, _ := d.buf.Read(p)
	if n == 0 && d.eof {
		return 0, io.EOF
	}
	return n, nil
}
