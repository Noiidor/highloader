package highloader

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

var ErrorsChan = make(chan error, 5000)

func Run(args AppArgs) error {
	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*time.Duration(args.Timeout))
	defer cancel()

	client := http.Client{
		Timeout: time.Millisecond * time.Duration(args.ReqTimeout),
	}

	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		for TotalRequests.Add(1) <= args.ReqTotal {

			body := new(bytes.Buffer) // TODO: reuse?
			if args.Payload != nil {

				jsonData, err := json.Marshal(args.Payload)
				if err != nil {
					ErrorRequests.Add(1)
					ErrorsChan <- err
					continue
				}

				_, err = body.Write(jsonData)
				if err != nil {
					ErrorRequests.Add(1)
					ErrorsChan <- err
					continue
				}
			}
			req, err := http.NewRequestWithContext(ctx, args.Method.String(), args.URL, body)
			if err != nil {
				ErrorRequests.Add(1)
				ErrorsChan <- err
				continue
			}

			for k, v := range args.Headers {
				req.Header.Set(k, v)
			}

			res, err := client.Do(req)
			if err != nil {
				ErrorRequests.Add(1)
				ErrorsChan <- err
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
				ErrorsChan <- err
				continue
			}

		}
		close(ErrorsChan)
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		for TotalRequests.Load() < args.ReqTotal { // TODO: change to be dependent on requests goroutines
			select {
			case <-time.After(time.Second * 1):
				fmt.Printf("Total Requests: %d\n", TotalRequests.Load())
				fmt.Printf("Successful Requests: %d\n", SuccessfulRequests.Load())
				fmt.Printf("Failed Requests: %d\n", FailedRequests.Load())
				fmt.Printf("Error Requests: %d\n", ErrorRequests.Load())
				fmt.Println("-----")
			}
		}
		wg.Done()
	}()

	wg.Wait()

	for v := range ErrorsChan {
		fmt.Printf("Err: %s\n", v)
	}

	return nil
}
