#!/bin/sh

echo "### Running gofmt..."
files="$(gofmt -l .)"

if [ !  -z "$files" ]; then
	# run gofmt to print out the diff of what needs to be changed
	gofmt -d -e .
	exit 1
fi

echo "### Running staticcheck..."
staticcheck ./...
