package analytics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSiteTreeSeparatesStructureFromObservedNavigation(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	first := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"})
	site.Record(first, now, 4<<20)
	second := conversionEvent(first.Visitor, "3333333333333333", "/docs/guide", 1, Signal{Kind: "page"})
	site.Record(second, now.Add(time.Minute), 4<<20)
	report := site.Report(now.Add(2*time.Minute), 7).Conversion
	tree := BuildSiteTree([]PublishedPage{{Path: "/", Title: "Home"}, {Path: "/docs/guide", Title: "Getting started"}, {Path: "/pricing", Title: "Pricing"}}, report.Paths)
	if tree.Root.Title != "Home" || tree.Root.Traffic.Entrances != 1 || tree.Root.Traffic.Users != 1 {
		t.Fatal("entry page metrics missing")
	}
	if len(tree.Root.Children) != 2 || tree.Root.Children[0].Path != "/docs" || tree.Root.Children[0].Published {
		t.Fatal("URL structure differs from the site's page tree")
	}
	guide := tree.Root.Children[0].Children[0]
	if guide.Title != "Getting started" || guide.Traffic.Users != 1 || guide.Traffic.Entrances != 0 {
		t.Fatal("continuation reported as an entrance")
	}
	if tree.Root.Children[1].Traffic != nil {
		t.Fatal("unvisited published page received invented traffic")
	}
	if tree.Root.Traffic.Children[0].Label != "/docs/guide" {
		t.Fatal("actual next step lost")
	}
}

func TestSiteTreeBoundsSyntheticFolders(t *testing.T) {
	pages := []PublishedPage{}
	for index := 0; index < 1000; index++ {
		pages = append(pages, PublishedPage{Path: fmt.Sprintf("/branch%d/", index) + strings.Repeat("level/", 20) + "page"})
	}
	tree := BuildSiteTree(pages, nil)
	if !tree.Truncated || tree.Root == nil {
		t.Fatal("large tree was not bounded")
	}
}
