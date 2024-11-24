package highloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/time/rate"
)

type HTTPMethod uint8

const (
	HTTPMethodGET HTTPMethod = iota
	HTTPMethodPOST
	HTTPMethodPUT
	HTTPMethodPATCH
	HTTPMethodDELETE
)

func (m HTTPMethod) String() string {
	return [...]string{"GET", "POST", "PUT", "PATCH", "DELETE"}[m]
}

var HTTPMethodEnum = map[string]HTTPMethod{
	"GET":    HTTPMethodGET,
	"POST":   HTTPMethodPOST,
	"PUT":    HTTPMethodPUT,
	"PATCH":  HTTPMethodPATCH,
	"DELETE": HTTPMethodDELETE,
}

type AppArgs struct {
	URL         string
	Method      HTTPMethod
	HTTPVersion int
	Headers     http.Header
	Payload     []byte

	RPS        uint32
	ReqTotal   uint64
	ReqTimeout time.Duration

	Timeout time.Duration
}

// TODO: replace with something ither than global vars
var TotalRequests atomic.Uint64
var SuccessfulRequests atomic.Uint64
var FailedRequests atomic.Uint64
var ErrorRequests atomic.Uint64

func Run(ctx context.Context, args AppArgs, output io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, args.Timeout)
	defer cancel()

	client := http.Client{
		Timeout: args.ReqTimeout,
	}

	switch args.HTTPVersion {
	case 1:
		break
	case 2:
		client.Transport = &http2.Transport{}
	default:
		return errors.New("unsupported HTTP version")
	}

	bodyBuf := new(bytes.Buffer)
	if len(args.Payload) > 0 {
		_, err := bodyBuf.Write(args.Payload)
		if err != nil {
			return err
		}
	}

	body := io.NopCloser(bodyBuf)

	maxRoutines := runtime.GOMAXPROCS(0)

	beforeReq := time.Now()

	errorsChan := make(chan error, args.ReqTotal)

	wg := sync.WaitGroup{}

	limiter := rate.NewLimiter(rate.Limit(args.RPS), int(args.RPS))

	for range maxRoutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					if TotalRequests.Load() >= args.ReqTotal {
						return
					}
					TotalRequests.Add(1)

					if err := limiter.Wait(ctx); err != nil {
						errorsChan <- err
						return
					}

					req, err := http.NewRequestWithContext(ctx, args.Method.String(), args.URL, body)
					if err != nil {
						ErrorRequests.Add(1)
						errorsChan <- err
						continue
					}

					for k, v := range args.Headers {
						req.Header.Add(k, strings.Join(v, ","))
					}

					res, err := client.Do(req)
					if err != nil {
						ErrorRequests.Add(1)
						errorsChan <- err
						continue
					}
					res.Body.Close()

					switch res.Status[0] {
					case '2':
						SuccessfulRequests.Add(1)
					case '4' | '5':
						FailedRequests.Add(1)
					}

					// _, err = io.ReadAll(req.Body)
					// if err != nil {
					// 	ErrorRequests.Add(1)
					// 	errorsChan <- err
					// 	continue
					// }
				}
			}

		}()
	}

	if output != nil {
		go func(out io.Writer) {
			firstClear := false

			printStats := func() {
				if firstClear {
					// clearLines(5)
				}

				msPassed := time.Since(beforeReq).Milliseconds()

				totalReq := TotalRequests.Load()

				rps := int(float64(totalReq) / (float64(msPassed) / 1000))

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
				case <-time.After(time.Millisecond * 1000):
					printStats()
				}
			}
		}(output)
	}

	wg.Wait()
	close(errorsChan)

	fmt.Printf("Total time: %s\n", time.Since(beforeReq))

	errorsMap := make(map[string]struct{})
	for v := range errorsChan {
		_, ok := errorsMap[v.Error()]
		if !ok {
			errorsMap[v.Error()] = struct{}{}
			fmt.Printf("Err: %s\n", v)
		}
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
