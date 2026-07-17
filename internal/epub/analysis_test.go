package epub

import (
	"strings"
	"testing"
)

func TestAnalyzeReturnsStructuredMetadataSpineTOCAndResources(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>分析书名</dc:title><dc:creator>作者</dc:creator><dc:language>zh-CN</dc:language></metadata><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`,
		"nav.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="c1.xhtml">正文</a></li></ol></nav></body></html>`,
		"c1.xhtml":               `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>正文页</title></head><body><p>内容</p></body></html>`,
	})
	analysis := Analyze(filename, Options{})
	if !analysis.Compatible() || analysis.Metadata.Title != "分析书名" || len(analysis.Spine) != 1 || analysis.Spine[0].Href != "c1.xhtml" || len(analysis.TOC) != 1 || len(analysis.Resources) != 2 {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestAnalyzeCollectsIndependentSpineAndTOCIssues(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata/><manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/><item id="missing" href="missing.xhtml" media-type="application/xhtml+xml"/><item id="bad" href="image.jpg" media-type="image/jpeg"/></manifest><spine><itemref idref="missing"/><itemref idref="bad"/><itemref idref="unknown"/></spine></package>`,
		"nav.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol><li><a href="outside.xhtml">错误目录</a></li></ol></nav></body></html>`,
	})
	analysis := Analyze(filename, Options{})
	want := map[string]bool{"spine_document_missing": false, "spine_media_type_unsupported": false, "spine_manifest_missing": false, "toc_target_not_in_spine": false}
	for _, issue := range analysis.Issues {
		if _, ok := want[issue.Code]; ok {
			want[issue.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing issue %s in %#v", code, analysis.Issues)
		}
	}
}

func TestAnalyzeLocatesCSSAndImageFailures(t *testing.T) {
	pngData, _ := encodedTestImages(t)
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/><item id="css" href="bad.css" media-type="text/css"/><item id="image" href="bad.jpg" media-type="image/jpeg"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><head><link rel="stylesheet" href="bad.css"/></head><body><img src="bad.jpg"/></body></html>`,
		"bad.css":                `body { background: url("unterminated"; }`,
		"bad.jpg":                string(pngData),
	})
	analysis := Analyze(filename, Options{})
	if analysis.Compatible() {
		t.Fatal("invalid resources unexpectedly passed")
	}
	foundLocation := false
	for _, issue := range analysis.Issues {
		if issue.Stage == "resources" && (strings.Contains(issue.Message, "bad.css") || strings.Contains(issue.Message, "bad.jpg")) {
			foundLocation = true
		}
	}
	if !foundLocation {
		t.Fatalf("resource issue lacks path: %#v", analysis.Issues)
	}
}

func TestAnalyzeLocatesBrokenBodyLinks(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="one" href="one.xhtml" media-type="application/xhtml+xml"/><item id="two" href="two.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="one"/><itemref idref="two"/></spine></package>`,
		"one.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml"><body><a href="two.xhtml#missing">坏锚点</a><a href="outside.xhtml">坏文档</a><a href="https://example.com">外链</a></body></html>`,
		"two.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml"><body><p id="present">正文</p></body></html>`,
	})
	analysis := Analyze(filename, Options{})
	want := map[string]CompatibilityIssue{}
	for _, issue := range analysis.Issues {
		want[issue.Code] = issue
	}
	fragment, ok := want["body_link_fragment_missing"]
	if !ok || fragment.Document != "one.xhtml" || fragment.Resource != "two.xhtml" || fragment.Reference != "two.xhtml#missing" {
		t.Fatalf("fragment issue = %#v", fragment)
	}
	target, ok := want["body_link_target_not_in_spine"]
	if !ok || target.Document != "one.xhtml" || target.Resource != "outside.xhtml" || target.Reference != "outside.xhtml" {
		t.Fatalf("target issue = %#v", target)
	}
}

func TestAnalyzeAcceptsBodyLinksToExistingFragments(t *testing.T) {
	filename := writeFixture(t, map[string]string{
		"META-INF/container.xml": containerFixture("book.opf"),
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata/><manifest><item id="one" href="one.xhtml" media-type="application/xhtml+xml"/><item id="two" href="two.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="one"/><itemref idref="two"/></spine></package>`,
		"one.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml"><body><a href="two.xhtml#present">有效链接</a></body></html>`,
		"two.xhtml":              `<html xmlns="http://www.w3.org/1999/xhtml"><body><p id="present">正文</p></body></html>`,
	})
	analysis := Analyze(filename, Options{})
	if !analysis.Compatible() {
		t.Fatalf("valid links failed compatibility: %#v", analysis.Issues)
	}
}
