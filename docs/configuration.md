# 配置与初始化

配置是 **cyber-harness 的通用功能**。下文使用 `aiscan` 举例；通用 `agent` CLI 支持相同的配置命令，嵌入式宿主可直接使用公共配置包。

## 首次配置

```sh
aiscan init
aiscan init --non-interactive --provider openai --model your-model
aiscan init --project
```

`init` 默认写 `~/.cyber/cyber.yaml`。在交互终端中引导填写协议、端点、模型和 API key；密钥输入隐藏，留空可使用环境变量。非终端或 `--non-interactive` 不进入问答，只写显式参数，不读取或持久化环境变量凭据。初始化不会向模型服务发请求。

`init --project` 在当前目录创建最小 `cyber.yaml`，不复制用户模型、个人凭据或运行时配置。未填写的示例保持注释，不会阻止 `OPENAI_*`、`ANTHROPIC_*` 补齐配置。

已有文件默认保持不变。`--force` 创建独立备份后原子替换；失败保留旧文件。`-c <path> init` 创建指定文件，不能与 `--project` 同用。旧 `--init` 兼容当前目录、非交互初始化，并提示使用新命令；它也不再直接覆盖文件。

## 文件与优先级

```text
CLI > CYBER_*／集成专用环境变量 > 项目配置 > 用户配置
    > 协议环境变量 > 默认值
```

用户文件为 `~/.cyber/cyber.yaml`。项目配置从工作目录向上寻找最近的 `cyber.yaml`，查找止于最近 Git 根目录、用户目录或文件系统根，只加载最近一份。当前目录配置优先于全局配置。通用 agent 的 `--workdir` 同时确定项目发现起点。

未发现项目配置时，兼容二进制旁的 `cyber.yaml`，其优先级高于用户配置。显式 `-c` 仅加载指定文件，适合 CI；仍允许环境变量和 CLI 覆盖。

普通映射按字段合并，列表整体替换。`llm.providers` 按 ID 合并：项目可以只覆盖某个 profile 的模型，继承其端点与凭据。没有 ID 的旧条目按顺序分配 `profile-1` 等 ID；建议多层配置显式填写 ID。

文件中空的 LLM 协议、端点、模型、密钥字符串按未填写处理，允许低优先级来源补齐。显式 CLI 空值表示清空；`false`、`0`、空列表不会被当成缺省值。

## 模型 profiles

```yaml
llm:
  active_profile: work
  providers:
    - id: work
      provider: openai
      base_url: https://api.example.com/v1
      model: your-model
```

```sh
aiscan config profiles
aiscan agent --profile work --model another-model -p "列出当前目录"
aiscan config use work
aiscan config use work --project
```

选择 profile 后逐字段应用覆盖，`--model` 不会丢失端点、密钥、代理和限额。协议环境变量只补齐所选协议的缺失值；其他协议的凭据不会改变已选 profile。没有显式选择时使用第一项，错误 ID 和重复 ID 报错，不自动切换模型重试。

`config use` 默认保存用户级选择，`--project` 修改最近项目配置，无项目配置则创建当前目录文件；`-c` 修改指定文件。用户级选择不能引用仅存在于项目的 profile。更高层覆盖保存结果时会提示。

## 查看与检查

```sh
aiscan config show --sources
aiscan config show --sources --json
aiscan config validate
aiscan doctor
aiscan doctor --online
```

`show` 展示有效配置，密钥及 URL 凭据始终脱敏；`--sources` 辅助定位文件、环境变量或 CLI 覆盖。`validate` 离线检查字段、类型及 profile 引用。未知的基础字段报错；当前发行版未注册的扩展保留原文并提示不可用，已注册扩展严格校验。

`doctor` 默认检查配置和目录条件，不访问网络、不启动扫描；`--online` 才执行模型及宿主提供的已配置连接检查。未配置可选模型会跳过连接测试。检查失败退出码为 1；成功为 0。命令支持 `--json`，提示信息写 stderr。

## 数据与 Web

数据目录优先级为 `--data-dir`、`CYBER_DATA_DIR`、配置中的 `misc.data_dir`；未指定时，依次复用当前目录已有 `.cyber`、二进制旁已有 `.cyber`，否则使用 `~/.cyber`。复用旧目录时提示路径，不自动迁移历史和缓存。

配置内的相对 `data_dir` 相对于定义它的文件；CLI 和环境变量中的相对路径相对于工作目录。配置查看与目录发现不创建目录。

Web 设置沿用现有配置接口。保存目标为显式 `-c`、项目配置、正在使用的二进制旁配置，最后才是用户配置。页面展示可编辑的文件配置；运行时启动参数、环境变量可能覆盖这些值。

node 上线、重连和 Web 配置保存后，server 自动下发实际生效的 LLM 配置，包括配置文件、环境变量和启动参数解析后的 API key、模型与端点。node 无需单独配置相同的 key；运行时凭据只用于下发和连接检测，不回写配置文件。Web 顶部的 LLM 健康提示同样检测 server 实际生效的配置。

保存只修改目标层中编辑过的值，保留未修改的继承项和空白密钥，不把用户层或运行时凭据复制到项目文件。继承的 profile 要到其来源文件删除。保存前按原分层构建候选运行时，验证失败保留旧文件和旧运行时。
