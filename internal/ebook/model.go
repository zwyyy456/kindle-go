package ebook

type Book struct {
	Metadata  Metadata
	Style     Style
	Spine     []Document
	Resources []Resource
	TOC       []TOCEntry
	Guide     []GuideRef
}

type Metadata struct {
	Title      string
	Author     string
	Language   string
	Identifier string
}

type Style struct {
	LineHeight       float64
	ParagraphIndent  string
	ParagraphSpacing string
	TextAlign        string
}

type Document struct {
	Href        string
	Title       string
	Body        *Node
	Stylesheets []string
}

type Node struct {
	Type     NodeType
	Data     string
	Attr     []Attr
	Children []*Node
}

type NodeType int

const (
	ElementNode NodeType = iota
	TextNode
)

type Attr struct {
	Key string
	Val string
}

type Resource struct {
	Href      string
	MediaType string
	Data      []byte
}

type TOCEntry struct {
	Title    string
	Href     string
	Children []TOCEntry
}

type GuideRef struct {
	Type  string
	Title string
	Href  string
}

func Element(name string, attr []Attr, children ...*Node) *Node {
	return &Node{Type: ElementNode, Data: name, Attr: attr, Children: children}
}

func Text(s string) *Node {
	return &Node{Type: TextNode, Data: s}
}

func A(key, val string) Attr {
	return Attr{Key: key, Val: val}
}

func AttrValue(n *Node, key string) string {
	if n == nil {
		return ""
	}
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}
