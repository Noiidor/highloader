package main

import (
	"fmt"

	"github.com/Noiidor/highloader/pkg/highloader"
)

func main() {
	err := highloader.Run(highloader.AppArgs{
		URL:         "http://127.0.0.1:5050/echo",
		Method:      highloader.POST,
		HTTPVersion: "1.1",
		ReqTotal:    5000,
		Payload:     "{test: \"test\"}",
		ReqTimeout:  100,
		Timeout:     100,
	})
	if err != nil {
		fmt.Printf("Error: %s", err)
	}
}
