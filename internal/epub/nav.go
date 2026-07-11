package epub

import (
	"fmt"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func readNav(data []byte, navPath string) ([]ebook.TOCEntry, error) {
	root, err := parseXMLTree(data)
	if err != nil {
		return nil, fmt.Errorf("parse EPUB3 nav %q: %w", navPath, err)
	}
	var navs []*ebook.Node
	collectElements(root, "nav", &navs)
	var toc *ebook.Node
	for _, nav := range navs {
		if hasToken(ebook.AttrValue(nav, "type"), "toc") {
			toc = nav
			break
		}
	}
	if toc == nil && len(navs) == 1 {
		toc = navs[0]
	}
	if toc == nil {
		return nil, fmt.Errorf("EPUB3 nav %q has no toc nav", navPath)
	}
	ol := findElement(toc, "ol")
	if ol == nil {
		return nil, fmt.Errorf("EPUB3 nav %q has no ordered list", navPath)
	}
	return navListEntries(ol, navPath), nil
}

func navListEntries(ol *ebook.Node, navPath string) []ebook.TOCEntry {
	var entries []ebook.TOCEntry
	for _, li := range directElements(ol, "li") {
		link := firstElementBeforeNestedList(li, "a")
		if link == nil {
			continue
		}
		href, err := resolveReference(navPath, ebook.AttrValue(link, "href"))
		if err != nil {
			continue
		}
		entry := ebook.TOCEntry{Title: strings.TrimSpace(textContent(link)), Href: href}
		for _, childOL := range directElements(li, "ol") {
			entry.Children = append(entry.Children, navListEntries(childOL, navPath)...)
		}
		entries = append(entries, entry)
	}
	return entries
}

func firstElementBeforeNestedList(node *ebook.Node, name string) *ebook.Node {
	for _, child := range node.Children {
		if child.Type != ebook.ElementNode {
			continue
		}
		if child.Data == "ol" || child.Data == "ul" {
			break
		}
		if child.Data == name {
			return child
		}
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func readNCX(data []byte, ncxPath string) ([]ebook.TOCEntry, error) {
	root, err := parseXMLTree(data)
	if err != nil {
		return nil, fmt.Errorf("parse EPUB2 NCX %q: %w", ncxPath, err)
	}
	navMap := findElement(root, "navmap")
	if navMap == nil {
		return nil, fmt.Errorf("EPUB2 NCX %q has no navMap", ncxPath)
	}
	return ncxEntries(navMap, ncxPath), nil
}

func ncxEntries(parent *ebook.Node, ncxPath string) []ebook.TOCEntry {
	var entries []ebook.TOCEntry
	for _, point := range directElements(parent, "navpoint") {
		label := findElement(findElement(point, "navlabel"), "text")
		content := findElement(point, "content")
		href, err := resolveReference(ncxPath, ebook.AttrValue(content, "src"))
		if err != nil {
			continue
		}
		entries = append(entries, ebook.TOCEntry{
			Title:    strings.TrimSpace(textContent(label)),
			Href:     href,
			Children: ncxEntries(point, ncxPath),
		})
	}
	return entries
}

func collectElements(node *ebook.Node, name string, out *[]*ebook.Node) {
	if node == nil {
		return
	}
	if node.Type == ebook.ElementNode && strings.EqualFold(node.Data, name) {
		*out = append(*out, node)
	}
	for _, child := range node.Children {
		collectElements(child, name, out)
	}
}

func hasToken(value, want string) bool {
	for _, token := range strings.Fields(value) {
		if token == want {
			return true
		}
	}
	return false
}
