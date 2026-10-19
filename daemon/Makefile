# sing-box features we embed: utls+reality, QUIC (hysteria2), gvisor + openvpn endpoint.
TAGS    := with_gvisor,with_quic,with_utls,with_openvpn,badlinkname,tfogo_checklinkname0
# -trimpath and -buildid= keep build-machine paths and ids out of the binary.
LDFLAGS := -s -w -buildid= -checklinkname=0
GOFLAGS := -trimpath -tags $(TAGS) -ldflags "$(LDFLAGS)"

bin/mayvpnd: $(shell find . -name '*.go') go.mod go.sum
	go build $(GOFLAGS) -o $@ ./cmd/mayvpnd

test:
	go vet -tags $(TAGS) ./...
	go test -tags $(TAGS) -ldflags "-checklinkname=0" ./...

.PHONY: test
