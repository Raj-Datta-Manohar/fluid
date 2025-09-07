SHELL := /usr/bin/env bash

.PHONY: all build run run-memberlist cli example lint tidy clean

all: build

build:
	go build ./...

run:
	go run ./cmd/agent

run-memberlist:
	GOSSIP_IMPL=memberlist \
	GOSSIP_BIND?=127.0.0.1:7946 \
	GOSSIP_NODE?=node-1 \
	go run ./cmd/agent

cli:
	go build -o bin/fluidctl ./cmd/fluidctl

example:
	go run ./examples/basic

lint:
	@golangci-lint run || true

tidy:
	go mod tidy

clean:
	rm -rf bin
