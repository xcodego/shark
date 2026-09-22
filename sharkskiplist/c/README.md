# C 版跳表（SkipList）

本目录是对仓库 `sharkskiplist` 包（Go 泛型跳表）的一个 C 语言实现，并附带对齐的基准测试，
用于对比 C 与 Go 两种实现的核心操作性能。

## 与 Go 版的一致性

C 实现与 `sharkskiplist/sharkskiplist.go` 保持算法一致，确保对比公平：

| 维度 | Go 版 | C 版 |
|---|---|---|
| 最大层数 | `maxLevel = 32` | `SKIPLIST_MAX_LEVEL = 32` |
| 晋升概率 | `p = 1/4` | `(rng & 0x3) == 0`（1/4） |
| 存储方向 | 原生方向可选（`NewAsc`/`NewDesc`） | 原生方向可选（`new_asc`/`new_desc`） |
| 搜索算法 | `findPredecessors` 从高层向下 | `find_predecessors` 同构 |
| key/value | `int`（64 位平台 64 位） | `int64_t` |
| 线程安全 | 无锁（单线程） | 无锁（单线程） |

构造：`new_asc`（升序）/ `new_desc`（降序）/ `new`（`new_asc` 别名）。
核心 API：`set_or_update` / `set_if_not_exists` / `get` / `delete` / `contains` /
`len` / `clear` / `min` / `max` / `ceiling` / `floor` / `range_asc` / `range_desc` /
`range_between`。

## 文件结构

| 文件 | 说明 |
|---|---|
| `skiplist.h` | 跳表实现（header-only 单头文件，含全部 API） |
| `bench.c` | 性能基准测试（对齐 `test/sharkskiplist_bench_test.go`） |
| `test.c` | 正确性测试 |
| `Makefile` | 编译脚本 |

## 编译运行

```sh
cd sharkskiplist/c
make          # 编译基准测试
./bench       # 运行基准测试
make test     # 编译并运行正确性测试
make clean    # 清理产物
```

## 性能对比

测试环境：macOS / Apple M5 (arm64)，Go 1.26，C 编译器 Apple clang 21.0.0（`-O2`）。
操作与数据规模与 `test/sharkskiplist_bench_test.go` 完全对齐（查找/遍历类预置 100000
个连续 key，写类随机 key 空间 1<<20）。

| 操作 | Go (ns/op) | C (ns/op) | C 加速比 |
|---|---:|---:|---:|
| SetOrUpdate（随机） | 734.9 | 322.2 | 2.3× |
| SetOrUpdateSeq（顺序） | 138.2 | 62.8 | 2.2× |
| SetIfNotExists | 726.8 | 301.2 | 2.4× |
| Get | 192.1 | 101.9 | 1.9× |
| GetMiss | 67.7 | 13.7 | 4.9× |
| Contains | 190.5 | 101.7 | 1.9× |
| Ceiling | 187.5 | 113.8 | 1.6× |
| Floor | 198.1 | 101.5 | 2.0× |
| Min | 1.7 | 0.2 | 7.2× |
| Max | 35.7 | 13.7 | 2.6× |
| Delete | 37.5 | 29.7 | 1.3× |
| RangeAsc | 223237 | 95125 | 2.3× |
| RangeDesc | 395953 | 151433 | 2.6× |
| RangeBetween | 17450 | 9647 | 1.8× |

> 说明：加速比 = Go 耗时 / C 耗时。C 版普遍快约 1.2～7 倍；header-only 内联使
> `Min` 等 O(1) 小操作被进一步优化（约 0.2 ns/op），数据存在运行波动。

## 100 万级 + GC 压力测试

为贴近真实负载，新增了百万级数据规模、并制造大量临时数据（垃圾）的基准，以评估
Go GC 的影响。测试仍与 C 版逐一对齐（相同的操作与数据规模）。

| 操作 | Go (ns/op) | C (ns/op) | C 加速比 |
|---|---:|---:|---:|
| MillionSeq（顺序写百万级） | 132.1 | 62.8 | 2.1× |
| MillionRand（100 万空间随机写） | 722.8 | 193.7 | 3.7× |
| MillionGet（预置 100 万后查找） | 435.6 | 226.2 | 1.9× |
| MillionRangeAsc（遍历 100 万） | 2 333 966 | 1 157 484 | 2.0× |
| MillionGarbage（制造 64B 临时数据） | 113.0 | 121.0 | 0.93× |
| MillionGarbageNoGC（同上，关闭 GC） | 108.1 | — | — |

### 关于 Go GC 的影响

`MillionGarbage` 在 100 万节点规模下，通过覆盖 `[]byte` value 制造了大量临时数据
（64B × 千万次 ≈ 640MB 垃圾）。对比 GC 开启（113.0 ns/op）与关闭（108.1 ns/op），
二者仅差约 4.5%，说明：

1. 现代 Go GC 为并发、低停顿，对小对象的大量分配/回收开销很小，不会明显拖慢
   单 goroutine 的写入吞吐。
2. 小对象分配走 per-P 缓存，`make([]byte, 64)` 的成本反而低于 C 的 `malloc(64)`
   （后者需进入 arena 并带锁）。因此在"分配密集"场景下 Go 与 C 基本持平（0.93×）。

> 结论：Go/C 跳表在 Set/Get 上的差距主要来自 **并发安全随机源、运行时边界检查与
> 部分内联限制**，而非 GC 或内存分配本身。

## 差异来源分析

C 版更快主要源于以下几点（均为 Go 版为「内存安全 + 运行时 + 泛型」付出的成本）：

1. **随机数开销**：Go 版使用 `math/rand/v2` 全局并发安全随机源（`rand.Intn`/`rand.Float64`
   内部有锁与更复杂的算法），C 版用无锁 xorshift32。
2. **内存管理**：Go 有 GC 与切片分配（如每次 `SetOrUpdate` 分配 `make([]*node, 32)` 前驱
   数组，插入时再分配节点与 next 切片），C 版用栈上前驱数组 + 手动 `malloc/free`。
3. **函数调用/边界检查**：Go 运行时边界检查与部分内联限制带来常数级开销。

> 注：Go 版已移除内置锁（改为无锁单线程），因此锁开销不再构成差异来源。

因此，本对比反映的是「各自原生语言下的实现成本」，并非纯粹算法差异——两者算法复杂度
同为平均 O(log n)。

## 复现

```sh
# Go 基准（在仓库根目录）
go test -run=^$ -bench='BenchmarkSkipList|BenchmarkMap' -benchmem -benchtime=1s ./test/

# Go 100 万级 + GC 压力基准
go test -run=^$ -bench='BenchmarkSkipListMillion' -benchmem -benchtime=1s ./test/

# C 基准
cd sharkskiplist/c && make && ./bench
```
