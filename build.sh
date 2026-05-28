#!/bin/bash

# aiscan 构建脚本
# 用法:
#   ./build.sh                                  # 读取 config.yaml，编译多平台可执行文件
#   ./build.sh -g                               # 仅打印生成的 ldflags，不编译
#   ./build.sh -o linux/amd64                   # 快速编译单一平台
#   ./build.sh -o "linux/amd64 darwin/arm64"    # 编译指定平台
#   ./build.sh --config prod.yaml               # 使用指定配置文件
#   ./build.sh --llm-model deepseek-chat        # CLI 覆盖配置文件中的值
#   ./build.sh --embed                          # 嵌入扫描资源（不加 emptytemplates/noembed tag）
#   ./build.sh --ioa                            # 同时编译 ioa server 二进制

set -euo pipefail

# ─── 变量 ───────────────────────────────────────────────────────

CONFIG_FILE="config.yaml"
OSARCH=""
EXTRA_TAGS=""
OUTPUT_DIR="dist"
GENERATE_ONLY=false
EMBED_RESOURCES=false
BUILD_IOA=false
QUICK_TARGET=""
PROFILE="mini"
AISCAN_BIN="aiscan"

# CLI 覆盖（优先级高于 config.yaml）
OPT_PROVIDER=""
OPT_BASE_URL=""
OPT_API_KEY=""
OPT_MODEL=""
OPT_PROXY=""
OPT_CYBERHUB_URL=""
OPT_CYBERHUB_KEY=""
OPT_CYBERHUB_MODE=""
OPT_IOA_URL=""
OPT_IOA_NODE_NAME=""
OPT_IOA_SPACE=""
OPT_VERIFY=""
OPT_VERIFY_TIMEOUT=""
OPT_TAVILY_KEYS=""
OPT_WEBSEARCH_PROXY=""

MODULE="github.com/chainreactors/aiscan/cmd"

DEFAULT_OSARCH="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64"

# ─── YAML 解析 ──────────────────────────────────────────────────

yaml_val() {
    local file="$1" section="$2" key="$3"
    [ -f "$file" ] || return 0
    sed -n "/^${section}:/,/^[a-zA-Z_]/{
        /^  ${key}:/{
            s/^  ${key}:[[:space:]]*//
            s/[[:space:]]#.*$//
            s/\r$//
            s/^\"//; s/\"$//
            s/^'//; s/'$//
            /^$/d
            p
        }
    }" "$file" 2>/dev/null | head -1
}

# ─── 参数解析 ────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
    case $1 in
        --config|-c)        CONFIG_FILE="$2"; shift 2 ;;
        -o)                 OSARCH="$2"; shift 2 ;;
        --tags)             EXTRA_TAGS="$2"; shift 2 ;;
        --output)           OUTPUT_DIR="$2"; shift 2 ;;
        -g|--ldflags)       GENERATE_ONLY=true; shift ;;
        --embed)            EMBED_RESOURCES=true; shift ;;
        --ioa)              BUILD_IOA=true; shift ;;
        --profile)          PROFILE="$2"; shift 2 ;;
        --llm-provider)     OPT_PROVIDER="$2"; shift 2 ;;
        --llm-base-url)     OPT_BASE_URL="$2"; shift 2 ;;
        --llm-api-key)      OPT_API_KEY="$2"; shift 2 ;;
        --llm-model)        OPT_MODEL="$2"; shift 2 ;;
        --llm-proxy)        OPT_PROXY="$2"; shift 2 ;;
        --cyberhub-url)     OPT_CYBERHUB_URL="$2"; shift 2 ;;
        --cyberhub-key)     OPT_CYBERHUB_KEY="$2"; shift 2 ;;
        --cyberhub-mode)    OPT_CYBERHUB_MODE="$2"; shift 2 ;;
        --ioa-url)          OPT_IOA_URL="$2"; shift 2 ;;
        --ioa-node-name)    OPT_IOA_NODE_NAME="$2"; shift 2 ;;
        --space)            OPT_IOA_SPACE="$2"; shift 2 ;;
        --verify)           OPT_VERIFY="$2"; shift 2 ;;
        --verify-timeout)   OPT_VERIFY_TIMEOUT="$2"; shift 2 ;;
        --tavily-keys)      OPT_TAVILY_KEYS="$2"; shift 2 ;;
        --websearch-proxy)  OPT_WEBSEARCH_PROXY="$2"; shift 2 ;;
        -h|--help)
            cat <<'HELP'
aiscan 构建脚本

用法: ./build.sh [选项]

配置:
  --config, -c FILE     配置文件路径 (默认: config.yaml)
  -g, --ldflags         仅打印生成的 ldflags，不编译

构建:
  -o OSARCH             目标平台，空格分隔 (默认: linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64)
  --tags TAGS           额外 build tags，逗号分隔
  --output DIR          输出目录 (默认: dist)
  --embed               嵌入扫描资源（不加 emptytemplates/noembed tag）
  --ioa                 同时编译 ioa server 二进制
  --profile PROFILE     构建配置: mini (默认, 核心工具) 或 full (包含 katana/uncover/ioa)

LLM 覆盖（优先级高于 config.yaml）:
  --llm-provider NAME
  --llm-base-url URL
  --llm-api-key KEY
  --llm-model NAME
  --llm-proxy URL

Cyberhub 覆盖:
  --cyberhub-url URL
  --cyberhub-key KEY
  --cyberhub-mode MODE

IOA 覆盖:
  --ioa-url URL
  --ioa-node-name NAME
  --space NAME

Web Search:
  --tavily-keys KEYS    Comma-separated Tavily API keys (rotation)

扫描覆盖:
  --verify MODE         auto, off, low, medium, high, critical
  --verify-timeout SEC

示例:
  ./build.sh                                    # 读取 config.yaml 编译全平台
  ./build.sh -o linux/amd64                     # 快速编译单平台
  ./build.sh --config prod.yaml -o linux/amd64  # 使用生产配置编译
  ./build.sh --cyberhub-url http://10.0.0.1:9000 --cyberhub-key mykey
  ./build.sh --llm-provider deepseek --llm-model deepseek-chat
  ./build.sh --embed                            # 嵌入资源的完整构建
  ./build.sh -g                                 # 打印 ldflags（用于自定义构建命令）
  ./build.sh --ioa -o linux/amd64               # 同时编译 ioa server
  ./build.sh --profile full -o linux/amd64      # full 构建 (含 katana/uncover/ioa)
HELP
            exit 0
            ;;
        *)
            echo "未知选项: $1" >&2
            echo "使用 -h 查看帮助" >&2
            exit 1
            ;;
    esac
done

# ─── 读取配置 ────────────────────────────────────────────────────

resolve() {
    # resolve CLI_VALUE CONFIG_VALUE → 非空的那个（CLI 优先）
    if [ -n "$1" ]; then echo "$1"; elif [ -n "$2" ]; then echo "$2"; fi
}

CFG_PROVIDER=$(resolve "$OPT_PROVIDER" "$(yaml_val "$CONFIG_FILE" llm provider)")
CFG_BASE_URL=$(resolve "$OPT_BASE_URL" "$(yaml_val "$CONFIG_FILE" llm base_url)")
CFG_API_KEY=$(resolve "$OPT_API_KEY" "$(yaml_val "$CONFIG_FILE" llm api_key)")
CFG_MODEL=$(resolve "$OPT_MODEL" "$(yaml_val "$CONFIG_FILE" llm model)")
CFG_SCANNER_PROXY=$(resolve "$OPT_PROXY" "$(yaml_val "$CONFIG_FILE" cyberhub proxy)")

CFG_CYBERHUB_URL=$(resolve "$OPT_CYBERHUB_URL" "$(yaml_val "$CONFIG_FILE" cyberhub url)")
CFG_CYBERHUB_KEY=$(resolve "$OPT_CYBERHUB_KEY" "$(yaml_val "$CONFIG_FILE" cyberhub key)")
CFG_CYBERHUB_MODE=$(resolve "$OPT_CYBERHUB_MODE" "$(yaml_val "$CONFIG_FILE" cyberhub mode)")

CFG_IOA_URL=$(resolve "$OPT_IOA_URL" "$(yaml_val "$CONFIG_FILE" ioa url)")
CFG_IOA_NODE_NAME=$(resolve "$OPT_IOA_NODE_NAME" "$(yaml_val "$CONFIG_FILE" ioa node_name)")
CFG_IOA_SPACE=$(resolve "$OPT_IOA_SPACE" "$(yaml_val "$CONFIG_FILE" ioa space)")

CFG_VERIFY=$(resolve "$OPT_VERIFY" "$(yaml_val "$CONFIG_FILE" scan verify)")
CFG_VERIFY_TIMEOUT=$(resolve "$OPT_VERIFY_TIMEOUT" "$(yaml_val "$CONFIG_FILE" scan verify_timeout)")

CFG_TAVILY_KEYS=$(resolve "$OPT_TAVILY_KEYS" "$(yaml_val "$CONFIG_FILE" websearch tavily_keys)")
CFG_WEBSEARCH_PROXY=$(resolve "$OPT_WEBSEARCH_PROXY" "$(yaml_val "$CONFIG_FILE" websearch proxy)")

# build 段仅从 config.yaml 读取（不做 CLI 覆盖）
if [ -z "$OSARCH" ]; then
    OSARCH=$(yaml_val "$CONFIG_FILE" build osarch)
fi
if [ -z "$EXTRA_TAGS" ]; then
    EXTRA_TAGS=$(yaml_val "$CONFIG_FILE" build tags)
fi
CFG_OUTPUT=$(yaml_val "$CONFIG_FILE" build output)
if [ -n "$CFG_OUTPUT" ] && [ "$OUTPUT_DIR" = "dist" ]; then
    OUTPUT_DIR="$CFG_OUTPUT"
fi

# ─── 生成 ldflags ───────────────────────────────────────────────

LDFLAGS="-s -w"

add_ldflag() {
    local var="$1" val="$2"
    if [ -n "$val" ]; then
        LDFLAGS="$LDFLAGS -X '${MODULE}.${var}=${val}'"
    fi
}

add_ldflag DefaultProvider     "$CFG_PROVIDER"
add_ldflag DefaultBaseURL      "$CFG_BASE_URL"
add_ldflag DefaultAPIKey       "$CFG_API_KEY"
add_ldflag DefaultModel        "$CFG_MODEL"
add_ldflag DefaultScannerProxy  "$CFG_SCANNER_PROXY"
add_ldflag DefaultCyberhubURL  "$CFG_CYBERHUB_URL"
add_ldflag DefaultCyberhubKey  "$CFG_CYBERHUB_KEY"
add_ldflag DefaultCyberhubMode "$CFG_CYBERHUB_MODE"
add_ldflag DefaultIOAURL       "$CFG_IOA_URL"
add_ldflag DefaultIOANodeName  "$CFG_IOA_NODE_NAME"
add_ldflag DefaultSpace        "$CFG_IOA_SPACE"
add_ldflag DefaultVerify       "$CFG_VERIFY"
add_ldflag DefaultVerifyTimeout "$CFG_VERIFY_TIMEOUT"
add_ldflag DefaultTavilyKeys      "$CFG_TAVILY_KEYS"
add_ldflag DefaultWebSearchProxy  "$CFG_WEBSEARCH_PROXY"

# ─── 仅打印 ldflags ─────────────────────────────────────────────

if [ "$GENERATE_ONLY" = true ]; then
    echo "$LDFLAGS"
    exit 0
fi

# ─── 打印配置摘要 ────────────────────────────────────────────────

echo "=== aiscan build ==="
echo "profile:  $PROFILE"
[ -f "$CONFIG_FILE" ] && echo "config:   $CONFIG_FILE" || echo "config:   (none)"
[ -n "$CFG_PROVIDER" ]     && echo "provider: $CFG_PROVIDER"
[ -n "$CFG_MODEL" ]        && echo "model:    $CFG_MODEL"
[ -n "$CFG_BASE_URL" ]     && echo "base_url: $CFG_BASE_URL"
[ -n "$CFG_CYBERHUB_URL" ] && echo "cyberhub: $CFG_CYBERHUB_URL"
[ -n "$CFG_IOA_URL" ]      && echo "ioa:      $CFG_IOA_URL"
[ -n "$CFG_VERIFY" ]       && echo "verify:   $CFG_VERIFY"

# ─── Profile ────────────────────────────────────────────────────

case "$PROFILE" in
    mini) ;;
    full)
        EXTRA_TAGS="full${EXTRA_TAGS:+,$EXTRA_TAGS}"
        BUILD_IOA=true
        AISCAN_BIN="aiscan-full"
        ;;
    *)
        echo "未知 profile: $PROFILE (可选: mini, full)" >&2
        exit 1
        ;;
esac

# ─── Build tags ──────────────────────────────────────────────────

TAGS="forceposix osusergo netgo"
if [ "$BUILD_IOA" = true ]; then
    TAGS="$TAGS sqlite"
fi
if [ "$EMBED_RESOURCES" != true ]; then
    TAGS="$TAGS emptytemplates noembed"
fi
if [ -n "$EXTRA_TAGS" ]; then
    TAGS="$TAGS $(echo "$EXTRA_TAGS" | tr ',' ' ')"
fi
echo "tags:     $TAGS"

# ─── 资源生成 ────────────────────────────────────────────────────

if [ "$EMBED_RESOURCES" = true ]; then
    echo "生成嵌入资源..."
    go generate ./pkg/resources
fi

# ─── 目标平台 ────────────────────────────────────────────────────

if [ -z "$OSARCH" ]; then
    OSARCH="$DEFAULT_OSARCH"
fi
echo "targets:  $OSARCH"
echo "output:   $OUTPUT_DIR"
echo ""

# ─── 编译 ────────────────────────────────────────────────────────

mkdir -p "$OUTPUT_DIR"

build_one() {
    local goos="$1" goarch="$2" main_pkg="$3" name="$4"
    local suffix=""
    [ "$goos" = "windows" ] && suffix=".exe"
    local output="${OUTPUT_DIR}/${name}_${goos}_${goarch}${suffix}"

    echo "  ${goos}/${goarch} -> ${output}"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -tags "$TAGS" -ldflags "$LDFLAGS" -buildvcs=false -o "$output" "$main_pkg"
}

# 解析 OSARCH 列表
OSARCH_NORMALIZED=$(echo "$OSARCH" | tr ',' ' ')
read -ra TARGETS <<< "$OSARCH_NORMALIZED"

echo "编译 aiscan..."
for target in "${TARGETS[@]}"; do
    IFS='/' read -ra PARTS <<< "$target"
    build_one "${PARTS[0]}" "${PARTS[1]}" ./cmd/aiscan "$AISCAN_BIN"
done

if [ "$BUILD_IOA" = true ]; then
    echo ""
    echo "编译 ioa..."
    for target in "${TARGETS[@]}"; do
        IFS='/' read -ra PARTS <<< "$target"
        build_one "${PARTS[0]}" "${PARTS[1]}" ./cmd/ioa ioa
    done
fi

# ─── 完成 ────────────────────────────────────────────────────────

echo ""
echo "构建完成:"
ls -lh "$OUTPUT_DIR"/aiscan* 2>/dev/null || true
[ "$BUILD_IOA" = true ] && ls -lh "$OUTPUT_DIR"/ioa_* 2>/dev/null || true
