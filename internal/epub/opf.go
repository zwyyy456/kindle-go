package epub

import (
	"encoding/xml"
	"fmt"
	"strings"
)

type packageDocument struct {
	Metadata struct {
		Titles      []string `xml:"title"`
		Creators    []string `xml:"creator"`
		Languages   []string `xml:"language"`
		Identifiers []string `xml:"identifier"`
	} `xml:"metadata"`
	Manifest struct {
		Items []manifestItem `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		TOC      string         `xml:"toc,attr"`
		ItemRefs []spineItemRef `xml:"itemref"`
	} `xml:"spine"`
	Guide struct {
		References []guideReference `xml:"reference"`
	} `xml:"guide"`
}

type manifestItem struct {
	ID         string `xml:"id,attr"`
	Href       string `xml:"href,attr"`
	MediaType  string `xml:"media-type,attr"`
	Properties string `xml:"properties,attr"`
}

type spineItemRef struct {
	IDRef  string `xml:"idref,attr"`
	Linear string `xml:"linear,attr"`
}

type guideReference struct {
	Type  string `xml:"type,attr"`
	Title string `xml:"title,attr"`
	Href  string `xml:"href,attr"`
}

func readPackage(a *archive, opfPath string) (packageDocument, error) {
	data, err := a.read(opfPath)
	if err != nil {
		return packageDocument{}, err
	}
	var pkg packageDocument
	if err := xml.Unmarshal(data, &pkg); err != nil {
		return packageDocument{}, fmt.Errorf("parse package document %q: %w", opfPath, err)
	}
	if len(pkg.Manifest.Items) == 0 {
		return packageDocument{}, fmt.Errorf("package document %q has no manifest", opfPath)
	}
	if len(pkg.Spine.ItemRefs) == 0 {
		return packageDocument{}, fmt.Errorf("package document %q has no spine", opfPath)
	}
	return pkg, nil
}

func firstNonBlank(values []string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func nonBlank(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func hasProperty(properties, want string) bool {
	for _, property := range strings.Fields(properties) {
		if property == want {
			return true
		}
	}
	return false
}
