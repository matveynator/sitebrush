package goprofile

import (
	"math"
	"runtime/metrics"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/publicsite"
)

type RAMReport struct {
	publicsite.RAMCacheStats
	Hits   uint64 `json:"hits"`
	Misses uint64 `json:"misses"`
}

type RuntimeSample struct {
	ElapsedMS int64             `json:"elapsedMS"`
	Counts    map[string]uint64 `json:"counts"`
}

type SchedulerReport struct {
	Count uint64   `json:"count"`
	P95MS *float64 `json:"p95MS,omitempty"`
	P99MS *float64 `json:"p99MS,omitempty"`
}

type RuntimeReport struct {
	Supported []string        `json:"supported"`
	Samples   []RuntimeSample `json:"samples"`
	Scheduler SchedulerReport `json:"scheduler"`
}

// Runtime metrics are sampled by the capture worker once per second. These
// approximate gauges are never added together to invent a total goroutine count.
type runtimeSampler struct {
	entries []metrics.Sample
	initial []uint64
}

func newRuntimeSampler() *runtimeSampler {
	available := make(map[string]bool)
	for _, description := range metrics.All() {
		available[description.Name] = true
	}
	sampler := &runtimeSampler{}
	for _, name := range []string{"/sched/goroutines:goroutines", "/sched/goroutines/running:goroutines", "/sched/goroutines/runnable:goroutines", "/sched/goroutines/waiting:goroutines", "/sched/goroutines/not-in-go:goroutines", "/sched/gomaxprocs:threads", "/sched/latencies:seconds"} {
		if available[name] {
			sampler.entries = append(sampler.entries, metrics.Sample{Name: name})
		}
	}
	metrics.Read(sampler.entries)
	for _, entry := range sampler.entries {
		if entry.Name == "/sched/latencies:seconds" && entry.Value.Kind() == metrics.KindFloat64Histogram {
			sampler.initial = append([]uint64(nil), entry.Value.Float64Histogram().Counts...)
		}
	}
	return sampler
}

func (sampler *runtimeSampler) sample(started time.Time) RuntimeSample {
	metrics.Read(sampler.entries)
	sample := RuntimeSample{ElapsedMS: time.Since(started).Milliseconds(), Counts: make(map[string]uint64)}
	for _, entry := range sampler.entries {
		if entry.Value.Kind() == metrics.KindUint64 {
			sample.Counts[entry.Name] = entry.Value.Uint64()
		}
	}
	return sample
}

func (sampler *runtimeSampler) supported() []string {
	names := make([]string, 0, len(sampler.entries))
	for _, entry := range sampler.entries {
		names = append(names, entry.Name)
	}
	return names
}

func (sampler *runtimeSampler) scheduler() SchedulerReport {
	for _, entry := range sampler.entries {
		if entry.Name != "/sched/latencies:seconds" || entry.Value.Kind() != metrics.KindFloat64Histogram {
			continue
		}
		histogram := entry.Value.Float64Histogram()
		if len(sampler.initial) != len(histogram.Counts) {
			return SchedulerReport{}
		}
		counts := make([]uint64, len(histogram.Counts))
		var total uint64
		for index, count := range histogram.Counts {
			counts[index] = count - sampler.initial[index]
			total += counts[index]
		}
		return SchedulerReport{Count: total, P95MS: runtimePercentile(counts, histogram.Buckets, 95), P99MS: runtimePercentile(counts, histogram.Buckets, 99)}
	}
	return SchedulerReport{}
}

func runtimePercentile(counts []uint64, buckets []float64, percent uint64) *float64 {
	var total, seen uint64
	for _, count := range counts {
		total += count
	}
	if total == 0 || len(buckets) != len(counts)+1 {
		return nil
	}
	threshold := (total*percent + 99) / 100
	for index, count := range counts {
		seen += count
		if seen < threshold {
			continue
		}
		upper := buckets[index+1] * 1000
		if math.IsInf(upper, 0) || math.IsNaN(upper) {
			return nil
		}
		return &upper
	}
	return nil
}
