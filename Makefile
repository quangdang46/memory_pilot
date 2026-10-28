BINARY := mempilotd
CLI := mempilot

.PHONY: build test vet gates ci clean

build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) ./cmd/$(BINARY)
	CGO_ENABLED=0 go build -o bin/$(CLI) ./cmd/$(CLI)

test:
	CGO_ENABLED=0 go test ./...

# Race detector must run with cgo enabled (the detector needs it) and without
# the pure-Go-only assumption. The daemon crashed with "concurrent map read
# and map write" in production because the plain test target cannot see races.
race:
	go test -race ./...

ci: fmt vet test race gates build

vet:
	CGO_ENABLED=0 go vet ./...

# Formatting was unchecked, and 15 files had drifted out of gofmt — including
# three in internal/ that the CI target compiled and tested happily.
fmt:
	@test -z "$$(gofmt -l . 2>/dev/null | grep -v '^\.tmp')" || \
		{ echo "gofmt needed:"; gofmt -l . 2>/dev/null | grep -v '^\.tmp'; exit 1; }

gates:
	@scripts/gates/st1_no_signal_capability.sh
	@scripts/gates/st2_sealed_actions.sh
	@scripts/gates/st3_layering.sh
	@scripts/gates/st4_no_network.sh
	@scripts/gates/st13_no_user_data_deletes.sh

ci: vet test gates build
	@echo "CI GREEN"
