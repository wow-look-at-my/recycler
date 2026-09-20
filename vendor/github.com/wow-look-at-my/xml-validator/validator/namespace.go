package validator

import (
	"fmt"
	"strings"
)

func (p *parser) validateQName(name string) error {
	colons := strings.Count(name, ":")
	if colons == 0 {
		return nil
	}
	if colons > 1 {
		return p.errorf("name %q contains multiple colons; QNames may have at most one", name)
	}
	parts := strings.SplitN(name, ":", 2)
	prefix := parts[0]
	local := parts[1]
	if len(prefix) == 0 {
		return p.errorf("empty prefix in qualified name %q", name)
	}
	if len(local) == 0 {
		return p.errorf("empty local part in qualified name %q", name)
	}
	if !IsNCNameStartChar(rune(prefix[0])) {
		return p.errorf("invalid prefix start character in %q", name)
	}
	for _, r := range prefix[1:] {
		if !IsNCNameChar(r) {
			return p.errorf("invalid character %q in prefix of %q", string(r), name)
		}
	}
	if !IsNCNameStartChar(rune(local[0])) {
		return p.errorf("invalid local part start character in %q", name)
	}
	for _, r := range local[1:] {
		if !IsNCNameChar(r) {
			return p.errorf("invalid character %q in local part of %q", string(r), name)
		}
	}
	return nil
}

func (p *parser) pushNSScope(decls map[string]string) {
	scope := make(map[string]string)
	for k, v := range decls {
		scope[k] = v
	}
	p.nsStack = append(p.nsStack, scope)
}

func (p *parser) popNSScope() {
	if len(p.nsStack) > 0 {
		p.nsStack = p.nsStack[:len(p.nsStack)-1]
	}
}

func (p *parser) resolvePrefix(prefix string) (string, bool) {
	if prefix == "xml" {
		return "http://www.w3.org/XML/1998/namespace", true
	}
	if prefix == "xmlns" {
		return "http://www.w3.org/2000/xmlns/", true
	}
	for i := len(p.nsStack) - 1; i >= 0; i-- {
		if uri, ok := p.nsStack[i][prefix]; ok {
			if uri == "" && prefix != "" {
				return "", false
			}
			return uri, true
		}
	}
	return "", prefix == ""
}

func (p *parser) validatePrefixDeclared(name string) error {
	if !strings.Contains(name, ":") {
		return nil
	}
	prefix := name[:strings.Index(name, ":")]
	if _, ok := p.resolvePrefix(prefix); !ok {
		return p.errorf("undeclared namespace prefix %q in name %q", prefix, name)
	}
	return nil
}

func (p *parser) checkAttributeUniqueness(attrs []attribute) error {
	type expanded struct {
		ns    string
		local string
	}
	seen := make(map[expanded]string)
	for _, a := range attrs {
		var ns, local string
		if idx := strings.Index(a.name, ":"); idx >= 0 {
			prefix := a.name[:idx]
			local = a.name[idx+1:]
			ns, _ = p.resolvePrefix(prefix)
		} else {
			local = a.name
		}
		key := expanded{ns: ns, local: local}
		if prev, ok := seen[key]; ok {
			return &Error{Line: a.line, Col: a.col,
				Message: fmt.Sprintf("attribute %q conflicts with %q (same namespace-expanded name)", a.name, prev)}
		}
		seen[key] = a.name
	}
	return nil
}
