package main

import (
	"fmt"
	"os"

	"github.com/Noiidor/highloader/pkg/highloader"
)

func main() {
	err := highloader.Run(highloader.AppArgs{
		URL:         "http://127.0.0.1:5050/echo",
		Method:      highloader.POST,
		HTTPVersion: "1.1",
		ReqTotal:    20000,
		Payload:     []byte("{test: \"test\"}"),
		ReqTimeout:  5,
		Timeout:     100,
		RPS:         3000,
	},
		os.Stdout,
	)
	if err != nil {
		fmt.Printf("Error: %s", err)
	}
}
