package epub

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type Analysis struct {
	Book      ebook.Book
	Metadata  MetadataInfo
	Cover     CoverInfo
	Spine     []SpineInfo
	TOC       []TOCInfo
	Resources []ResourceInfo
	Issues    []CompatibilityIssue
}

type MetadataInfo struct {
	Title, Author, Language, Identifier string
}

type CoverInfo struct {
	ImageHref, TitlePageHref string
}

type SpineInfo struct {
	IDRef, Href, MediaType, Title string
	Linear                        bool
}

type TOCInfo struct {
	Title, Href string
	Children    []TOCInfo
}

type ResourceInfo struct {
	ID, Href, MediaType, Properties string
	Size                            uint64
	Exists                          bool
}

type CompatibilityIssue struct {
	Code, Stage, Document, Resource, Reference, Message string
}

func (a Analysis) Compatible() bool { return len(a.Issues) == 0 }

func Analyze(filename string, opts Options) Analysis {
	var result Analysis
	a, err := openArchive(filename)
	if err != nil {
		result.Issues = append(result.Issues, issue("epub_open_failed", "archive", "", "", filename, err))
		return result
	}
	defer a.Close()
	for _, name := range a.invalidPaths {
		result.Issues = append(result.Issues, CompatibilityIssue{Code: "archive_path_unsafe", Stage: "archive", Resource: name, Reference: name, Message: fmt.Sprintf("unsafe EPUB archive path %q", name)})
	}
	opfPath, err := readRootfile(a)
	if err != nil {
		result.Issues = append(result.Issues, issue(codeForError("container", err), "container", "META-INF/container.xml", "", "META-INF/container.xml", err))
		return result
	}
	pkg, err := readPackage(a, opfPath)
	if err != nil {
		result.Issues = append(result.Issues, issue(codeForError("package", err), "package", opfPath, "", opfPath, err))
		return result
	}
	items := make(map[string]manifestItem, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		items[item.ID] = item
		info := ResourceInfo{ID: item.ID, MediaType: item.MediaType, Properties: item.Properties}
		href, resolveErr := resolveReference(opfPath, item.Href)
		if resolveErr != nil {
			result.Issues = append(result.Issues, issue("manifest_reference_invalid", "manifest", opfPath, item.Href, item.Href, resolveErr))
			info.Href = item.Href
		} else {
			info.Href = referencePath(href)
			if size, sizeErr := a.size(info.Href); sizeErr == nil {
				info.Exists, info.Size = true, size
			} else {
				result.Issues = append(result.Issues, issue("manifest_resource_missing", "manifest", opfPath, info.Href, item.Href, sizeErr))
			}
		}
		result.Resources = append(result.Resources, info)
	}
	result.Book.Metadata = ebook.Metadata{
		Title: firstNonBlank(pkg.Metadata.Titles), Author: strings.Join(nonBlank(pkg.Metadata.Creators), " & "),
		Language: firstNonBlank(pkg.Metadata.Languages), Identifier: firstNonBlank(pkg.Metadata.Identifiers),
	}
	if result.Book.Metadata.Title == "" {
		result.Book.Metadata.Title = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	if result.Book.Metadata.Language == "" {
		result.Book.Metadata.Language = strings.TrimSpace(opts.DefaultLanguage)
	}
	result.Metadata = MetadataInfo{Title: result.Book.Metadata.Title, Author: result.Book.Metadata.Author, Language: result.Book.Metadata.Language, Identifier: result.Book.Metadata.Identifier}
	cover, coverErr := readCover(a, opfPath, pkg, items)
	if coverErr != nil {
		result.Issues = append(result.Issues, issue(codeForError("cover", coverErr), "cover", opfPath, referencedResource(coverErr, result.Resources), "", coverErr))
	} else {
		result.Book.Cover = cover
		if cover != nil {
			result.Cover = CoverInfo{ImageHref: cover.ImageHref, TitlePageHref: cover.TitlePageHref}
		}
	}
	for index, ref := range pkg.Spine.ItemRefs {
		item, ok := items[ref.IDRef]
		info := SpineInfo{IDRef: ref.IDRef, Linear: !strings.EqualFold(ref.Linear, "no")}
		if !ok {
			result.Issues = append(result.Issues, CompatibilityIssue{Code: "spine_manifest_missing", Stage: "spine", Document: ref.IDRef, Reference: ref.IDRef, Message: fmt.Sprintf("spine item %q is missing from manifest", ref.IDRef)})
			result.Spine = append(result.Spine, info)
			continue
		}
		info.MediaType = item.MediaType
		if item.MediaType != "application/xhtml+xml" && item.MediaType != "text/html" {
			result.Issues = append(result.Issues, CompatibilityIssue{Code: "spine_media_type_unsupported", Stage: "spine", Document: item.Href, Resource: item.Href, Reference: ref.IDRef, Message: fmt.Sprintf("unsupported spine media type %q for %q", item.MediaType, item.Href)})
			result.Spine = append(result.Spine, info)
			continue
		}
		docPath, resolveErr := resolveReference(opfPath, item.Href)
		if resolveErr != nil {
			result.Issues = append(result.Issues, issue("spine_reference_invalid", "spine", item.Href, item.Href, item.Href, resolveErr))
			result.Spine = append(result.Spine, info)
			continue
		}
		info.Href = referencePath(docPath)
		data, readErr := a.read(info.Href)
		if readErr != nil {
			result.Issues = append(result.Issues, issue("spine_document_missing", "spine", info.Href, info.Href, item.Href, readErr))
			result.Spine = append(result.Spine, info)
			continue
		}
		body, title, stylesheets, parseErr := xhtmlBody(data, info.Href)
		if parseErr != nil {
			result.Issues = append(result.Issues, issue("spine_document_invalid", "spine", info.Href, info.Href, item.Href, parseErr))
			result.Spine = append(result.Spine, info)
			continue
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", index+1)
		}
		info.Title = title
		result.Spine = append(result.Spine, info)
		result.Book.Spine = append(result.Book.Spine, ebook.Document{Href: info.Href, Title: title, Body: body, Stylesheets: stylesheets})
	}
	spinePaths := make(map[string]bool, len(result.Book.Spine))
	for _, document := range result.Book.Spine {
		spinePaths[document.Href] = true
	}
	collectBodyLinkIssues(result.Book.Spine, spinePaths, &result.Issues)
	toc, tocErr := readTOC(a, opfPath, pkg, items)
	if tocErr != nil {
		result.Issues = append(result.Issues, issue(codeForError("toc", tocErr), "toc", "", referencedResource(tocErr, result.Resources), "", tocErr))
	} else {
		result.Book.TOC = toc
		result.TOC = tocInfo(toc)
		collectTOCIssues(toc, spinePaths, &result.Issues)
	}
	for _, ref := range pkg.Guide.References {
		href, guideErr := resolveReference(opfPath, ref.Href)
		if guideErr != nil {
			result.Issues = append(result.Issues, issue("guide_reference_invalid", "guide", opfPath, "", ref.Href, guideErr))
			continue
		}
		if spinePaths[referencePath(href)] {
			result.Book.Guide = append(result.Book.Guide, ebook.GuideRef{Type: ref.Type, Title: ref.Title, Href: href})
		}
	}
	resources, resourceErr := readResources(a, &result.Book, opfPath, pkg.Manifest.Items)
	if resourceErr != nil {
		result.Issues = append(result.Issues, issue(codeForError("resources", resourceErr), "resources", "", referencedResource(resourceErr, result.Resources), "", resourceErr))
	} else {
		result.Book.Resources = resources
	}
	return result
}

func issue(code, stage, document, resource, reference string, err error) CompatibilityIssue {
	return CompatibilityIssue{Code: code, Stage: stage, Document: document, Resource: resource, Reference: reference, Message: err.Error()}
}

func codeForError(stage string, err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not found") || strings.Contains(message, "missing"):
		return stage + "_resource_missing"
	case strings.Contains(message, "css") || strings.Contains(message, "stylesheet"):
		return "css_invalid"
	case strings.Contains(message, "image") || strings.Contains(message, "svg"):
		return "image_invalid"
	case strings.Contains(message, "link") || strings.Contains(message, "target"):
		return "link_invalid"
	default:
		return stage + "_invalid"
	}
}

func referencedResource(err error, resources []ResourceInfo) string {
	message := err.Error()
	for _, resource := range resources {
		if resource.Href != "" && strings.Contains(message, resource.Href) {
			return resource.Href
		}
	}
	return ""
}

func tocInfo(entries []ebook.TOCEntry) []TOCInfo {
	result := make([]TOCInfo, 0, len(entries))
	for _, entry := range entries {
		result = append(result, TOCInfo{Title: entry.Title, Href: entry.Href, Children: tocInfo(entry.Children)})
	}
	return result
}

func collectTOCIssues(entries []ebook.TOCEntry, spine map[string]bool, issues *[]CompatibilityIssue) {
	for _, entry := range entries {
		if !spine[referencePath(entry.Href)] {
			*issues = append(*issues, CompatibilityIssue{Code: "toc_target_not_in_spine", Stage: "toc", Document: referencePath(entry.Href), Reference: entry.Href, Message: fmt.Sprintf("TOC target %q is not present in the spine", entry.Href)})
		}
		collectTOCIssues(entry.Children, spine, issues)
	}
}

func collectBodyLinkIssues(documents []ebook.Document, spine map[string]bool, issues *[]CompatibilityIssue) {
	anchors := make(map[string]map[string]bool, len(documents))
	for _, document := range documents {
		anchors[document.Href] = collectAnchors(document.Body)
	}
	seen := map[string]bool{}
	for _, document := range documents {
		var links []string
		collectLinks(document.Body, &links)
		for _, href := range links {
			u, err := url.Parse(href)
			if err != nil {
				appendLinkIssue(document.Href, href, "body_link_reference_invalid", "", err.Error(), seen, issues)
				continue
			}
			if u.Host != "" || u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto" || u.Scheme == "tel" {
				continue
			}
			if u.Scheme != "" {
				appendLinkIssue(document.Href, href, "body_link_scheme_unsupported", "", fmt.Sprintf("body link %q in %q uses unsupported scheme %q", href, document.Href, u.Scheme), seen, issues)
				continue
			}
			target := referencePath(href)
			if !spine[target] {
				appendLinkIssue(document.Href, href, "body_link_target_not_in_spine", target, fmt.Sprintf("body link %q in %q targets a document outside the spine", href, document.Href), seen, issues)
				continue
			}
			if u.Fragment != "" && !anchors[target][u.Fragment] {
				appendLinkIssue(document.Href, href, "body_link_fragment_missing", target, fmt.Sprintf("body link %q in %q targets missing fragment %q", href, document.Href, u.Fragment), seen, issues)
			}
		}
	}
}

func collectAnchors(node *ebook.Node) map[string]bool {
	anchors := map[string]bool{}
	var walk func(*ebook.Node)
	walk = func(current *ebook.Node) {
		if current == nil || current.Type != ebook.ElementNode {
			return
		}
		if id := strings.TrimSpace(ebook.AttrValue(current, "id")); id != "" {
			anchors[id] = true
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(node)
	return anchors
}

func collectLinks(node *ebook.Node, links *[]string) {
	if node == nil || node.Type != ebook.ElementNode {
		return
	}
	if strings.EqualFold(node.Data, "a") {
		if href := strings.TrimSpace(ebook.AttrValue(node, "href")); href != "" {
			*links = append(*links, href)
		}
	}
	for _, child := range node.Children {
		collectLinks(child, links)
	}
}

func appendLinkIssue(document, reference, code, resource, message string, seen map[string]bool, issues *[]CompatibilityIssue) {
	key := document + "\x00" + code + "\x00" + reference
	if seen[key] {
		return
	}
	seen[key] = true
	*issues = append(*issues, CompatibilityIssue{Code: code, Stage: "links", Document: document, Resource: resource, Reference: reference, Message: message})
}
