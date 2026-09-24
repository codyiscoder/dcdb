BINARY := dcdb
SRC := ./src

.PHONY: all build test vet cross run install clean

all: build

build:
	go build -o $(BINARY) $(SRC)

test:
	go test ./...

vet:
	go vet ./...

cross:
	GOFLAGS=-buildmode=exe GOOS=windows GOARCH=amd64 go build -o $(BINARY).exe $(SRC)
	GOFLAGS=-buildmode=exe GOOS=linux   GOARCH=amd64 go build -o $(BINARY)-linux-amd64 $(SRC)
	GOFLAGS=-buildmode=exe GOOS=linux   GOARCH=arm64 go build -o $(BINARY)-linux-arm64 $(SRC)

run: build
	./$(BINARY)

install:
	go install $(SRC)

clean:
	rm -f $(BINARY) $(BINARY).exe $(BINARY)-linux-amd64 $(BINARY)-linux-arm64
