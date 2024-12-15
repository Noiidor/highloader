package highloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
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

type Args struct {
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

func (o Args) Validate() (err error) {
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
	RPS             uint32 `json:"rps"`
}

func (s Stats) String() string {
	return fmt.Sprintf( // ugly but necessary
		`Total Requests: %d
RPS: %d
Successful Requests: %d
Failed Requests: %d`,
		s.TotalRequests,
		s.RPS,
		s.SuccessRequests,
		s.FailedRequests,
	)
}

type Request struct {
	URL     string
	Method  HTTPMethod
	Headers http.Header
	Payload io.Reader
}

func Run(ctx context.Context, numIOWorkers, numCPUWorkers int, args Args) (<-chan *Stats, <-chan error, error) {
	if err := args.Validate(); err != nil {
		return nil, nil, err
	}

	bodyBuf := new(bytes.Buffer)
	if len(args.Payload) > 0 {
		n, _ := bodyBuf.Write(args.Payload)
		if n != len(args.Payload) {
			return nil, nil, errors.New("buffer write: n != len")
		}
	}
	body := io.NopCloser(bodyBuf)

	errs := make(chan error, args.ReqTotal)
	jobs := make(chan *Request, numIOWorkers^2)

	client := newClient(args.ReqTimeout, args.HTTPVersion)

	wg := new(sync.WaitGroup)

	// Jobs producer
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(jobs)
		for range args.ReqTotal {
			req := &Request{
				URL:     args.URL,
				Method:  args.Method,
				Headers: args.Headers,
				Payload: body,
			}

			select {
			case <-ctx.Done():
				return
			case jobs <- req:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(errs)
	}()

	limiter := rate.NewLimiter(rate.Limit(args.RPS), int(args.RPS))
	wg.Add(1)
	// Workers for I/O bound load of requests
	results := spawnWorkers(ctx, wg, limiter, client, numIOWorkers, jobs, errs)

	// Results consumer for CPU bound body processing
	codes := spawnConsumers(ctx, numCPUWorkers, results)

	// Calculating stats
	// I doubt this is right
	stats := spawnStatsProcessor(ctx, args.StatsPushFreq, codes)

	return stats, errs, nil
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

// func spawnProducer(ctx context.Context, )

func spawnStatsProcessor(ctx context.Context, pushFreq time.Duration, codes <-chan int) <-chan *Stats {
	stats := make(chan *Stats, 1)
	go func() {
		defer close(stats)

		beforeReq := time.Now()
		ticker := time.NewTicker(pushFreq)

		total := 0
		success := 0
		fail := 0

		for {
			select {
			case <-ctx.Done():
				return
			case code, ok := <-codes:
				if !ok {
					return
				}

				total++
				if code >= 400 {
					fail++
				} else {
					success++
				}

			case <-ticker.C:
				msPassed := time.Since(beforeReq).Milliseconds()
				rps := int(float64(total) / (float64(msPassed) / 1000))

				stat := &Stats{
					TotalRequests:   uint64(total),
					SuccessRequests: uint64(success),
					FailedRequests:  uint64(fail),
					RPS:             uint32(rps),
				}

				select {
				case <-ctx.Done():
					return
				case stats <- stat:
				}

			}
		}
	}()

	return stats
}

func spawnWorkers(ctx context.Context, outerWg *sync.WaitGroup, limiter *rate.Limiter, client *http.Client, numWorkers int, reqs <-chan *Request, errs chan<- error) <-chan *http.Response {
	wg := new(sync.WaitGroup)
	resps := make(chan *http.Response, numWorkers)
	for range numWorkers {
		wg.Add(1)
		go func() {
			worker(ctx, limiter, client, reqs, resps, errs)
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		outerWg.Done()
		close(resps)
	}()
	return resps
}

func worker(ctx context.Context, limiter *rate.Limiter, client *http.Client, reqs <-chan *Request, resps chan<- *http.Response, errs chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		case request, ok := <-reqs:
			if !ok {
				return
			}

			req, err := newRequest(ctx, request.Method.String(), request.URL, request.Payload, request.Headers)
			if err != nil {
				return
			}

			_ = limiter.Wait(ctx)

			res, err := client.Do(req)
			if err != nil {
				errs <- err
			}

			select {
			case <-ctx.Done():
				return
			case resps <- res:
			}
		}
	}
}

func spawnConsumers(ctx context.Context, numConsumers int, resps <-chan *http.Response) <-chan int {
	wg := new(sync.WaitGroup)
	codes := make(chan int, numConsumers)
	for range numConsumers {
		wg.Add(1)
		go func() {
			consumer(ctx, resps, codes)
			wg.Done()
		}()
	}
	go func() {
		wg.Wait()
		close(codes)
	}()
	return codes
}

// Separate consumer is needed in case if there is a need for body processing
func consumer(ctx context.Context, resps <-chan *http.Response, codes chan<- int) {
	for {
		select {
		case <-ctx.Done():
			return
		case res, ok := <-resps:
			if !ok {
				return
			}
			res.Body.Close()

			codes <- res.StatusCode
		}
	}
}
