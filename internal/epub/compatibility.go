package epub

import "encoding/json"

// CompatibilitySnapshot is the stable serialized form persisted for an EPUB
// compatibility analysis. File identity and check timing belong to callers.
type CompatibilitySnapshot struct {
	Status        string
	MetadataJSON  string
	SpineJSON     string
	TOCJSON       string
	ResourcesJSON string
	IssuesJSON    string
}

// CompatibilitySnapshot serializes the user-visible parts of an analysis for
// persistence without exposing the parsed ebook.Book implementation model.
func (a Analysis) CompatibilitySnapshot() (CompatibilitySnapshot, error) {
	metadata, err := json.Marshal(struct {
		Metadata MetadataInfo `json:"metadata"`
		Cover    CoverInfo    `json:"cover"`
	}{a.Metadata, a.Cover})
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	spine, err := json.Marshal(a.Spine)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	toc, err := json.Marshal(a.TOC)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	resources, err := json.Marshal(a.Resources)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	issues, err := json.Marshal(a.Issues)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	status := "passed"
	if !a.Compatible() {
		status = "failed"
	}
	return CompatibilitySnapshot{
		Status: status, MetadataJSON: string(metadata), SpineJSON: string(spine), TOCJSON: string(toc),
		ResourcesJSON: string(resources), IssuesJSON: string(issues),
	}, nil
}
