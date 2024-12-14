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

func (o Opts) Validate() (err error) {
	if o.RPS == 0 {
		err = errors.Join(err, errors.New("0 RPS is not allowed"))
	}
	if o.StatsPushFreq == 0 {
		err = errors.Join(err, errors.New("push frequency cannot be 0"))
	}
	if o.HTTPVersion != 1 && o.HTTPVersion != 2 { // meh
		err = errors.Join(err, errors.New("unsupported HTTP version"))
	}

	return err
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
	var err error

	if err = args.Validate(); err != nil {
		return nil, nil, err
	}

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

	// Maybe do it somehow through channels?
	var totalRequests, successfulRequests, failedRequests, errorRequests atomic.Uint64

	maxRoutines := runtime.GOMAXPROCS(0) // TODO: find more optimal number of goroutines. (maybe pool?)

	wg := sync.WaitGroup{}

	limiter := rate.NewLimiter(rate.Limit(args.RPS), int(args.RPS))

	beforeReq := time.Now()

	statsChan := make(chan Stats, 1) // Am i sure?
	errorsChan := make(chan error, args.ReqTotal)
	reqQueue := make(chan *http.Request, maxRoutines^2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(reqQueue)
		for totalRequests.Load() <= args.ReqTotal {
			if err = limiter.Wait(ctx); err != nil {
				errorsChan <- fmt.Errorf("req limiter: %w", err)
				return
			}

			req, err := newRequest(ctx, args.Method.String(), args.URL, body, args.Headers)
			if err != nil {
				errorsChan <- fmt.Errorf("new request: %w", err)
				return
			}

			select {
			case <-ctx.Done():
				return
			case reqQueue <- req:
			}
		}
	}()

	for range maxRoutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case req := <-reqQueue:
					if req == nil {
						return
					}
					totalRequests.Add(1)

					res, err := client.Do(req)
					if err != nil {
						errorRequests.Add(1)
						errorsChan <- fmt.Errorf("request: %w", err)
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
		ticker := time.NewTicker(args.StatsPushFreq)

	loop:
		for {
			// Proceeds on either ctx cancellation or ticker tick
			select {
			case <-ticker.C:
			case <-ctx.Done():
			}

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
			case <-ctx.Done():
				select {
				case statsChan <- stats:
				default:
				}
				close(statsChan)
				break loop
			case statsChan <- stats:
			default: // default to have somewhat relevant stats regardles of channel readiness
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
