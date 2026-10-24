package sitefreeze

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// A checkpoint contains references and properties, never a second copy of page
// bodies or an archive of the site's file tree.
type Page struct {
	Path          string
	Title         string
	Revision      int64
	Hash          string
	Published     bool
	PendingDelete bool
}
type Revision struct {
	Path   string
	ID     int64
	Active bool
}
type File struct {
	Tree  string
	Path  string
	Hash  string
	Bytes int64
	Mode  uint32
	Link  string
}
type Property struct {
	Table   string
	Columns []string
	Rows    [][]any
}
type Checkpoint struct {
	ID         string
	CreatedAt  string
	Pages      []Page
	Published  []Page
	Revisions  []Revision
	Files      []File
	Properties []Property
}
type Change struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Action string `json:"action"`
}
type Preview struct {
	ID        string   `json:"checkpoint_id"`
	CreatedAt string   `json:"created_at"`
	Digest    string   `json:"digest"`
	Changes   []Change `json:"changes"`
	Total     int      `json:"total"`
}

func Compare(baseline, current Checkpoint) Preview {
	baseline = stableCheckpoint(baseline)
	current = stableCheckpoint(current)
	preview := Preview{ID: baseline.ID, CreatedAt: baseline.CreatedAt, Changes: []Change{}}
	before := make(map[string]string)
	after := make(map[string]string)
	add := func(target map[string]string, checkpoint Checkpoint) {
		for _, page := range checkpoint.Pages {
			encoded, _ := json.Marshal(page)
			target["page\x00"+page.Path] = string(encoded)
		}
		for _, page := range checkpoint.Published {
			encoded, _ := json.Marshal(page)
			target["publication\x00"+page.Path] = string(encoded)
		}
		for _, file := range checkpoint.Files {
			encoded, _ := json.Marshal(file)
			target[file.Tree+"\x00"+file.Path] = string(encoded)
		}
		for _, property := range checkpoint.Properties {
			encoded, _ := json.Marshal(property)
			target["properties\x00"+property.Table] = string(encoded)
		}
		for _, revision := range checkpoint.Revisions {
			encoded, _ := json.Marshal(revision)
			target["revision\x00"+revisionName(revision.ID)] = string(encoded)
		}
	}
	add(before, baseline)
	add(after, current)
	for key, previous := range before {
		next, found := after[key]
		action := "modified"
		if !found {
			action = "deleted"
		}
		if !found || previous != next {
			kind, path := splitKey(key)
			preview.Changes = append(preview.Changes, Change{kind, path, action})
		}
	}
	for key := range after {
		if _, found := before[key]; !found {
			kind, path := splitKey(key)
			preview.Changes = append(preview.Changes, Change{kind, path, "created"})
		}
	}
	revisionPaths := make(map[string]string)
	for _, revision := range append(append([]Revision(nil), baseline.Revisions...), current.Revisions...) {
		if revision.Path != "" {
			identifier := revisionName(revision.ID)
			revisionPaths[identifier] = revision.Path + " (#" + identifier + ")"
		}
	}
	for index, change := range preview.Changes {
		if change.Kind == "revision" && revisionPaths[change.Path] != "" {
			preview.Changes[index].Path = revisionPaths[change.Path]
		}
	}
	sort.Slice(preview.Changes, func(left, right int) bool {
		first, second := preview.Changes[left], preview.Changes[right]
		if first.Kind != second.Kind {
			return first.Kind < second.Kind
		}
		return first.Path < second.Path
	})
	current.ID = ""
	current.CreatedAt = ""
	encoded, _ := json.Marshal(current)
	digest := sha256.Sum256(encoded)
	preview.Digest = hex.EncodeToString(digest[:])
	preview.Total = len(preview.Changes)
	return preview
}
func splitKey(key string) (string, string) {
	for offset, character := range key {
		if character == 0 {
			return key[:offset], key[offset+1:]
		}
	}
	return key, ""
}
func revisionName(identifier int64) string {
	encoded, _ := json.Marshal(identifier)
	return string(encoded)
}

// Download consumption is security history, not an editorial change. A preview
// remains valid while visitors consume links; restoration preserves live counters.
func stableCheckpoint(point Checkpoint) Checkpoint {
	properties := make([]Property, len(point.Properties))
	copy(properties, point.Properties)
	point.Properties = properties
	for index, property := range properties {
		if property.Table != "file_access_rules" {
			continue
		}
		rows := make([][]any, len(property.Rows))
		for rowIndex, record := range property.Rows {
			rows[rowIndex] = append([]any(nil), record...)
			for columnIndex, column := range property.Columns {
				if column == "single_use_left" || column == "token_use_count" {
					rows[rowIndex][columnIndex] = 0
				}
			}
		}
		point.Properties[index].Rows = rows
	}
	return point
}
