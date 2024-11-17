package highloader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type HTTPMethod uint8

const (
	GET HTTPMethod = iota
	POST
	PUT
	PATCH
	DELETE
)

func (m HTTPMethod) String() string {
	return [...]string{"GET", "POST", "PUT", "PATCH", "DELETE"}[m]
}

type AppArgs struct {
	URL         string
	Method      HTTPMethod
	HTTPVersion string // TODO: change to enum
	Headers     map[string]string
	Payload     interface{}

	RPS        uint32
	ReqTotal   uint64
	ReqTimeout uint64 // msec

	Timeout uint64 // seconds
}

// TODO: replace with not global vars
var TotalRequests atomic.Uint64
var SuccessfulRequests atomic.Uint64
var FailedRequests atomic.Uint64
var ErrorRequests atomic.Uint64

func Run(args AppArgs, output io.Writer) error {
	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*time.Duration(args.Timeout))
	defer cancel()

	errorsChan := make(chan error, args.ReqTotal)

	client := http.Client{
		Timeout: time.Millisecond * time.Duration(args.ReqTimeout),
	}

	bodyBuf := new(bytes.Buffer)
	if args.Payload != nil {

		jsonData, err := json.Marshal(args.Payload)
		if err != nil {
			return err
		}

		_, err = bodyBuf.Write(jsonData)
		if err != nil {
			return err
		}
	}

	body := io.NopCloser(bodyBuf)

	maxRoutines := runtime.GOMAXPROCS(0)

	beforeReq := time.Now()

	wg := sync.WaitGroup{}

	for range maxRoutines {
		wg.Add(1)
		go func() {
			for TotalRequests.Load() <= args.ReqTotal {
				TotalRequests.Add(1)

				req, err := http.NewRequestWithContext(ctx, args.Method.String(), args.URL, body)
				if err != nil {
					ErrorRequests.Add(1)
					errorsChan <- err
					continue
				}

				for k, v := range args.Headers {
					req.Header.Set(k, v)
				}

				res, err := client.Do(req)
				if err != nil {
					ErrorRequests.Add(1)
					errorsChan <- err
					continue
				}
				defer res.Body.Close()

				if res.Status[0] == []byte("2")[0] {
					SuccessfulRequests.Add(1)
				} else {
					FailedRequests.Add(1)
				}

				_, err = io.ReadAll(req.Body)
				if err != nil {

					ErrorRequests.Add(1)
					errorsChan <- err
					continue
				}

			}
			wg.Done()
		}()
	}

	if output != nil {
		go func(out io.Writer) {
			firstClear := false

			printStats := func() {
				if firstClear {
					clearLines(5)
				}

				msPassed := time.Since(beforeReq).Milliseconds()

				totalReq := TotalRequests.Load()

				rps := uint32(float64(totalReq) / (float64(msPassed) / 1000))

				fmt.Fprintf(out, "Total Requests: %d\n", totalReq)
				fmt.Fprintf(out, "RPS: %d\n", rps)
				fmt.Fprintf(out, "Successful Requests: %d\n", SuccessfulRequests.Load())
				fmt.Fprintf(out, "Failed Requests: %d\n", FailedRequests.Load())
				fmt.Fprintf(out, "Error Requests: %d\n", ErrorRequests.Load())

				sync.OnceFunc((func() { // overkill?
					firstClear = true
				}))()
			}
		loop:
			for {
				select {
				case <-ctx.Done():
					printStats()
					break loop
				case <-time.After(time.Millisecond * 200):
					printStats()
				}
			}
		}(output)
	}

	wg.Wait()
	close(errorsChan)
	cancel()

	fmt.Printf("Total time: %s\n", time.Since(beforeReq))

	for v := range errorsChan {
		fmt.Printf("Err: %s\n", v)
	}

	return nil
}

func clearLines(n int) {
	for i := 0; i < n; i++ {
		// Move the cursor up one line
		fmt.Print("\033[1A")
		// Clear the entire line
		fmt.Print("\033[2K")
	}
}
