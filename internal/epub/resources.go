package epub

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

const maxImageResourceBytes = 64 << 20
const maxTotalImageBytes = 128 << 20

func readImageResources(a *archive, book ebook.Book, opfPath string, manifest []manifestItem, extraRefs []string) ([]ebook.Resource, error) {
	items := make(map[string]manifestItem, len(manifest))
	for _, item := range manifest {
		href, err := resolveReference(opfPath, item.Href)
		if err == nil {
			items[referencePath(href)] = item
		}
	}
	var refs []string
	seen := map[string]bool{}
	for _, doc := range book.Spine {
		collectImageRefs(doc.Body, &refs, seen)
	}
	for _, href := range extraRefs {
		if !seen[href] {
			seen[href] = true
			refs = append(refs, href)
		}
	}
	if book.Cover != nil && book.Cover.ImageHref != "" && !seen[book.Cover.ImageHref] {
		seen[book.Cover.ImageHref] = true
		refs = append(refs, book.Cover.ImageHref)
	}
	if len(refs) == 0 {
		return nil, nil
	}
	resources := make([]ebook.Resource, 0, len(refs))
	total := 0
	for _, href := range refs {
		u, err := url.Parse(href)
		if err != nil || u.Scheme != "" || u.Host != "" {
			return nil, fmt.Errorf("unsupported remote image reference %q", href)
		}
		item, ok := items[href]
		if !ok {
			return nil, fmt.Errorf("image reference %q is missing from manifest", href)
		}
		mediaType := strings.ToLower(strings.TrimSpace(item.MediaType))
		if mediaType != "image/jpeg" && mediaType != "image/png" && mediaType != "image/svg+xml" {
			return nil, fmt.Errorf("unsupported image media type %q for %q", item.MediaType, href)
		}
		size, err := a.size(href)
		if err != nil {
			return nil, err
		}
		if size > maxImageResourceBytes || uint64(total)+size > maxTotalImageBytes {
			return nil, fmt.Errorf("image resources exceed size limit at %q", href)
		}
		data, err := a.read(href)
		if err != nil {
			return nil, err
		}
		if mediaType == "image/svg+xml" {
			root, err := parseXMLTree(data)
			if err != nil || root == nil || !strings.EqualFold(root.Data, "svg") {
				return nil, fmt.Errorf("decode SVG image %q: invalid SVG", href)
			}
		} else {
			_, format, err := image.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return nil, fmt.Errorf("decode image %q: %w", href, err)
			}
			actual := map[string]string{"jpeg": "image/jpeg", "png": "image/png"}[format]
			if actual != mediaType {
				return nil, fmt.Errorf("image %q declares %q but contains %q", href, mediaType, actual)
			}
		}
		total += len(data)
		resources = append(resources, ebook.Resource{Href: href, MediaType: mediaType, Data: data})
	}
	return resources, nil
}

func collectImageRefs(node *ebook.Node, refs *[]string, seen map[string]bool) {
	if node == nil || node.Type != ebook.ElementNode {
		return
	}
	if strings.EqualFold(node.Data, "img") {
		if href := strings.TrimSpace(ebook.AttrValue(node, "src")); href != "" && !seen[href] {
			seen[href] = true
			*refs = append(*refs, href)
		}
	}
	for _, child := range node.Children {
		collectImageRefs(child, refs, seen)
	}
}
