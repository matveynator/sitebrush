package analytics

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInitialFunnelsUseRealLinksAndObservedActions(t *testing.T) {
	pages := []PublishedPage{
		InspectPublishedPage(PublishedPage{Path: "/", Title: "Home"}, `<a href="/product">Product</a>`, "example.org"),
		InspectPublishedPage(PublishedPage{Path: "/product", Title: "Product"}, `<a href="/download">Download</a>`, "example.org"),
		InspectPublishedPage(PublishedPage{Path: "/download", Title: "Download"}, `<a data-analytics-key="download" href="https://github.com/site/releases/app.zip">Get app</a>`, "example.org"),
		{Path: "/product/checkout", Title: "Checkout"},
	}
	goals := InitialGoals(pages)
	if err := ValidateGoals(goals); err != nil {
		t.Fatal(err, goals)
	}
	if len(goals) < 2 || !goals[0].Primary || goals[0].Kind != "download" || goals[0].Element != "key:download" {
		t.Fatal("missing acquisition-independent primary download goal", goals)
	}
	route := goals[1].Steps
	if len(route) != 4 || route[0].Page != "/" || route[1].Page != "/product" || route[2].Page != "/download" || route[3].Kind != "download" {
		t.Fatal("published link route missing", route)
	}
	for _, goal := range goals {
		for _, step := range goal.Steps {
			if step.Page == "/product/checkout" {
				t.Fatal("URL hierarchy invented a navigation step")
			}
		}
	}
	tree := BuildSiteTree(pages, nil)
	if !reflect.DeepEqual(tree.Links["/"], []string{"/product"}) || len(tree.Links["/download"]) != 0 {
		t.Fatal("external download became a site navigation link", tree.Links)
	}
	site := New(time.Now().UTC())
	site.Goals = goals
	visit := conversionEvent("1111111111111111", "2222222222222222", "/download", 1, Signal{Kind: "page"}, Signal{Kind: "download", Element: "key:download"})
	if !site.Record(visit, time.Now().UTC(), 4<<20) {
		t.Fatal("direct landing rejected")
	}
	completed := false
	for _, funnel := range site.Report(time.Now().UTC(), 7).Conversion.Funnels {
		if funnel.Primary {
			completed = funnel.Completed == 1
		}
	}
	if !completed {
		t.Fatal("direct download landing excluded from primary conversion")
	}
}

func TestInitialFormsAndFallbackDoNotInventBusinessSuccess(t *testing.T) {
	page := InspectPublishedPage(PublishedPage{Path: "/contact", Title: strings.Repeat("Контакт", 30)}, `<form data-analytics-key="contact"><input value="personal-secret"><textarea>message-secret</textarea></form><form data-analytics-key="other"></form>`, "example.org")
	goals := InitialGoals([]PublishedPage{page})
	if err := ValidateGoals(goals); err != nil {
		t.Fatal(err, goals)
	}
	if len(goals) != 2 || goals[0].Kind != "form-submit" || goals[0].Name == goals[1].Name {
		t.Fatal("form attempts mislabeled or names duplicated", goals)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "purchase") {
		t.Fatal("page structure retained field contents or guessed outcome")
	}
	fallback := InitialGoals([]PublishedPage{{Path: "/", Title: "Home"}})
	if len(fallback) != 1 || fallback[0].Steps[len(fallback[0].Steps)-1].Kind != "scroll" {
		t.Fatal("single-page default missing", fallback)
	}
}

func TestInitialConfigurationDoesNotOverwriteManualOrEmptyGoals(t *testing.T) {
	store := OpenStore(t.TempDir())
	defer store.Close()
	for _, domain := range []string{"manual.example", "empty.example"} {
		existing := `[]`
		if domain == "manual.example" {
			existing = `[{"Name":"Purchase","Kind":"event","Match":"purchase"}]`
		}
		if result := store.Exchange(StorageRequest{Operation: SaveConfiguration, Domain: domain, State: existing}); result.Err != nil {
			t.Fatal(result.Err)
		}
		result := store.Exchange(StorageRequest{Operation: InitializeConfiguration, Domain: domain, State: `[{"Name":"Changed"}]`})
		if result.Err != nil || result.Text != existing {
			t.Fatal("initialization overwrote existing configuration", result)
		}
	}
	initial := `[{"Name":"Visit","Kind":"page","Page":"/"}]`
	if result := store.Exchange(StorageRequest{Operation: InitializeConfiguration, Domain: "new.example", State: initial}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := store.Exchange(StorageRequest{Operation: ReadConfiguration, Domain: "new.example"}); result.Err != nil || result.Text != initial {
		t.Fatal(result)
	}
}
