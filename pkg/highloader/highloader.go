package highloader

import (
	"bytes"
	"context"
	"errors"
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

	TotalTimeout  time.Duration // maximum duration of execution
	StatsPushFreq time.Duration // frequency of sending stats struct to a channel
}

type Stats struct {
	TotalRequests   uint64
	SuccessRequests uint64
	FailedRequests  uint64
	Errors          uint64
	RPS             uint32
}

func Run(ctx context.Context, args Opts) (<-chan Stats, <-chan error, error) {
	if args.RPS == 0 {
		return nil, nil, errors.New("0 RPS is not allowed")
	}
	if args.StatsPushFreq == 0 {
		return nil, nil, errors.New("push frequency cannot be 0")
	}

	ctx, cancel := context.WithTimeout(ctx, args.TotalTimeout)
	var err error
	defer func() { // hack?
		if err != nil {
			cancel()
		}
	}()

	client := http.Client{
		Timeout: args.ReqTimeout,
	}

	switch args.HTTPVersion {
	case 1:
		break
	case 2:
		client.Transport = &http2.Transport{}
	default:
		return nil, nil, errors.New("unsupported HTTP version")
	}

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

	statsChan := make(chan Stats, args.TotalTimeout/args.StatsPushFreq) // possible memory issues?(too big buffer)
	errorsChan := make(chan error, args.ReqTotal)

	wg := sync.WaitGroup{}

	limiter := rate.NewLimiter(rate.Limit(args.RPS), int(args.RPS))

	beforeReq := time.Now()

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

					req, err := http.NewRequestWithContext(ctx, args.Method.String(), args.URL, body)
					if err != nil {
						errorRequests.Add(1)
						errorsChan <- err
						continue
					}

					req.Header = args.Headers

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

	loop:
		for {
			select {
			case <-ctx.Done():
				sendStats()
				break loop
			case <-time.After(args.StatsPushFreq):
				sendStats()
			}
		}
	}()

	go func() {
		wg.Wait()
		cancel()
		close(statsChan)
		close(errorsChan)
	}()

	return statsChan, errorsChan, nil
}
