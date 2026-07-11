package epub

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

func parseXMLTree(data []byte) (*ebook.Node, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	decoder.Strict = false
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		encoding, err := htmlindex.Get(label)
		if err != nil {
			return nil, err
		}
		return transform.NewReader(input, encoding.NewDecoder()), nil
	}
	var stack []*ebook.Node
	var root *ebook.Node
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse xml: %w", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			node := ebook.Element(strings.ToLower(token.Name.Local), xmlAttrs(token.Attr))
			if len(stack) == 0 {
				root = node
			} else {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, node)
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 0 && len(token) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, ebook.Text(string(token)))
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("xml document has no root element")
	}
	return root, nil
}

func xmlAttrs(attrs []xml.Attr) []ebook.Attr {
	out := make([]ebook.Attr, 0, len(attrs))
	for _, attr := range attrs {
		key := strings.ToLower(attr.Name.Local)
		if attr.Name.Space == "http://www.idpf.org/2007/ops" {
			key = "epub:" + key
		}
		if key == "xmlns" {
			continue
		}
		out = append(out, ebook.A(key, attr.Value))
	}
	return out
}

func xhtmlBody(data []byte, sourcePath string) (*ebook.Node, string, error) {
	root, err := parseXMLTree(data)
	if err != nil {
		return nil, "", err
	}
	body := findElement(root, "body")
	if body == nil {
		return nil, "", fmt.Errorf("xhtml %q has no body", sourcePath)
	}
	cleaned := sanitizeNode(body, sourcePath)
	if len(cleaned) != 1 {
		return nil, "", fmt.Errorf("xhtml %q produced invalid body", sourcePath)
	}
	title := strings.TrimSpace(textContent(findElement(root, "title")))
	if title == "" {
		for _, heading := range []string{"h1", "h2", "h3", "h4", "h5", "h6"} {
			if node := findElement(cleaned[0], heading); node != nil {
				title = strings.TrimSpace(textContent(node))
				break
			}
		}
	}
	return cleaned[0], title, nil
}

var keptElements = map[string]bool{
	"body": true, "section": true, "div": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"p": true, "blockquote": true, "ol": true, "ul": true, "li": true,
	"br": true, "a": true, "span": true, "em": true, "strong": true,
	"b": true, "i": true, "aside": true, "sup": true, "sub": true,
}

var droppedElements = map[string]bool{
	"script": true, "style": true, "form": true, "input": true, "button": true,
	"select": true, "textarea": true, "object": true, "embed": true, "iframe": true,
	"svg": true,
}

var keptAttrs = map[string]bool{
	"id": true, "class": true, "href": true, "src": true,
	"lang": true, "title": true, "role": true, "type": true, "name": true,
	"epub:type": true,
}

func sanitizeNode(node *ebook.Node, sourcePath string) []*ebook.Node {
	if node == nil {
		return nil
	}
	if node.Type == ebook.TextNode {
		return []*ebook.Node{ebook.Text(node.Data)}
	}
	name := strings.ToLower(node.Data)
	if droppedElements[name] {
		return nil
	}
	children := make([]*ebook.Node, 0, len(node.Children))
	for _, child := range node.Children {
		children = append(children, sanitizeNode(child, sourcePath)...)
	}
	if !keptElements[name] {
		return children
	}
	attrs := make([]ebook.Attr, 0, len(node.Attr))
	hasID := ebook.AttrValue(node, "id") != ""
	for _, attr := range node.Attr {
		key := strings.ToLower(attr.Key)
		if !keptAttrs[key] {
			continue
		}
		value := attr.Val
		if key == "href" || key == "src" {
			if normalized, err := resolveReference(sourcePath, value); err == nil {
				value = normalized
			}
		}
		attrs = append(attrs, ebook.A(key, value))
		if key == "name" && !hasID && strings.EqualFold(name, "a") && strings.TrimSpace(value) != "" {
			attrs = append(attrs, ebook.A("id", value))
			hasID = true
		}
	}
	return []*ebook.Node{ebook.Element(name, attrs, children...)}
}

func findElement(node *ebook.Node, name string) *ebook.Node {
	if node == nil {
		return nil
	}
	if node.Type == ebook.ElementNode && strings.EqualFold(node.Data, name) {
		return node
	}
	for _, child := range node.Children {
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func directElements(node *ebook.Node, name string) []*ebook.Node {
	var out []*ebook.Node
	if node == nil {
		return out
	}
	for _, child := range node.Children {
		if child.Type == ebook.ElementNode && strings.EqualFold(child.Data, name) {
			out = append(out, child)
		}
	}
	return out
}

func textContent(node *ebook.Node) string {
	if node == nil {
		return ""
	}
	if node.Type == ebook.TextNode {
		return node.Data
	}
	var out strings.Builder
	for _, child := range node.Children {
		out.WriteString(textContent(child))
	}
	return out.String()
}
