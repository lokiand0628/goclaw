.PHONY: build run dev tidy clean

# 编译
build:
	go build -o goclaw ./cmd/clawdbot/
	chmod +x goclaw

# 加载 .env 后启动
run: build
	@export $(shell grep -v '^#' .env | grep -v '^$$' | xargs) && ./goclaw start

# 开发模式（不编译二进制，直接 go run）
dev:
	@export $(shell grep -v '^#' .env | grep -v '^$$' | xargs) && go run ./cmd/clawdbot/ start

# 管理命令（加载 .env）
sessions:
	@export $(shell grep -v '^#' .env | grep -v '^$$' | xargs) && ./goclaw sessions list

rollback:
	@export $(shell grep -v '^#' .env | grep -v '^$$' | xargs) && ./goclaw rollback

checkpoints:
	@export $(shell grep -v '^#' .env | grep -v '^$$' | xargs) && ./goclaw rollback list

tidy:
	go mod tidy

clean:
	rm -f goclaw goclaw.backup
