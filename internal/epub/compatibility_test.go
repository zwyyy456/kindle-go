package epub

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCompatibilitySnapshotEncodesAnalysisProjectionAndStatus(t *testing.T) {
	analysis := Analysis{
		Metadata:  MetadataInfo{Title: "Book", Author: "Author", Language: "zh-CN", Identifier: "book-id"},
		Cover:     CoverInfo{ImageHref: "cover.jpg", TitlePageHref: "cover.xhtml"},
		Spine:     []SpineInfo{{IDRef: "chapter", Href: "chapter.xhtml", MediaType: "application/xhtml+xml", Title: "Chapter", Linear: true}},
		TOC:       []TOCInfo{{Title: "Chapter", Href: "chapter.xhtml"}},
		Resources: []ResourceInfo{{ID: "chapter", Href: "chapter.xhtml", MediaType: "application/xhtml+xml", Size: 42, Exists: true}},
		Issues:    []CompatibilityIssue{{Code: "link_invalid", Stage: "links", Document: "chapter.xhtml", Message: "broken link"}},
	}

	snapshot, err := analysis.CompatibilitySnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "failed" {
		t.Fatalf("status = %q", snapshot.Status)
	}
	var metadata struct {
		Metadata MetadataInfo `json:"metadata"`
		Cover    CoverInfo    `json:"cover"`
	}
	if err := json.Unmarshal([]byte(snapshot.MetadataJSON), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Metadata != analysis.Metadata || metadata.Cover != analysis.Cover {
		t.Fatalf("metadata snapshot = %#v", metadata)
	}
	assertSnapshotJSON(t, snapshot.SpineJSON, analysis.Spine)
	assertSnapshotJSON(t, snapshot.TOCJSON, analysis.TOC)
	assertSnapshotJSON(t, snapshot.ResourcesJSON, analysis.Resources)
	assertSnapshotJSON(t, snapshot.IssuesJSON, analysis.Issues)

	analysis.Issues = nil
	snapshot, err = analysis.CompatibilitySnapshot()
	if err != nil || snapshot.Status != "passed" {
		t.Fatalf("compatible snapshot = %#v, %v", snapshot, err)
	}
}

func assertSnapshotJSON[T any](t *testing.T, encoded string, want T) {
	t.Helper()
	var got T
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot JSON = %#v, want %#v", got, want)
	}
}
