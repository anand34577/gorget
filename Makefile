VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -buildid= -X github.com/anand34577/gorget/internal/core.Version=$(VERSION)
CLDFLAGS := -s -w -buildid= -X github.com/anand34577/gorget/client.Version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 linux/arm windows/amd64 windows/arm64 darwin/amd64 darwin/arm64

.PHONY: repro bench docs-site all web server cli desktop proto test lint release release-cli packages-linux packages-linux-desktop docker run clean android-aar android android-release

all: web server

web:
	cd web && npm ci --no-audit --no-fund && npm run build

server:
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o bin/gorget-server ./cmd/gorget-server

proto:
	buf lint && buf generate

# Client CLI + daemon (pure Go, no cgo)
cli:
	CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "$(CLDFLAGS)" -o bin/gorget ./cmd/gorget

# Tray app for the current OS (cgo; on Linux needs libgtk-3-dev and libwebkit2gtk-4.1-dev)
desktop:
	cd desktop/frontend && npm ci --no-audit --no-fund && npm run build
	cd desktop && CGO_ENABLED=1 go build -tags gtk3 -trimpath -buildvcs=false -ldflags "$(CLDFLAGS)" -o ../bin/gorget-desktop .

test:
	go vet ./...
	go test -race ./...
	GOOS=windows go vet ./client/... ./cmd/gorget
	GOOS=darwin go vet ./client/... ./cmd/gorget
	cd web && npx tsc -b
	cd desktop/frontend && npx tsc -b
	cd desktop && go vet -tags gtk3 ./...
	cd terraform-provider-gorget && go vet ./...

release: web
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "building $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" \
			-o dist/gorget-server_$(VERSION)_$${os}_$${arch}$$ext ./cmd/gorget-server || exit 1; \
	done
	cd dist && sha256sum gorget-server_* > SHA256SUMS

# Client CLI for every desktop platform. Tray apps, .deb/.rpm, MSI and .pkg are built per OS:
#   Linux packages: make packages-linux   Windows: packaging/windows/build-msi.ps1   macOS: packaging/macos/build-pkg.sh
release-cli:
	@for p in linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64; do 		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; 		echo "building gorget $$os/$$arch"; 		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -buildvcs=false -ldflags "$(CLDFLAGS)" 			-o dist/gorget_$(VERSION)_$${os}_$${arch}$$ext ./cmd/gorget || exit 1; 	done

# Needs nfpm (https://nfpm.goreleaser.com). The desktop package needs bin/linux-ARCH/gorget-desktop built on Linux.
packages-linux:
	@for arch in amd64 arm64; do 		mkdir -p bin/linux-$$arch dist; 		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -buildvcs=false -ldflags "$(CLDFLAGS)" -o bin/linux-$$arch/gorget ./cmd/gorget || exit 1; 		for t in deb rpm; do VERSION=$(patsubst v%,%,$(VERSION)) ARCH=$$arch envsubst '$$ARCH $$VERSION' < packaging/nfpm.yaml > bin/nfpm-$$arch.yaml && nfpm package -f bin/nfpm-$$arch.yaml -p $$t -t dist/ || exit 1; done; 	done

# Linux tray app packages for the current architecture (cgo: needs libgtk-3-dev and libwebkit2gtk-4.1-dev).
packages-linux-desktop:
	cd desktop/frontend && npm ci --no-audit --no-fund && npm run build
	arch=$$(go env GOARCH); mkdir -p bin/linux-$$arch dist; \
	(cd desktop && CGO_ENABLED=1 go build -tags gtk3 -trimpath -buildvcs=false -ldflags "$(CLDFLAGS)" -o ../bin/linux-$$arch/gorget-desktop .) && \
	for t in deb rpm; do VERSION=$(patsubst v%,%,$(VERSION)) ARCH=$$arch envsubst '$$ARCH $$VERSION' < packaging/nfpm-desktop.yaml > bin/nfpm-$$arch.yaml && nfpm package -f bin/nfpm-$$arch.yaml -p $$t -t dist/ || exit 1; done

docker:
	docker build --build-arg VERSION=$(VERSION) -t gorget-server:$(VERSION) .

# Local development: plain HTTP on :8080, data in ./data
run: server
	./bin/gorget-server serve -public-url http://localhost:8080 -listen 127.0.0.1:8080 -tls-mode off -data-dir ./data

# ---------- Android (needs ANDROID_HOME, ANDROID_NDK_HOME, JDK 17, gomobile) ----------
android-aar:
	mkdir -p android/app/libs
	gomobile bind -target=android/arm64,android/arm,android/amd64 -androidapi 26 -javapkg io.gorget 		-trimpath -ldflags "-s -w -X github.com/anand34577/gorget/client.Version=$(patsubst v%,%,$(VERSION))" -o android/app/libs/gorgetcore.aar ./mobile/gorgetcore

android: android-aar
	cd android && ./gradlew assembleDebug

android-release: android-aar
	cd android && ./gradlew assembleRelease

clean:
	rm -rf bin dist

# ---------- verification ----------

# Builds the server and the client twice in different directories and checks that the bytes are identical.
repro:
	sh scripts/repro-check.sh

# Micro-benchmarks (policy engine, packet filter, discovery crypto, post-quantum exchange, tunnel throughput).
bench:
	go test -run '^$$' -bench . -benchmem ./internal/policy ./client/tunx ./client/disco ./client/pq ./client

# Documentation site (needs: pip install mkdocs-material)
docs-site:
	mkdocs build --strict
