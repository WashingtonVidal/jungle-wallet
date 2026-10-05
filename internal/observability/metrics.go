package observability

import (
	"fmt"
	"io"
	"sync"
	"time"
)

var mu sync.Mutex
var counters = map[string]uint64{}
var durationSum float64
var durationCount uint64

func Inc(name string)         { mu.Lock(); counters[name]++; mu.Unlock() }
func Observe(d time.Duration) { mu.Lock(); durationSum += d.Seconds(); durationCount++; mu.Unlock() }
func Write(w io.Writer) {
	mu.Lock()
	defer mu.Unlock()
	for name, v := range counters {
		_, _ = fmt.Fprintf(w, "jungle_wallet_%s_total %d\n", name, v)
	}
	_, _ = fmt.Fprintf(w, "jungle_wallet_processing_seconds_sum %g\njungle_wallet_processing_seconds_count %d\n", durationSum, durationCount)
}
