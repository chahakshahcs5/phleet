// Package totp implements the RFC 6238 time-based one-time password used as a
// second factor for the panel login.
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func GenerateSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return encoding.EncodeToString(b), nil
}

func decode(secret string) ([]byte, error) {
	s := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	s = strings.TrimRight(s, "=")
	return encoding.DecodeString(s)
}

func stepCode(secret string, step uint64) (string, error) {
	key, err := decode(secret)
	if err != nil {
		return "", err
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], step)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000), nil
}

func Code(secret string, at time.Time) (string, error) {
	return stepCode(secret, uint64(at.Unix()/30))
}

// ValidateStep accepts the current interval plus one step either side, which
// tolerates modest clock drift. It returns the matched time step so callers can
// persist it and reject replay of the same code.
func ValidateStep(secret, code string, at time.Time) (uint64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	step := uint64(at.Unix() / 30)
	for _, delta := range []int64{-1, 0, 1} {
		candidate := uint64(int64(step) + delta)
		want, err := stepCode(secret, candidate)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}

// Validate is the boolean form used by setup flows that do not need the step.
func Validate(secret, code string, at time.Time) bool {
	_, ok := ValidateStep(secret, code, at)
	return ok
}

func ProvisioningURL(issuer, account, secret string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}
