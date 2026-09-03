# metron

[English](README.md) · 简体中文

[![ci](https://github.com/yanmxa/metron/actions/workflows/ci.yml/badge.svg)](https://github.com/yanmxa/metron/actions/workflows/ci.yml)
[![go reference](https://pkg.go.dev/badge/github.com/yanmxa/metron.svg)](https://pkg.go.dev/github.com/yanmxa/metron)
[![go report card](https://goreportcard.com/badge/github.com/yanmxa/metron)](https://goreportcard.com/report/github.com/yanmxa/metron)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

现在我们的代码有很大一部分是 AI 写的。为了让它规矩一点,我们用测试驱动开发:先说清楚
代码该做什么,再让它做到。

但 TDD 有一个它自己检查不了的前提——**你拿到的那些测试,到底值不值得拥有**。有三件事会
出问题,而且在 diff 里一件都看不出来。

---

## 一、这些测试到底有没有用?

你让 agent 写测试,它就会写出测试。测试能跑、能过,覆盖率报告一片绿。但那份报告回答的是
「这一行有没有被执行」,而不是你真正想问的那个问题。

下面是 agent 写的一个函数,和它给这个函数写的测试:

```go
func Discount(total int, tier string) (int, error) {
	if total < 0 {
		return 0, ErrNegative
	}
	if tier == "gold" {
		return total * 80 / 100, nil
	}
	if total > 100 {
		return total - 10, nil
	}
	return total, nil
}
```

```go
func TestDiscount(t *testing.T) {
	for _, tc := range []struct{ total int; tier string }{
		{200, "gold"}, {200, "std"}, {50, "std"}, {-1, "std"},
	} {
		got, err := Discount(tc.total, tc.tier)
		if tc.total < 0 { if err == nil { t.Fatal("want error") }; continue }
		if got < 0 { t.Fatalf("negative result %d", got) }
	}
}
```

**行覆盖率:100%。** 每条分支都跑到了。而这个测试几乎什么都没断言——把
`total * 80 / 100` 改成 `total * 90 / 100`,它照样全绿。

### 变异测试回答覆盖率答不了的那个问题

**故意把代码改坏,看测试察不察觉。** metron 会把改动过的代码做一些小而刻意的改写——
翻转一个比较、丢掉一个返回的错误、把分支强行定死——然后拿测试套件去跑每一个改写。
一个没人察觉的改写,就是一个缺口。

```
  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed
```

两条缩进的读数说出了**是哪一种**问题。**reach 是 100%**:测试确实跑到了每一行。
**strength 只有 20%**:跑到了却什么都不检查。这是两种不同的病、两种不同的修法,
而单一个数字会把你到底得的是哪一种藏起来。

把测试改成断言确切的值,得分变成 **100**——**行覆盖率还是同样的 100%**。
覆盖率分不出这两份测试,这个能。

### 而且它会告诉你该写什么

一个存活的变异体不是一句抱怨,而是**那条缺失的测试的规格说明**,而且具体是哪一条,可以
从算子机械地推导出来:

```
  pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
    - if total < 0 {
    + if total <= 0 {
    assert the behaviour at the boundary total == 0
```

最后那一行是**推导**出来的,不是生成的——没有模型参与,同一个 commit 进去,同一条指令
出来。这正是它能被 agent 直接使用、而不只是给人看的原因。

---

## 二、半年后还有人改得动它吗?

把代码写出来是最短的那一段。它之后会被读、被扩展、被调试很久很久,而一个只想着「让测试
过」的 agent,对这些一点利害关系都没有。

### 用认知复杂度,不用圈复杂度

常用的那个指标数的是判定点。它分不出「三个并排的判定」和「三个套在一起的判定」——
而这两者读起来的难度完全不是一回事:

```go
func Flat(a, b, c bool) int {          func Nested(a, b, c bool) int {
	n := 0                                     n := 0
	if a { n++ }                               if a {
	if b { n++ }                                       if b {
	if c { n++ }                                               if c { n++ }
	return n                                           }
}                                              }
                                               return n
                                       }
```

```
  Flat     cognitive=3   cyclomatic=4
  Nested   cognitive=6   cyclomatic=4
```

**圈复杂度完全一样。** 认知复杂度翻倍,因为它对**每一层嵌套**都要额外记账——而嵌套正是
让代码难以在脑子里装下的那个东西。

### 增量比绝对值更要紧

agent 很少一次写出一个巨兽函数。它往一个已有函数里加一个分支,再加一个,每一次单看都还
算合理。下面是对 `spf13/cobra` 的一次真实改动:

```
  cognitive max         12   ≤ 15      ✓    RangeArgs
  cognitive Δ           +9   = 0       H    RangeArgs
```

**绝对值是过关的。** 12 离上限 15 还很宽裕。只有增量抓住了它——这个函数在一次改动里从
3 涨到了 12。把 `Δ = 0` 设成闸门,代码库就没法悄悄腐化。

### CRAP:验证不了的复杂度才是危险的复杂度

复杂度本身不等于风险。一个很绕但每条分支都被测试钉死的函数没问题;一个中等复杂、却没人
检查的函数,才是改动会悄无声息弄坏东西的地方。
[CRAP](https://www.artima.com/weblogs/viewpost.jsp?thread=210575) 把两者合起来:

```
CRAP(f) = cyclomatic(f)² × (1 − tested(f))³ + cyclomatic(f)
```

Crap4j 原版的 `tested` 用的是**行覆盖率**。metron 换成了这个函数自己的**变异得分**——
因为我们刚刚才确认过,覆盖率正是那个不能信的数。

```
  cognitive max       6   ≤ 15      ✓          ← 复杂度说这个函数没问题

  complexity
    risky.go:4  Route is the riskiest thing in this change
      CRAP 42 (0% of mutants caught) — over the usual limit of 30 · cyclomatic 6
```

任何**单独一项**读数都不会点名 `Route`。复杂度过关;变异得分只是一个关于整个包的数字。
只有两者合起来,才说得出那句:**先看这里。**

---

## 三、它和已经有的东西合得上吗?

最让我意外的失败形态不是「写错了」,而是**写得完全正确、但根本不该存在**的代码——
一个因为找不到现成的所以重写了一遍的工具函数、一个被绕过去的包装器、一条这个仓库里
从来没人画过的依赖方向。

**再多的测试也抓不到这类问题。代码是对的。** 变糟的是仓库的**形状**,而形状这个东西,
站在一个 diff 内部是看不见的。

### 用整个仓库的图

metron 会读一份 [CodeGraph](https://github.com/colbymchenry/codegraph) 索引——仓库里
所有符号和它们之间的所有边——然后拿这次改动跟它对照。

**冗余代码** —— 本来不必存在的东西:

```
  redundant code        1   = 0       H    1 unreachable

  graph
    dead.go:8  orphan is never reached
      no inbound edge in the graph, and the identifier appears nowhere else
```

**不一致代码** —— 和已有结构不合的东西:

- 别人都经过包装器,它直接调目标
- 画了一条这个仓库没有先例的依赖方向
- 同目录每个邻居都遵守的惯例,它没遵守

**只有这次改动新增的边才算。** 不做这个比对的话,一个只是被碰过的函数一直以来做过的每一次
调用都会被报出来——在 `cobra` 上实测,六次 no-op 改动在修复前产生了五条发现,修复后是零。

---

## 合起来看

三个问题、七项读数、一个退出码:

| | 问什么 | 读数 |
| --- | --- | --- |
| **mutation** | 测试撑不撑得住? | `score`、`strength`、`reach` |
| **complexity** | 以后还改得动吗? | `cognitive max`、`cognitive Δ` |
| **graph** | 和已有的合得上吗? | `redundant`、`inconsistent` |

外加 CRAP,它负责排序而不参与闸门。

**没有加权总分。** 单个综合分会把「是哪一项烂了」藏起来,而且必然被刷。

**不用 LLM,不联网,不需要 API key。** 每个数字都来自解析代码和跑它的测试。同一个 commit
进去,同一组数字出来——这正是它敢放进循环、也敢拿去做闸门的原因。

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

## 所有读数一览

| 读数 | 超出区间意味着 |
| --- | --- |
| **mutation score** | 这次改动没有被测试撑住。这一项是闸门。 |
| ↳ test strength | 测试跑到了这些代码,但断言得太少。 |
| ↳ reach | 改动里有很大一部分没有任何测试执行到。 |
| **cognitive max** | 有个改动过的函数很难读。输出里会点名。 |
| **cognitive Δ** | 你把一个已有函数改得更糟了,而不是把逻辑抽出来。 |
| **redundant code** | 有东西够不着,或者重复了已经存在的东西。 |
| **inconsistent code** | 有东西绕过了包装器、画了没有先例的依赖、或破坏了本地惯例。 |
| CRAP *(按函数)* | 用「测得有多差」给复杂度加权。惯例红线 30。只排序,不参与闸门。 |

粗体的默认参与闸门,可以用 `--fail-on` 改。每条发现都会附上能把它关掉的那个改动。

**完整定义、精确公式,以及每个指标一个可复现的样例:
[docs/metrics.zh.md](docs/metrics.zh.md)。**

不占表格行的数字——圈复杂度、扇出、参数个数、嵌套深度、各条图规则的单项计数、变异体原始
计数——全都在 `--format json` 的 `diagnostics` 里。

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
