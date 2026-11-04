# fbt 使用手册

fbt 在本机起一条 FISCO-BCOS AIR 链，按生产配置回放，跑一组测试，用退出码告诉你这个版本能不能发。下面是最常见的一次运行：

```console
$ fbt --config fbt.yaml gate run -p production-enterprise --scenarios dual_rpc,malformed
...
  scenario dual_rpc       pass
  scenario malformed      pass

GATE: PASS
$ echo $?
0
```

这一条命令做了五件事：建 4 节点链、回放 `production-enterprise` 配置、跑 `dual_rpc` 和 `malformed` 两族场景、每族前后检查链的健康、退出时停链并清理。退出后端口已释放，磁盘上只留下这一轮的事件记录。在 3.18.0 上这一轮约 1 分钟。

本文的命令和输出都以 3.18.0 AIR 链为准。哪些路径真跑过，见[验证范围](#验证范围)。

## 判决依据

一轮的结果由两类判决决定，任何一类不通过，退出码就是 10。

**三个 oracle** 判断链本身有没有出事，在基线和每个场景之后各检查一次：

| oracle | 判定条件 | 实现 |
|---|---|---|
| `crash` | 节点进程消失 | `scripts/oracle_crash.sh` |
| `halt` | 有待处理交易但区块高度不再增长 | `scripts/oracle_liveness.sh` |
| `state-mismatch` | 不同节点同一高度的 stateRoot 不一致 | `scripts/oracle_stateroot.sh` |

fbt 不以日志里的 ERROR 关键字作为判据。

**四族场景** 对链施加操作，并判断操作结果：

| 场景 | 做什么 | 额外依赖 |
|---|---|---|
| `ut` | 跑每个已编译模块的单元测试 | `tools.build_dir` 下的测试二进制 |
| `dual_rpc` | 分别通过 BCOS RPC 和 Web3 RPC 部署并调用一个合约，核对回执和返回值，再比较两边同一高度的 stateRoot | `tools.web3_private_key`、`node`、viem |
| `malformed` | 发篡改过的交易，确认被拒绝且节点存活 | `java`、`tools.tamper_helper` |
| `jsd` | 用 java-sdk-demo 跑 DAG 并发转账，核对余额守恒 | `jsd.dir`、`java`、`tools.web3_private_key`、`node`、viem |

范围只覆盖 Air 部署、RocksDB 存储和 DAG 执行，不覆盖 Pro/Max、TiKV 和 sharding。

## 安装

### 有 Go 的机器

```bash
git clone https://github.com/kyonRay/fisco-bcos-release-gate-skill
cd fisco-bcos-release-gate-skill
./install.sh --prefix ~/fbt --sibling /path/to/fisco-bcos-testing
```

`--sibling` 指向 `fisco-bcos-testing` skill 的目录，`cluster_up.sh` 和 `run_ut.sh` 从那里复制进安装目录。不传时默认找 `../fisco-bcos-testing`。缺了这个目录，安装仍能完成，但建不了链。

### 没有 Go 的测试机

在开发机上交叉编译，把二进制和源码一起传过去：

```bash
# 开发机
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o fbt-linux ./cmd/fbt
COPYFILE_DISABLE=1 tar --exclude='._*' --exclude='.git' -czf fbt-src.tgz .

# 测试机
mkdir fbt-src && tar -xzf fbt-src.tgz -C fbt-src && cd fbt-src
bash ./install.sh --binary /path/to/fbt-linux --prefix /opt/fbt --sibling /path/to/fisco-bcos-testing
```

在 macOS 上打包必须带 `COPYFILE_DISABLE=1`，否则 tar 会带上 `._*.case` 这类隐藏文件。

### 安装后的目录

```text
<prefix>/bin/fbt
<prefix>/libexec/fbt/engine.json
<prefix>/libexec/fbt/scripts/     引擎脚本
<prefix>/libexec/fbt/tools/       tamper-helper.sh
<prefix>/share/fbt/profiles/      配置画像
<prefix>/share/fbt/cases/         随版本发布的回归用例
```

fbt 根据二进制自身的位置找到其余目录，整个目录可以移动。

## 机器要求

| 依赖 | 要求 | 用途 |
|---|---|---|
| bash | 4 及以上。macOS 自带 3.2，要 `brew install bash` 并放到 `PATH` 前面 | 引擎脚本 |
| `curl`、`pgrep` | 能在 `PATH` 中找到 | 调 RPC、查节点进程 |
| 节点二进制 | 3.18.0 及以上，带 `sysconfig`、`tx`、`balance` 等子命令 | 被测对象，也是引擎驱动链的唯一手段 |
| OpenSSL | 支持 `dgst -keccak-256`，即 OpenSSL 3.2+ 或铜锁 | `build_chain.sh` 生成账户 |
| 磁盘 | 运行期间至少留 3GB | 每轮占约 1.5GB，节点在可用空间低于 1GB 时拒绝启动 |
| 端口 | P2P、RPC、Web3 三段都空闲 | 每段占用的端口数等于节点数 |

系统 OpenSSL 不支持 Keccak 时，把 `tools.openssl_cmd` 指向另一个可执行文件，比如编译好的铜锁 CLI。

跑 `dual_rpc` 或 `jsd` 还要让 viem 能被解析：在 `repo.root` 目录下要有 `node_modules/viem`。

## 配置

### 查找顺序

fbt 按下面的顺序找配置文件，用第一个命中的：

1. `--config <path>`
2. 当前目录下的 `fbt.yaml`
3. `$XDG_CONFIG_HOME/fbt/config.yaml`
4. `~/.config/fbt/config.yaml`

同一个配置键的取值优先级是：命令行参数 > 环境变量 > 配置文件 > `.profile` > 默认值。

运行数据放在状态目录：`--state-dir`，否则 `$XDG_STATE_HOME/fbt`，否则 `~/.local/state/fbt`。

### 最小配置

```yaml
repo:
  root: /path/to/FISCO-BCOS            # 含 tools/BcosAirBuilder/build_chain.sh
tools:
  fisco_bin: /path/to/fisco-bcos       # 被测二进制
  openssl_cmd: /path/to/openssl        # 系统 OpenSSL 支持 Keccak 时可省略
  web3_private_key: "0x<测试用私钥>"    # dual_rpc 和 jsd 的签名账户
  build_dir: /path/to/FISCO-BCOS/build # 只有 ut 需要
jsd:
  dir: /path/to/java-sdk-demo/dist     # 只有 jsd 需要
cluster:
  p2p_base_port: 31300                 # 默认端口被占用时才需要改
  bcos_base_port: 21200
  web3_base_port: 9545
  fund_addresses: "0x<私钥对应的地址>"  # 启用余额的画像要给签名账户充值
upgrade:
  rollback: true                       # 跑 T8 降级拒绝检查
```

`tools.web3_private_key` 是测试链上的账户，不要填真实资产的私钥。配置文件里的未知键和类型错误会在启动任何进程前报 20。

### 常用配置键

| 键 | 环境变量 | 默认值 | 含义 |
|---|---|---|---|
| `repo.root` | `FBT_REPO_ROOT` | 安装目录 | FISCO-BCOS 源码目录，也是引擎的工作目录 |
| `tools.fisco_bin` | `FISCO_BIN` | 无 | 被测节点二进制 |
| `tools.ops_bin` | `OPS_BIN` | 同 `fisco_bin` | 执行子命令的二进制。升级时自动用 `--new-bin` |
| `tools.ops_timeout_ms` | `OPS_TIMEOUT_MS` | `60000` | 单条子命令的超时 |
| `tools.openssl_cmd` | `OPENSSL_CMD` | `openssl` | `build_chain.sh` 用的 OpenSSL |
| `tools.build_dir` | `BUILD_DIR` | 无 | 单元测试二进制所在的编译目录 |
| `tools.web3_private_key` | `WEB3_PRIVATE_KEY` | 无 | Web3 交易的签名私钥 |
| `tools.tamper_helper` | `TAMPER_HELPER` | 安装目录下的脚本 | 篡改交易的工具 |
| `tools.java_bin` | `JAVA_BIN` | `java` | Java 路径 |
| `jsd.dir` | `JSD_DIR` | 无 | 编译好的 java-sdk-demo `dist` 目录 |
| `jsd.count` / `jsd.qps` | `JSD_COUNT` / `JSD_QPS` | 脚本默认 50 / 10 | 每次运行的交易数和发送速率 |
| `cluster.node_count` | 无 | `4` | 节点数，可被画像覆盖 |
| `cluster.p2p_base_port` | `P2P_BASE_PORT` | `30300` | P2P 起始端口 |
| `cluster.bcos_base_port` | `BCOS_RPC_BASE_PORT` | `20200` | BCOS RPC 起始端口 |
| `cluster.web3_base_port` | `WEB3_BASE` | `8545` | Web3 RPC 起始端口 |
| `cluster.fund_addresses` | `RG_FUND_ADDRESSES` | 无 | 建链后要充值的地址 |
| `upgrade.rollback` | `UPGRADE_ROLLBACK` | `0` | 升级时是否跑 T8 |
| `run.keep_cluster` | `RG_KEEP_CLUSTER` | `0` | 等同 `--keep-cluster` |
| `run.timeout_sec` | 无 | 不限 | 单条命令的超时秒数，超时退出 30 |

完整列表和当前生效值用这条命令查，`SOURCE` 列说明每个值来自哪一层：

```console
$ fbt --config fbt.yaml config show --all-keys
KEY                     VALUE                    SOURCE    ENV
cluster.bcos_base_port  21200                    fbt.yaml  BCOS_RPC_BASE_PORT
cluster.fund_amount     1000000000000000000      default   RG_FUND_AMOUNT
...
```

`fbt config path` 显示实际用到的配置文件、状态目录和安装目录。

## 第一次运行

按这个顺序走，前三步都不碰链：

```bash
alias fbt='/opt/fbt/bin/fbt --config /path/to/fbt.yaml'

fbt config path                                    # 1. 确认读到了哪个配置
fbt doctor --command gate --scenarios dual_rpc     # 2. 查缺什么，缺依赖时退出 30
fbt gate plan -p production-enterprise --scenarios dual_rpc   # 3. 看这一轮要做什么
fbt gate run  -p production-enterprise --scenarios dual_rpc   # 4. 真跑
fbt report                                         # 5. 看结果
```

`doctor` 的 `--command` 可选 `gate`、`case`、`upgrade`、`cluster-up`、`fuzz`。不带 `--scenarios` 时，`gate` 会检查四族的全部依赖。

## 命令

全局参数放在子命令之前：

| 参数 | 含义 |
|---|---|
| `--config <path>` | 配置文件 |
| `--state-dir <path>` | 状态目录 |
| `--engine-dir <path>` | 安装目录，默认由二进制位置推出 |
| `--output human\|json\|jsonl` | 输出形式，默认 `human` |
| `-v`, `--verbose` | 详细输出 |
| `--no-color` | 关闭颜色 |
| `--version` | 打印版本 |

### `fbt gate run`

跑一轮门禁。

```bash
fbt gate run -p production-enterprise                                   # 四族加全部 active 用例
fbt gate run -p production-enterprise --scenarios dual_rpc,malformed    # 只跑这两族
fbt gate run -p production-enterprise --cases 'fuzz_*'                  # 只跑匹配的用例
```

| 参数 | 含义 |
|---|---|
| `-p <画像>` | 画像名或路径，默认 `production-enterprise` |
| `--scenarios a,b` | 选场景族 |
| `--cases <glob>` | 按文件名选用例，`.case` 后缀可写可不写 |
| `--fail-fast` | 第一个门禁失败就停止后续用例 |
| `--keep-cluster` | 退出时保留链和目录 |
| `--allow-parallel` | 允许第二条链并存，要求端口段不重叠 |

两个过滤参数决定这一轮跑什么：

| 写法 | 场景族 | 用例 |
|---|---|---|
| 都不写 | 四族全跑 | 全部 `active` |
| 只写 `--scenarios` | 选中的 | 不跑 |
| 只写 `--cases` | 不跑 | 匹配的 |
| 两个都写 | 选中的 | 匹配的 |

`--cases` 匹配不到任何用例时退出 20，只匹配到 `pending` 用例时也退出 20。

### `fbt gate plan`

打印这一轮的画像、场景、端口和每条用例的去留，不碰链。参数与 `gate run` 的过滤参数相同。

```console
$ fbt gate plan -p production-enterprise --scenarios dual_rpc
profile:   production-enterprise
scenarios: dual_rpc
ports:     p2p:31300-31303 rpc:21200-21203 web3:9545-9548

  skip example.case             example: a format demonstration, never swept
  skip fuzz_seed43_idx11.case   pending: the defect is not fixed, so a sweep would be permanently red
```

### `fbt gate upgrade`

验证从旧版本滚动升级到新版本。

```bash
fbt gate upgrade -p production-enterprise \
  --old-bin /path/to/fisco-bcos-3.17.0 \
  --new-bin /path/to/fisco-bcos-3.18.0 \
  --target-ver 3.18.0
```

| 步骤 | 做什么 | 通过条件 |
|---|---|---|
| T0 | 用旧二进制按画像建链 | 链起来了 |
| T1 | 跑一次三个 oracle，记录目标版本 feature 的取值 | 作为基线 |
| T2-T4 | 逐节点停止、换二进制、重启 | 没有分叉 |
| T5 | 用新二进制把 `compatibility_version` 升到目标版本 | 回读等于目标值。auth 链自动走提案 |
| T6 | 重读 feature | 目标版本的 feature 从空变成 `1` |
| T7 | 再跑一次三个 oracle | 与 T1 一致 |
| T8 | 尝试降回画像的创世版本，要求 `upgrade.rollback: true` | 链拒绝，版本不变 |

`--new-bin` 必须是 3.18.0 及以上，因为整条时间线都靠它的子命令操作链。`--old-bin` 没有这个要求。`--dry-run` 只打印计划，`--keep-cluster` 保留升级后的链。

`gate upgrade` 不属于四族场景，也不能写进 `--scenarios`。

### `fbt case list` 和 `fbt case run`

回放单条回归用例，返回它的真实结果。

```console
$ fbt case list --status all
example.case             example  pass     default-latest       shipped
fuzz_seed43_idx11.case   pending  reject   production-enterprise shipped
```

```bash
fbt case run fuzz_seed43_idx11            # 退出码是这条用例的实际结果
fbt case run --dry-run fuzz_seed43_idx11  # 只打印解析结果
fbt case run --keep-cluster example       # 跑完保留链
```

参数要写在用例名之前。`--status` 可选 `active`、`pending`、`example`、`all`，默认 `active`。

用例是一个 INI 风格的文件：

```ini
[case]
status = active
profile = default-latest
input = "${OPS_BIN:-$FISCO_BIN}" sysconfig get tx_count_limit --node-dir "$NODE_DIR/node0" --json
expect_oracle = pass
```

| 字段 | 含义 |
|---|---|
| `status` | `active` 表示缺陷已修，`gate run` 每轮回放。`pending` 表示还没修，只报告不回放。`example` 只作示例 |
| `profile` | 要复现的画像 |
| `input` | 用 `bash -c` 执行的命令，可以用 `$NODE_DIR`、`$FISCO_BIN`、`$OPS_BIN` |
| `expect_oracle` | `pass` 要求命令成功且三个 oracle 都没触发。`reject` 要求命令失败且节点存活 |

无论 `expect_oracle` 写什么，节点崩溃都算失败。自己写的用例放到状态目录的 `cases/` 下，`fbt case list` 会标记为 `state`。

用例格式的完整说明见 `scenarios/README.md`。

### `fbt cluster`

建一条链并保持运行，用于手动调试或配合 `fuzz run --attach`。

```bash
fbt cluster up -p production-enterprise   # 打印 run id 和端口
fbt cluster ls                            # 列出注册的链和状态
fbt cluster down --run-id <id>            # 停节点、删目录、释放端口
```

`cluster up` 失败时当场清理。`cluster down` 不带目标时，只在恰好有一条活链时才执行。有多条时退出 20，要用 `--run-id` 或 `--workspace` 指定，fbt 不会猜。

`cluster down --workspace <dir>` 只删除 fbt 自己布局的 `runs/<id>/cluster` 目录。其他路径只停节点，目录留着。`--keep-registration` 只停节点，目录和注册项都保留。

### `fbt fuzz run`

对活链做交易变异探索。发现问题时会把用例写进状态目录的 `cases/`，状态为 `pending`。它本身不建链：

```bash
fbt cluster up -p production-enterprise
fbt fuzz run --attach <run-id> --transport web3 --strategy both --seed 42 --iters 1000
fbt cluster down --run-id <run-id>
```

| 参数 | 含义 |
|---|---|
| `--attach <run-id>` | 目标链 |
| `--transport` | `bcos`、`web3` 或 `web3method` |
| `--strategy` | `struct`、`bytes` 或 `both` |
| `--seed`、`--iters`、`--batch` | 随机种子、迭代数、批大小 |
| `--restart-cmd` | 节点崩溃后拉起它的命令，让一次运行不因发现崩溃而结束 |

依赖 `tools.fuzz_jar`。`gate run --with-fuzz` 目前未实现，用了会退出 20。

### `fbt report`

从磁盘记录读取一轮的结果，不重新运行任何东西。无论那一轮结果如何，这条命令自己都返回 0。

```console
$ fbt report --run-id <id>
...
KIND      STAGE                  PHASE            RESULT
oracle    crash                  T1-baseline      pass
oracle    halt                   T1-baseline      pass
scenario  upgrade                                 pass

no defects recorded
```

有缺陷时显示复现命令和证据目录：

```text
2 defect(s), 2 not yet reported anywhere:
  [NEW] case:fuzz_seed43_idx11.case/crash 高
      oracle_crash tripped replaying fuzz_seed43_idx11.case
      repro:    bash .../run_case.sh .../fuzz_seed43_idx11.case
      evidence: .../runs/<id>/evidence/127.0.0.1
```

不带参数时读最近一轮，`--list` 列出所有轮次。

### `fbt profile` 和 `fbt config`

```bash
fbt profile list
fbt profile show production-enterprise
fbt config show [-p <画像>] [--command <命令>] [--all-keys]
fbt config path
```

## 画像

`-p` 接受下表的名字，或任意 `.profile` 文件的路径。

| 画像 | 创世版本 | 用途 |
|---|---|---|
| `production-enterprise` | 3.16.4 | 从真实生产链采集的配置，发版前主要跑这一个 |
| `default-latest` | 3.18.0 | 开源默认链，无配置偏移 |
| `evm-full` | 3.16.4 | 打开全部 `feature_evm_*` |
| `rpbft-scale` | 3.16.4 | rPBFT 共识，7 节点 |
| `sm-gov` | 3.16.4 | 国密链 |
| `upgrade-legacy` | 3.0.0 | 很早的版本，用于长距离升级 |

写新画像的方法见 `references/profile-authoring.md`。

## 一轮结束后留下什么

每轮退出时都会停节点、释放端口、删掉 `runs/<id>/cluster`。通过、门禁失败和 Ctrl-C 都走同一条路径，在 `internal/cli/leftover.go` 的 `settle`。

```text
<state>/runs/<id>/
├── events.jsonl      每轮都有：这一轮的事件记录，fbt report 读它
├── failures.jsonl    记录了缺陷时才有
└── evidence/         失败或有缺陷时才有内容：节点日志、config、场景日志
```

| 结束方式 | 留下的大小（4 节点） |
|---|---|
| 通过 | 约 12K |
| 门禁失败 | 约 1M |
| Ctrl-C | 约 500K |

二进制拷贝、RocksDB 数据和密钥任何情况下都不会进 `evidence/`。

**要看活链：** 给 `gate run`、`gate upgrade`、`case run` 加 `--keep-cluster`，链和目录都留下，端口保持占用，用 `fbt cluster down --run-id <id>` 收尾。这一轮的 stderr 会打印这条命令。

**如果 fbt 被 `kill -9`：** 它自己清理不了，节点还在运行。下一次 `gate run`、`gate upgrade`、`case run` 或 `cluster up` 会在端口检查前回收，日志保留在那一轮的 `evidence/` 下。只有之后再也不跑这些命令时，节点才会一直留着。

旧的运行记录不会自动删除，可以直接删 `runs/` 下不需要的目录。

## 退出码

| 退出码 | 含义 | 下一步 |
|---|---|---|
| 0 | 全部通过 | 无 |
| 10 | 门禁失败，这个版本有问题 | `fbt report` 看缺陷 |
| 20 | 配置或参数错误 | 修命令或配置 |
| 30 | 机器环境不满足，或超时 | `fbt doctor` |
| 40 | fbt 内部错误 | 报 bug，附 `events.jsonl` |
| 130 | 用户取消 | 无 |

多个结果并存时取优先级最高的：130 > 40 > 30 > 20 > 10 > 0。

### 在 CI 里用

```bash
fbt --output json gate run -p production-enterprise --scenarios dual_rpc,malformed > gate.json
code=$?
case $code in
  0)  echo "可以发布" ;;
  10) echo "版本有问题"; fbt report; exit 1 ;;
  *)  echo "门禁没跑成，结果不算数（$code）"; exit 1 ;;
esac
```

要把 30 和 10 分开处理：30 表示这个版本还没被测过，不代表它有问题。

三种输出形式：

- **`human`：** stdout 是结果，引擎日志走 stderr。
- **`json`：** stdout 只有一个 JSON 文档。失败时形如 `{"exit":20,"class":"config","reason":"..."}`。
- **`jsonl`：** 事件一条一条流式输出，适合显示进度。

## 排障

| 现象 | 原因 | 处理 |
|---|---|---|
| `tools.build_dir points at ..., which does not exist`，退出 30 | 默认要跑 `ut`，但没有测试二进制 | 用 `--scenarios` 排除 `ut`，或者先编译 |
| 提示端口被占用，退出 30 | 别的链占着配置的端口段 | 改 `cluster.*_base_port`，或用 `fbt cluster ls` 找到那条链停掉 |
| `build_chain` 报 keccak 相关错误 | OpenSSL 太旧 | 配 `tools.openssl_cmd`，`doctor` 会检查这一项 |
| 节点报 `available disk space is less than 1GB` | 磁盘不够 | 腾空间，至少留 3GB |
| `balance add` 报 `Permission denied`，升级场景失败 | `build_chain.sh` 的 admin 地址 bug，见[已知问题](#已知问题) | 打 PR #5762 的补丁 |
| Java SDK 报 `encoded key spec not recognized` | 私钥不是 PKCS#8 | `jsd` 会自动转换，确认 `tools.openssl_cmd` 可用 |
| `--cases dual_rpc` 报 `matches no case` | `--cases` 选用例，不选场景 | 改用 `--scenarios` |
| 想保留现场却发现目录没了 | 默认会清理 | 加 `--keep-cluster` 重跑。失败轮的日志在 `evidence/` |

`fbt report` 的第二行是这一轮的目录，详细过程在其中的 `events.jsonl`，每行一个事件。

## 已知问题

- **一轮只能建一次链。** 同一轮有两个及以上会建链的阶段时，第二个阶段因 `build_chain` 拒绝已存在的目录而失败，退出 40。这包括“场景加用例”和“两条以上用例”。目前随版本发布的用例没有 `active` 状态，所以默认的 `gate run` 不会碰到。把用例改成 `active` 之后才会暴露。规避办法是用 `fbt case run` 逐条跑。
- **`build_chain.sh` admin 地址错误。** 3.18.0 的脚本给 3.18 之前的二进制建链时，算出的 admin 地址没人持有私钥，治理操作全部失败，`gate upgrade` 因此不通过。修复在 [FISCO-BCOS#5762](https://github.com/FISCO-BCOS/FISCO-BCOS/pull/5762)，合入前要给 `repo.root` 里的脚本打这个补丁。
- **T7 的 crash 判据只看活着的 PID。** 升级时节点重启失败，PID 集合变小仍判通过。其他步骤可能抓到，但不保证。
- **`jsd` 前置条件缺失时退出 10。** 理论上应为 30。`doctor` 会先查目录是否存在，只有目录存在但内容不对时才走到这一步。
- **缺陷台账未接入 fbt。** `fbt report` 只提示 `scripts/report_defects.sh <目录> --dry-run`，推送到腾讯文档要手动运行。
- **`fuzz_seed43_idx11` 是真实缺陷。** 在 3.18.0 上仍能复现，所以这条用例 `pending`，`case run` 退出 10 是预期结果。

## 验证范围

2026-11-04 在 Ubuntu x86_64 上，用 release-3.18.0 的静态构建实测：

| 路径 | 结果 |
|---|---|
| `gate run --scenarios dual_rpc,malformed` | 通过，退出 0 |
| `gate run --scenarios dual_rpc --keep-cluster`，再 `cluster down` | 链保留，之后清理干净 |
| `gate upgrade` 3.17.0 到 3.18.0，T0 到 T8 | 通过，约 90 秒 |
| `gate upgrade --keep-cluster`，再 `cluster down` | 链保留，之后清理干净 |
| `case run fuzz_seed43_idx11` | 退出 10，缺陷和日志保留 |
| `case run` 配 `RG_KEEP_CLUSTER=1` | 链保留 |
| `gate run --scenarios jsd` | 通过，退出 0 |
| `cluster up`、`cluster ls`、`cluster down` | 通过 |
| 运行中 Ctrl-C | 退出 130，清理干净 |
| 运行中 `kill -9`，再跑下一轮 | 下一轮回收后通过 |

下面这些没有在 3.18.0 真链上跑过：

- **`ut` 场景：** 只跑过其中的部分单元测试，没有通过 fbt 跑过。
- **`fuzz run`：** 3.18.0 上未运行。
- **`rpbft-scale`、`sm-gov`、`evm-full`、`upgrade-legacy` 画像：** 真链只跑过 `production-enterprise` 和 `default-latest`。
- **macOS：** 只跑过单元测试，真链只在 Linux 上验证过。

单元测试这样跑：

```bash
go test ./...
for t in tests/*_test.sh; do bash "$t"; done
```
