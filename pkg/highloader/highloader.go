package highloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
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

func Run(ctx context.Context, args Args) (<-chan Stats, <-chan error, error) {
	if err := args.Validate(); err != nil {
		return nil, nil, err
	}

	bodyBuf := new(bytes.Buffer)
	if len(args.Payload) > 0 {
		_, err := bodyBuf.Write(args.Payload)
		if err != nil {
			return nil, nil, err
		}
	}
	body := io.NopCloser(bodyBuf)

	numWorkers := runtime.GOMAXPROCS(0) // TODO: find more optimal number of goroutines. (maybe pool?)
	numConsumers := 1

	statsChan := make(chan Stats, 1) // Am i sure?
	errs := make(chan error, args.ReqTotal)
	reqQueue := make(chan *http.Request, numWorkers^2)
	resQueue := make(chan *http.Response, args.ReqTotal)
	statusCodes := make(chan int, args.ReqTotal)

	client := newClient(args.ReqTimeout, args.HTTPVersion)

	wg := new(sync.WaitGroup)

	// Workers for I/O bound load of requests
	wg.Add(1)
	workWg := new(sync.WaitGroup)
	for range numWorkers {
		workWg.Add(1)
		go worker(ctx, workWg, client, reqQueue, resQueue, errs)
	}
	go func() {
		workWg.Wait()
		wg.Done()
		close(resQueue)
	}()

	// Results consumer for CPU bound body processing
	consWg := new(sync.WaitGroup)
	for range numConsumers {
		consWg.Add(1)
		go consumer(ctx, consWg, resQueue, statusCodes)
	}
	go func() {
		consWg.Wait()
		close(statusCodes)
	}()

	limiter := rate.NewLimiter(rate.Limit(args.RPS), int(args.RPS))

	// Jobs producer
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(reqQueue)
		for range args.ReqTotal {
			if err := limiter.Wait(ctx); err != nil {
				errs <- fmt.Errorf("req limiter: %w", err)
				return
			}

			req, err := newRequest(ctx, args.Method.String(), args.URL, body, args.Headers)
			if err != nil {
				errs <- fmt.Errorf("new request: %w", err)
				return
			}

			select {
			case <-ctx.Done():
				return
			case reqQueue <- req:
			}
		}
	}()
	go func() {
		wg.Done()
		close(errs)
	}()

	// Calculating stats
	// This is the most questionable part
	// I have a strong feeling that this is ugly
	go func() {
		defer close(statsChan)

		beforeReq := time.Now()
		ticker := time.NewTicker(args.StatsPushFreq)

		total := 0
		success := 0
		fail := 0

		for {
			select {
			case <-ctx.Done():
				return
			case code := <-statusCodes:
				if code == 0 {
					return
				}

				total++
				switch []rune(strconv.Itoa(code))[0] { // surely it can be done without conversion
				case '5' | '4':
					fail++
				default:
					success++
				}

			case <-ticker.C:
				msPassed := time.Since(beforeReq).Milliseconds()
				rps := int(float64(total) / (float64(msPassed) / 1000))

				stats := Stats{
					TotalRequests:   uint64(total),
					SuccessRequests: uint64(success),
					FailedRequests:  uint64(fail),
					RPS:             uint32(rps),
				}

				select {
				case <-ctx.Done():
					select {
					case statsChan <- stats:
					default:
						return
					}
				case statsChan <- stats:
				}

			}
		}
	}()

	return statsChan, errs, nil
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

func worker(ctx context.Context, wg *sync.WaitGroup, client *http.Client, reqs <-chan *http.Request, resps chan<- *http.Response, errs chan<- error) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-reqs:
			if req == nil { // maybe nil chan to get rid of this?
				return
			}

			res, err := client.Do(req)
			if err != nil {
				errs <- err
			}
			resps <- res
		}
	}
}

// Separate consumer is needed in case if there is a need for body processing
func consumer(ctx context.Context, wg *sync.WaitGroup, resps <-chan *http.Response, codes chan<- int) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case res := <-resps:
			if res == nil {
				return
			}
			res.Body.Close()

			codes <- res.StatusCode
		}
	}
}
