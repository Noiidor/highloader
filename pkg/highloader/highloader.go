package highloader

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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

func Run(args AppArgs) error {
	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*time.Duration(args.Timeout))
	defer cancel()

	var body *bytes.Buffer
	if args.Payload != nil {
		err := json.NewEncoder(body).Encode(args.Payload)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, args.Method.String(), args.URL, body)
	if err != nil {
		return err
	}

	client := http.Client{
		Timeout: time.Millisecond * time.Duration(args.ReqTimeout),
	}

	res, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = res

	return nil
}
