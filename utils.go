package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"runtime"
	"runtime/pprof"
	"time"
)

func convertHumanReadable(alloc uint64) string {
	const unit = 1024
	if alloc < unit {
		return fmt.Sprintf("%d B", alloc)
	}
	div, exp := int64(unit), 0
	for n := alloc / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(alloc)/float64(div), "KMGTPE"[exp])
}

func monitorHeapSize(ctx context.Context, withTriggerGC bool) {
	var mem runtime.MemStats

	maxAlloc := uint64(0)

	t := time.NewTicker(200 * time.Millisecond)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// trigger gc
			if withTriggerGC {
				runtime.GC()
			}
			runtime.ReadMemStats(&mem)
			if mem.HeapAlloc > maxAlloc {
				maxAlloc = mem.HeapAlloc
				memSize := convertHumanReadable(maxAlloc)
				fmt.Fprintf(os.Stderr, "MaxAlloc: %s\n", memSize)
			}
		}
	}
}

func writeProfile(filepath string) error {
	// write mem profile
	f, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	err = pprof.WriteHeapProfile(f)
	if err != nil {
		return err
	}

	log.Printf(`
	To view the memory profile, run the following command:
	go tool pprof %s
	go tool pprof -alloc_space %s
`, filepath, filepath)
	return nil
}

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var seededRand *rand.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))

	result := make([]byte, length)
	for i := range result {
		result[i] = charset[seededRand.Intn(len(charset))]
	}
	return string(result)
}
