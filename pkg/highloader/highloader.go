package highloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
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

type Opts struct {
	URL         string
	Method      HTTPMethod
	HTTPVersion int
	Headers     http.Header
	Payload     []byte

	RPS        uint32
	ReqTotal   uint64
	ReqTimeout time.Duration

	StatsPushFreq time.Duration // frequency of sending stats struct to a channel
}

type Stats struct {
	TotalRequests   uint64 `json:"totalRequests"`
	SuccessRequests uint64 `json:"successRequests"`
	FailedRequests  uint64 `json:"failedRequests"`
	Errors          uint64 `json:"errors"`
	RPS             uint32 `json:"rps"`
}

func (s Stats) String() string {
	return fmt.Sprintf( // ugly but necessary
		`Total Requests: %d
RPS: %d
Successful Requests: %d
Failed Requests: %d
Error Requests: %d`,
		s.TotalRequests,
		s.RPS,
		s.SuccessRequests,
		s.FailedRequests,
		s.Errors,
	)
}

func newClient(reqTimeout time.Duration, HTTPVer int) *http.Client {
	client := http.Client{
		Timeout: reqTimeout,
	}

	switch HTTPVer {
	case 1:
		break
	case 2:
		client.Transport = &http2.Transport{}
	}

	return &client
}

func newRequest(ctx context.Context, method, url string, body io.Reader, headers http.Header) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header = headers

	return req, nil
}

func Run(ctx context.Context, args Opts) (<-chan Stats, <-chan error, error) {
	if args.RPS == 0 {
		return nil, nil, errors.New("0 RPS is not allowed")
	}
	if args.StatsPushFreq == 0 {
		return nil, nil, errors.New("push frequency cannot be 0")
	}
	if args.HTTPVersion != 1 && args.HTTPVersion != 2 { // meh
		return nil, nil, errors.New("unsupported HTTP version")
	}

	var err error

	ctx, cancel := context.WithCancel(ctx)
	go func() {
		if err != nil {
			cancel()
		}
	}()

	client := newClient(args.ReqTimeout, args.HTTPVersion)

	bodyBuf := new(bytes.Buffer)
	if len(args.Payload) > 0 {
		_, err := bodyBuf.Write(args.Payload)
		if err != nil {
			return nil, nil, err
		}
	}
	body := io.NopCloser(bodyBuf)

	var totalRequests, successfulRequests, failedRequests, errorRequests atomic.Uint64

	maxRoutines := runtime.GOMAXPROCS(0) // TODO: find more optimal number of goroutines. (maybe pool?)

	statsChan := make(chan Stats, 1000) // bad
	errorsChan := make(chan error, args.ReqTotal)

	wg := sync.WaitGroup{}

	limiter := rate.NewLimiter(rate.Limit(args.RPS), int(args.RPS))

	beforeReq := time.Now()

	// reqQueue := make(chan *http.Request, maxRoutines)
	//
	// for totalRequests.Load() < args.ReqTotal {
	//
	// }
	//
	for range maxRoutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					if totalRequests.Load() >= args.ReqTotal {
						return
					}
					totalRequests.Add(1)

					if err := limiter.Wait(ctx); err != nil {
						errorsChan <- err
						return
					}

					req, err := newRequest(ctx, args.Method.String(), args.URL, body, args.Headers)
					if err != nil {
						errorRequests.Add(1)
						errorsChan <- err
						continue
					}

					res, err := client.Do(req)
					if err != nil {
						errorRequests.Add(1)
						errorsChan <- err
						continue
					}
					res.Body.Close()

					switch res.Status[0] {
					case '4' | '5':
						failedRequests.Add(1)
					default:
						successfulRequests.Add(1)
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

	go func() {
		sendStats := func() {
			totalReq := totalRequests.Load()

			msPassed := time.Since(beforeReq).Milliseconds()
			rps := int(float64(totalReq) / (float64(msPassed) / 1000))

			stats := Stats{
				TotalRequests:   totalReq,
				SuccessRequests: successfulRequests.Load(),
				FailedRequests:  failedRequests.Load(),
				Errors:          errorRequests.Load(),
				RPS:             uint32(rps),
			}

			select {
			case statsChan <- stats:
			case <-ctx.Done():
				return
			}
		}

		ticker := time.NewTicker(args.StatsPushFreq)

	loop:
		for {
			select {
			case <-ctx.Done():
				sendStats()
				close(statsChan)
				break loop
			case <-ticker.C:
				sendStats()
			}
		}
	}()

	go func() {
		wg.Wait()
		cancel()
		close(errorsChan)
	}()

	return statsChan, errorsChan, nil
}
