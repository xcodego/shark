// bench.c — C 版跳表基准测试。
//
// 与 test/sharkskiplist_bench_test.go 中的 Go 基准对齐：相同的操作、相同规模的
// 预置数据与随机 key 空间，输出 ns/op 便于直接对比。
//
// 编译：cc -O2 -std=c11 -o bench bench.c skiplist.c
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

#include "skiplist.h"

#define KEY_SPACE (1u << 20) // Set 类随机 key 空间，与 Go 版 benchKeySpace 一致
#define PRESET_N 100000      // 查找/遍历类预置元素数量，与 Go 版 100000 一致

// 写类迭代次数
#define N_WRITE 2000000
// 查找类迭代次数
#define N_LOOKUP 10000000
// Min/Max 迭代次数
#define N_MIN 50000000
#define N_MAX 20000000
// 遍历类迭代次数
#define N_RANGE 2000
#define N_RANGE_BETWEEN 10000

// 100 万级数据规模
#define MILLION 1000000
// 顺序/随机插入 100 万的迭代次数
#define N_MILLION_WRITE 1000000
// 100 万预置后查找次数
#define N_MILLION_LOOKUP 10000000
// 遍历 100 万的次数
#define N_MILLION_RANGE 1000
// 制造临时数据的迭代次数与参数
#define N_MILLION_GARBAGE 5000000
#define GARBAGE_SPACE 100000
#define GARBAGE_SIZE 64

// xs32 — 独立于跳表内部的 xorshift32，用于生成随机 key（与 Go 的 rand.Intn 对应）。
static uint32_t xs32(uint32_t *s) {
    uint32_t x = *s;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    *s = x;
    return x;
}

static double now_ns(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec * 1e9 + (double)ts.tv_nsec;
}

static void report(const char *name, long n, double total_ns) {
    printf("%-36s %12ld  %10.2f ns/op\n", name, n, total_ns / (double)n);
}

// preset_n 预置一个含 0..n-1 连续 key 的跳表。
static skiplist *preset_n(int64_t n) {
    skiplist *sl = skiplist_new();
    for (int64_t i = 0; i < n; i++) {
        skiplist_set_or_update(sl, i, i);
    }
    return sl;
}

// preset 预置一个含 0..PRESET_N-1 连续 key 的跳表。
static skiplist *preset(void) {
    return preset_n(PRESET_N);
}

static void sum_fn(int64_t key, int64_t value, void *ctx) {
    int64_t *s = (int64_t *)ctx;
    *s += key + value;
}

// 写入类 ----------------------------------------------------------------

static int64_t bench_set_or_update(void) {
    skiplist *sl = skiplist_new();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_WRITE; i++) {
        skiplist_set_or_update(sl, (int64_t)(xs32(&rng) & (KEY_SPACE - 1)), 1);
        sink++;
    }
    double t1 = now_ns();
    report("SetOrUpdate", N_WRITE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_set_or_update_seq(void) {
    skiplist *sl = skiplist_new();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_WRITE; i++) {
        skiplist_set_or_update(sl, (int64_t)i, (int64_t)i);
        sink++;
    }
    double t1 = now_ns();
    report("SetOrUpdateSeq", N_WRITE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_set_if_not_exists(void) {
    skiplist *sl = skiplist_new();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_WRITE; i++) {
        if (skiplist_set_if_not_exists(sl, (int64_t)(xs32(&rng) & (KEY_SPACE - 1)), 1)) {
            sink++;
        }
    }
    double t1 = now_ns();
    report("SetIfNotExists", N_WRITE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 查找类 ----------------------------------------------------------------

static int64_t bench_get(void) {
    skiplist *sl = preset();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_LOOKUP; i++) {
        int64_t v = 0;
        if (skiplist_get(sl, (int64_t)(xs32(&rng) % PRESET_N), &v)) {
            sink += v;
        }
    }
    double t1 = now_ns();
    report("Get", N_LOOKUP, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_get_miss(void) {
    skiplist *sl = preset();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_LOOKUP; i++) {
        int64_t v = 0;
        // 全部未命中，走到链表尾，与 Go 版 GetMiss 一致
        if (skiplist_get(sl, (int64_t)(PRESET_N + (xs32(&rng) % 1000)), &v)) {
            sink += v;
        }
    }
    double t1 = now_ns();
    report("GetMiss", N_LOOKUP, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_contains(void) {
    skiplist *sl = preset();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_LOOKUP; i++) {
        if (skiplist_contains(sl, (int64_t)(xs32(&rng) % PRESET_N))) {
            sink++;
        }
    }
    double t1 = now_ns();
    report("Contains", N_LOOKUP, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_ceiling(void) {
    skiplist *sl = preset();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_LOOKUP; i++) {
        int64_t k = 0, v = 0;
        if (skiplist_ceiling(sl, (int64_t)(xs32(&rng) % PRESET_N), &k, &v)) {
            sink += k;
        }
    }
    double t1 = now_ns();
    report("Ceiling", N_LOOKUP, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_floor(void) {
    skiplist *sl = preset();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_LOOKUP; i++) {
        int64_t k = 0, v = 0;
        if (skiplist_floor(sl, (int64_t)(xs32(&rng) % PRESET_N), &k, &v)) {
            sink += k;
        }
    }
    double t1 = now_ns();
    report("Floor", N_LOOKUP, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_min(void) {
    skiplist *sl = preset();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MIN; i++) {
        int64_t k = 0, v = 0;
        if (skiplist_min(sl, &k, &v)) {
            sink += k;
        }
        __asm__ volatile("" ::: "memory"); // 阻止编译器把返回常量的 Min 循环优化掉
    }
    double t1 = now_ns();
    report("Min", N_MIN, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_max(void) {
    skiplist *sl = preset();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MAX; i++) {
        int64_t k = 0, v = 0;
        if (skiplist_max(sl, &k, &v)) {
            sink += k;
        }
        __asm__ volatile("" ::: "memory"); // 阻止编译器把返回常量的 Max 循环优化掉
    }
    double t1 = now_ns();
    report("Max", N_MAX, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 删除类 ----------------------------------------------------------------

static int64_t bench_delete(void) {
    skiplist *sl = preset();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_WRITE; i++) {
        if (skiplist_delete(sl, (int64_t)(xs32(&rng) % PRESET_N))) {
            sink++;
        }
    }
    double t1 = now_ns();
    report("Delete", N_WRITE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 遍历类 ----------------------------------------------------------------

static int64_t bench_range_asc(void) {
    skiplist *sl = preset();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_RANGE; i++) {
        skiplist_range_asc(sl, sum_fn, &sink);
    }
    double t1 = now_ns();
    report("RangeAsc", N_RANGE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_range_desc(void) {
    skiplist *sl = preset();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_RANGE; i++) {
        skiplist_range_desc(sl, sum_fn, &sink);
    }
    double t1 = now_ns();
    report("RangeDesc", N_RANGE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

static int64_t bench_range_between(void) {
    skiplist *sl = preset();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_RANGE_BETWEEN; i++) {
        skiplist_range_between(sl, 20000, 30000, sum_fn, &sink);
    }
    double t1 = now_ns();
    report("RangeBetween", N_RANGE_BETWEEN, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 100 万级 + 临时数据 ----------------------------------------------------

// 顺序插入 100 万元素：每次插入分配一个新节点（malloc），百万级堆分配。
static int64_t bench_million_seq(void) {
    skiplist *sl = skiplist_new();
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MILLION_WRITE; i++) {
        skiplist_set_or_update(sl, (int64_t)i, (int64_t)i);
        sink++;
    }
    double t1 = now_ns();
    report("MillionSeq", N_MILLION_WRITE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 在 100 万 key 空间内随机写入/覆盖。
static int64_t bench_million_rand(void) {
    skiplist *sl = skiplist_new();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MILLION_WRITE; i++) {
        skiplist_set_or_update(sl, (int64_t)(xs32(&rng) % MILLION), 1);
        sink++;
    }
    double t1 = now_ns();
    report("MillionRand", N_MILLION_WRITE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 预置 100 万元素后随机查找。
static int64_t bench_million_get(void) {
    skiplist *sl = preset_n(MILLION);
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MILLION_LOOKUP; i++) {
        int64_t v = 0;
        if (skiplist_get(sl, (int64_t)(xs32(&rng) % MILLION), &v)) {
            sink += v;
        }
    }
    double t1 = now_ns();
    report("MillionGet", N_MILLION_LOOKUP, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 遍历 100 万元素。
static int64_t bench_million_range_asc(void) {
    skiplist *sl = preset_n(MILLION);
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MILLION_RANGE; i++) {
        skiplist_range_asc(sl, sum_fn, &sink);
    }
    double t1 = now_ns();
    report("MillionRangeAsc", N_MILLION_RANGE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

// 制造大量临时数据：固定 key 空间内循环覆盖 + 每次 malloc/free 64 字节，
// 对应 Go 版 make([]byte, 64) 产生的临时垃圾（C 无 GC，用 free 立即回收）。
static int64_t bench_million_garbage(void) {
    skiplist *sl = skiplist_new();
    uint32_t rng = 0x9e3779b9u;
    int64_t sink = 0;
    double t0 = now_ns();
    for (long i = 0; i < N_MILLION_GARBAGE; i++) {
        skiplist_set_or_update(sl, (int64_t)(xs32(&rng) % GARBAGE_SPACE), 1);
        void *tmp = malloc(GARBAGE_SIZE);
        if (tmp != NULL) {
            free(tmp);
        }
        sink++;
    }
    double t1 = now_ns();
    report("MillionGarbage", N_MILLION_GARBAGE, t1 - t0);
    skiplist_free(sl);
    return sink;
}

int main(void) {
    int64_t sink = 0;
    printf("%-36s %12s  %10s\n", "Benchmark", "iterations", "ns/op");
    printf("-------------------------------------------------------------\n");
    sink += bench_set_or_update();
    sink += bench_set_or_update_seq();
    sink += bench_set_if_not_exists();
    sink += bench_get();
    sink += bench_get_miss();
    sink += bench_contains();
    sink += bench_ceiling();
    sink += bench_floor();
    sink += bench_min();
    sink += bench_max();
    sink += bench_delete();
    sink += bench_range_asc();
    sink += bench_range_desc();
    sink += bench_range_between();
    sink += bench_million_seq();
    sink += bench_million_rand();
    sink += bench_million_get();
    sink += bench_million_range_asc();
    sink += bench_million_garbage();
    printf("-------------------------------------------------------------\n");
    printf("checksum: %lld\n", (long long)sink);
    return 0;
}
