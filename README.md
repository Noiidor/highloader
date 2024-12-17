# TODO
- Cookies?
- Distributed requests? 
- Use CLI frontend for pretty output

# What is this?
CLI utility for load testing of HTTP servers, written in Go, fully concurrent, utilizing every CPU core available(by default).

# Is this useful?
Apart from being a little toy utility to test your pet-projects, it can be integrated into CI/CD pipeline for automated testing(if i ever push the image to DockerHub).

# Why?
I wanted to make something, that can take advantage of Go's first-class concurrency and do something clever. Is it clever? Not really, but i solved many interesting issues along the way.

# Usage
It can be used as a binary or Go package.  
Binary:
```sh
hloader -X POST -n 100 --rps 999 "http://localhost:5050/endpoint"
```
`-X` is a HTTP method. Currently supported methods: GET, POST, PUT, PATCH, DELETE  
`-n` is a total number of requests.  
`--rps` is a Requests Per Second.  
For more options call `--help`.

Package:
```go
func main() {
    ctx := context.Background()
    opts := highloader.Opts{
        URL: "url",
	Method: highloader.HTTPMethodEnum["GET"],
        RPS: 100,
        // There are more options
    }
    numRoutines := runtime.GOMAXPROCS(0)
    numReceiverRoutines := 1
    stats, errs, err := highloader.Run(ctx, numRoutines, numReceiverRoutines opts)
    if err != nil {
        log.Fatal(err)
    }

    for {
        select {
        case stat := <-stats:
            fmt.Println(stat)
        case err = <-errs:
            fmt.Println(err)
        case <-ctx.Done():
            return
        }
    }
}
```

You can see additional examples in `pkg/highloader/highloader_test.go`.
