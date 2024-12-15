package highloader

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func BenchmarkRun100(b *testing.B)   { benchmarkRun(100, b) }
func BenchmarkRun1000(b *testing.B)  { benchmarkRun(1000, b) }
func BenchmarkRun10000(b *testing.B) { benchmarkRun(10000, b) }

// Not sure if this is a good benchmark
func benchmarkRun(n uint64, b *testing.B) {
	ctx := context.Background()

	// simplest echo server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Body)
	}))
	defer server.Close()

	pushFreq := 1 * time.Second

	args := Args{
		URL:           server.URL,
		Method:        HTTPMethodPOST,
		HTTPVersion:   1,
		Payload:       []byte("test_payload"),
		RPS:           2 << 20,
		ReqTotal:      n,
		StatsPushFreq: pushFreq,
	}

	numIOWorkers := runtime.GOMAXPROCS(0)
	numCPUWorkers := 1

	b.ResetTimer()
	for range b.N {
		_, _, err := Run(ctx, numIOWorkers, numCPUWorkers, args)
		if err != nil {
			b.Fatal(err)
		}
	}

}

func BenchmarkNewRequest(b *testing.B) {
	ctx := context.Background()
	for range b.N {
		_, err := newRequest(ctx, "POST", "http://0.0.0.0:5050", bytes.NewReader([]byte("test_payload")), nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Body)
	}))
	defer server.Close()

	pushFreq := 50 * time.Millisecond

	args := Args{
		URL:           server.URL,
		Method:        HTTPMethodPOST,
		HTTPVersion:   1,
		Payload:       []byte("test_payload"),
		RPS:           2 << 20,
		ReqTotal:      50000,
		StatsPushFreq: pushFreq,
	}

	numIOWorkers := runtime.GOMAXPROCS(0)
	numCPUWorkers := 1

	stats, errs, err := Run(ctx, numIOWorkers, numCPUWorkers, args)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-stats:
	case <-time.After(pushFreq * 2):
		t.Error("Expected non-zero number of stats, got 0")
	}

	for n := range errs {
		t.Errorf("Expected no errors, got: %s", n.Error())
	}
	// select {
	// case err := <-errs:
	// 	t.Errorf("Expected none errors, got: %s", err.Error())
	// case <-ctx.Done():
	// 	break
	// }
}
