set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

# 原生 UI 热重载
dev *args:
    ./scripts/native-env go tool mygo dev {{args}}

# 构建本机应用包
build out="build": deps
    ./scripts/native-env go tool mygo build -skip-dmg -skip-installer -o {{quote(out)}}
    ./scripts/native-env go run ./cmd/package -out {{quote(out)}}

# 获取固定版本的人脸检测运行库并校验 SHA-256
deps:
    go run ./cmd/deps

# 检查 Go 与 MyGo API
check:
    ./scripts/native-env go vet ./...
    ./scripts/native-env go tool mygo vet .
    test -z "$(gofmt -l .)"

# 仅运行算法、数据和媒体内核单元测试
test:
    ./scripts/native-env go test -race ./...

fmt:
    gofmt -w .

# 检查本机媒体依赖
doctor:
    ./scripts/native-env go run ./cmd/doctor
