package azw3

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

var allowedCSSProperties = map[string]bool{
	"text-align": true, "text-indent": true, "font-weight": true, "font-style": true,
	"font-size": true, "line-height": true, "display": true,
	"margin": true, "margin-top": true, "margin-right": true, "margin-bottom": true, "margin-left": true,
	"padding": true, "padding-top": true, "padding-right": true, "padding-bottom": true, "padding-left": true,
	"page-break-before": true, "page-break-after": true, "page-break-inside": true,
	"break-before": true, "break-after": true, "break-inside": true,
	"list-style": true, "list-style-type": true, "list-style-position": true,
	"list-style-image": true,
	"width":            true, "max-width": true, "height": true, "max-height": true,
}

func compileDocumentCSS(book ebook.Book, catalog resourceCatalog) (map[string]string, error) {
	styles := map[string]string{}
	for _, resource := range book.Resources {
		if strings.EqualFold(strings.TrimSpace(resource.MediaType), "text/css") {
			styles[resource.Href] = string(resource.Data)
		}
	}
	out := map[string]string{}
	for _, doc := range book.Spine {
		var combined strings.Builder
		for _, href := range doc.Stylesheets {
			raw, ok := styles[href]
			if !ok {
				return nil, fmt.Errorf("stylesheet %q used by %q has no resource", href, doc.Href)
			}
			clean, err := sanitizeCSS(raw, catalog)
			if err != nil {
				return nil, fmt.Errorf("stylesheet %q: %w", href, err)
			}
			combined.WriteString(clean)
		}
		out[doc.Href] = combined.String()
	}
	return out, nil
}

func sanitizeCSS(input string, catalog resourceCatalog) (string, error) {
	var out strings.Builder
	for i := 0; i < len(input); {
		open := strings.IndexByte(input[i:], '{')
		if open < 0 {
			break
		}
		open += i
		close := findCSSBlockEnd(input, open+1)
		if close < 0 {
			return "", fmt.Errorf("unterminated rule")
		}
		selector := strings.TrimSpace(input[i:open])
		if safeSelector(selector) {
			declarations, err := sanitizeDeclarations(input[open+1:close], catalog)
			if err != nil {
				return "", err
			}
			if declarations != "" {
				out.WriteString(selector)
				out.WriteByte('{')
				out.WriteString(declarations)
				out.WriteString("}\n")
			}
		}
		i = close + 1
	}
	return out.String(), nil
}

func findCSSBlockEnd(s string, start int) int {
	quote := byte(0)
	depth := 1
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '{' {
			depth++
		}
		if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func safeSelector(s string) bool {
	if s == "" || strings.Contains(s, "@") || strings.Contains(s, "[") {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || strings.ContainsRune(".#,:>+~-_*", r)) {
			return false
		}
	}
	return true
}

func sanitizeDeclarations(block string, catalog resourceCatalog) (string, error) {
	var out strings.Builder
	for _, declaration := range splitCSSDeclarations(block) {
		name, value, ok := strings.Cut(declaration, ":")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if !allowedCSSProperties[name] || !safeCSSValue(value) {
			continue
		}
		rewritten, err := rewriteCSSURLs(value, catalog)
		if err != nil {
			return "", err
		}
		if rewritten == "" {
			continue
		}
		out.WriteString(name)
		out.WriteByte(':')
		out.WriteString(rewritten)
		out.WriteByte(';')
	}
	return out.String(), nil
}

func splitCSSDeclarations(s string) []string {
	var out []string
	start := 0
	quote := byte(0)
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
		}
		if c == '(' {
			depth++
		}
		if c == ')' && depth > 0 {
			depth--
		}
		if c == ';' && depth == 0 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func safeCSSValue(value string) bool {
	lower := strings.ToLower(value)
	for _, bad := range []string{"expression(", "javascript:", "behavior:", "-moz-binding", "position:", "@import"} {
		if strings.Contains(lower, bad) {
			return false
		}
	}
	return true
}

func rewriteCSSURLs(value string, catalog resourceCatalog) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); {
		idx := strings.Index(strings.ToLower(value[i:]), "url(")
		if idx < 0 {
			out.WriteString(value[i:])
			break
		}
		idx += i
		out.WriteString(value[i:idx])
		end := strings.IndexByte(value[idx+4:], ')')
		if end < 0 {
			return "", fmt.Errorf("unterminated url")
		}
		end += idx + 4
		raw := strings.Trim(strings.TrimSpace(value[idx+4:end]), "\"'")
		index, ok := catalog.byHref[raw]
		if !ok {
			return "", fmt.Errorf("CSS image %q has no resource", raw)
		}
		resource := catalog.resources[index]
		fmt.Fprintf(&out, "url(\"kindle:embed:%s?mime=%s\")", paddedBase32(index+1, 4), resource.mediaType)
		i = end + 1
	}
	return out.String(), nil
}
