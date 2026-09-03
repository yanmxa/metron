# metron

[English](README.md) · 简体中文

[![ci](https://github.com/yanmxa/metron/actions/workflows/ci.yml/badge.svg)](https://github.com/yanmxa/metron/actions/workflows/ci.yml)
[![go reference](https://pkg.go.dev/badge/github.com/yanmxa/metron.svg)](https://pkg.go.dev/github.com/yanmxa/metron)
[![go report card](https://goreportcard.com/badge/github.com/yanmxa/metron)](https://goreportcard.com/report/github.com/yanmxa/metron)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**用明确的指标衡量 AI 刚写出来的代码,并把每一处缺口变成一条它能照着做的指令。**

agent 写出来的代码能过评审,但撑不住。它写的测试跑遍每一行、什么都不断言;它往已有函数里
塞分支而不是抽出来;它找不到现成的工具函数就自己再写一个。这些在 diff 里看不出来,在覆盖率
里也看不出来。

metron 把它们量出来,然后说该怎么办——用 agent 能照着做、也能自己复验的话。

**不用 LLM,不联网,不需要 API key。** 每个数字都来自解析代码和跑它的测试。同一个 commit
进去,同一组数字出来——这正是它敢放进循环、也敢拿去做闸门的原因。

## 这个循环

agent 刚写完 `Discount` 和它的测试。测试的**行覆盖率是 100%**。

(下面是截取过的:只保留变动的读数,以及十二条发现中的一条。)

```
$ metron --since main --axes all

  reading            value   reference
  ─────────────────────────────────────
  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed

  2 out of range

  mutation
    pricing/pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
      assert the behaviour at the boundary total == 0
```

reach 是 100%:测试确实跑到了每一行。strength 是 20%:跑到了却几乎什么都没检查。而最后
那一行不是抱怨,是**一件事**——每个存活的变异体都带着它证明缺失的那条断言,由算子和它的
操作数推导出来。

agent 照着这些指令改完,再跑一次:

```
  mutation score     100%   ≥ 70%     ✓
    test strength    100%   ≥ 80%     ✓    0 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed

  all within range
```

退出码 0。**前后行覆盖率都是 100%**,只有 metron 能把这两者分开。

## 安装

```bash
# 直接装二进制,不需要 Go 环境
curl -fsSL https://raw.githubusercontent.com/yanmxa/metron/main/install.sh | sh

# 或者,如果你有 Go
go install github.com/yanmxa/metron/cmd/metron@latest
```

需要一个 git 仓库;只有从源码构建才需要 Go 1.26+。graph 这条轴还需要一份
[CodeGraph](https://github.com/colbymchenry/codegraph) 索引,跑 `codegraph init`
就有了;没有的话这条轴报 `n/a` 并说明原因,而不是默默地算通过。

## 使用

```bash
cd your-repo
metron init                         # 可选:按这个仓库现状校准参考区间
metron --since main                 # complexity + graph,大约一秒
metron --since main --axes all      # 加上 mutation:会跑你的测试套件
metron --all                        # 量整个仓库,而不是一次改动
```

`metron init` 会先量一遍,再写出一份 `metron.json`,把复杂度上限设成**这个仓库今天最差的
那个函数**,增量设成 0。已有的复杂度被容忍,但一点都不许再涨。一个第一次跑就报红、又找不到
哪次改动该负责的工具,会在说出任何有用的话之前就被关掉。

退出码:`0` 全部在区间内 · `1` 出错 · `2` 有读数超出区间 · `3` 预算用尽,读数只覆盖了一部分。

## 接进 agent

```
metron --since main --axes all --format json
```

三件事让它能放进循环里:

- **每条发现的 `detail` 是一条指令**,不是一个诊断——「assert the behaviour at the
  boundary `total == 0`」,而不是「覆盖率偏低」。
- **退出码就是停止条件。** `0` 完成 · `2` 还有读数超区间 · `3` 结果不完整,不能当通过 ·
  `1` 出错。
- **重跑很便宜。** 判定按内容哈希缓存,改一个函数不会重测其余:冷跑 8.3 秒,全命中 0.26 秒。

### 安装 skill

上面这套指令是一份文档:[`agent/metron.md`](agent/metron.md)。每个助手读指令的**路径和
格式都不一样**,所以安装脚本会把同一份内容写到你用的那个助手会去找的地方:

```bash
curl -fsSL https://raw.githubusercontent.com/yanmxa/metron/main/install.sh | sh -s -- --skill
```

| 助手 | 文件 |
| --- | --- |
| Claude Code | `.claude/skills/metron/SKILL.md` |
| Cursor | `.cursor/rules/metron.mdc` |
| Windsurf | `.windsurf/rules/metron.md` |
| GitHub Copilot | `.github/copilot-instructions.md` |
| Codex CLI、Amp 等 | `AGENTS.md` |

不带参数时,它会装到**仓库里已经存在**的那些助手上,一个都没有就退回 `AGENTS.md`。
`--agent all` 会全部写一遍。`AGENTS.md` 里 metron 的段落有标记包起来,重复运行是**替换**
而不是追加,你自己写的内容不会被动。

它给 agent 的规则里,最后一条比其余都重要:

> 绝不允许改 `metron.json` 的阈值、删测试、或者加 `//nolint` 来让读数通过。

**闸门只有在被量的一方挪不动它的时候才有意义。**

## 和其它工具比

| | metron | gocyclo / gocognit | go test -cover | gremlins |
| --- | --- | --- | --- | --- |
| 复杂度 | ✅ 认知复杂度**和**相对 base 的增量 | ✅ 只有绝对值 | — | — |
| 测试撑不撑得住 | ✅ 变异测试,按 diff 收敛 | — | ⚠️ 只有覆盖率 | ✅ 全仓 |
| 死代码 / 重复代码 | ✅ | — | — | — |
| 告诉你该写什么 | ✅ 每条发现都带 | — | — | — |
| 可断点续跑、有缓存 | ✅ | n/a | n/a | — |
| 一个闸门管全部 | ✅ | — | — | — |

最接近 metron 的做法是分别跑 gocognit、覆盖率和 gremlins,然后读三份报告。区别在于这些读数
是**合起来**的——CRAP 之所以存在,正是因为复杂度和变异得分被一起测量——而且每条发现都带着
能把它关掉的那个改动。

## 分析现有代码

`--all` 能回答的比 `--since` 严格地少。没有 base 版本,就没有「变糟了多少」,也判断不出
哪条依赖是新画的,那些读数会报 `n/a` 而不是猜一个。**绝不要把这种缺席当成通过。**

## 各项读数

每个指标的完整定义、作用、含义,以及可复现的样例:
**[docs/metrics.zh.md](docs/metrics.zh.md)**。

表格里有七项读数,其中五项参与闸门。每一项下面都跟着支撑它的具体发现。

### mutation —— 测试有没有把代码撑住?

变异体只在改动碰到的函数体内生成,每个都是一处刻意的改写。只要有测试挂掉或挂死,
这个变异体就算被**察觉**。

| 读数 | 怎么算的 | 超出区间意味着 |
| --- | --- | --- |
| **mutation score** | `察觉 / (察觉 + 存活 + 未覆盖)` | 这次改动没有被测试撑住。这一项是闸门。 |
| ↳ test strength | `察觉 / (察觉 + 存活)` | 测试跑到了这些代码,但断言得太少。 |
| ↳ reach | `1 − 未覆盖 / 总数` | 改动里有很大一部分没有任何测试执行到。 |

```
  mutation
    pricing/pricing.go:9  no test caught this change to Quote (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
```


每个存活的变异体都带着它证明缺失的那条断言,由算子和它的操作数推导出来:

```
  mutation
    pricing/pricing.go:9  no test caught this change to Quote (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
      assert the behaviour at the boundary total == 0
```

最后那行才是重点。`--format json` 里它是 `detail` 字段,所以一个拿 metron 做反馈循环的
agent 拿到的是一件具体、可验证的事,而不是一个还需要它自己解读的数字。这是**推导**出来的
不是生成出来的——没有任何模型参与,同一个 commit 永远给出同一条指令。

它的措辞永远是「该补哪条断言」,而不是「测试当前做了什么」。变异体存活分辨不出「这个输入
从没被传进来」和「传进来了但结果没人检查」;在后一种情况下说成前一种,会让人去写一个已经
存在的测试。

**没被覆盖的代码要算进分母。** agent 写的代码里最典型的失败形态,是写了 200 行、把其中
20 行测得很好。把未覆盖的变异体排除在分母之外,这种改动会拿到接近满分,而且可以直接刷分
——给一个小函数写一个漂亮测试就够了。*strength* 问的是「你写的那些测试够不够狠」,*score*
问的是「这次改动有没有被测试撑住」。只有后者值得设闸门。编译不过的变异体完全不算——那是
metron 自己的问题,不是你的,单独报。

### complexity —— 有多难读、多难改?

按 SonarSource 规范在 `go/ast` 上算认知复杂度:每个打断线性流程的结构记 1 分,再按它所处
的嵌套层级每层加 1 分。

**Go 的 err 卫语句要打折。** `if err != nil { return err }` 占 Go 标准库全部分支关键字的
7.7%,应用代码里更高。Go 读者把它当成一个 token 而不是一次分支;全额计入会让每个 Go 函数
都显得复杂,这个指标也就废了。只有**纯粹用来 bail out** 的才打折——带 `else` 的、或者真做
了错误处理的,是真分支。未打折的那个数保留在 JSON 里,和 gocognit 可比。

| 读数 | 怎么算的 | 超出区间意味着 |
| --- | --- | --- |
| **cognitive max** | 变更函数里打折后的最高分 | 有个改动过的函数很难读。输出里会点名。 |
| **cognitive Δ** | 现在的分数减去 merge base 上的分数,按名字+receiver 配对 | 你把一个已有函数改得更糟了,而不是把逻辑抽出来。 |

```
  complexity
    pricing/pricing.go:8  Quote (Δ +9, was 3)
      CRAP 54 (10% of mutants caught) — over the usual limit of 30 · cognitive 7 · cyclomatic 8
```

圈复杂度(cyclomatic)、扇出、参数个数、行数、嵌套深度对每个变更函数也都算了。它们出现在
每条发现里、也在 `--format json` 里,但不单独占一项读数。

### graph —— 它和已有的东西合不合?

从 CodeGraph 索引里读:仓库里的符号和它们之间的边。会跟 merge base 比对,**只有这次改动
新增的边**才算。

| 读数 | 怎么算的 | 超出区间意味着 |
| --- | --- | --- |
| **redundant code** | 无法到达的符号 + 近重复 | 有些代码本来不必以这种形式存在。 |
| **inconsistent code** | 绕过包装器 + 无先例的依赖方向 + 破坏本地惯例 | 有些代码和这个仓库不合。 |

```
  graph
    pricing/pricing.go:22  unusedHelper is never reached
      no inbound edge in the graph, and the identifier appears nowhere else in the source
```

这两项是**刻意做粗的**。原来五个独立的计数器在用五种说法讲同一件事,而你对它们的处理方式
完全一样:去看下面那条发现。单项的计数仍然在 `--format json` 里。

**没有加权总分。** 单个综合分会把「是哪一项烂了」藏起来,而且必然被刷。

## CRAP —— 先修哪一个?

```
CRAP(f) = cyclomatic(f)² × (1 − mutationScore(f))³ + cyclomatic(f)
```

[Change Risk Analysis and Predictions](https://www.artima.com/weblogs/viewpost.jsp?thread=210575),Alberto Savoia
2007 年提出,由 Crap4j 实现。复杂度在被测住时可以被原谅,没测住时就重罚:
圈复杂度 10 的函数,全测住是 10 分,完全没测是 110 分。惯例的红线是 30。

metron 在一个地方和原版不同,而且是关键的那个地方。Crap4j 吃的是**行覆盖率**——正是这个
工具存在的理由所要质疑的那个数。这里的「测住」一项换成了**该函数的变异得分**,所以一个
覆盖率 100% 却什么都不断言的函数,仍然是危险的,而不会被算成安全。那正是 CRAP 当初要抓的
情况,也正是基于覆盖率的版本会漏掉的。

它**不是第八项读数**。它是按函数算的,作用是排序而不是设闸门,所以它给 complexity 的发现
加注并排序——并且会把一个 complexity 轴放过去的函数提上来。上面那个例子里,圈复杂度 8 离
阈值 15 还很远,但只有 10% 的变异体被抓住,它仍然是这次改动里最危险的东西,而其它任何一项
读数都不会这么说。

CRAP 需要两条轴都跑过。不加 `--axes all` 的话,面板会**明说**而不是什么都不打印:

```
  all within range · risk ranking needs the mutation axis — add --axes all
```

没有变异体的函数不给分,而不是编一个出来。

## 值得知道的几个行为

**结果不完整时绝不让构建失败。** 采样不是总体,一个在证据不全时就报红的工具,会教会
大家忽略它。

**采样不到位就拒绝出分。** 如果预算只够评不到四分之一的变异体,这条轴会报 `n/a` 并把
算式写出来,而不是拿一小把样本编一个数。

**断点续跑是默认开的。** 判定按内容哈希、一边跑一边落盘到 `.metron/`,中断之后接着跑
而不是从头来:冷跑 8.3 秒,全命中缓存 0.26 秒。源码或测试只要动一个字节,整份缓存就作废
——一个过期的判定比没有缓存危险得多。`--fresh` 强制重测。

## 这些数字凭什么可信

认知复杂度在 `spf13/cobra` 全部 528 个函数上和
[gocognit](https://github.com/uudashr/gocognit) 对过:**523 个完全一致。** 剩下 5 个是
同一处刻意的分歧——SonarSource 规范规定 `else` 体要抬高嵌套层级,gocognit 没有抬。

图规则是拿一条标准收敛出来的:对 `cobra` 做六次 no-op 改动——只给一个没动过的函数加一行
注释——必须报出**零**项发现。做到零用了三次修正,每一次都来自真实的误报:

- Go 里把函数当值传太常见了(`return defaultUsageFunc`),而索引不会为此记录调用边,
  所以孤儿判定要叠加一层真实标识符使用扫描。
- 包装器不等于「调用者多的那个」,它的目标必须是被**漏斗式**收拢的。
- 只有这次改动**真正新增**的边才算。不跟 merge base 比对的话,一个只是被碰过的函数
  一直以来做的每一次调用都会被报出来。

变异这条轴有三个坑,而且全都**朝着分数虚高的方向**失败,所以每一个都配了回归测试:
构建失败在 JSON 流里和测试失败长得一模一样;vet 默认会跑,而 vet 失败看起来又像构建
失败;并发会唤醒抖动测试,它们随后会被读成「杀死」。

[docs/mutation-design.md](docs/mutation-design.md)(英文)记录了这些决定背后的全部实测
数据。

## 开发

```
go test ./...
go run ./cmd/metron --since HEAD~1 --axes all
```
