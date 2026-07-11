package azw3

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type resourceCatalog struct {
	resources []compiledResource
	byHref    map[string]int
}

func compileResources(resources []ebook.Resource) (resourceCatalog, error) {
	catalog := resourceCatalog{byHref: make(map[string]int, len(resources))}
	byDigest := map[[32]byte]int{}
	for _, resource := range resources {
		mediaType := strings.ToLower(strings.TrimSpace(resource.MediaType))
		if mediaType != "image/jpeg" && mediaType != "image/png" {
			return resourceCatalog{}, fmt.Errorf("unsupported AZW3 resource media type %q for %q", resource.MediaType, resource.Href)
		}
		if len(resource.Data) == 0 {
			return resourceCatalog{}, fmt.Errorf("empty AZW3 resource %q", resource.Href)
		}
		digest := sha256.Sum256(resource.Data)
		index, ok := byDigest[digest]
		if !ok {
			index = len(catalog.resources)
			byDigest[digest] = index
			catalog.resources = append(catalog.resources, compiledResource{mediaType: mediaType, data: resource.Data})
		}
		catalog.byHref[resource.Href] = index
	}
	return catalog, nil
}

func rewriteImageReferences(documents []preparedDocument, catalog resourceCatalog) error {
	for _, doc := range documents {
		var walk func(*ebook.Node) error
		walk = func(node *ebook.Node) error {
			if node == nil || node.Type != ebook.ElementNode {
				return nil
			}
			if strings.EqualFold(node.Data, "img") {
				for i := range node.Attr {
					if !strings.EqualFold(node.Attr[i].Key, "src") {
						continue
					}
					raw := strings.TrimSpace(node.Attr[i].Val)
					if strings.HasPrefix(raw, "kindle:embed:") {
						break
					}
					u, err := url.Parse(raw)
					if err != nil || u.Scheme != "" || u.Host != "" {
						return fmt.Errorf("unsupported image source %q in %q", raw, doc.document.Href)
					}
					index, ok := catalog.byHref[raw]
					if !ok {
						return fmt.Errorf("image source %q in %q has no resource", raw, doc.document.Href)
					}
					resource := catalog.resources[index]
					node.Attr[i].Val = fmt.Sprintf("kindle:embed:%s?mime=%s", paddedBase32(index+1, 4), resource.mediaType)
					break
				}
			}
			for _, child := range node.Children {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(doc.document.Body); err != nil {
			return err
		}
	}
	return nil
}
