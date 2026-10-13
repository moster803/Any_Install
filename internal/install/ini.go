package install

import "strings"

// ParseIni reads "key = value" lines. Keys are lower case; sections are
// ignored because the files we handle use unique keys.
func ParseIni(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := iniLine(line)
		if ok {
			out[k] = v
		}
	}
	return out
}

func iniLine(line string) (key, value string, ok bool) {
	t := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if t == "" || t[0] == ';' || t[0] == '#' || t[0] == '[' {
		return "", "", false
	}
	i := strings.IndexByte(t, '=')
	if i <= 0 {
		return "", "", false
	}
	return strings.ToLower(strings.TrimSpace(t[:i])), strings.TrimSpace(t[i+1:]), true
}

// MergeIni keeps the layout of the new file but takes the values of keys
// that also existed in the old file.
func MergeIni(newText string, old map[string]string) string {
	lines := strings.Split(newText, "\n")
	for i, line := range lines {
		k, _, ok := iniLine(line)
		if !ok {
			continue
		}
		if v, found := old[k]; found {
			lines[i] = setValue(line, v)
		}
	}
	return strings.Join(lines, "\n")
}

// SetIniValue sets key to value in an ini text. If the key does not exist
// it is appended to the given section (created if needed).
func SetIniValue(text, section, key, value string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		k, _, ok := iniLine(line)
		if ok && k == strings.ToLower(key) {
			lines[i] = setValue(line, value)
			return strings.Join(lines, "\n")
		}
	}
	eol := "\n"
	if strings.Contains(text, "\r\n") {
		eol = "\r\n"
	}
	header := "[" + section + "]"
	for i, line := range lines {
		if strings.EqualFold(strings.TrimSpace(strings.TrimSuffix(line, "\r")), header) {
			entry := key + " = " + value
			if strings.HasSuffix(line, "\r") {
				entry += "\r"
			}
			lines = append(lines[:i+1], append([]string{entry}, lines[i+1:]...)...)
			return strings.Join(lines, "\n")
		}
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += eol
	}
	return text + eol + header + eol + key + " = " + value + eol
}

func setValue(line, value string) string {
	cr := strings.HasSuffix(line, "\r")
	body := strings.TrimSuffix(line, "\r")
	i := strings.IndexByte(body, '=')
	out := strings.TrimRight(body[:i], " \t") + " = " + value
	if cr {
		out += "\r"
	}
	return out
}
