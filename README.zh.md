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

## 七项读数

| 读数 | 超出区间意味着 |
| --- | --- |
| **mutation score** | 这次改动没有被测试撑住。这一项是闸门。 |
| ↳ test strength | 测试跑到了这些代码,但断言得太少。 |
| ↳ reach | 改动里有很大一部分没有任何测试执行到。 |
| **cognitive max** | 有个改动过的函数很难读。输出里会点名。 |
| **cognitive Δ** | 你把一个已有函数改得更糟了,而不是把逻辑抽出来。 |
| **redundant code** | 有些代码本来不必以这种形式存在:没人能到达,或者重复了已有的东西。 |
| **inconsistent code** | 有些代码和这个仓库不合:绕过了包装器、画了一条别处没有的依赖、或者破坏了邻居们都遵守的惯例。 |

每一项读数后面都跟着具体的发现——一个存活的变异体会附上没有测试察觉的那处 diff,
一个孤立符号会给出文件和行号。

最后两项是**刻意做粗的**。原来五个独立的图计数器在用五种说法讲同一件事,而你对它们的
处理方式完全一样:去看下面那条发现。单项的计数仍然在 `--format json` 里,连同未折抵的
认知复杂度和 metron 自身的生成器健康度。

**没有加权总分。** 单个综合分会把「是哪一项烂了」藏起来,而且必然被刷。

## 两个有主张的定义

```
                   KILLED + TIMED_OUT
  mutation score = ─────────────────────────────────────────
                   KILLED + TIMED_OUT + SURVIVED + NOT_COVERED
```

**没被覆盖的代码要算在你头上。** agent 写的代码里最典型的失败形态,是写了 200 行、把
其中 20 行测得很好。把未覆盖的变异体排除在分母之外,这种改动会拿到接近满分,而且可以
直接刷分——给一个小函数写一个漂亮测试就够了。*strength* 问的是「你写的那些测试够不够
狠」,*score* 问的是「这次改动有没有被测试撑住」。只有后者值得设闸门。(编译不过的变异
体不算进去——那是 metron 自己的问题,不是你的,单独报。)

**Go 的 err 卫语句要打折。** `if err != nil { return err }` 占 Go 标准库全部分支关键字
的 7.7%,应用代码里更高。Go 读者把它当成一个 token 而不是一次分支;全额计入会让每个 Go
函数都显得复杂,这个指标也就废了。只有**纯粹用来 bail out** 的才打折——带 `else` 的、
或者真做了错误处理的,是真分支。未打折的那个数仍然保留在 JSON 里,和 gocognit 可比。

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
