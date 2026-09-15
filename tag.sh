#!/bin/bash
set -e

# 默认起始 Tag: 仓库没有任何 tag 时使用
DEFAULT_TAG="v1.0.1"

# 拉取远端 tag, 保证本地 tag 列表最新
git fetch --tags --quiet 2>/dev/null || true

if [ -n "$1" ]; then
    # 指定 tag 时直接使用, 例如: ./tag.sh v1.0.1
    NEW_TAG="$1"
    echo "使用指定 Tag: $NEW_TAG"
else
    # 获取最新 tag(按版本号排序)
    LATEST_TAG=$(git tag --list --sort=-v:refname | head -n 1)
    if [ -z "$LATEST_TAG" ]; then
        # 未找到 tag, 使用默认起始 Tag
        NEW_TAG="$DEFAULT_TAG"
        echo "未找到 Tag, 使用默认 Tag: $NEW_TAG"
    else
        # 找到 tag, 在最新 tag 基础上递增 patch 版本号
        echo "当前 Tag: $LATEST_TAG"
        # 提取最后一段版本号
        PREFIX=$(echo "$LATEST_TAG" | sed -E 's/\.[0-9]+$//')
        LAST_NUM=$(echo "$LATEST_TAG" | sed -E 's/^.*\.([0-9]+)$/\1/')
        NEW_NUM=$((LAST_NUM + 1))
        NEW_TAG="${PREFIX}.${NEW_NUM}"
        echo "新建 Tag: $NEW_TAG"
    fi
fi

# 校验 tag 是否已存在
if git rev-parse -q --verify "refs/tags/$NEW_TAG" >/dev/null; then
    echo "Tag 已存在: $NEW_TAG"
    exit 1
fi

# 创建 tag
git tag "$NEW_TAG"
# 推送 tag
git push origin "$NEW_TAG"
echo "Tag 发布成功: $NEW_TAG"

