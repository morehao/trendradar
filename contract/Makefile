# trendradar-contract (nested Go module + npm package). Run targets from this directory.
SHELL := /bin/bash
.PHONY: install generate verify verify-versions go-test test publish

install:
	npm install

generate:
	node scripts/generate.mjs

verify:
	node scripts/generate.mjs
	git diff --exit-code -- gen contract.gen.go schema

verify-versions:
	node scripts/check-versions.mjs

go-test:
	go test ./...

test: verify-versions go-test

publish:
	node scripts/check-versions.mjs
	npm publish --access public
