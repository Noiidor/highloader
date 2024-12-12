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
