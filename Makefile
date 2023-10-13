build kourai:
	CGO_ENABLED=0 go build -o kourai -ldflags='-extldflags=-static' -x
