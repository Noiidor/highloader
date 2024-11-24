package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/Noiidor/highloader/pkg/highloader"
	"github.com/jessevdk/go-flags"
)

type AppFlags struct {
	Method       string        `short:"X" long:"method" default:"GET" description:"HTTP method to use. Supported methods are: GET, POST, PUT, PATCH, DELETE"`
	TotalReq     uint64        `short:"n" long:"total" default:"1" description:"Number of total requests to send"`
	Payload      string        `short:"p" long:"payload" default:"" description:"Request payload"`
	RPS          uint          `long:"rps" default:"1" description:"RPS to hold. Can be higher than specified in the first few seconds"`
	ReqTimeout   time.Duration `short:"T" long:"req-timeout" default:"5s" description:"Individual request timeout to wait"`
	TotalTimeout time.Duration `short:"t" long:"timeout" default:"1h" description:"Total time to execute"`
	Positional   struct {
		URL string
	} `positional-args:"yes" required:"yes"`
}

// func (o AppFlags) Usage() string {
// 	return ""
// }

func main() {

	// memStats := new(runtime.MemStats)
	// go func() {
	// 	for {
	// 		time.Sleep(time.Second * 1)
	// 		runtime.ReadMemStats(memStats)
	// 		PrintMemStats(memStats)
	// 	}
	// }()

	opts := AppFlags{}
	flagsParser := flags.NewParser(&opts, flags.Default)
	_, err := flagsParser.Parse()
	if err != nil && errors.Is(err, flags.ErrHelp) {
		panic(err)
	}
	// _, err := flags.Parse(&opts)
	// if err != nil {
	// 	if err == flags.ErrHelp {
	// 		os.Exit(0)
	// 	}
	// 	panic(err)
	// }

	if opts.Positional.URL == "" {
		fmt.Println("URL arg is empty")
		os.Exit(1)
	}

	if opts.RPS == 0 {
		fmt.Println("0 RPS is nit allowed")
		os.Exit(1)
	}

	if _, ok := highloader.HTTPMethodEnum[opts.Method]; !ok {
		fmt.Println("Unsupported HTTP method")
		os.Exit(1)
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

	err = highloader.Run(
		ctx,
		highloader.AppArgs{
			URL:         opts.Positional.URL,
			Method:      highloader.HTTPMethodEnum[opts.Method],
			HTTPVersion: 1,
			ReqTotal:    opts.TotalReq,
			Payload:     []byte(opts.Payload),
			ReqTimeout:  opts.ReqTimeout,
			Timeout:     opts.TotalTimeout,
			RPS:         uint32(opts.RPS),
		},
		os.Stderr,
	)
	if err != nil {
		fmt.Printf("Error: %s", err)
	}
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
