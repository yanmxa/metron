# 变异引擎设计(实测版)

所有数字都是 go1.26.2 / darwin arm64 / 10 核上实测的,fixture 是 `spf13/cobra` @ HEAD
(36 个 Go 文件,root 包 261 个顶层测试)和几个专门构造的小模块。

**结论:自研引擎成立。** overlay 不打穿构建缓存;冷构建 3.68 s 之后每个变异体
~0.9 s。真实规模的 diff(7 个变异体)端到端 **4.8 s**,w=2 加隔离后 **2.2 s**。

---

## 一、三个会让分数造假的坑(全部实测复现)

按危害排序。三个都**朝着"分数虚高"的方向失败**,这是最坏的失败方向。

### 坑 1:构建失败和测试失败在 JSON 里长得一模一样

编译不过的变异体退出码是 1,包级事件是 `{"Action":"fail"}` —— 和真的测试挂了
逐字节相同。按退出码或者"包失败了"来分类,**每个编译不过的变异体都会被记成
KILLED**。cobra `command.go` 上是 274 个里的 26 个(9.5%)被算成"测试很给力"的证据。

判别字段是 `FailedBuild`(以及 `Action:"build-fail"`),这是 `cmd/test2json`
文档里写明的字段,不是碰巧:

```json
{"ImportPath":"…[…test]","Action":"build-fail"}
{"Action":"fail","Package":"…","FailedBuild":"example.com/toy/calc [example.com/toy/calc.test]"}
```

### 坑 2:`go test` 默认跑 vet,vet 失败又和编译失败长得一样

构造:`n >= 0 || n < 0`,把 `<` 变异成 `>=`,得到 `n >= 0 || n >= 0`。
这是**完全合法的 Go**,但 vet 报 `redundant or`:

```
$ go test -json -overlay=mut.json ./v/
{"Action":"build-fail"} …                    → 判成 NOT_VIABLE ✗
$ go test -vet=off -json -overlay=mut.json ./v/
{"Action":"pass",…}                          → SURVIVED ✓
```

判成 NOT_VIABLE 就把一个**真幸存者**从分母里删掉了,分数直接虚高。
**`-vet=off` 是正确性要求,不是性能优化。**(顺带快 7%。)

### 坑 3:并发会激活抖动测试,造成"假杀死"

cobra baseline 顺序跑 5 次全绿;4 路并发跑,`TestDeadcodeElimination` 两轮里
各挂 2 次(它 shell out 到 `go build` 用相对路径,负载下竞争)。

后果不是理论上的:`RangeArgs` 里 `len(args) > max` → `>= max` 是**真幸存者**,
顺序跑 10/10 SURVIVED;w=4 下 3 次试验里有 2 次报成 KILLED,整体结果在
`KILLED=15/SURVIVED=3` 和 `16/2` 之间跳。

隔离前后的结果集哈希:

```
带隔离    w=2 ×3, w=4 ×3  → 六次全部 59f1968c…,和 w=1 的 ground truth 一致
不带隔离  w=4 ×3          → 59f1968c…, 59f1968c…, 93fc3ddb…   ← 发散
```

**结论反直觉但很重要:让并发安全的是隔离,不是把并发数调低。** 隔离还顺带更快
(w=2 下 1.37 s vs 2.22 s),因为甩掉了一个重测试。

---

## 二、分母 —— 全工具最要紧的一个定义(已修正)

PIT 区分两个指标,这个区分值得照搬。metron 报三个数,**头条(也是闸门)是把
未覆盖算进分母的那个**:

```
                     KILLED + TIMED_OUT
  score    = ─────────────────────────────────────────────   ← 头条 + 闸门
             KILLED + TIMED_OUT + SURVIVED + NOT_COVERED

  strength = (KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED)
  reach    = 1 − NOT_COVERED / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)
```

**为什么 NOT_COVERED 必须进分母。** metron 量的是 agent 生成的改动,而 agent 最
典型的失败形态就是写了 200 行、把其中 20 行测得很好。只报 strength 的话这种改动
拿 ~1.0 —— 灾难性的假通过,而且**可以直接刷分**:给一个小函数写一个漂亮测试就行。
strength 回答的是"你写的测试够不够狠",而产品要问的是"这次改动是不是被测试撑住了"。
两者不是一回事,只有后者值得设闸门。

**为什么 NOT_VIABLE 必须出分母。** 它是 metron 自己生成器的产物,不是用户测试的
属性。算进去会让分数取决于我的类型门做得好不好,而且方向是错的——一个字符串拼接
多的文件会因为跟测试无关的原因得分更低。实测:`command.go` 上算进去让分数从
68.3% 变成 66.8%,纯粹是生成器噪音。单独报成生成器健康度诊断
(`NOT_VIABLE / total > 15%` 就警告我自己的算子门没做好)。

`TIMED_OUT` 计入检测到:变异让测试挂死,测试确实察觉到了变化。PIT 和 gremlins 都
这么算。单独报,免得掩盖病理。

strength 和 reach 一起把头条拆开,告诉你落在哪种失败形态里——
**reach 低 = 这块你压根没测;strength 低 = 测试跑到了但什么都没断言。**
这个拆解才是可行动的输出,单个数字只是闸门。

cobra `command.go` 真实数据(274 个变异体,已过覆盖率预筛):

```
NOT_COVERED 35  KILLED 182  TIMED_OUT 1  SURVIVED 50  NOT_VIABLE 6
score = 183/268 = 0.683    strength = 183/233 = 0.785    reach = 1−35/268 = 0.869
```

---

## 三、生成必须是类型感知的

Go 里 `+` 对字符串重载了,`-` 没有。落在字符串拼接上的算术变异体**保证编译不过**,
而 Go 代码里到处是字符串拼接。纯语法生成器在 `command.go` 上 274 个变异体里 26 个
(9.5%)编译不过。

用 `golang.org/x/tools/go/packages` 带 `NeedTypes|NeedTypesInfo` 加类型门之后:

```
candidates=293  emitted=258  SUPPRESSED=35
  ARITHMETIC_BASE        emitted=  4  suppressed= 26   ← 这个算子 87% 是垃圾
  CONDITIONALS_NEGATION  emitted=164  suppressed=  0
  INVERT_LOGICAL         emitted= 53  suppressed=  0
  CONDITIONALS_BOUNDARY  emitted= 25  suppressed=  0
  INVERT_LOOP_CTRL       emitted= 10  suppressed=  4
```

五层抑制,按成本排序:

1. **类型门**(`go/types`,免费,包已经加载了):算术/赋值算子要求操作数
   `*types.Basic` 是 `IsInteger|IsFloat|IsComplex`。上面消掉 31 个。
2. **常量操作数门**(`go/constant`):`*`/`/` by 1、`+`/`-` by 0、`/` by 0、
   非整数取模、无符号数和 `0` 比大小(`u >= 0` 是恒真,翻转它必然等价)。
3. **标签门**:`break L` → `continue L` 只有 `L` 标的是 `for` 才合法。要解析
   标签指向的语句,不能一刀切拒绝(探针里一刀切多杀了 4 个)。
4. **调用目标黑名单**(给 `REMOVE_STATEMENT`):`log.*`/`slog.*`/`fmt.Print*`/
   `*.Debug|Info|Warn`/OTel。删掉一个可观测性调用在语义上是隐形的,天生杀不掉。
5. **等价体账本**:`sha256(算子 ‖ 归一化 AST 上下文 ‖ 所在函数签名)` 持久化到
   `.metron/equivalents.json`。跑过完整一轮且被标成等价的,后续报 `EQUIVALENT`
   并出分母。这是唯一需要人工输入的机制,按仓库 opt-in。

**字节替换,不是 AST 重打印。** 所有 token 算子只改 `OpPos`/`TokPos` 那几个字节,
其余原样。实测输出与原文件逐行一致(26 行 → 26 行)。这保住三件下游的事:覆盖率
block 映射仍然有效、编译错误位置仍然有意义、报告能展示精确的前后两行。
`REMOVE_STATEMENT` 把语句字节范围填成空格来保住行数。

---

## 四、测试选择 —— 显而易见的答案是错的

先看成本分解(cobra,热缓存,同一个变异体):

| 阶段 | 累计 | 边际 |
| --- | --- | --- |
| `go build -overlay .` | 42 ms | 23 ms |
| `go test -c -overlay .`(编译+**链接**) | 223 ms | **181 ms** |
| `go test -overlay . -run=^NOTHING$`(跑 0 个测试) | 359 ms | **136 ms** |
| `go test -overlay .`(跑全部 261 个) | 532 ms | 173 ms |

**不可压缩的地板是 ~360 ms:链接 + 进程启动。** 532 ms 里只有 173 ms 是真在跑测试。

所以:**完美的测试选择最多省 33%**。而两个"聪明"办法都不划算——

- **逐测试覆盖率剖析**:一次 `-run='^TestX$' -coverprofile` 要 530 ms,cobra root 包
  260 个测试 → **~138 s** 才能拿到选择信息。为省 10 s 花 138 s,净亏。
- **`codegraph affected`**:对 Go **没有选择性**。默认 glob 匹配不到 Go 测试约定,
  返回空;加 `-f '*_test.go'` 之后返回 18 个测试文件里的 17 个。延迟倒是不错
  (116–132 ms),而且没有 `.codegraph/` 时退出码 1、stderr 有明确提示。

**真正的做法:包级选择,用 `go list -json ./...` 的反向依赖闭包。** 免费、精确、
不依赖任何外部索引。在两包 fixture 上验证:变异 `core` 正确选出 `api`(测试在那儿)。
在 100 个包的仓库上,这是 60 秒套件和 0.5 秒套件的差别——省时间基本全在这儿。

逐测试选择只在**实测**某个包的 baseline 耗时超过阈值(默认 > 3× 地板 ≈ 1.1 s)时
才启用,结果缓存到 `.metron/` 跨运行摊销。`codegraph affected` 只作为可选加速器,
永远当提示不当事实,永远有回退。

**只选顶层测试,不选子测试。** 三条实测理由:`go test -list` **列不出子测试**
(运行时才发现);选中子测试仍然会跑父测试的 body(包括表格构造),对表驱动测试
省不下来;子测试名会被消毒(空格变 `_`、重名加 `#01`)且可能含正则元字符,而
**顶层测试名是 Go 标识符**,拼 alternation 不需要转义。

261 个测试名的 alternation 是 7690 字节,ARG_MAX 是 1048576,规模不是问题。
`-run` 没有取反语法,所以排除隔离测试要显式拼补集。

---

## 五、覆盖率预筛的三个边界

预筛本身很有效且**可靠**:`command.go` 上预测未覆盖的 35 个,真跑之后 **0 个被杀**
(15 SURVIVED / 20 NOT_VIABLE)。预筛从不丢掉测试本来能抓住的变异体。

1. **`-coverpkg=./...` 必须加**(见计划正文),开销实测为零(570 vs 573 ms)。
2. **合并后的 profile 有重复 block 且计数不同**——它是各测试二进制 profile 的拼接。
   自己写 `map[block]count` 会因为迭代顺序拿到错的答案。用
   `golang.org/x/tools/cover.ParseProfiles`,它正确 OR 合并。
   另外 profile 里的文件名是 **import path 不是磁盘路径**,要用 `go list -json` 映射。
3. **`case` 的守卫表达式不在任何 block 里**。274 个变异体里 21 个映射不到 block:

   ```go
   case strings.HasPrefix(s, "--") && !strings.Contains(s, "="):   // 691 行
   ```
   ```
   command.go:691.97,694.15 1 1    ← block 从第 97 列开始,即冒号之后
   ```
   Go 插桩的是 case **体**不是 case **表达式**。策略:**映射不到 block 就回退到
   所在函数的聚合覆盖**(函数里任何 block 跑过就算覆盖)。往安全方向保守——
   可能多跑几个,绝不静默丢掉。**绝不能用后一个 block 的计数**:守卫表达式即使
   分支没走到也会被求值。

---

## 六、并发、超时、确定性

**并发在 2 就饱和,再加就退化。** 因为 `go test` 自己已经是并行的(`-p` 默认
`GOMAXPROCS`,加上测试内的 `t.Parallel()`),叠更多 worker 只是拉高延迟并触发假杀死:

| workers | 总时长 | 平均延迟 |
| --- | --- | --- |
| 1 | 3.51 s | 293 ms |
| 2 | **1.92 s** | 308 ms |
| 4 | 1.95 s | 574 ms |
| 8 | 2.06 s | 1037 ms |

取 `clamp(NumCPU/4, 2, 4)`,子进程再带 `-p=2 -parallel=<n>` 限制内层扇出。

**每个变异体的超时必须推导,不能用固定默认值。** 实测:一个失控变异体在 120 s 固定
超时下吃掉了 354 s 总时长里的 **120.8 s(34%)**。取
`clamp(4 × baseline 包耗时, 5s, 60s)`,cobra 上是 5 s 而不是 120 s,整轮从
354 s 降到 ~238 s(−33%)且不丢任何信息——超时反正都算杀死。

**确定性来源逐个消除:**

| 来源 | 消除方式 |
| --- | --- |
| map 迭代顺序 | 输出路径上永不 range map。候选按 `(file, line, col, operator)` 排序 |
| worker 完成顺序 | 结果按派发下标写进预分配 slice,不从 goroutine 里 append |
| 变异体 ID | 内容哈希,不是序号——预算截断后仍然稳定 |
| 测试结果缓存 | 永远 `-count=1` |
| vet | 永远 `-vet=off`(同时也是正确性修复) |
| 超时边缘抖动 | 耗时在超时 80% 以内的变异体先顺序重跑一次再记 TIMED_OUT |
| 预算截断 | 确定性的**分层**派发顺序:先在各变更函数间轮转,再在函数内深入。 |
| 抖动测试 | 隔离 + 裁定(见坑 3) |
| 环境 | 子进程固定 `GOMAXPROCS`/`GOFLAGS=""`/`TZ=UTC`/`LC_ALL=C`,结果里记工具链版本 |

**baseline 校验四步:**
1. 未变异套件在**变异阶段将要用的同样并发和同样 flag 下**跑 N=3 次
2. 没有 3 次全过的测试 → 隔离名单
3. 3 次全挂的测试 → baseline 是红的,**中止本轴**并报
   `Available() = false, "baseline suite is red"`。对着红 baseline 打分会让每个
   变异体都像被杀死了
4. 裁定:任何 KILLED 变异体,若 `KilledBy ⊆ 隔离名单`,不算真杀,单独顺序重跑一次定夺

分层派发让**预算截断后的部分结果是无偏的**——每个变更函数都被采样到,而不是把
字母序第一个函数跑穿。

---

## 七、风险与回退

**风险 1:360 ms/变异体的地板碰上大 diff。** 实测变异体密度 **~7.5 行 1 个**
(`args.go` 140 行 → 18 个;`command.go` 2072 行 → 274 个)。500 行的 diff ≈ 65 个
≈ 35 s,可以;2000 行 ≈ 270 个 ≈ 240 s,不行。
*缓解*:覆盖率预筛(白省 13%)+ 类型门(12%)+ 硬上限 `MaxMutants` 配分层采样,
诚实报成部分结果。*回退*:**mutant schemata**——把一个包的所有变异体编译进同一个
二进制,用运行时开关 `METRON_MUTANT=<id>` 选择,编译+链接每包只付一次,把 360 ms
地板压到 ~136 ms。v1 不做:每个算子都要再写一个 schema 形式,复杂度跳跃太大,而
v1 实测数字对真实 diff 已经够用。

**风险 2:假杀死静默抬高分数。** 三个独立机制都实测到了(坑 1/2/3)。缓解就是那三个
修复。*额外保险*:`--paranoid` 模式,报告前把每个 KILLED 顺序重跑一遍,成本约 2×
执行阶段(7 个变异体的 diff 是 +3.7 s),便宜到可以在 CI 里当默认。

**风险 3:单包测试套件本身就慢的仓库。** 这里所有数字都来自一个全套件 173 ms 的包。
一个 30 s 套件的包(集成测试、容器、网络)会让每个变异体 ~30 s,本轴就没用了。
这是最可能的真实世界失败,cobra 把我惯坏了。
*缓解*:baseline 耗时本来就要测(推导超时用),按它分支。*回退,也是重点*:如果
projected cost 仍然超预算,**报 `mutation: unmeasured (套件太慢: 30s/包 × 41 个变异体 > 45s 预算)`
并把闸门标成 Unmeasured。绝不用 3 个变异体的采样去编一个数出来。** 化验单上一个空
读数是诚实的,一个编造的读数比没有工具更糟。这也是 `Status` 要有 `Unmeasured`
这个一等状态的原因。

**两个小风险**:cgo(overlay 对从 include path 之外引入的 cgo 文件有文档记载的限制,
用 `go list -json` 的 `CgoFiles` 检测并跳过、说明原因);生成的文件
(`//go:generate`、`.pb.go`)默认排除——变异生成代码量不出作者测试的任何信息。
