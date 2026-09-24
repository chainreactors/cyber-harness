逆向 CLI 选型调研，2026-09-24。范围：原生 PE/ELF/Mach-O、Java/Android、.NET、固件；目标是供 audit 调用，并最终通过 Arsenal 选择、下载和离线打包。版本来自上游 release/NuGet，运行验证使用自行编译的小样本。本轮完成选型和 CLI 验证，尚未接入 audit 或发布 CI。

当前实施范围：仅单个可执行文件。Windows amd64 选择 radare2 6.2.2（r2blob）、capa 9.4.0、FLOSS 3.1.1；Linux amd64 选择 capa/FLOSS。工具定义由本仓库 `tools/arsenal/arsenal.yaml` 维护，直接按名称调用。以下多目录、运行时、插件方案是调研备选，暂不实施。

后续覆盖更多目标时可考虑四个分析入口：**radare2、JADX、ilspycmd、Binwalk**。radare2 配套 r2ghidra，固件配套实际需要的解包器；优先增加 capa 做能力识别。按场景选择 bundle，保留同一份 Arsenal 工具定义。

| 场景 | 选择与版本 | audit 获得的能力 | 分发边界 |
| --- | --- | --- | --- |
| 原生二进制 | [radare2 6.2.2](https://github.com/radareorg/radare2/releases/tag/6.2.2) + [r2ghidra 6.2.2](https://github.com/radareorg/r2ghidra/releases/tag/6.2.2) | 格式、架构、段、导入、字符串、函数、交叉引用、反汇编及函数反编译；JSON 可保留地址关联 | 一套锁定版本的 r2、插件、Sleigh 数据及动态库；插件不需要完整 Ghidra/JDK |
| Java / Android | [JADX 1.5.6](https://github.com/skylot/jadx/releases/tag/v1.5.6) | JAR/class、DEX/APK 等输入；Java 源码、Android 资源解码；可选 JSON 输出 | JAR、启动入口和 Java 11+ 运行时；首版不另装 JVM 反编译器 |
| .NET | [ilspycmd 11.1.0.9782](https://www.nuget.org/packages/ilspycmd/11.1.0.9782) | C#、IL、类型/成员、项目导出和元数据；元数据表支持 JSON | CLI 来自 NuGet，依赖 .NET 10；ILSpy 桌面 ZIP 不能直接作为 CLI 包 |
| 固件 | [Binwalk 3.1.0](https://github.com/ReFirmLabs/binwalk/releases/tag/v3.1.0) | 嵌入文件识别、偏移、熵分析、递归解包、JSON 日志 | 本版本无官方二进制 release asset，需要构建；完整解包依赖外部工具，首版优先 Linux |
| 能力识别增强 | [capa 9.4.0](https://github.com/mandiant/capa/releases/tag/v9.4.0) | PE/ELF/.NET 等的能力规则与位置证据，帮助定位后续分析函数 | 官方独立程序包含 Python、规则和资源；最适合优先接入现有单可执行文件路径 |

这里的“覆盖”指四类目标各有明确分析路径。反编译不保证还原原始源码；.NET NativeAOT 和 Android JNI 库走原生分析。宿主平台与目标架构分开判断，例如 Windows 版 r2 可以读取 ELF，但这不能证明 Linux ARM64 版工具已经可发布。

固件的最小可交付范围需要明确到文件系统。建议首版覆盖常见压缩归档和 SquashFS：Binwalk + [7-Zip 26.03](https://github.com/ip7z/7zip/releases/tag/26.03) + [sasquatch v4.5.1-6](https://github.com/onekey-sec/sasquatch/releases/tag/sasquatch-v4.5.1-6)。7zz 可以处理通用归档，但不能替代 SquashFS/UBIFS 解包器。Binwalk 3.1.0 实际查找的是 `7z`、`sasquatch`、`sasquatch-v4be`，工具包必须提供相应命令。UBI/UBIFS 再增加 `ubireader_extract_images` / `ubireader_extract_files`；不能仅安装 Binwalk 就宣称完整固件解包。相关依据：[7z 调用](https://github.com/ReFirmLabs/binwalk/blob/v3.1.0/src/extractors/sevenzip.rs)、[SquashFS 调用](https://github.com/ReFirmLabs/binwalk/blob/v3.1.0/src/extractors/squashfs.rs)、[UBI 调用](https://github.com/ReFirmLabs/binwalk/blob/v3.1.0/src/extractors/ubi.rs)。这些解包组合本轮尚未运行验证。

其余候选的取舍如下，避免同一层默认安装多个相似引擎。

| 候选 | 调研结果 | 决策 |
| --- | --- | --- |
| [Rizin 0.9.1](https://github.com/rizinorg/rizin/releases/tag/v0.9.1) + [rz-ghidra 0.9.0](https://github.com/rizinorg/rz-ghidra/releases/tag/v0.9.0) | 有 JSON/CLI；当前 rz-ghidra release 只有源码附件，插件组合仍需构建验证；Linux x64 静态 Rizin 归档约 131 MiB | 保留为 r2 的替代候选，默认只选一套 |
| [Ghidra 12.1.4](https://github.com/NationalSecurityAgency/ghidra/releases/tag/Ghidra_12.1.4_build) | 完整分析平台及 headless 模式；发行 ZIP 约 543 MiB，另需 JDK 21；批量导出通常需要脚本 | 复杂原生分析的可选后端，首版先用 r2ghidra；[JDK 要求以发布标签为准](https://github.com/NationalSecurityAgency/ghidra/tree/Ghidra_12.1.4_build) |
| [Detect It Easy / diec 3.21](https://github.com/horsicq/DIE-engine/releases/tag/3.21) | 编译器、壳、文件类型识别，支持 JSON；Windows 包解压约 65 MiB，包含数据库和 Qt 等文件 | 需要细化壳/编译器识别时加入；基础格式识别先复用 r2；下载源应使用 DIE-engine |
| [FLOSS 3.1.1](https://github.com/mandiant/flare-floss/releases/tag/v3.1.1) | 独立程序，支持静态/栈上/解码字符串与 JSON；具体恢复能力取决于后端及目标架构 | 混淆字符串场景选装，普通字符串先复用 r2 |
| [Unblob 26.6.4](https://github.com/onekey-sec/unblob/releases/tag/26.6.4) | 固件递归解包和 JSON 报告；Python/native wheel 与外部解包器构成较重环境，未见 Windows wheel | 作为固件专用环境的替代方案，首版不与 Binwalk 同时维护 |
| [RetDec 5.0](https://github.com/avast/retdec/releases/tag/v5.0) | 原生反编译；最新稳定 release 仍为 2022-12，发行包约 158–181 MiB | 暂不选，优先当前维护和打包路径更合适的组合 |
| [LLVM 23.1.2](https://github.com/llvm/llvm-project/releases/tag/llvmorg-23.1.2) | llvm-readobj / llvm-objdump 适合元数据和反汇编；完整工具链远大于所需子集 | 不默认引入完整 LLVM；基础能力已有 r2 |
| [UPX 5.2.1](https://github.com/upx/upx/releases/tag/v5.2.1) | 小型 CLI，但只处理 UPX 格式；本次 release 未见 macOS 原生程序附件 | 识别到 UPX 后按需使用 |
| [Redress 1.2.86](https://github.com/goretk/redress/releases/tag/v1.2.86) | Go 二进制元数据与符号恢复，独立程序约 4 MiB | Go 专项增强，不占基础工具集 |
| [Vineflower 1.12.0](https://github.com/Vineflower/vineflower/releases/tag/1.12.0) | JVM 反编译专用 JAR | 遇到 JADX 不理想的 JVM 样本再比较，首版避免重叠 |

动态调试/插桩另有目标进程、设备和权限依赖。本次选择先建立静态分析路径；没有验证 GDB、LLDB、Frida 的动态工作流。

本地验证环境为 Windows amd64、WSL Ubuntu amd64、Java 11。样本包含同一标记字符串 `AUDIT_REVERSE_FIXTURE`：C 编译为 PE/ELF，Java 编译为 class/JAR 并通过 D8 生成 DEX，C# 编译为 .NET 9 DLL。原生及 C# 校验函数按字符串匹配返回 7 或 2，Java 校验函数返回布尔值。命令输出落在忽略目录 `.cache/reverse-cli-research/results/`。

| 验证 | 结果 | 能证明的范围 |
| --- | --- | --- |
| Windows r2blob：PE/ELF 的 `ij`、PE 的 `pdfj` | 通过，输出可解析 JSON | 基础元数据与反汇编可以用官方单 exe 完成 |
| Windows 普通 r2 + r2ghidra + Sleigh：PE 函数 `pdgj` | 通过，恢复字符串比较及 7/2 分支，46 条代码注解 | 该版本组合的原生反编译可用 |
| WSL Linux r2 + r2ghidra：ELF 函数 `pdgj` | 通过，同样恢复比较分支，46 条注解 | 本地释放 DEB 内容，设置私有库和 Sleigh 路径后可用，无全局安装 |
| Windows r2blob + 同版官方 r2ghidra DLL | 失败，提示缺少插件 | 不能把“r2blob 单文件可运行”等同于“该 DLL 可加载”；未据此否定其他自编译组合 |
| JADX：自建 JAR、DEX | 均通过，输出 Java 源码并保留字符串及判断逻辑 | 字节码反编译可用；完整 APK 的 manifest/resources 流程未测 |
| ilspycmd：自建 .NET 9 DLL | 通过，恢复 C# 分支；`MethodDef --json` 返回 `Check` 及 token/RVA | 私有 .NET 10.0.12 运行时可运行该 CLI；不是依赖系统 .NET 9 启动 |
| capa：自建 PE | 通过，JSON 内有 10 条匹配规则，无需另装 Python/规则 | 官方独立包可用；规则命中不等于漏洞或恶意判定 |
| diec：自建 PE | 通过，JSON 正确识别 PE64 / MinGW | 编译器识别可用 |
| FLOSS：自建 PE | 退出码 0，JSON 含标记静态字符串；stderr 有 CFG 不完整警告 | 只验证了调用及静态字符串输出，未证明混淆字符串恢复质量 |
| Binwalk / 固件解包 | 本轮仅核验 release、源码和依赖 | 不能声称固件端到端测试通过 |

Mach-O、ARM/MIPS 固件、macOS 宿主、ARM64 宿主尚未实测。以上属于 CLI 冒烟验证，没有测量大型程序的分析质量、性能或复杂混淆支持。

调用方式无需增加统一结果 DTO。源码、JSON 和反汇编直接写入 audit 工作目录，由已有 read/rg/bash 工具消费；报告引用二进制哈希、函数地址或托管成员 token，并保留原始工具输出。

```sh
# 元数据；先用 aflj 获取函数地址，再对选定函数做分析。
radare2 -N -q -e bin.relocs.apply=true -c 'ij' sample.bin
radare2 -N -q -e bin.relocs.apply=true -c 'aa;aflj' sample.bin

# r2ghidra 已加载且 SLEIGHHOME 已设置后，使用 pdgj 输出反编译 JSON。
# 本次验证通过 L <plugin-path> 显式加载包内插件。
radare2 -N -q -c 'aa;s sym.audit_check;pdgj' sample.bin

jadx -d out/java sample.jar
jadx -d out/android classes.dex
ilspycmd --disable-updatecheck -p -o out/dotnet sample.dll
ilspycmd --disable-updatecheck --dump-table MethodDef --json sample.dll
capa -q -j sample.exe

# 固件命令来自上游 CLI 定义，本轮未执行。
binwalk --log out/firmware.json firmware.bin
binwalk -e -M -C out/firmware --log out/firmware.json firmware.bin
```

若后续决定发布多文件工具，需要解决一个共性问题：目前 CRTM 的 `Artifact` 是一个可执行文件流，安装器从下载归档中提取一个程序。这足以支持 capa/FLOSS，但不能完整表达 r2 的动态库和插件、Sleigh 数据、JADX 的 JAR/JRE、ilspycmd 的 .NET 运行时。Windows 实测解压体积可说明区别：r2blob 单 exe 约 29 MiB；普通 r2 包约 41 MiB，另加约 3 MiB 插件和 12 MiB Sleigh；JADX 约 75 MiB，运行时另计；ilspycmd NuGet 内容约 11 MiB，私有 .NET 10 约 77 MiB。这些是上游解压体积，不是最终 audit 的增量大小。

该后续方案可从 Arsenal 的安装单位解决：**工具目录 + 入口命令**。单 exe 就是只有一个程序的目录。构建阶段准备好插件、数据和运行时，bundle 嵌入同一份内容，运行时释放后直接执行。r2 与 r2ghidra/Sleigh 按一个经过验证的组合锁定，JADX/ilspycmd 的运行时随包固定；不让 audit 首次运行时再调用 r2pm、pip、dotnet tool install。优先用上游自包含包，必要时在构建阶段组合固定文件，避免为此实现通用依赖求解器。

职责维持简单：Arsenal 维护统一定义；audit 的 bundle YAML 只选工具；已有执行工具负责调用。基础格式识别、字符串提取与反汇编复用 r2，先不增加 file/strings/objdump 三套同类依赖。独立工具接入不需要增加一组 MCP 工具或专用 DTO。

当前 audit release 包含 Linux/macOS/Windows × amd64/arm64。不能直接把所选工具声明为六个平台全部可发布：r2ghidra 6.2.2 附件有 Windows x64、Linux amd64、macOS x64/arm64，未见 Linux arm64、Windows arm64 插件二进制；capa 9.4.0 有 Linux/macOS 的 x64/arm64 和 Windows x64，未见 Windows arm64。Java/.NET 本身跨平台也不等于私有运行时和启动入口已经验证；Binwalk 仍需要构建和逐个解包器验证。

多文件工具的后续调研路径如下（本轮不实施）：

1. Arsenal 支持保留完整工具目录，先把已验证的 r2+r2ghidra 与 capa 接入；保留现有单程序路径作为简单情形。
2. 按需加入 JADX 与 ilspycmd，构建时准备私有运行时；audit 继续消费生成的源码和证据文件。
3. 固件先交付 Linux 上经过实际样本验证的归档/SquashFS 解包组合，再增加 UBI 等格式。用解出的文件继续进入原生或源码分析。
4. 清理 audit 中默认要求 OSV/Proton 的源码专属检查，使检查要求随目标适用性确定。复用现有证据与报告路径，不把源码扫描是否运行作为所有逆向任务的成功条件。
5. 每个发布目标在干净缓存、无预装工具/运行时、禁网条件下，从最终 audit 单文件释放工具并分析固定样本。断言源码内容、函数地址/成员 token 和固件解出文件，不能只验证 `--version`。未完成这些检查前，不把 CLI 冒烟结果标作 audit bundle 或 CI 通过。
