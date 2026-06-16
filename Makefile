.PHONY: help check smoke shatter shatter-clean

help:
	@printf 'Targets:\n'
	@printf '  check          Run CI quality gates (vet, gofmt check, tests)\n'
	@printf '  smoke          Run smoke tests\n'
	@printf '  shatter        Run full shatter scan (requires SHATTER_BIN)\n'
	@printf '  shatter-clean  Remove shatter report output and write-guard artifacts\n'

check:
	go vet ./...
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		printf 'gofmt needs to be run on:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi
	go test ./cmd/...
	go test -race ./internal/...

smoke:
	./scripts/smoke.sh

shatter:
	./scripts/shatter-full-scan.sh

shatter-clean:
	rm -rf shatter-report .shatter
