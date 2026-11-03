// Package secrets encrypts project environment variables at rest with
// AES-256-GCM. The key is derived from the control plane session secret.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

func derive(key []byte) []byte {
	sum := sha256.Sum256(append([]byte("phleet-env-v1|"), key...))
	return sum[:]
}

func Encrypt(key, plaintext []byte) (string, error) {
	block, err := aes.NewCipher(derive(key))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func Decrypt(key []byte, encoded string) ([]byte, error) {
	if encoded == "" {
		return []byte{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(derive(key))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

const (
	fileMagic = "PHLEETBK1"
	fileChunk = 1 << 20
)

func fileGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(derive(key))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// EncryptFile encrypts src into dst with chunked AES-256-GCM and returns the
// SHA-256 of the plaintext. The format is:
//
//	"PHLEETBK1" | repeated (4-byte length, 12-byte nonce, ciphertext+tag) | 0
//
// Chunking keeps peak memory bounded and makes a partially written backup
// detectable as truncated.
func EncryptFile(key []byte, srcPath, dstPath string) (string, error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dstPath)
		}
	}()
	gcm, err := fileGCM(key)
	if err != nil {
		return "", err
	}
	if _, err := out.Write([]byte(fileMagic)); err != nil {
		return "", err
	}
	h := sha256.New()
	buf := make([]byte, fileChunk)
	var lenBuf [4]byte
	for {
		n, readErr := io.ReadFull(in, buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return "", err
			}
			nonce := make([]byte, gcm.NonceSize())
			if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
				return "", err
			}
			sealed := gcm.Seal(nil, nonce, buf[:n], nil)
			binary.BigEndian.PutUint32(lenBuf[:], uint32(len(nonce)+len(sealed)))
			if _, err := out.Write(lenBuf[:]); err != nil {
				return "", err
			}
			if _, err := out.Write(nonce); err != nil {
				return "", err
			}
			if _, err := out.Write(sealed); err != nil {
				return "", err
			}
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	binary.BigEndian.PutUint32(lenBuf[:], 0)
	if _, err := out.Write(lenBuf[:]); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	ok = true
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DecryptTo streams a file written by EncryptFile to w.
func DecryptTo(key []byte, r io.Reader, w io.Writer) error {
	gcm, err := fileGCM(key)
	if err != nil {
		return err
	}
	magic := make([]byte, len(fileMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return fmt.Errorf("backup header: %w", err)
	}
	if string(magic) != fileMagic {
		return fmt.Errorf("backup is not in the encrypted format")
	}
	var lenBuf [4]byte
	for {
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return fmt.Errorf("backup chunk header: %w", err)
		}
		n := binary.BigEndian.Uint32(lenBuf[:])
		if n == 0 {
			return nil
		}
		maxChunk := uint32(fileChunk + gcm.Overhead() + gcm.NonceSize())
		if n > maxChunk {
			return fmt.Errorf("backup chunk is too large")
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return fmt.Errorf("backup chunk: %w", err)
		}
		nonce := payload[:gcm.NonceSize()]
		plain, err := gcm.Open(nil, nonce, payload[gcm.NonceSize():], nil)
		if err != nil {
			return fmt.Errorf("backup authentication failed: %w", err)
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
	}
}
