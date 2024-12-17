package hloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

func RunApp(ctx context.Context) error {
	args := AppArgs{}
	flagsParser := flags.NewParser(&args, flags.Default)
	_, err := flagsParser.Parse()
	if err != nil {
		if errors.Is(err, flags.ErrHelp) {
			return nil
		}
		return err
	}

	if _, ok := highloader.HTTPMethodEnum[args.Method]; !ok {
		return errors.New("unsupported HTTP method")
	}

	headers := http.Header{}
	for k, v := range args.Headers {
		headers[k] = strings.Split(v, ",")
	}

	ctx, cancel := context.WithTimeout(ctx, args.TotalTimeout)
	ctx, cancel = signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	before := time.Now()

	numIOWorkers := runtime.GOMAXPROCS(0)
	numCPUWorkers := 1

	statsChan, errorsChan, err := highloader.Run(
		ctx,
		numIOWorkers,
		numCPUWorkers,
		highloader.Args{
			URL:           args.Positional.URL,
			Method:        highloader.HTTPMethodEnum[args.Method],
			HTTPVersion:   1,
			ReqTotal:      args.TotalReq,
			Payload:       []byte(args.Payload),
			Headers:       headers,
			ReqTimeout:    args.ReqTimeout,
			RPS:           uint32(args.RPS),
			StatsPushFreq: args.PrintStatsFreq,
		},
	)
	if err != nil {
		return err
	}

	for v := range statsChan {
		fmt.Println(v)
	}
	// Print only unique errors
	errorsMap := make(map[string]struct{})
	for v := range errorsChan {
		if _, ok := errorsMap[v.Error()]; !ok {
			errorsMap[v.Error()] = struct{}{}
			fmt.Printf("\nErr: %s\n", v)
		}
	}

	fmt.Printf("Total time: %s\n", time.Since(before))

	return nil
}
