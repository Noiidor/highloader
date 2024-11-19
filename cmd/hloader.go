package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Noiidor/highloader/pkg/highloader"
)

func main() {
	ctx := context.Background()
	err := highloader.Run(
		ctx,
		highloader.AppArgs{
			URL:         "http://127.0.0.1:5050/echo",
			Method:      highloader.POST,
			HTTPVersion: 1,
			ReqTotal:    20000,
			Payload:     []byte("{test: \"test\"}"),
			ReqTimeout:  time.Second * 5,
			Timeout:     time.Second * 100,
			RPS:         3000,
		},
		os.Stdout,
	)
	if err != nil {
		fmt.Printf("Error: %s", err)
	}
}
