# metron

[English](README.md) · 简体中文

给一次代码改动出一张化验单:七项读数,每项对照参考区间,附带一个可以拿去做闸门的退出码。

**不用 LLM,不联网,不需要 API key。** 每个数字都来自解析你的代码和跑你的测试。同一个
commit 进去,同一组数字出来。

## 问题长什么样

```
$ metron --since main --axes all

  METRON  main · 1 files · 18+

  reading            value   reference
  ─────────────────────────────────────
  cognitive max         3   ≤ 15      ✓    Discount
  cognitive Δ           0   = 0       ✓
  redundant code        0   = 0       ✓
  inconsistent code     0   = 0       ✓
  mutation score      20%   ≥ 70%     L
    test strength     20%   ≥ 80%     L    12 of 15 mutants survived
    reach            100%   ≥ 85%     ✓    every mutant was executed

  2 out of range

  mutation
    pricing/pricing.go:9  no test caught this change to Discount (CONDITION_FORCE)
      - if total < 0 {
      + if true {
    pricing/pricing.go:9  no test caught this change to Discount (CONDITIONALS_BOUNDARY)
      - if total < 0 {
      + if total <= 0 {
    …
```

**这份代码的行覆盖率是 100%。** 变异得分 20。把测试换成一份真的会断言的,同样是 100%
覆盖率,得分变成 100。

两条缩进的读数说出了**是哪一种**问题:reach 是 100%,说明测试确实执行到了这些代码;
strength 只有 20%,说明它们执行了却什么都没检查。

## 安装和使用

```
go install github.com/yanmxa/metron/cmd/metron@latest

cd your-repo
metron --since main                 # complexity + graph,大约一秒
metron --since main --axes all      # 加上 mutation:会跑你的测试套件
```

需要 Go 1.26+ 和一个 git 仓库。graph 这条轴还需要一份
[CodeGraph](https://github.com/colbymchenry/codegraph) 索引,跑 `codegraph init` 就有了;
没有的话这条轴报 `n/a` 并说明原因,而不是默默地算通过。

退出码:`0` 全部在区间内 · `1` 出错 · `2` 有读数超出区间 ·
`3` 预算用尽,读数只覆盖了一部分。

## 各项读数

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
