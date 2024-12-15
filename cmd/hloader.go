package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/Noiidor/highloader/internal/app/hloader"
)

func main() {

	// memStats := new(runtime.MemStats)
	// go func() {
	// 	for {
	// 		time.Sleep(time.Second * 1)
	// 		runtime.ReadMemStats(memStats)
	// 		PrintProfStats(memStats)
	// 	}
	// }()

	ctx := context.Background()
	if err := hloader.RunApp(ctx); err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}
}

func PrintProfStats(m *runtime.MemStats) {
	fmt.Printf(
		`
		MEM STATS:
		Total Allocated: %d MB,
		Allocated: %d MB,
		GC Cycles: %d,
		Last GC: %s,
		Stack inuse: %d MB,
		G: %d
		`,
		m.TotalAlloc/(1024*1024),
		m.HeapAlloc/(1024*1024),
		m.NumGC,
		time.Unix(0, int64(m.LastGC)).String(),
		m.StackInuse/(1024*1024),
		runtime.NumGoroutine(),
	)
}
