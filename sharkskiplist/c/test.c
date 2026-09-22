// test.c — C 版跳表正确性测试。
//
// 编译运行：cc -O2 -std=c11 -o test test.c skiplist.c && ./test
// 或直接 make test。
#include <assert.h>
#include <stdint.h>
#include <stdio.h>

#include "skiplist.h"

static int g_idx;
static int64_t g_keys[10001];

static void collect(int64_t key, int64_t value, void *ctx) {
    (void)value;
    (void)ctx;
    g_keys[g_idx++] = key;
}

// test_desc 验证原生降序跳表（对应 Go 版 NewDesc）的语义。
static void test_desc(void) {
    skiplist *sl = skiplist_new_desc();

    skiplist_set_or_update(sl, 3, 30);
    skiplist_set_or_update(sl, 1, 10);
    skiplist_set_or_update(sl, 5, 50);
    skiplist_set_or_update(sl, 2, 20);
    skiplist_set_or_update(sl, 4, 40);

    // 点操作语义不变
    int64_t v = 0;
    assert(skiplist_get(sl, 3, &v) && v == 30);
    assert(skiplist_contains(sl, 5));
    assert(!skiplist_contains(sl, 99));
    assert(skiplist_len(sl) == 5);

    // Min 自然最小、Max 自然最大（与原生方向无关）
    int64_t k = 0;
    assert(skiplist_min(sl, &k, &v) && k == 1);
    assert(skiplist_max(sl, &k, &v) && k == 5);

    // RangeAsc 自然升序 1..5
    g_idx = 0;
    skiplist_range_asc(sl, collect, NULL);
    assert(g_idx == 5);
    assert(g_keys[0] == 1 && g_keys[1] == 2 && g_keys[2] == 3 && g_keys[3] == 4 && g_keys[4] == 5);

    // RangeDesc 自然降序 5..1
    g_idx = 0;
    skiplist_range_desc(sl, collect, NULL);
    assert(g_idx == 5);
    assert(g_keys[0] == 5 && g_keys[1] == 4 && g_keys[2] == 3 && g_keys[3] == 2 && g_keys[4] == 1);

    // RangeBetween 升序 [2,4]
    g_idx = 0;
    skiplist_range_between(sl, 2, 4, collect, NULL);
    assert(g_idx == 3);
    assert(g_keys[0] == 2 && g_keys[1] == 3 && g_keys[2] == 4);

    // RangeBetween 降序 [4,2]
    g_idx = 0;
    skiplist_range_between(sl, 4, 2, collect, NULL);
    assert(g_idx == 3);
    assert(g_keys[0] == 4 && g_keys[1] == 3 && g_keys[2] == 2);

    // Ceiling/Floor 严格语义（自然序，与原生方向无关）
    // Ceiling: >= key 的最小
    assert(skiplist_ceiling(sl, 3, &k, &v) && k == 3);
    assert(skiplist_ceiling(sl, 0, &k, &v) && k == 1);
    assert(skiplist_ceiling(sl, 2, &k, &v) && k == 2);
    assert(!skiplist_ceiling(sl, 6, &k, &v));
    // Floor: <= key 的最大
    assert(skiplist_floor(sl, 3, &k, &v) && k == 3);
    assert(skiplist_floor(sl, 6, &k, &v) && k == 5);
    assert(skiplist_floor(sl, 4, &k, &v) && k == 4);
    assert(!skiplist_floor(sl, 0, &k, &v));

    // 删除
    assert(skiplist_delete(sl, 3));
    assert(!skiplist_contains(sl, 3));
    assert(skiplist_len(sl) == 4);

    skiplist_free(sl);
}

int main(void) {
    skiplist *sl = skiplist_new();

    // set/get 与覆盖
    skiplist_set_or_update(sl, 1, 10);
    skiplist_set_or_update(sl, 2, 20);
    skiplist_set_or_update(sl, 3, 30);
    int64_t v = 0;
    assert(skiplist_get(sl, 2, &v) && v == 20);
    skiplist_set_or_update(sl, 2, 200); // 覆盖
    assert(skiplist_get(sl, 2, &v) && v == 200);
    assert(skiplist_len(sl) == 3);
    assert(!skiplist_get(sl, 999, &v));

    // set_if_not_exists
    assert(skiplist_set_if_not_exists(sl, 4, 40));
    assert(!skiplist_set_if_not_exists(sl, 4, 999));
    assert(skiplist_get(sl, 4, &v) && v == 40);

    // contains
    assert(skiplist_contains(sl, 1));
    assert(!skiplist_contains(sl, 1000));

    // min/max
    int64_t k, val;
    assert(skiplist_min(sl, &k, &val) && k == 1);
    assert(skiplist_max(sl, &k, &val) && k == 4);

    // ceiling/floor
    assert(skiplist_ceiling(sl, 3, &k, &val) && k == 3);
    assert(skiplist_ceiling(sl, 0, &k, &val) && k == 1);
    assert(!skiplist_ceiling(sl, 5, &k, &val));
    assert(skiplist_floor(sl, 3, &k, &val) && k == 3);
    assert(skiplist_floor(sl, 5, &k, &val) && k == 4);
    assert(!skiplist_floor(sl, 0, &k, &val));

    // range_asc 升序
    g_idx = 0;
    skiplist_range_asc(sl, collect, NULL);
    assert(g_idx == 4);
    assert(g_keys[0] == 1 && g_keys[1] == 2 && g_keys[2] == 3 && g_keys[3] == 4);

    // range_desc 降序
    g_idx = 0;
    skiplist_range_desc(sl, collect, NULL);
    assert(g_idx == 4);
    assert(g_keys[0] == 4 && g_keys[1] == 3 && g_keys[2] == 2 && g_keys[3] == 1);

    // range_between 闭区间 [2,3]
    g_idx = 0;
    skiplist_range_between(sl, 2, 3, collect, NULL);
    assert(g_idx == 2);
    assert(g_keys[0] == 2 && g_keys[1] == 3);

    // delete
    assert(skiplist_delete(sl, 2));
    assert(!skiplist_delete(sl, 2));
    assert(skiplist_len(sl) == 3);
    assert(!skiplist_contains(sl, 2));

    // clear
    skiplist_clear(sl);
    assert(skiplist_len(sl) == 0);
    assert(!skiplist_min(sl, &k, &val));
    assert(!skiplist_max(sl, &k, &val));

    // 大规模顺序插入 + 顺序校验
    for (int64_t i = 0; i < 10000; i++) {
        skiplist_set_or_update(sl, i, i * 10);
    }
    assert(skiplist_len(sl) == 10000);
    g_idx = 0;
    skiplist_range_asc(sl, collect, NULL);
    assert(g_idx == 10000);
    for (int i = 0; i < 10000; i++) {
        assert(g_keys[i] == (int64_t)i);
    }

    test_desc();

    skiplist_free(sl);
    printf("ALL TESTS PASSED\n");
    return 0;
}
