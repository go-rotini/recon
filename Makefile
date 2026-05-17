.PHONY: all clean lint test test-acceptance test-bench \
        test-fuzz test-mutation test-race

all: clean lint test test-acceptance test-bench test-fuzz test-mutation test-race

clean:
	@rm -rf *.out test_mutation.json

lint:
	@gofmt_unformatted=$$(gofmt -l . 2>/dev/null | grep -v '^testdata/' || true); \
	test -z "$$gofmt_unformatted" || (echo "files not formatted:" && echo "$$gofmt_unformatted" && exit 1)
	go vet ./...
	go mod verify
	go tool golangci-lint run ./...
	go tool go-licenses check ./...
	go tool govulncheck ./...

test:
	@go test -v -count=1 -coverprofile=test.out ./...
	@go tool cover -func=test.out | tail -1

test-acceptance:
	@go test -v -count=1 -run TestAcceptance -coverprofile=test_acceptance.out ./...
	@go tool cover -func=test_acceptance.out | tail -1

test-bench:
	@go test -bench=. -benchmem -count=1 ./... | tee test_bench.out

# Fuzz targets land in Phase 11 (see RECON_PACKAGE_REQUIREMENTS.md §11 and §8.6).
# Until then this target is a no-op so `make all` doesn't fail on an empty package.
test-fuzz:
	@for target in FuzzParsePath FuzzCoerce FuzzMergeMaps FuzzBind; do \
		if go test -list "^$$target$$" ./... 2>/dev/null | grep -q "^$$target$$"; then \
			echo "→ $$target"; \
			go test -fuzz="^$$target$$" -fuzztime=60s -run=^$$ ./... ; \
		fi ; \
	done

test-mutation:
	@go tool github.com/go-gremlins/gremlins/cmd/gremlins unleash --config .gremlins.yaml

test-race:
	@go test -race -count=1 -coverprofile=test_race.out ./...
	@go tool cover -func=test_race.out | tail -1
