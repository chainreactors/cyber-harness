# MCP CLI：真实 LLM 与 JADX 验证

2026-10-11 在 Windows x64 上，用 [mcp-agent](../../examples/mcp-agent/main.go) 完成两个实际模型驱动的静态逆向任务。MCP 命令接入、模型调用、证据返回、状态保持和清理均通过；模型的最终文字仍需要事实核对，下文保留具体遗漏。

调用链为 `gpt-6.1-sol → StandardLoop → bash → CommandRegistry → MCP ext → jadx-headless-mcp → jadx-core`。模型自行选择命令和参数；没有模拟 provider、模拟反编译器或预写工具调用序列。宿主使用现有 Session/Loop extensions，不安装 delegation tools。

## 输入与环境

- OpenAI-compatible endpoint：`https://api.chainreactors.cn/v1`，模型 `gpt-6.1-sol`。先实际验证 function tool calling，再运行任务。凭据只由专用环境变量传入，创建工具前从环境移除，未提交到仓库。
- 现有 JBR 21.0.7；没有安装 Java、Android emulator 或其他前置软件。
- [jadx-headless-mcp v0.7.1](https://github.com/1013503897/jadx-headless-mcp/releases/tag/v0.7.1)，沿用 REA 的引擎选择，原始 JAR SHA-256：`6e5eacf500b64292bfb73c49797c1958f6ee44646e43e868039ae7feb573ff75`。启动参数 `-Xmx512m -jar <jar>`，单次 MCP 调用超时 90 秒。
- 每个任务独立工作目录、harness 和 stdio 连接；输入统一复制为 `target.apk`。模型只获知目标及问题，没有读取源码或 walkthrough 的工具调用。

| 输入 | 固定来源 | SHA-256 |
| --- | --- | --- |
| OWASP UnCrackable Level 1 | [MASTG commit 02ffd85](https://github.com/OWASP/mastg/blob/02ffd85fb74383ec2a30b42866d38f89d72c0ead/Crackmes/Android/Level_01/UnCrackable-Level1.apk) | `1da8bf57d266109f9a07c01bf7111a1975ce01f190b9d914bcd3ae3dbef96f21` |
| Appium ApiDemos debug 6.0.18 | [发布资产](https://github.com/appium/android-apidemos/releases/tag/v6.0.18) | `a9eecf37b26cd084855c530db81c2bb1b91f4c1b095a04f47aa7c20e2791f686` |

## 任务与实际结果

| 任务 | 模型决策 | bash 调用 | API attempts | 已报告 tokens | 耗时 |
| --- | ---: | ---: | ---: | ---: | ---: |
| UnCrackable：混淆类追踪、环境检查、加密口令恢复与本地正反例 | 10 | 16 | 14 | 90,719 | 521.50 秒 |
| ApiDemos：异步应用列表、缓存、广播、取消、释放与 UI 流程 | 13 | 41 | 16 | 181,251 | 448.31 秒 |

两次 stop reason 均为 `completed`，退出码均为 0；模型都执行 `unload_apk` 并确认 `status` 为 `EMPTY`，宿主都报告 `harness_closed=true`。完成后这两次运行创建的 Java 和 host 进程均已退出。引擎发现 26 个工具，两个 APK 分别索引 6 和 2,876 个类。

API attempts 与模型决策不是同一指标。Loop 的 usage 累计分别记录 4 和 3 次 `usage_missing`；表中 tokens 只包含服务实际返回的用量，不能据此推算完整费用。原始 provider/HTTP 帧未开启。

### UnCrackable

模型通过 manifest/main-activity、类/方法查询，追踪到：

- `sg.vantagepoint.uncrackable1.MainActivity.onCreate` 调用 root 检查和 debuggable 检查。
- `sg.vantagepoint.a.c` 检查 PATH 中的 `su`、`Build.TAGS` 中的 `test-keys` 及固定 Superuser/`su` 路径。
- `sg.vantagepoint.a.b.a` 检查 `(ApplicationInfo.flags & 2) != 0`。这是 `FLAG_DEBUGGABLE`，不能据此判断 debugger 当前已附加。
- `MainActivity.a` 创建不可取消的对话框，OK 回调调用 `System.exit(0)`；静态调用不代表设备上已实际退出。
- `MainActivity.verify → sg.vantagepoint.uncrackable1.a.a → sg.vantagepoint.a.a.a` 完成输入检查、十六进制 key 解码、Base64 密文解码和 AES 解密。

提取的 key 是 `8d127684cbc37c17616d806cf50473cc`，密文是 `5UJiFctbmgbDoLXmpL12mkno8HT4Lv8dlat8FxR2GOc=`。模型执行本地 Python verifier，恢复 `I want to believe`，并保存 `solution.json`；正例为 true，单字符改动 `I want to believX` 为 false。

独立核验重新从原始工具证据提取 key/密文，使用 PyCryptodome 解密并验证 PKCS#7 padding、将恢复的明文重新加密后与原密文逐字节比较，再验证单字符反例不能产生相同密文。全部通过。源码实际调用是 `Cipher.getInstance("AES")`，没有显式 IV；ECB/padding 和默认字符集仍属于 Android provider/运行环境因素，本地 oracle 明确采用 AES-ECB/PKCS#7 与 UTF-8。没有执行 APK。

### ApiDemos LoaderCustom

模型从 2,876 个类中定位 `io.appium.android.apis.app.LoaderCustom` 及内部类，查询方法体与 incoming references，并保存 `findings.json`。实际恢复的流程包括：

`Activity → AppListFragment → initLoader(0) → AppListLoader.loadInBackground → AppEntry → AppListAdapter → UI`。

模型确认应用枚举参数 `8704 (0x2200)`、label 的 Collator 排序、缓存交付、`takeContentChanged / mApps == null / configChange` 的 force-load 条件、五种 package/storage 广播、取消和 reset 清理、SearchView 到 Adapter filter 的调用。

它还发现 `onReleaseResources` 是空函数，`deliverResult` 在交付后对传入的新列表调用该 hook，没有释放此前缓存的列表。独立比对[发布 tag 对应源码](https://github.com/appium/android-apidemos/blob/3e716ee04ef3390284bfab761e28da8c15c08143/app/src/main/java/io/appium/android/apis/app/LoaderCustom.java)确认原代码为 `List<AppEntry> oldApps = apps`；这不是仅靠反编译命名推断的异常。原始源码 SHA-256：`953727ab2d8d2d274bde674f47fd6047f11ee5f55428e9672d0a1cadfa5f0cc9`，只用于模型运行后的核验。

独立核对九个有限检查点通过：loader ID、枚举 flags、排序、条件刷新、空 release hook、释放参数、reset 注销、搜索委托以及缓存图标分支。模型报告有两项需要补充/纠正：

- 对“APK 消失时返回默认图标”的描述过于宽泛。`mIcon != null && mMounted` 时直接返回缓存，不重新执行 `File.exists()`；label 也仅在 `mLabel == null || !mMounted` 时重新检查。不能声称每次调用都即时检测文件消失。
- 报告给出了配置 mask `772 (0x304)`，没有补全其字段名。固定源码说明对应 locale、UI mode、screen layout，density 单独比较；这些名称来自核验源码，不能归功于模型已自行证明。

因此本次结果建立了真实模型与工具基础设施的完整闭环，不能作为“任意逆向任务的结论均自动正确”的证明。

## 失败与恢复

- UnCrackable 中一次 `get_strings --max_bytes` 被 schema 参数校验拒绝；模型查看帮助后改用 `--limit` 并继续。失败通过 bash 状态返回，没有丢失诊断。
- ApiDemos 中一次 `find` 命中了 Windows FIND，返回参数错误；模型使用已经列出的文件继续。
- 上游 `get_class_source` 对部分内部类查询返回空 text 且未置 `isError`。模型转向顶层源码和带 `$` 的方法查询，得到所需方法体。扩展保留了该原始结果，没有伪造源码或成功覆盖。
- `loadInBackground` 的 incoming method references 为零，模型没有把它解释成“从不调用”；class references 与实际方法体用于交叉核对。框架回调、反射、动态执行仍有限制。

## 复现与记录

按 [MCP 文档](../mcp.md#真实-llm-驱动的分析)准备服务配置与现有引擎，使用 `examples/mcp-agent`。给每个任务单独的目录、trace 文件和 APK。任务文件要求仅使用 bash/JADX、不得访问网络/源码/兄弟目录、保存 class/method 证据、区分静态与运行时结论，最后 unload 并确认 EMPTY。

UnCrackable 的问题为入口和环境检查、完整加密检查链、恢复口令、执行正反例 verifier；ApiDemos 的问题为 LoaderCustom 跨类数据流、刷新条件/广播、停止/取消/reset/释放的实际行为、图标/label/search 逻辑。只给问题，不给已知答案。命令参数为 `-model gpt-6.1-sol -max-turns 40 -timeout 15m`，provider 输出上限 8,192 tokens。

将 host 的 stdout/stderr 放在任务目录之外，避免与模型自行生成的同名报告相互覆盖。本次 UnCrackable 的 host stdout 与模型自建 `report.md` 同名；完整分析报告已从保留的工具调用原文恢复，最终回答从完成消息恢复，原始 trace 没有受影响。

完整 trace、任务、模型产物与独立核验脚本留在本地，没有提交 APK/JAR、凭据或大量执行产物。原始 AOP JSONL 的 SHA-256：

| 记录 | SHA-256 |
| --- | --- |
| UnCrackable events.jsonl | `de7f4ece36525586ebb4e64d717410fa92eeb8583ff4478964df20a32cebc5ea` |
| ApiDemos events.jsonl | `efb2316d9eee704d7fed1ac49f9d1b52504ae7b9f9bed93676b034dc5bdefd07` |

本次只验证 Windows 上 stdio JADX 与指定模型接口的静态分析。没有建立 Android runtime、JADX HTTP、IDA、Hopper 或 Ghidra 的执行覆盖。与固定调用序列的 [真实引擎 integration lane](../../exts/mcp/jadx_integration_test.go)互补，真实 LLM 任务不进入默认 CI，也不以模拟结果替代真实模型行为。
