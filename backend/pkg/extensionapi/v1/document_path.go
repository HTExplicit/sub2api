package extensionv1

import (
	"errors"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func NormalizeDocumentPath(value string) (string, error) {
	if value == "" || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') {
		return "", errors.New("empty, NUL, or backslash path")
	}
	for _, char := range value {
		if char < 0x20 || strings.ContainsRune(`<>:"|?*`, char) {
			return "", errors.New("invalid path character")
		}
	}
	if strings.HasPrefix(value, "/") || filepath.IsAbs(filepath.FromSlash(value)) {
		return "", errors.New("absolute path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path traversal")
	}
	if clean != value || strings.Contains(value, "//") {
		return "", errors.New("non-canonical path")
	}
	for _, segment := range strings.Split(clean, "/") {
		if strings.TrimRight(segment, " .") != segment || ReservedDocumentPathSegment(segment) {
			return "", errors.New("ambiguous or reserved path segment")
		}
	}
	return clean, nil
}

func ReservedDocumentPathSegment(segment string) bool {
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
}

func FoldDocumentPath(name string) string {
	return cases.Fold().String(norm.NFC.String(strings.ReplaceAll(name, `\`, "/")))
}
