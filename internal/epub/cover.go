package epub

import (
	"fmt"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func readCover(a *archive, opfPath string, pkg packageDocument, items map[string]manifestItem) (*ebook.Cover, error) {
	var imageHref string
	for _, item := range pkg.Manifest.Items {
		if hasProperty(item.Properties, "cover-image") {
			href, err := resolveReference(opfPath, item.Href)
			if err != nil {
				return nil, err
			}
			imageHref = referencePath(href)
			break
		}
	}
	if imageHref == "" {
		for _, meta := range pkg.Metadata.Metas {
			if !strings.EqualFold(strings.TrimSpace(meta.Name), "cover") {
				continue
			}
			if item, ok := items[strings.TrimSpace(meta.Content)]; ok {
				href, err := resolveReference(opfPath, item.Href)
				if err != nil {
					return nil, err
				}
				imageHref = referencePath(href)
				break
			}
		}
	}
	pageHref, err := coverPageHref(a, opfPath, pkg)
	if err != nil {
		return nil, err
	}
	if pageHref != "" {
		pageImage, err := imageFromCoverPage(a, pageHref)
		if err != nil {
			return nil, err
		}
		if imageHref == "" {
			imageHref = pageImage
		}
	}
	if imageHref == "" {
		return nil, nil
	}
	return &ebook.Cover{ImageHref: imageHref, TitlePageHref: pageHref}, nil
}

func coverPageHref(a *archive, opfPath string, pkg packageDocument) (string, error) {
	for _, ref := range pkg.Guide.References {
		if strings.EqualFold(strings.TrimSpace(ref.Type), "cover") {
			href, err := resolveReference(opfPath, ref.Href)
			if err != nil {
				return "", err
			}
			return referencePath(href), nil
		}
	}
	for _, item := range pkg.Manifest.Items {
		if !hasProperty(item.Properties, "nav") {
			continue
		}
		navHref, err := resolveReference(opfPath, item.Href)
		if err != nil {
			return "", err
		}
		data, err := a.read(referencePath(navHref))
		if err != nil {
			return "", err
		}
		root, err := parseXMLTree(data)
		if err != nil {
			return "", err
		}
		if raw := landmarkCoverLink(root); raw != "" {
			href, err := resolveReference(referencePath(navHref), raw)
			if err != nil {
				return "", err
			}
			return referencePath(href), nil
		}
	}
	return "", nil
}

func landmarkCoverLink(root *ebook.Node) string {
	var searchLinks func(*ebook.Node) string
	searchLinks = func(n *ebook.Node) string {
		if n == nil || n.Type != ebook.ElementNode {
			return ""
		}
		if n.Data == "a" && hasToken(ebook.AttrValue(n, "epub:type"), "cover") {
			return ebook.AttrValue(n, "href")
		}
		for _, c := range n.Children {
			if href := searchLinks(c); href != "" {
				return href
			}
		}
		return ""
	}
	var walk func(*ebook.Node) string
	walk = func(n *ebook.Node) string {
		if n == nil || n.Type != ebook.ElementNode {
			return ""
		}
		if n.Data == "nav" && hasToken(ebook.AttrValue(n, "epub:type"), "landmarks") {
			return searchLinks(n)
		}
		for _, c := range n.Children {
			if href := walk(c); href != "" {
				return href
			}
		}
		return ""
	}
	return walk(root)
}

func imageFromCoverPage(a *archive, page string) (string, error) {
	data, err := a.read(referencePath(page))
	if err != nil {
		return "", err
	}
	root, err := parseXMLTree(data)
	if err != nil {
		return "", fmt.Errorf("parse cover page %q: %w", page, err)
	}
	img := findElement(root, "img")
	if img == nil {
		return "", nil
	}
	href, err := resolveReference(referencePath(page), ebook.AttrValue(img, "src"))
	if err != nil {
		return "", err
	}
	return referencePath(href), nil
}
