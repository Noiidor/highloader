package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Noiidor/highloader/pkg/highloader"
	"github.com/jessevdk/go-flags"
)

type AppArgs struct {
	Method         string            `short:"X" long:"method" default:"GET" description:"HTTP method to use. Supported methods are: GET, POST, PUT, PATCH, DELETE"`
	TotalReq       uint64            `short:"n" long:"total" default:"1" description:"Number of total requests to send"`
	Payload        string            `short:"p" long:"payload" default:"" description:"Request payload"`
	Headers        map[string]string `short:"H" long:"header" description:"Request headers. You can specify multiple values for header separated by comma"`
	RPS            uint              `long:"rps" default:"1" description:"RPS to hold. Can be higher than specified in the first few seconds"`
	ReqTimeout     time.Duration     `short:"T" long:"req-timeout" default:"5s" description:"Individual request timeout to wait"`
	TotalTimeout   time.Duration     `short:"t" long:"timeout" default:"1h" description:"Total time to execute"`
	PrintStatsFreq time.Duration     `long:"freq" default:"300ms" description:"Stats printing frequency"`
	Positional     struct {
		URL string
	} `positional-args:"yes" required:"yes"`
}

func main() {

	// memStats := new(runtime.MemStats)
	// go func() {
	// 	for {
	// 		time.Sleep(time.Second * 1)
	// 		runtime.ReadMemStats(memStats)
	// 		PrintMemStats(memStats)
	// 	}
	// }()

	args := AppArgs{}
	flagsParser := flags.NewParser(&args, flags.Default)
	_, err := flagsParser.Parse()
	if err != nil && errors.Is(err, flags.ErrHelp) {
		panic(err)
	}

	if _, ok := highloader.HTTPMethodEnum[args.Method]; !ok {
		fmt.Println("Unsupported HTTP method")
		os.Exit(1)
	}

	headers := http.Header{}
	for k, v := range args.Headers {
		headers[k] = strings.Split(v, ",")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, syscall.SIGINT, syscall.SIGTERM)

		<-exit
		fmt.Println("\nGracefully shutting down...")
		cancel()
	}()

	before := time.Now()

	statsChan, errorsChan, err := highloader.Run(
		ctx,
		highloader.Opts{
			URL:           args.Positional.URL,
			Method:        highloader.HTTPMethodEnum[args.Method],
			HTTPVersion:   1,
			ReqTotal:      args.TotalReq,
			Payload:       []byte(args.Payload),
			Headers:       headers,
			ReqTimeout:    args.ReqTimeout,
			TotalTimeout:  args.TotalTimeout,
			RPS:           uint32(args.RPS),
			StatsPushFreq: args.PrintStatsFreq,
		},
	)
	if err != nil {
		fmt.Printf("Error: %s", err)
		os.Exit(1)
	}

	for v := range statsChan {
		fmt.Printf("Total Requests: %d\n", v.TotalRequests)
		fmt.Printf("RPS: %d\n", v.RPS)
		fmt.Printf("Successful Requests: %d\n", v.SuccessRequests)
		fmt.Printf("Failed Requests: %d\n", v.FailedRequests)
		fmt.Printf("Error Requests: %d\n", v.Errors)
	}
	errorsMap := make(map[string]struct{})
	for v := range errorsChan {
		if _, ok := errorsMap[v.Error()]; !ok {
			errorsMap[v.Error()] = struct{}{}
			fmt.Printf("Err: %s\n", v)
		}
	}

	fmt.Printf("Total time: %s\n", time.Since(before))
}

func PrintMemStats(m *runtime.MemStats) {
	fmt.Printf(
		`
		MEM STATS:
		Total Allocated: %d MB,
		Allocated: %d MB,
		GC Cycles: %d,
		Last GC: %s,
		Stack inuse: %d MB,
		`,
		m.TotalAlloc/(1024*1024),
		m.HeapAlloc/(1024*1024),
		m.NumGC,
		time.Unix(0, int64(m.LastGC)).String(),
		m.StackInuse/(1024*1024),
	)
}
