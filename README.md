# 计量链复核台 · Metrology Chain Lab

一套可独立运行的**测量不确定度传播与可复核计量链**服务。原始观测、派生结果与
操作事件分库持久化；关键判定（量纲、相关性半正定性、依赖环、证书有效期）全部在
服务端执行，浏览器只是视图。

## 一分钟启动

```bash
go mod download
go test ./... -count=1
go run ./cmd/server --listen 127.0.0.1:5199
```

打开固定页面：<http://127.0.0.1:5199>

数据默认写入工作目录下的 `data/`，可用 `--data <dir>` 或环境变量
`METROLAB_DATA` 覆盖。仅依赖 Go 标准库（含 `embed`、`archive/tar`），无外部数据库。

## 数据模型（三类数据分开保存）

所有写操作先进入 **WAL**（`events.wal`，每条记录带行内 SHA-256、单条/批次落盘后
`fsync`），再原子重写三个互相独立的物化集合：

| 集合文件 | 内容 | 说明 |
| --- | --- | --- |
| `observations.json` | 原始输入 `Observation`（值、单位、标准不确定度、分布、自由度） | 原始输入，不就地改写 |
| `scenarios.json` / `certificates.json` | 方案（节点/边/相关组/两两相关）与校准证书 | 证书以追加新版本方式替换，旧版保留 |
| `results.json` | **派生结果** `Result`：完整输入快照 `engine.Spec`、输出、状态、审计摘要 | 与原始输入物理分离 |
| `events.json` | **操作事件**（append-only 投影：seq/event/id/request_id/time） | 只追加，不修改 |
| `events.wal` | 权威事件日志 | 启动时重放重建内存状态 |

关键不变量：

- **幂等**：所有变更请求接受 `X-Request-Id`（或 `?request_id=`）。同一 request_id
  重放返回同一对象，**不会产生第二份业务结果**。
- **崩溃一致性**：写路径为「WAL 追加+fsync → 临时文件 → rename + 目录 fsync」。
  落盘中途退出后重启，WAL 末尾残缺/校验失败的记录会被截断丢弃；物化集合至多是旧的
  但永远不会出现半个 JSON。所有权威状态由 WAL 重放得到。
- **原子批次**：证书替换后的多结果重算可走批量接口，多条事件一次写入、一次 fsync，
  要么全部可见要么全部不可见（先整体校验，再整体提交）。

## 计量模型与规则版本

引擎（`internal/engine`）对方案做**全 DAG 一阶 GUM 传播**（JCGM 100:2008）：

- 协方差矩阵以基本输入（叶节点 + 每张校准证书的 slope/intercept 参数）为行；
  相关来源包括等相关组、显式两两相关，以及证书自身 slope/intercept 协方差。
- 每个节点的输出 `u_c² = cᵀ V c`；贡献率同时列出自身项与相关交叉项（带符号份额）。
- 有效自由度采用 GUM G.4 Welch–Satterthwaite 公式；覆盖因子 `k=t_p(ν_eff)`
  （ν=∞ 时退化为标准正态分位数，t 分位数由正则化不完全 Beta 数值求解）。
- 每个结果固定保留规则版本：`GUM-JCGM100-2008`、`WS-GUM-G4-2008`、
  `UNITS-SI-v2019`、`WEIGHTED-MEAN-v1`、`LINEAR-DIFFERENCE-v1`、
  `CALIBRATION-LINEAR-v1`、`UNIT-CONVERT-v1`。

四类派生步骤：

- `convert`：单位换算，输出值回到声明单位；仿射单位（degC/degF）只允许单独使用。
- `weighted`：归一化权值的加权平均。
- `diff`：角色 `a`/`b` 的差值。
- `calibration`：线性校准 `y = reading + intercept + slope·(reading − x0)`，
  含 slope/intercept 不确定度及其相关性；参考值 x0 也是一个传播输入。

单位：七维 SI 量纲向量（kg/m/s/A/K/mol/cd），支持 SI 词头、复合单位（`m/s^2`、
`N*m`、`ug/mL`）、百分率/ppm、升、eV、bar、分/小时等。每条边的换算路径在结果的
`unit_paths` 与审计包中逐步记录。

## 硬性拒绝（不给出貌似正常的数字）

以下情况计算请求返回 `400 invalid_input`，并把问题定位到具体**节点/输入/边**：

- 量纲不一致：`dimension_mismatch`，带 `edge`（如 `m1->bad`）；
- 相关矩阵非半正定：Jacobi 特征分解检测负特征值，`correlation_not_psd`
  附带最小特征值与主要相关输入；
- 依赖成环：`dependency_cycle`，逐条列出环上的边；
- 引用不存在的组/成员/证书、节点 arity 错误、权重非法、分布与自由度不匹配等。

## 校准证书生命周期

- 证书有 `valid_from/valid_until`。**新计算**在当前时间（或请求中的 `as_of`）
  晚于有效期、或证书已注销时，结果进入 `pending`（等待确认）状态：系统保存校验输出，
  但该节点的 `k` 与覆盖区间一律为 `null`，不呈现置信数字。
- 用户显式调用确认接口后才重新计算并出具数字（`POST /api/results/{id}/confirm`）。
- **冻结**（`POST /api/scenarios/{id}/freeze``）会把方案图与完整输入快照钉死；
  证书到期/替换后，既有冻结结果仍可用旧证书参数逐位复现，冻结方案拒绝任何编辑
  （返回 `409 state_conflict`），需要调整请“复制为新方案”。
- **替换证书**（`POST /api/certificates/{id}/replace`）安装新版本、注销旧版本，
  自动把草稿方案中的引用指向新证书，并返回**受影响结果清单**（含冻结标记与原因）。
  之后可：
  - 逐个重算：`POST /api/results/{id}/recompute`；
  - 原子批次：`POST /api/results/recompute-batch`。

## 审计包

`GET /api/results/{id}/audit` 导出确定性 tar：

```
metrolab-bundle.json      # 清单：每个文件的 SHA-256、规则版本
inputs/observations.json
inputs/certificates.json
inputs/scenario.json
rules/versions.json
work/spec.json            # 完整引擎快照
work/unit_paths.json      # 单位换算路径
result/output.json
checks/hashes.json        # 输入规范化哈希
```

`POST /api/audit/import`（可选 `?store=true`）：先逐文件校验 SHA-256，再用引擎对
快照重算，逐数值节点比较值/标准不确定度/k/区间（容差 1e-12），并核对输入哈希。
报告给出 `spec_checksum_ok`、`numeric_diffs[]`、`all_match`；入库按审计哈希幂等，
重复导入同一包不会产生第二份结果。

## HTTP 错误三分类

| HTTP | `error.kind` | 场景 |
| --- | --- | --- |
| 400 | `invalid_input` | JSON 格式错误、字段缺失/非法、量纲/PSD/环/引用校验失败 |
| 409 | `state_conflict` | 冻结对象被改、重复 ID、幂等冲突、对冻结结果做重算 |
| 500 | `internal` | 持久化/序列化等内部故障（响应体不含内部细节，服务端记日志） |

校验失败响应还带 `problems[]`：`code/target/edge/detail` 精确定位。

## 主要 API

```
POST   /api/observations                 # X-Request-Id 幂等
GET    /api/observations
POST   /api/scenarios                     # 新建草稿方案
PUT    /api/scenarios/{id}                # 编辑草稿（冻结后 409）
POST   /api/scenarios/{id}/copy           # 复制，可覆盖 groups/pairs
POST   /api/scenarios/{id}/freeze
POST   /api/scenarios/{id}/compute        # {"confirmed":bool,"as_of":RFC3339}
GET    /api/results?scenario=...
GET    /api/results/{id}
POST   /api/results/{id}/confirm
POST   /api/results/{id}/recompute
POST   /api/results/recompute-batch       # {"result_ids":[...]}
POST   /api/results/{a}/diff/{b}
POST   /api/certificates
POST   /api/certificates/{id}/replace
GET    /api/certificates/{id}/affected
GET    /api/results/{id}/audit            # tar
POST   /api/audit/import?store=true       # 上传 tar
GET    /api/events
```

## 崩溃恢复方式

服务启动即打开并校验 `events.wal`：完整记录按序重放重建内存，末尾损坏/截断的记录
被就地截断；随后重写物化集合。数据目录里 `.tmp/.new` 临时文件启动时清理。可用
`kill -9` 后再次执行同一条 `go run` 命令验证。

## 目录结构

```
cmd/server/           服务入口
internal/engine/      单位量纲、GUM 传播、PSD/环校验、t 分位数（纯函数，无状态）
internal/store/       WAL、fsync、原子集合重写、残帧截断
internal/service/     领域判定、幂等、冻结、证书生命周期、审计导入导出
internal/httpapi/     JSON API 与错误分类
internal/web/         内嵌浏览器界面（依赖图/贡献率/敏感性/并排对比/审计）
```

## 浏览器界面

- **原始观测**：值、单位、标准不确定度、分布、自由度；
- **校准证书**：登记、替换、受影响结果清单（逐个/原子批次重算）；
- **方案与图**：分层依赖图 SVG，节点/边/相关组编辑器，计算后展示合成不确定度、
  覆盖区间、贡献率条形（含相关交叉项）、敏感性排序、公式版本与单位换算路径；
- **并排对比**：两个结果逐节点的值差与不确定度变化；
- **审计包**：下载 tar、重新导入并显示哈希/数值校验结论；
- **事件日志**：append-only 操作流水。

## 测试

`go test ./... -count=1` 覆盖：GUM 数值与相关贡献、仿射温度差值、量纲/环/PSD
定位、证书到期 pending/确认/冻结复现、WAL 残帧恢复、批次原子性、幂等重放、审计
往返哈希一致，以及 HTTP 400/409/200 语义。
