package epub

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

const maxCSSResourceBytes = 2 << 20
const maxTotalCSSBytes = 8 << 20
const maxCSSImportDepth = 8

func readResources(a *archive, book *ebook.Book, opfPath string, manifest []manifestItem) ([]ebook.Resource, error) {
	items := map[string]manifestItem{}
	for _, item := range manifest {
		href, err := resolveReference(opfPath, item.Href)
		if err == nil {
			items[referencePath(href)] = item
		}
	}
	css := map[string]ebook.Resource{}
	dependencies := map[string][]string{}
	var order, imageRefs []string
	total := 0
	var load func(string, int, map[string]bool) error
	load = func(href string, depth int, active map[string]bool) error {
		if _, ok := css[href]; ok {
			return nil
		}
		if depth > maxCSSImportDepth {
			return fmt.Errorf("CSS import depth exceeds %d at %q", maxCSSImportDepth, href)
		}
		if active[href] {
			return fmt.Errorf("CSS import cycle at %q", href)
		}
		item, ok := items[href]
		if !ok || !strings.EqualFold(strings.TrimSpace(item.MediaType), "text/css") {
			return fmt.Errorf("stylesheet %q is missing from manifest or is not text/css", href)
		}
		size, err := a.size(href)
		if err != nil {
			return err
		}
		if size > maxCSSResourceBytes || uint64(total)+size > maxTotalCSSBytes {
			return fmt.Errorf("CSS resources exceed size limit at %q", href)
		}
		data, err := a.read(href)
		if err != nil {
			return err
		}
		normalized, imports, urls, err := normalizeCSSReferences(string(data), href)
		if err != nil {
			return err
		}
		active[href] = true
		for _, imported := range imports {
			if err := load(imported, depth+1, active); err != nil {
				return err
			}
		}
		delete(active, href)
		total += len(data)
		css[href] = ebook.Resource{Href: href, MediaType: "text/css", Data: []byte(normalized)}
		dependencies[href] = imports
		order = append(order, href)
		imageRefs = append(imageRefs, urls...)
		return nil
	}
	for i := range book.Spine {
		for _, href := range book.Spine[i].Stylesheets {
			if err := load(href, 0, map[string]bool{}); err != nil {
				return nil, err
			}
		}
		book.Spine[i].Stylesheets = expandStylesheets(book.Spine[i].Stylesheets, dependencies)
	}
	var resources []ebook.Resource
	for _, href := range order {
		resources = append(resources, css[href])
	}
	images, err := readImageResources(a, *book, opfPath, manifest, uniqueStrings(imageRefs))
	if err != nil {
		return nil, err
	}
	return append(resources, images...), nil
}

func expandStylesheets(roots []string, dependencies map[string][]string) []string {
	var out []string
	seen := map[string]bool{}
	var add func(string)
	add = func(href string) {
		if seen[href] {
			return
		}
		seen[href] = true
		for _, imported := range dependencies[href] {
			add(imported)
		}
		out = append(out, href)
	}
	for _, root := range roots {
		add(root)
	}
	return out
}

func normalizeCSSReferences(input, source string) (string, []string, []string, error) {
	var out strings.Builder
	var imports, urls []string
	for i := 0; i < len(input); {
		if input[i] == '/' && i+1 < len(input) && input[i+1] == '*' {
			end := strings.Index(input[i+2:], "*/")
			if end < 0 {
				return "", nil, nil, fmt.Errorf("unterminated CSS comment in %q", source)
			}
			i += end + 4
			continue
		}
		if strings.HasPrefix(strings.ToLower(input[i:]), "url(") {
			value, next, err := cssFunctionValue(input, i+4)
			if err != nil {
				return "", nil, nil, fmt.Errorf("CSS url in %q: %w", source, err)
			}
			href, local, err := normalizeCSSURL(source, value)
			if err != nil {
				return "", nil, nil, err
			}
			if local {
				urls = append(urls, href)
				out.WriteString(`url("` + href + `")`)
			} else {
				out.WriteString("url()")
			}
			i = next
			continue
		}
		if strings.HasPrefix(strings.ToLower(input[i:]), "@import") {
			j := i + 7
			for j < len(input) && unicode.IsSpace(rune(input[j])) {
				j++
			}
			var raw string
			if j < len(input) && (input[j] == '\'' || input[j] == '"') {
				raw, j = quotedCSS(input, j)
			} else if strings.HasPrefix(strings.ToLower(input[j:]), "url(") {
				var err error
				raw, j, err = cssFunctionValue(input, j+4)
				if err != nil {
					return "", nil, nil, err
				}
			}
			href, local, err := normalizeCSSURL(source, raw)
			if err != nil {
				return "", nil, nil, err
			}
			if local {
				imports = append(imports, href)
			}
			for j < len(input) && input[j] != ';' {
				j++
			}
			if j < len(input) {
				j++
			}
			i = j
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.String(), uniqueStrings(imports), uniqueStrings(urls), nil
}

func cssFunctionValue(s string, start int) (string, int, error) {
	i := start
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	var value string
	if i < len(s) && (s[i] == '\'' || s[i] == '"') {
		value, i = quotedCSS(s, i)
	} else {
		j := i
		for j < len(s) && s[j] != ')' {
			j++
		}
		value = strings.TrimSpace(s[i:j])
		i = j
	}
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	if i >= len(s) || s[i] != ')' {
		return "", i, fmt.Errorf("unterminated function")
	}
	return value, i + 1, nil
}

func quotedCSS(s string, start int) (string, int) {
	quote := s[start]
	var b strings.Builder
	for i := start + 1; i < len(s); i++ {
		if s[i] == quote {
			return b.String(), i + 1
		}
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String(), len(s)
}

func normalizeCSSURL(source, raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid CSS URL %q", raw)
	}
	if u.Scheme != "" || u.Host != "" {
		return "", false, nil
	}
	href, err := resolveReference(source, raw)
	if err != nil {
		return "", false, err
	}
	return referencePath(href), true, nil
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
