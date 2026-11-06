set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

# 原生 UI 热重载
dev *args:
    ./scripts/native-env go tool mygo dev {{args}}

# 仅在用户明确要求时构建，统一复用 build 目录
build: deps
    ./scripts/native-env go tool mygo build -skip-dmg -skip-installer -o build
    ./scripts/native-env go run ./cmd/package -out build

# 预览 DMG 背景与安装布局，不构建应用
preview-dmg:
    swift -O scripts/dmg-artwork.swift build/.dmg-artwork build/dmg-preview.png

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

# 隔离安装轻量投影模型的训练依赖
model-env:
    test -x .cache/projection-model/venv/bin/python || uv venv --python 3.11 .cache/projection-model/venv
    uv pip install --python .cache/projection-model/venv/bin/python -r scripts/projection-model/requirements.txt

# 下载 CC0 全景图，生成按场景分组的七类实验数据
model-data *args:
    .cache/projection-model/venv/bin/python scripts/projection-model/dataset.py {{args}}

# 本机训练并导出 TFLite 实验模型
model-train *args:
    .cache/projection-model/venv/bin/python scripts/projection-model/train.py {{args}}

# 原生 LiteRT 七分类验证，不修改媒体或播放历史
classify *args:
    ./scripts/native-env go run ./cmd/classify {{args}}

# 生成七类中性编号视频、边界样本和三轮识别对照
video-samples *args:
    .cache/projection-model/venv/bin/python scripts/projection-model/samples.py {{args}}
