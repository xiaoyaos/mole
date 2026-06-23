REPO := mole
BIN := bin
GO := go

PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64

# 生成带 .exe 后缀的输出文件名
output_name = nt-$(2)-$(subst /,-,$(1))$(if $(findstring windows,$(word 1,$(subst /, ,$(1)))),.exe)

define platform_bins
SERVER_BINS += $(BIN)/server/$(call output_name,$(1),server)
CLIENT_BINS += $(BIN)/client/$(call output_name,$(1),client)
endef

$(foreach p,$(PLATFORMS),$(eval $(call platform_bins,$(p))))

.PHONY: all deps build release clean test run-server run-client $(PLATFORMS)

all: deps build

deps:
	$(GO) mod download
	$(GO) mod tidy

# ---- 本地编译（当前平台） ----

build: $(BIN)/server/nt-server$(suffix_extra) $(BIN)/client/nt-client$(suffix_extra)

$(BIN)/server/nt-server:
	mkdir -p $(BIN)/server
	$(GO) build -o $@ $(REPO)/cmd/server

$(BIN)/client/nt-client:
	mkdir -p $(BIN)/client
	$(GO) build -o $@ $(REPO)/cmd/client

# ---- 交叉编译（全部6平台） ----

define build_server
$(BIN)/server/$(call output_name,$(1),server):
	mkdir -p $$(BIN)/server
	GOOS=$(word 1,$(subst /, ,$(1))) GOARCH=$(word 2,$(subst /, ,$(1))) \
		$(GO) build -o $$@ $(REPO)/cmd/server
endef

define build_client
$(BIN)/client/$(call output_name,$(1),client):
	mkdir -p $$(BIN)/client
	GOOS=$(word 1,$(subst /, ,$(1))) GOARCH=$(word 2,$(subst /, ,$(1))) \
		$(GO) build -o $$@ $(REPO)/cmd/client
endef

$(foreach p,$(PLATFORMS),$(eval $(call build_server,$(p))))
$(foreach p,$(PLATFORMS),$(eval $(call build_client,$(p))))

release: $(SERVER_BINS) $(CLIENT_BINS)

# ---- 工具 ----

clean:
	rm -rf $(BIN)/
	$(GO) clean

test:
	$(GO) test ./...

run-server:
	sudo ./$(BIN)/server/nt-server -listen :8080

run-client:
	sudo ./$(BIN)/client/nt-client -server 127.0.0.1:8080 -name $(NAME) $(ARGS)
