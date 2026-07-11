package azw3

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

const internalLinkPlaceholder = "kindle:pos:fid:0000:off:0000000000"

type internalLink struct {
	sourceHref string
	targetHref string
	node       *ebook.Node
	attrIndex  int
}

func prepareInternalLinks(documents []preparedDocument) ([]internalLink, error) {
	var links []internalLink
	documentHrefs := make(map[string]bool, len(documents))
	for _, doc := range documents {
		documentHrefs[doc.document.Href] = true
	}
	for _, doc := range documents {
		var walk func(*ebook.Node) error
		walk = func(node *ebook.Node) error {
			if node == nil || node.Type != ebook.ElementNode {
				return nil
			}
			if strings.EqualFold(node.Data, "a") {
				for i := range node.Attr {
					if !strings.EqualFold(node.Attr[i].Key, "href") {
						continue
					}
					target, internal, err := normalizeInternalHref(doc.document.Href, node.Attr[i].Val, documentHrefs)
					if err != nil {
						return fmt.Errorf("resolve link in %q: %w", doc.document.Href, err)
					}
					if internal {
						links = append(links, internalLink{sourceHref: doc.document.Href, targetHref: target, node: node, attrIndex: i})
						node.Attr[i].Val = internalLinkPlaceholder
					}
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
			return nil, err
		}
	}
	return links, nil
}

func resolveInternalLinks(links []internalLink, targets map[string]target) error {
	for _, link := range links {
		target, ok := targets[link.targetHref]
		if !ok {
			return fmt.Errorf("internal link %q in %q has no target", link.targetHref, link.sourceHref)
		}
		value := fmt.Sprintf("kindle:pos:fid:%s:off:%s", paddedBase32(target.chunkSeq, 4), paddedBase32(target.chunkOffset, 10))
		if len(value) != len(internalLinkPlaceholder) {
			return fmt.Errorf("internal link position exceeds KF8 field width for %q", link.targetHref)
		}
		link.node.Attr[link.attrIndex].Val = value
	}
	return nil
}

func normalizeInternalHref(sourceHref, raw string, documentHrefs map[string]bool) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("parse href %q: %w", raw, err)
	}
	if u.Scheme != "" || u.Host != "" || strings.HasPrefix(raw, "//") {
		return raw, false, nil
	}
	if u.Path == "" {
		u.Path = sourceHref
	} else if documentHrefs[u.Path] {
		// EPUB readers normalize local references to archive-root paths.
	} else if !strings.HasPrefix(u.Path, "/") {
		u.Path = path.Clean(path.Join(path.Dir(sourceHref), u.Path))
	} else {
		u.Path = strings.TrimPrefix(path.Clean(u.Path), "/")
	}
	target := u.Path
	if u.Fragment != "" {
		target += "#" + u.Fragment
	}
	return target, true, nil
}

func paddedBase32(value, width int) string {
	encoded := base32(value)
	if len(encoded) >= width {
		return encoded
	}
	return strings.Repeat("0", width-len(encoded)) + encoded
}
