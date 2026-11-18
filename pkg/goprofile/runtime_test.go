package goprofile

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSamplingSupportsAvailableGauges(t *testing.T) {
	sampler := newRuntimeSampler()
	sample := sampler.sample(time.Now())
	if sample.Counts["/sched/goroutines:goroutines"] == 0 {
		t.Fatal("missing goroutine total")
	}
	if len(sampler.supported()) == 0 {
		t.Fatal("runtime capabilities missing")
	}
	if _, err := json.Marshal(sampler.scheduler()); err != nil {
		t.Fatal(err)
	}
	if missing := (&runtimeSampler{}).sample(time.Now()); len(missing.Counts) != 0 {
		t.Fatal("missing metrics fabricated")
	}
}

func TestRuntimeHistogramBoundsAndTextExport(t *testing.T) {
	if bound := runtimePercentile([]uint64{8, 2}, []float64{0, 0.001, 0.01}, 95); bound == nil || *bound != 10 {
		t.Fatal(bound)
	}
	if bound := runtimePercentile([]uint64{1}, []float64{0, math.Inf(1)}, 95); bound != nil {
		t.Fatal("infinite JSON bound", bound)
	}
	if bound := runtimePercentile([]uint64{0}, []float64{0, 1}, 95); bound != nil {
		t.Fatal("empty histogram has a percentile")
	}
	report := Report{Runtime: &RuntimeReport{Samples: []RuntimeSample{{Counts: map[string]uint64{"/sched/goroutines:goroutines": 42}}}}, RAM: &RAMReport{Hits: 9, Misses: 1}}
	text := exportText(report)
	for _, expected := range []string{"Готовые RAM-ответы", "попаданий: 9", "Планировщик Go", "goroutine всего=42", "runnable=нет данных", "http1_response_complete"} {
		if !strings.Contains(text, expected) {
			t.Fatal("missing diagnostic", expected)
		}
	}
}
