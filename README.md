# Pair-wiseGSB 可复核计量链

这是一个无外部 Go 依赖的实验室计量链服务。后端负责单位量纲、相关矩阵、DAG、校准证书、不确定度传播、覆盖区间、冻结、复制、审计摘要和持久化恢复；浏览器界面只负责录入与展示。

## 安装与演示

```bash
go mod download
go test ./... -count=1
go run ./cmd/server --listen 127.0.0.1:5199
```

打开固定页面：`http://127.0.0.1:5199`

可选参数：`--data ./data` 指定持久化目录。首次启动会自动创建目录。

## 数据模型

原始输入、派生结果和操作事件分开保存：

- `observations/*.json`：原始观测值、单位、标准不确定度、分布、自由度、相关组。
- `groups/*.json`：组内统一相关系数；计算前与复制方案的两两覆盖合并检查 PSD。
- `certificates/*.json`：修正值、修正单位、不确定度、自由度、有效期和 `replacedBy` 链。
- `scenarios/*.json`：候选方案的节点、边、输出节点和相关性覆盖。
- `results/*.json`：活动派生结果。可能是 `ready` 或 `waiting_confirmation`。
- `frozen/*.json`：不可变冻结结果，包含冻结瞬间的数值、区间、节点、公式版本和单位换算路径。
- `events/*.json`：只追加的业务操作事件。
- `requests/*.json` 与 `manifest.json`：幂等记录和当前状态索引。

节点类型：

- `observation`：引入原始观测。
- `conversion`：同一量纲的线性单位换算；温度按 affine 规则处理。
- `weighted`：显式权重均值；未给权重时使用逆方差权重。
- `difference`：两个同量纲输入的差值。
- `calibration`：输入加证书修正，证书不确定度作为独立输入源传播。

计算采用一阶 GUM 线性传播和 Welch–Satterthwaite 有效自由度，覆盖区间使用 97.5% Student-t 分位数。结果记录下列公式版本：

- `source-input/v1`
- `linear-unit-conversion/v1`
- `inverse-variance-weighted-mean/v1`
- `linear-difference/v1`
- `additive-calibration/v1`
- `gum-first-order/v1`
- `student-t-coverage/v1`
- `psd-cholesky/v1`

## 拒绝和等待确认

以下错误不会产生看似正常的顶层数值，HTTP `400` 响应会定位到 `nodeId`、`edge`、`inputId` 或 JSON path：

- 单位未知或量纲不一致。
- 相关矩阵不在 `[-1,1]` 或非半正定。
- 引用不存在的观测、节点或证书。
- 依赖形成环、权重非法、输出节点不存在。

过期、未生效或已替换的证书属于状态问题：方案可保存，活动结果为 `waiting_confirmation`，问题指向具体校准边；替换并选择重算后才回到 `ready`。冻结操作只允许 `ready` 结果。

## 候选方案、冻结与证书替换

1. 用同一批观测创建多个 `scenario`。
2. 页面显示依赖图、每个输出节点的贡献率和敏感性排序。
3. 冻结一个方案后，后续证书替换不改变 `frozen/*` 中的历史结论。
4. 复制方案时提交 `correlationOverrides`，可并排比较区间和敏感性。
5. 替换证书先返回 `affectedScenarioIds`、受影响结果和 `recalculationRequired`。
6. 可对所选方案逐个重算；`/api/recalculations/atomic` 会预检查全部方案，任一硬错误都会整体拒绝，避免半批次更新。

## HTTP 接口与错误

- `GET /healthz`
- `GET /api/state`
- `POST /api/observations`
- `POST /api/groups`
- `POST /api/certificates`
- `POST /api/scenarios`
- `POST /api/scenarios/{id}/copy`
- `POST /api/scenarios/{id}/freeze`
- `POST /api/certificates/{id}/replace`
- `POST /api/recalculations`
- `POST /api/recalculations/atomic`
- `GET /api/scenarios/{id}/comparison?ids=s1,s2`
- `POST /api/audits/export`
- `POST /api/audits/import`

错误格式统一为：

```json
{"error":{"code":"INVALID_INPUT","message":"input validation failed","details":[{"code":"UNIT_DIMENSION_MISMATCH","nodeId":"c","edge":"n->c"}]}}
```

- `400 INVALID_INPUT`：JSON、格式、单位、PSD、环、引用等输入错误。
- `409 CONFLICT`：重复 ID、重复冻结、同 `X-Request-ID` 请求指纹不同、原子批次状态冲突。
- `500 INTERNAL_ERROR`：存储或编码等内部故障。
- 过期证书本身保存为业务状态：HTTP 成功但结果状态是 `waiting_confirmation`。

## 幂等与崩溃恢复

所有写请求接受 `X-Request-ID`。服务端保存方法、路径和请求体 SHA-256：

- 同一 request ID 且指纹相同：返回第一份响应，不创建第二份事件或业务结果。
- 同一 request ID 但请求体/路径不同：返回 `409`。

提交过程使用根目录下的 `.tx-*` 暂存目录：所有文件先写入并 fsync，写 `tx-manifest.json` 和 `COMMITTED` 后再 rename 为 `tx-*`，随后按清单把文件 rename 到正式位置并执行删除。启动时：

- 没有 `COMMITTED` 的暂存目录直接删除，不暴露半成品。
- 已提交但未应用完的 `tx-*` 会按清单继续应用。
- 不可变事件和请求记录一旦进入正式目录不会被覆盖。

## 审计包

导出包包含：

- 原始观测、相关组、证书、方案。
- 活动派生结果和冻结结果。
- 相关操作事件。
- 规则版本、模式版本。
- 每一类数据的 SHA-256 和整包 checksum。
- 结果内的单位换算路径和公式版本。

导入时先验证逐文件摘要和整包 checksum，再在一个业务事务中写入。重新导入到空库后，`/api/state` 中活动结果的数值、不确定度、覆盖区间、自由度、公式版本和冻结包均与导出一致。

## 手工演示建议

页面左侧先建相关组和两条原始观测，再用“差值”建 `s1`；复制为 `s2` 并把相关性覆盖改成不同 ρ；创建一个长期有效和一个已过期证书，观察校准边的等待状态；替换证书后先查看影响列表，再尝试逐个或原子重算；最后导出、下载并在另一个空数据目录启动后导入。
