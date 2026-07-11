package epub

import (
	"encoding/xml"
	"fmt"
)

type containerXML struct {
	Rootfiles []struct {
		FullPath  string `xml:"full-path,attr"`
		MediaType string `xml:"media-type,attr"`
	} `xml:"rootfiles>rootfile"`
}

func readRootfile(a *archive) (string, error) {
	data, err := a.read("META-INF/container.xml")
	if err != nil {
		return "", err
	}
	var container containerXML
	if err := xml.Unmarshal(data, &container); err != nil {
		return "", fmt.Errorf("parse META-INF/container.xml: %w", err)
	}
	if len(container.Rootfiles) == 0 {
		return "", fmt.Errorf("container.xml has no rootfile")
	}
	for _, root := range container.Rootfiles {
		if root.MediaType == "application/oebps-package+xml" {
			return cleanArchivePath(root.FullPath)
		}
	}
	return cleanArchivePath(container.Rootfiles[0].FullPath)
}
