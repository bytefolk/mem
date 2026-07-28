#!/usr/bin/env bash
# mem — 云端 provider 配置（idealab OpenAI 兼容网关）
#
# 用法：
#   1. cp scripts/cloud_env.example.sh scripts/cloud_env.sh
#   2. 编辑 scripts/cloud_env.sh，填入你的 idealab API key
#   3. source scripts/cloud_env.sh   # 把云端 provider 注入当前 shell
#   4. bash scripts/dev_up.sh        # 仅索引 worker 使用云端模型
#
# scripts/cloud_env.sh 已在 .gitignore 里（key 不会被提交）。
# dev_up.sh 的 provider 变量是 ${VAR:-默认ollama}，不 source 这个文件时
# 行为完全不变（仍走本地 ollama）——零破坏，按需切云端。

# ---- idealab OpenAI 兼容网关（已实测：/api/openai/v1/models 返回 200）----
export OPENAI_BASE_URL="https://idealab.alibaba-inc.com/api/openai"

# ---- 你的 idealab API key（替换下面这行；不要提交到 git）----
export OPENAI_API_KEY="<在这里填你的 idealab key>"

# ---- 文本 embedding ----
# embedding 输出维度必须与当前部署的 embeddings_text 列一致，且已有语料时
# 暂不允许在线切换向量空间。先用 `mem provider test embedding --spec <vendor:model>`
# 验证；版本化索引 generation 上线前，已有语料请离线迁移并完整重建。

# ---- 图片理解：云端 VLM 只在索引阶段生成 caption/metadata，不负责回答。----
export MEM_DEFAULT_VLM="openai:qwen-vl-max"

# ---- 视觉向量列仍用本地 CLIP（512-d，与 embeddings_visual 对齐）。
# 当前 VLM caption 会保存为可查看的索引元数据，但尚未单独写入文本向量；
# caption 的 text-route 检索是下一阶段任务，当前图片召回以 CLIP visual 为准。----
export MEM_DEFAULT_VISUAL_EMBEDDING="clip:ViT-B-32"
