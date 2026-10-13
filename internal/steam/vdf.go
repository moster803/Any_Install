// Package steam finds the Steam installation, all Steam libraries and the
// games installed in them.
package steam

import (
	"errors"
	"strings"
)

// KV is a parsed Valve KeyValues (VDF) block. Keys are stored in lower case
// because Steam treats them case-insensitively. Values are string or KV.
type KV map[string]any

// Get returns a nested block, or nil.
func (kv KV) Get(key string) KV {
	if kv == nil {
		return nil
	}
	if v, ok := kv[strings.ToLower(key)].(KV); ok {
		return v
	}
	return nil
}

// Str returns a string value, or "".
func (kv KV) Str(key string) string {
	if kv == nil {
		return ""
	}
	if v, ok := kv[strings.ToLower(key)].(string); ok {
		return v
	}
	return ""
}

// ParseVDF parses the text VDF format used by libraryfolders.vdf and
// appmanifest_*.acf files.
func ParseVDF(data string) (KV, error) {
	p := &vdfParser{s: strings.TrimPrefix(data, "\ufeff")}
	root := KV{}
	if err := p.block(root, false); err != nil {
		return nil, err
	}
	return root, nil
}

type vdfParser struct {
	s   string
	pos int
}

var errVDF = errors.New("invalid vdf")

func (p *vdfParser) block(into KV, nested bool) error {
	for {
		tok, kind, err := p.next()
		if err != nil {
			return err
		}
		switch kind {
		case tokEOF:
			if nested {
				return errVDF
			}
			return nil
		case tokClose:
			if !nested {
				return errVDF
			}
			return nil
		case tokOpen:
			return errVDF
		}
		key := strings.ToLower(tok)
		val, vkind, err := p.next()
		if err != nil {
			return err
		}
		switch vkind {
		case tokOpen:
			child := KV{}
			if err := p.block(child, true); err != nil {
				return err
			}
			if existing, ok := into[key].(KV); ok {
				for k, v := range child {
					existing[k] = v
				}
			} else {
				into[key] = child
			}
		case tokString:
			into[key] = val
		default:
			return errVDF
		}
		p.skipConditional()
	}
}

type tokKind int

const (
	tokEOF tokKind = iota
	tokString
	tokOpen
	tokClose
)

func (p *vdfParser) skipSpace() {
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			p.pos++
			continue
		}
		if c == '/' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '/' {
			for p.pos < len(p.s) && p.s[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		return
	}
}

// skipConditional skips "[$WIN32]" style platform conditions after a value.
func (p *vdfParser) skipConditional() {
	save := p.pos
	p.skipSpace()
	if p.pos < len(p.s) && p.s[p.pos] == '[' {
		if end := strings.IndexByte(p.s[p.pos:], ']'); end >= 0 {
			p.pos += end + 1
			return
		}
	}
	p.pos = save
}

func (p *vdfParser) next() (string, tokKind, error) {
	p.skipSpace()
	if p.pos >= len(p.s) {
		return "", tokEOF, nil
	}
	c := p.s[p.pos]
	switch c {
	case '{':
		p.pos++
		return "", tokOpen, nil
	case '}':
		p.pos++
		return "", tokClose, nil
	case '"':
		p.pos++
		var b strings.Builder
		for p.pos < len(p.s) {
			c := p.s[p.pos]
			if c == '\\' && p.pos+1 < len(p.s) {
				n := p.s[p.pos+1]
				switch n {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case '\\', '"':
					b.WriteByte(n)
				default:
					b.WriteByte('\\')
					b.WriteByte(n)
				}
				p.pos += 2
				continue
			}
			if c == '"' {
				p.pos++
				return b.String(), tokString, nil
			}
			b.WriteByte(c)
			p.pos++
		}
		return "", tokEOF, errVDF
	}
	start := p.pos
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '{' || c == '}' || c == '"' {
			break
		}
		p.pos++
	}
	return p.s[start:p.pos], tokString, nil
}
