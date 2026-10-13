package console

import "strings"

// utf8Locale reports whether the locale says that text is UTF-8. It gets
// the values of LC_ALL, LC_CTYPE and LANG, of which the first that is set
// decides; a locale that names no encoding, like C, does not.
func utf8Locale(values ...string) bool {
	for _, v := range values {
		if v == "" {
			continue
		}
		v = strings.ToLower(v)
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	return false
}
