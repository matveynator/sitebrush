package goprofile

import (
	"fmt"
	"testing"

	profile "github.com/google/pprof/profile"
)

func TestSampledRelationshipsKeepDirectEdgesAndRecursiveCosts(t *testing.T) {
	caller := &profile.Function{ID: 1, Name: "main.handle", Filename: "sitebrush.go"}
	worker := &profile.Function{ID: 2, Name: "main.work", Filename: "sitebrush.go"}
	recursive := &profile.Function{ID: 3, Name: "runtime.recursive", Filename: "runtime.go"}
	parsed := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}, {Type: "cpu", Unit: "nanoseconds"}},
		Sample: []*profile.Sample{{
			Value: []int64{3, 30},
			Location: []*profile.Location{
				{Line: []profile.Line{{Function: recursive, Line: 4}}},
				{Line: []profile.Line{{Function: recursive, Line: 3}}},
				{Line: []profile.Line{{Function: recursive, Line: 2}}},
				{Line: []profile.Line{{Function: worker, Line: 10}}},
				{Line: []profile.Line{{Function: caller, Line: 20}}},
			},
		}},
	}
	functions, _, calls, total, truncated := aggregate(parsed, "cpu", 100)
	if total != 30 || truncated || len(calls) != 3 {
		t.Fatal(functions, calls, total, truncated)
	}
	var selected Function
	for _, function := range functions {
		if function.Name == "runtime.recursive" {
			selected = function
		}
	}
	if selected.Self != 30 || selected.Total != 30 || selected.Samples != 3 {
		t.Fatal("recursion multiplied function costs", selected)
	}
	detail := functionDetail(functions, calls, selected.ID)
	if detail == nil || len(detail.Callers) != 2 || len(detail.Callees) != 1 {
		t.Fatal("direct callers/callees missing", detail)
	}
	for _, relation := range append(detail.Callers, detail.Callees...) {
		if relation.Total != 30 || relation.Samples != 3 {
			t.Fatal("recursion multiplied edge costs", relation)
		}
	}
	var self int64
	for _, group := range functionGroups(functions) {
		self += group.Self
	}
	if self != total {
		t.Fatal("CPU category breakdown double counted inclusive costs")
	}
}

func TestFunctionTableSortsAndFiltersBeforePaging(t *testing.T) {
	report := Report{CPU: 1000, Calls: []Call{{Caller: 1, Callee: 2, Total: 10}}}
	for index := 0; index < 70; index++ {
		report.Functions = append(report.Functions, Function{ID: index + 1, Name: fmt.Sprintf("main.work%02d", index), File: "sitebrush.go", Kind: "application", Self: int64(index), Total: int64(100 - index)})
	}
	more := false
	page := reportPage(report, Command{View: "functions", Sort: "self"}, &more)
	if len(page.Functions) != 50 || page.Functions[0].Self != 69 || !more || len(page.Calls) != 0 || page.Matches != 70 {
		t.Fatal("sorting happened after paging or unbounded call edges leaked", page)
	}
	page = reportPage(report, Command{View: "functions", Search: "sitebrush.go", Sort: "name", Ascending: true, Page: 1}, &more)
	if len(page.Functions) != 20 || page.Functions[0].Name != "main.work50" || more {
		t.Fatal(page)
	}
	page = reportPage(report, Command{View: "functions", Kind: "runtime"}, &more)
	if len(page.Functions) != 0 || more {
		t.Fatal("category filter did not apply", page)
	}
	page = reportPage(report, Command{View: "functions", Minimum: 9}, &more)
	if page.Matches != 11 {
		t.Fatal("minimum inclusive CPU filter did not apply", page.Matches)
	}
}

func TestFunctionDetailResponseIsBoundedAndIsolated(t *testing.T) {
	report := Report{Functions: []Function{{ID: 1, Name: "main.target"}}, Goroutines: "private stacks", Code: []Function{{Name: "source line"}}}
	for index := 2; index < 80; index++ {
		report.Functions = append(report.Functions, Function{ID: index, Name: fmt.Sprintf("main.caller%d", index)})
		report.Calls = append(report.Calls, Call{Caller: index, Callee: 1, Total: int64(index)})
	}
	more := false
	page := reportPage(report, Command{View: "function", Function: 1}, &more)
	if page.Detail == nil || len(page.Detail.Callers) != 50 || !page.Detail.Truncated || page.Detail.Callers[0].Total != 79 {
		t.Fatal(page.Detail)
	}
	if len(page.Functions) != 0 || len(page.Calls) != 0 || page.Goroutines != "" || len(page.Code) != 0 || more {
		t.Fatal("detail response leaked unrelated diagnostic payload")
	}
	page = reportPage(report, Command{View: "function", Function: 900}, &more)
	if page.Detail != nil {
		t.Fatal("unknown function returned a detail")
	}
}

func TestFunctionSymbolCategories(t *testing.T) {
	for name, want := range map[string]string{
		"main.serve": "application",
		"github.com/matveynator/sitebrush/v2/pkg/publicsite.(*Front).Serve": "application",
		"runtime.gcDrain":                  "runtime",
		"internal/runtime/maps.(*Map).get": "runtime",
		"net/http.(*conn).serve":           "standard",
		"syscall.Syscall":                  "standard",
		"time.Now":                         "standard",
		"os.Open":                          "standard",
		"sync.(*Pool).Get":                 "standard",
		"fmt.Sprintf":                      "standard",
		"crypto/tls.(*Conn).Write":         "standard",
		"slices.SortFunc[github.com/example/project.Record]":          "standard",
		"sync/atomic.Pointer[github.com/example/project.Record].Load": "standard",
		"github.com/matveynator/netchan/v2.(*Channel).Send":           "library",
		"github.com/example/project.Sort[net/http.Request]":           "library",
		"gopkg.in/yaml.v3.Marshal":                                    "library",
	} {
		if actual := functionKind(name); actual != want {
			t.Fatalf("%s: got %s, want %s", name, actual, want)
		}
	}
}
