package goprofile

import (
	"sort"
	"strings"
)

type Call struct {
	Caller  int   `json:"caller"`
	Callee  int   `json:"callee"`
	Total   int64 `json:"total"`
	Samples int64 `json:"samples"`
}
type Group struct {
	Kind string `json:"kind"`
	Self int64  `json:"self"`
}
type FunctionDetail struct {
	Function  Function   `json:"function"`
	Callers   []Function `json:"callers"`
	Callees   []Function `json:"callees"`
	Truncated bool       `json:"truncated"`
}

// Categories describe symbol ownership; only self costs form an additive CPU breakdown.
func functionKind(name string) string {
	// Type arguments can contain import paths; only the symbol's own package identifies its category.
	symbol, _, _ := strings.Cut(name, "[")
	firstPathSegment, _, hasPackagePath := strings.Cut(symbol, "/")
	switch {
	case strings.HasPrefix(name, "main."), strings.HasPrefix(name, "github.com/matveynator/sitebrush/"):
		return "application"
	case strings.HasPrefix(name, "runtime."), strings.HasPrefix(name, "internal/runtime/"):
		return "runtime"
	case !hasPackagePath, !strings.Contains(firstPathSegment, "."), strings.HasPrefix(name, "vendor/"):
		return "standard"
	default:
		return "library"
	}
}
func functionGroups(functions []Function) []Group {
	groups := []Group{{Kind: "application"}, {Kind: "runtime"}, {Kind: "standard"}, {Kind: "library"}}
	for _, function := range functions {
		for index := range groups {
			if groups[index].Kind == function.Kind {
				groups[index].Self += function.Self
				break
			}
		}
	}
	return groups
}

// Filtering and sorting happen before paging so the hottest function cannot be hidden on another page.
func reportPage(report Report, command Command, more *bool) Report {
	functions, frames, memory := report.Functions, report.Frames, report.Memory
	code, goroutines, stages, calls := report.Code, report.Goroutines, report.Stages, report.Calls
	report.Code, report.Functions, report.Frames, report.Memory, report.Calls = nil, nil, nil, nil, nil
	report.Goroutines, report.Stages, report.Detail = "", nil, nil
	*more = false
	switch command.View {
	case "graph":
		report.Frames = frames
		return report
	case "function":
		report.Detail = functionDetail(functions, calls, command.Function)
		return report
	case "goroutines":
		report.Goroutines = goroutines
		return report
	case "stages":
		report.Stages = stages
		return report
	case "network":
		return report
	case "code":
		functions = code
	case "memory":
		functions = memory
	}
	filtered := make([]Function, 0, len(functions))
	search := strings.ToLower(command.Search)
	minimum := max(0, min(command.Minimum, 100))
	for _, function := range functions {
		if command.Kind != "" && command.Kind != function.Kind {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(function.Name+" "+function.File), search) {
			continue
		}
		if command.View != "memory" && report.CPU > 0 && 100*float64(function.Total)/float64(report.CPU) < minimum {
			continue
		}
		filtered = append(filtered, function)
	}
	sortFunctions(filtered, command.Sort, command.Ascending)
	report.Matches = len(filtered)
	begin := min(max(command.Page, 0), len(filtered)/50+1) * 50
	if begin < len(filtered) {
		end := min(begin+50, len(filtered))
		if command.View == "memory" {
			report.Memory = filtered[begin:end]
		} else {
			report.Functions = filtered[begin:end]
		}
		*more = end < len(filtered)
	}
	return report
}
func sortFunctions(functions []Function, column string, ascending bool) {
	sort.Slice(functions, func(left, right int) bool {
		first, second := functions[left], functions[right]
		comparison := 0
		switch column {
		case "name":
			comparison = strings.Compare(first.Name, second.Name)
		case "self":
			if first.Self < second.Self {
				comparison = -1
			} else if first.Self > second.Self {
				comparison = 1
			}
		case "samples":
			if first.Samples < second.Samples {
				comparison = -1
			} else if first.Samples > second.Samples {
				comparison = 1
			}
		default:
			if first.Total < second.Total {
				comparison = -1
			} else if first.Total > second.Total {
				comparison = 1
			}
		}
		if comparison == 0 {
			if first.Name == second.Name {
				if first.File == second.File {
					return first.Line < second.Line
				}
				return first.File < second.File
			}
			return first.Name < second.Name
		}
		if ascending {
			return comparison < 0
		}
		return comparison > 0
	})
}

// Relationships retain direct sampled stack edges, not an invented count of function calls.
func functionDetail(functions []Function, calls []Call, identifier int) *FunctionDetail {
	indexed := make(map[int]Function, len(functions))
	for _, function := range functions {
		indexed[function.ID] = function
	}
	selected, found := indexed[identifier]
	if !found {
		return nil
	}
	detail := &FunctionDetail{Function: selected}
	for _, call := range calls {
		if call.Callee == identifier {
			caller := indexed[call.Caller]
			caller.Total, caller.Samples = call.Total, call.Samples
			detail.Callers = append(detail.Callers, caller)
		}
		if call.Caller == identifier {
			callee := indexed[call.Callee]
			callee.Total, callee.Samples = call.Total, call.Samples
			detail.Callees = append(detail.Callees, callee)
		}
	}
	sortFunctions(detail.Callers, "total", false)
	sortFunctions(detail.Callees, "total", false)
	if len(detail.Callers) > 50 {
		detail.Callers = detail.Callers[:50]
		detail.Truncated = true
	}
	if len(detail.Callees) > 50 {
		detail.Callees = detail.Callees[:50]
		detail.Truncated = true
	}
	return detail
}
