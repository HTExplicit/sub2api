package extensionv1

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

func ValidateTextDocument(body string, maxBytes int) (string, int, error) {
	if !utf8.ValidString(body) {
		return "", 0, fmt.Errorf("body is not valid UTF-8")
	}
	if strings.ContainsRune(body, '\x00') {
		return "", 0, fmt.Errorf("body contains NUL")
	}
	if strings.TrimSpace(body) == "" {
		return "", 0, fmt.Errorf("body is empty")
	}
	length := len([]byte(body))
	if length > maxBytes {
		return "", 0, fmt.Errorf("body exceeds %d bytes", maxBytes)
	}
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:]), length, nil
}
