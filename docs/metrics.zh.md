# 每个指标是什么意思

[English](metrics.md) · 简体中文

这里每一项读数都是确定性的:同一个 commit 进去,同一个数出来。每一节都会说清楚这个指标
**是什么、怎么算、干什么用**,并给一个可复现的样例。

样例都是很小的 Go 程序,你可以自己跑一遍。

---

## mutation score(变异得分)

**闸门。** 测试到底有没有把代码撑住。

```
(KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)
```
参考区间:**≥ 70%**

metron 会把代码做一些小而刻意的改写——翻转一个比较、丢掉一个返回的错误、把分支强行定死
——然后跑测试。只要有测试挂掉或挂死,这个变异体就算被**察觉**。得分就是被察觉的比例。

### 它为什么存在

行覆盖率只能说明某一行被执行过,说明不了任何东西**因为它**而被检查。一个调了函数、只确认
没 panic 的测试,能拿到 100% 覆盖率,却什么都没钉住。

### 样例

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

一个跑遍每条分支、但只断言 `got >= 0` 的测试:

```
coverage: 100.0% of statements

  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed
```

把测试改成在边界上断言确切的值,**行覆盖率同样是 100%**:

```
  mutation score     100%   ≥ 70%     ✓
    test strength    100%   ≥ 80%     ✓    0 of 15 mutants survived
```

### 能拿它干什么

放进 CI 当闸门。它是唯一一项「数字低就一定真有问题」的读数,而且修法永远是具体的:
metron 会把每个存活变异体所证明缺失的那条断言打印出来。

---

## test strength / reach(测试锐度 / 触及率)

**拆解。** 你遇到的到底是**哪一种**测试问题。

```
strength = (KILLED + TIMED_OUT) / (KILLED + TIMED_OUT + SURVIVED)      ≥ 80%
reach    = 1 − NOT_COVERED / (KILLED + TIMED_OUT + SURVIVED + NOT_COVERED)  ≥ 85%
```

strength 把未覆盖的变异体从分母里去掉,所以它只衡量测试**真正跑到**的那部分代码。
reach 衡量的是它们到底跑到了多少。

### 它为什么存在

变异得分低有两种完全不同的成因,修法也完全不同,而单看得分分辨不出来。

| | reach | strength | 问题是 | 该做什么 |
|---|---|---|---|---|
| 没测到 | **低** | 高 | 测试根本到不了 | 加用例 |
| 测到了没断言 | 高 | **低** | 跑到了但什么都不检查 | 加断言 |

### 样例

```
  mutation score     0%   ≥ 70%     L
    test strength    0%   ≥ 80%     L    13 of 13 mutants survived
    reach           38%   ≥ 85%     L    21 never executed
```

两个都低:这个包大部分根本没被执行,**而且**执行到的部分也没人检查。应该先修 reach——
给没人调用的代码加断言是白费力气。

---

## cognitive complexity(认知复杂度)

**一个函数有多难读。**

按 SonarSource 规范在 `go/ast` 上算:每个打断线性流程的结构记 1 分,再按它所处的嵌套层级
每层加 1 分。参考区间:变更函数里最差的那个 **≤ 15**。

### 精确规则

计分由两部分相加:每个打断线性流程的结构收一份**固定分**,再按它**所处的嵌套层数**收一份
**嵌套附加分**。

| 结构 | 固定分 | 收嵌套附加分 | 让自己的 body 深一层 |
| --- | --- | --- | --- |
| `if` | +1 | 是 | 是 |
| `else if` | +1 | **否** | 是 |
| `else` | +1 | **否** | 是 |
| `for`、`range` | +1 | 是 | 是 |
| `switch`、type switch | +1 | 是 | 是 |
| `select` | +1 | 是 | 是 |
| 一段连续的 `&&` 或 `\|\|` | 每段 +1 | 否 | 否 |
| **带标签**的 `break`/`continue` | +1 | 否 | — |
| 直接递归 | +1 | 否 | — |
| 函数字面量(闭包) | 0 | — | **是** |
| 普通 `break`/`continue`、`case`、`defer`、`go` | 0 | — | — |

`else` 和 `else if` 只收固定分,因为读者**已经身处这个条件里面**了;再收一次嵌套附加分,
等于为同一个判断收两遍钱。

**一段**指的是连续的同种运算符:`a && b && c` 是一个意思,记 1 分;而 `a && b || c` 中途
换了模式,记 2 分。

函数字面量自身不计分——写一个闭包不是一次判断——但它的 body 要深读一层,所以里面的东西
都更贵。

### 手算一遍

```go
func f(a, b int, xs []int) int {      // 累计
	if a > b && a < 10 {              // if +1+0, && 一段 +1       = 2
		for _, x := range xs {        // for +1+1                  = 4
			if x > 0 {                // if +1+2                   = 7
				return x
			}
		}
	} else {                          // else +1(平)             = 8
		return 0
	}
	return -1
}
```

认知复杂度 **8**。同一个函数的圈复杂度是 5。

### 一处刻意的偏离

规范规定 `else` 体要抬高嵌套层级,[gocognit](https://github.com/uudashr/gocognit)
没有抬。metron 跟规范走,因为 `else` 里的代码对读者确实深一层。

在 `spf13/cobra` 全部 528 个函数上和 gocognit 对过:**523 个完全一致**,不一致的 5 个
全部来自这一条规则。

### 它为什么存在,以及为什么不用圈复杂度

圈复杂度数的是判定点。它分不出「三个并排的判定」和「三个嵌套的判定」,而这两者读起来的
难度完全不同。

### 样例

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

**圈复杂度完全一样。** 认知复杂度翻倍,而这符合读这两段代码的实际感受。

### Go 的 err 卫语句

`if err != nil { return err }` 占 Go 标准库全部分支关键字的 7.7%,应用代码里更高。Go 读者
把它当成一个 token,不是一次分支。全额计入会让每个 Go 函数都显得复杂,这个指标就失去了
区分度,所以 metron 给它打折。

**这个折扣的口子是刻意开得很窄的。五个条件必须全部成立**,否则这个 `if` 就是一次真分支,
全价计入:

1. 条件形如 `X != nil`,且 `nil` 是字面量
2. `X` 是一个普通标识符,名字是 `err` 或 `e`,或者以 `Err`、`Error` 结尾
3. 没有 `else`
4. body 里**恰好一条**语句
5. 那条语句是 `return`,或者 `break`/`continue`

所以 `if err != nil { return err }` 会被打折,而下面这些都不会:

```go
if err != nil { log.Print(err); return err }   // 两条语句:做了真正的处理
if err != nil { return err } else { … }        // 带 else
if problem != nil { return problem }           // 名字不符合错误变量的惯例
if err == nil { … }                            // 不是卫语句
```

第 2 条是**基于命名的启发式**,也是最容易让人意外的一条:错误变量取了别的名字就要全价。
未打折的那个数是你的申诉渠道——它在 `--format json` 里是
`complexity.cognitive_raw_max`,不打任何折,和 gocognit 可比。

### 能拿它干什么

找出该抽哪个函数。输出会点名,而且每条发现都带着它的圈复杂度、行数、扇出、参数个数和嵌套
深度,让你能看出它**为什么**是这个分。

---

## cognitive Δ(认知复杂度增量)

**一个函数有没有变糟。**

```
现在的分数 − merge base 上的分数       (按 名字 + receiver 配对)
```
只算改动前就存在的函数。参考区间:**= 0**。

### 它为什么存在

这一项精确对准 agent 实际败坏代码库的方式。它们很少一次写出一个巨兽函数,而是往已有函数
里加一个分支、再加一个,每一次单看都还算合理。

### 样例

一次改动往 `spf13/cobra` 的 `RangeArgs` 里塞了一层嵌套守卫:

```
  cognitive max         12   ≤ 15      ✓    RangeArgs
  cognitive Δ           +9   = 0       H    RangeArgs
```

**绝对值是过关的。** 12 离上限 15 还很宽裕。只有增量抓住了它——这个函数在一次改动里从 3
涨到了 12。

### 能拿它干什么

把 `Δ = 0` 设成闸门,代码库就不会悄悄腐化。注意它需要一个 base 版本,所以 `--all` 会把它
报成未测,而不是报成 0。

---

## redundant code(冗余代码)

**本来不必以这种形式存在的代码。**

三条规则求和。参考区间:**= 0**。

| 规则 | 什么时候触发 |
|---|---|
| `orphan` 孤立符号 | 没有任何 calls/references/instantiates 入边,**并且**源码里找不到这个标识符的其它出现,**并且**不是约定入口(`main`、`init`、`Test*`、接口方法),**并且**未导出、或者在 `main`/`internal` 包里 |
| `near-duplicate` 近重复 | 出边集合 Jaccard ≥ 0.6 **并且** 名字分词重叠 ≥ 0.3 **并且** 不同文件 **并且** 双方出边都 ≥ 3 |
| `reimplementation` 重复造轮子 | 签名完全相同 **并且** 名字重叠 ≥ 0.5 **并且** 不同文件 **并且** 从不调用原来那个 |

### 条件为什么这么具体

每一条都是在真实误报之后加上去的。Go 里把函数当值传太常见了(`return defaultUsageFunc`),
而索引不会为此记录调用边,所以孤儿规则要叠加一层真实标识符使用扫描。只看结构相似度会把
`complete_text` 和 `complete_json` 报出来——那是同族变体,不是重复——所以名字重叠也必须
同时成立。

### 样例

```go
func Used() int { return helper() }
func helper() int { return 1 }

// orphan is never called and never referenced.
func orphan(xs []int) int { ... }
```

```
  redundant code        1   = 0       H    1 unreachable

  graph
    dead/dead.go:8  orphan is never reached
      no inbound edge in the graph, and the identifier appears nowhere else in the source
```

### 能拿它干什么

删掉够不着的。重复的那类,发现里会给出两边的位置,你自己挑哪个留下。

---

## inconsistent code(不一致代码)

**和已有的东西不合的代码。**

三条规则求和。参考区间:**= 0**。需要 base 版本——**只有这次改动引入的边才算**。

| 规则 | 什么时候触发 |
|---|---|
| `bypassed-wrapper` | 某个目标被一个包装器收拢(包装器 ≥ 3 个调用者、≤ 4 个被调、同目录,且目标的其它直接调用者 ≤ 1),而新代码直接调用了目标 |
| `layer-crossing` | 新增边的(源目录 → 目标目录)方向在仓库里没有先例 |
| `sibling-divergence` | 同目录 ≥ 5 个兄弟函数里 ≥ 80% 遵守某个惯例(首参 `context.Context`、返回 `error`),而新函数没有 |

### 「只算新增的边」为什么关键

不跟 base 比对的话,一个只是被碰过的函数一直以来做过的每一次调用都会被报出来。在
`spf13/cobra` 上实测:六次 no-op 改动——只给一个没动过的函数加一行注释——在修复前产生了
五条发现,修复后是零。

### 能拿它干什么

在 agent 绕过代码库已经建立的抽象**成为新的先例之前**抓住它。

---

## CRAP

**先修哪一个。** 按函数算,不是一项读数,不参与闸门。

```
CRAP(f) = cyclomatic(f)² × (1 − mutationScore(f))³ + cyclomatic(f)
```

其中 `mutationScore(f)` 是**这个函数自己的**变异得分。惯例红线:**30**。由 Alberto Savoia 在 2007 年提出,用于 Crap4j。

复杂度在被测住时可以被原谅,没测住时就重罚。圈复杂度 10 的函数,全测住是 10 分,完全没测
是 110 分。

### metron 和原版哪里不同

Crap4j 吃的是**行覆盖率**——正是这整个工具存在的理由所要质疑的那个数。metron 换成了
**该函数的变异得分**,所以一个覆盖率 100% 却什么都不断言的函数依然是危险的,而不会被算成
安全。那正是 CRAP 当初要抓的情况,也正是基于覆盖率的版本会漏掉的。

### 样例

```go
func Route(method, path string, admin bool) string {
	if method == "GET" {
		if path == "/health" { return "health" }
		return "read"
	}
	if admin && method == "DELETE" { return "purge" }
	if method == "POST" { return "write" }
	return "deny"
}
```

配一个跑遍所有组合、只断言 `!= ""` 的测试:

```
  cognitive max       6   ≤ 15      ✓    Nested
  mutation score     0%   ≥ 70%     L

  complexity
    risky/risky.go:4  Route is the riskiest thing in this change
      CRAP 42 (0% of mutants caught) — over the usual limit of 30 · cognitive 6 · cyclomatic 6
```

**复杂度是过关的。** 6 离上限 15 差得远,complexity 轴根本不会提到 `Route`。只有和变异
得分合起来看,它才成为这次改动里最危险的东西——CRAP 正是为此把它提进报告的。

### 能拿它干什么

当作工作顺序。CRAP 最高的地方,是一次改动最可能悄无声息地弄坏东西的地方:最难推理,又最
不可能被抓住。

没有变异体的函数**不给分**,而不是编一个。不跑 mutation 轴时,面板会说
`risk ranking needs the mutation axis`,而不是什么都不打印。

---

## 诊断字段

不占表格行、但会进 `--format json` 的数字:

| key | 是什么 |
|---|---|
| `complexity.cyclomatic_max` | 经典判定点计数,和 gocyclo 可比 |
| `complexity.cognitive_raw_max` | 折抵 err 卫语句之前的认知复杂度 |
| `complexity.crap_max` | 本次运行里最差的 CRAP |
| `complexity.fan_out_max` | 单个函数最多调用了多少个不同的东西 |
| `complexity.params_max` / `lines_max` / `nesting_max` | 接口宽度、长度、深度 |
| `complexity.over_threshold` / `functions` | 计数 |
| `graph.orphans` / `duplicates` / `bypassed` / `layer_crossings` / `sibling_divergence` | 合并成两项读数之前的各条规则计数 |
| `graph.changed_symbols` | 有多少符号在范围内 |
| `mutation.killed` / `survived` / `timed_out` / `not_covered` / `not_viable` / `skipped` | 原始计数 |
| `mutation.not_viable_rate` | 关于 metron 自己生成器的诊断,不是关于你的代码——超过 15% 说明它的算子门做得不够好 |

每条轴还会输出 `funcs`:按函数的记录,带 path、函数名、行号、圈复杂度、认知复杂度、增量,
以及 mutants/detected。这就是 CRAP 能跨两条轴计算的原因。

---

## 变异算子

十个算子,每一个在其变异体存活时都会给出一条具体的指令。

| 算子 | 改写什么 | 存活时说什么 |
|---|---|---|
| `CONDITIONALS_BOUNDARY` | `<`↔`<=`、`>`↔`>=` | assert the behaviour at the boundary `X == Y` |
| `CONDITIONALS_NEGATION` | 比较运算符取反 | assert behaviour that changes when `expr` is negated |
| `INVERT_LOGICAL` | `&&`↔`\|\|` | assert a case where exactly one side of `expr` holds |
| `ARITHMETIC_BASE` | `+`↔`-`、`*`↔`/` | assert the value `expr` computes |
| `INVERT_ASSIGNMENTS` | `+=`↔`-=`、`*=`↔`/=` | assert the value `x` computes |
| `INCREMENT_DECREMENT` | `++`↔`--` | assert the value of `x` after this runs |
| `INVERT_LOOP_CTRL` | `break`↔`continue` | assert behaviour with further iterations |
| `NIL_ERROR_RETURN` | `return err` → `return nil` | assert that this path returns a non-nil error |
| `REMOVE_STATEMENT` | 删掉一条调用 | assert the effect of `f()` |
| `CONDITION_FORCE` | 条件 → `true`/`false` | assert the behaviour that depends on `cond` being true/false |

`NIL_ERROR_RETURN` 是 Go 特化的,gremlins 和 go-mutesting 都没有。它在 agent 写的代码上
命中率最高——agent 大量生成错误传递代码,而几乎从不测它。

指令的措辞**永远是「该补哪条断言」**,而不是「测试当前做了什么」。变异体存活分辨不出
「这个输入从没被传进来」和「传进来了但结果没人检查」;在后一种情况下说成前一种,会让人
去写一个已经存在的测试。

---

## 变异体判定

| 判定 | 含义 | 进不进得分的分母? |
|---|---|---|
| `KILLED` | 有测试挂了 | 进,算察觉 |
| `TIMED_OUT` | 变异让套件挂死 | 进,算察觉——测试**确实**注意到了 |
| `SURVIVED` | 测试全绿 | 进,算未察觉 |
| `NOT_COVERED` | 被覆盖率预筛判定不可达,没执行 | **进**——理由见变异得分那一节 |
| `NOT_VIABLE` | 编译不过 | 不进——这是 metron 的问题,不是你的 |
| `SKIPPED` | 预算先用完了 | 不进 |
| `ERRORED` | 工具本身出错 | 不进,而且永远不算成察觉 |

这些在 `go test -json` 流里怎么区分,以及三个**全都朝分数虚高方向失败**的坑,见
[mutation-design.md](mutation-design.md)(英文)。
