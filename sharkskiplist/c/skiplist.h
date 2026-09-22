// skiplist.h — C 版跳表（有序映射），header-only 单头文件实现。
//
// 本实现与仓库内 Go 版 sharkskiplist 保持算法一致：
//   - 原生方向可选（升序 / 降序），key 唯一（重复 set 覆盖 value）；
//   - 晋升概率 p = 1/4，最大层数 32；
//   - 查找/插入/删除平均 O(log n)。
//
// key/value 使用 int64_t，对应 Go 版 SkipList[int, int]（64 位平台上 Go 的
// int 为 64 位），便于公平对比性能。
//
// 所有函数均为 static inline，只需包含本文件即可使用，无需单独编译 .c 文件：
//
//     #include "skiplist.h"
//     skiplist *sl = skiplist_new();
//     skiplist_set_or_update(sl, 1, 10);
#ifndef SHARK_SKIPLIST_H
#define SHARK_SKIPLIST_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

#ifdef __cplusplus
extern "C" {
#endif

// SKIPLIST_MAX_LEVEL 是跳表最大层数，与 Go 版 maxLevel = 32 一致。
#define SKIPLIST_MAX_LEVEL 32

// skiplist_node 是跳表内部节点，forward 为柔性数组成员（长度 = level）。
typedef struct skiplist_node {
    int64_t key;
    int64_t value;
    int level;
    struct skiplist_node *forward[]; // forward[i] 指向第 i 层下一节点
} skiplist_node;

// skiplist 是跳表实例，由 skiplist_new_asc/skiplist_new_desc 创建、skiplist_free 销毁。
typedef struct skiplist {
    skiplist_node *head; // 头节点，forward 长度固定为 SKIPLIST_MAX_LEVEL
    size_t size;         // 当前元素数量
    uint32_t rng_state;  // 层数随机源（xorshift32 状态）
    int desc;            // 原生方向：0=升序，1=降序
} skiplist;

// 遍历回调：返回 void（本实现不支持提前终止，与 Go 版回调返回 false 提前终止
// 的语义略有差异，但遍历成本一致）。ctx 为调用方透传的上下文指针。
typedef void (*skiplist_fn)(int64_t key, int64_t value, void *ctx);

// node_new 分配一个 level 层的节点并将 forward 全部初始化为 NULL。
static inline skiplist_node *node_new(int64_t key, int64_t value, int level) {
    skiplist_node *n =
        (skiplist_node *)malloc(offsetof(skiplist_node, forward) + (size_t)level * sizeof(skiplist_node *));
    if (n == NULL) {
        abort();
    }
    n->key = key;
    n->value = value;
    n->level = level;
    for (int i = 0; i < level; i++) {
        n->forward[i] = NULL;
    }
    return n;
}

// rng_next 返回下一个 32 位伪随机数（xorshift32，无锁、开销极低）。
static inline uint32_t rng_next(skiplist *sl) {
    uint32_t x = sl->rng_state;
    x ^= x << 13;
    x ^= x >> 17;
    x ^= x << 5;
    sl->rng_state = x;
    return x;
}

// random_level 以 p = 1/4 的概率晋升，与 Go 版 randomLevel 语义一致。
static inline int random_level(skiplist *sl) {
    int level = 1;
    while (level < SKIPLIST_MAX_LEVEL && (rng_next(sl) & 0x3u) == 0) {
        level++;
    }
    return level;
}

// key_before 判断在物理存储顺序中 a 是否排在 b 前面。
// 升序（desc=0）时 a < b；降序（desc=1）时 a > b。
static inline bool key_before(const skiplist *sl, int64_t a, int64_t b) {
    return sl->desc ? (a > b) : (a < b);
}

// first_node 返回物理存储顺序的第一个节点（O(1)），空表返回 NULL。
static inline skiplist_node *first_node(const skiplist *sl) {
    return sl->head->forward[0];
}

// last_node 返回物理存储顺序的最后一个节点（O(log n)），空表返回 NULL。
static inline skiplist_node *last_node(const skiplist *sl) {
    skiplist_node *x = sl->head;
    for (int i = SKIPLIST_MAX_LEVEL - 1; i >= 0; i--) {
        while (x->forward[i] != NULL) {
            x = x->forward[i];
        }
    }
    return x == sl->head ? NULL : x;
}

// find_predecessors 从最高层向下搜索 key。
// 返回第 0 层候选节点：命中时 key 相等；未命中时为物理序第一个"排在 key 之后"的节点或 NULL。
// preds 非空时写入各层前驱节点（供插入/删除修改指针）。
static inline skiplist_node *find_predecessors(const skiplist *sl, int64_t key, skiplist_node **preds) {
    skiplist_node *x = sl->head;
    for (int i = SKIPLIST_MAX_LEVEL - 1; i >= 0; i--) {
        while (x->forward[i] != NULL && key_before(sl, x->forward[i]->key, key)) {
            x = x->forward[i];
        }
        if (preds != NULL) {
            preds[i] = x;
        }
    }
    return x->forward[0];
}

// skiplist_clear 清空跳表中的所有元素。时间复杂度 O(n)。
static inline void skiplist_clear(skiplist *sl) {
    skiplist_node *x = sl->head->forward[0];
    while (x != NULL) {
        skiplist_node *next = x->forward[0];
        free(x);
        x = next;
    }
    for (int i = 0; i < SKIPLIST_MAX_LEVEL; i++) {
        sl->head->forward[i] = NULL;
    }
    sl->size = 0;
}

// skiplist_new_impl 根据原生方向构造跳表。
static inline skiplist *skiplist_new_impl(int desc) {
    skiplist *sl = (skiplist *)malloc(sizeof(*sl));
    if (sl == NULL) {
        abort();
    }
    sl->head = node_new(0, 0, SKIPLIST_MAX_LEVEL);
    sl->size = 0;
    sl->rng_state = 0x9e3779b9u; // 固定种子，保证可复现
    sl->desc = desc;
    return sl;
}

// skiplist_new_asc 创建原生升序的跳表（对应 Go 版 NewAsc）。
static inline skiplist *skiplist_new_asc(void) {
    return skiplist_new_impl(0);
}

// skiplist_new_desc 创建原生降序的跳表（对应 Go 版 NewDesc）。
static inline skiplist *skiplist_new_desc(void) {
    return skiplist_new_impl(1);
}

// skiplist_new 是 skiplist_new_asc 的别名，保留以兼容旧代码。
static inline skiplist *skiplist_new(void) {
    return skiplist_new_impl(0);
}

static inline void skiplist_free(skiplist *sl) {
    if (sl == NULL) {
        return;
    }
    skiplist_clear(sl);
    free(sl->head);
    free(sl);
}

static inline void skiplist_set_or_update(skiplist *sl, int64_t key, int64_t value) {
    skiplist_node *preds[SKIPLIST_MAX_LEVEL];
    skiplist_node *cand = find_predecessors(sl, key, preds);

    if (cand != NULL && cand->key == key) {
        cand->value = value; // 已存在，覆盖 value
        return;
    }

    int level = random_level(sl);
    skiplist_node *n = node_new(key, value, level);
    for (int i = 0; i < level; i++) {
        n->forward[i] = preds[i]->forward[i];
        preds[i]->forward[i] = n;
    }
    sl->size++;
}

static inline bool skiplist_set_if_not_exists(skiplist *sl, int64_t key, int64_t value) {
    skiplist_node *preds[SKIPLIST_MAX_LEVEL];
    skiplist_node *cand = find_predecessors(sl, key, preds);

    if (cand != NULL && cand->key == key) {
        return false; // 已存在，不覆盖
    }

    int level = random_level(sl);
    skiplist_node *n = node_new(key, value, level);
    for (int i = 0; i < level; i++) {
        n->forward[i] = preds[i]->forward[i];
        preds[i]->forward[i] = n;
    }
    sl->size++;
    return true;
}

static inline bool skiplist_get(skiplist *sl, int64_t key, int64_t *value) {
    skiplist_node *cand = find_predecessors(sl, key, NULL);
    if (cand == NULL || cand->key != key) {
        return false;
    }
    if (value != NULL) {
        *value = cand->value;
    }
    return true;
}

static inline bool skiplist_delete(skiplist *sl, int64_t key) {
    skiplist_node *preds[SKIPLIST_MAX_LEVEL];
    skiplist_node *cand = find_predecessors(sl, key, preds);
    if (cand == NULL || cand->key != key) {
        return false;
    }

    for (int i = 0; i < cand->level; i++) {
        preds[i]->forward[i] = cand->forward[i];
    }
    free(cand);
    sl->size--;
    return true;
}

static inline bool skiplist_contains(skiplist *sl, int64_t key) {
    return skiplist_get(sl, key, NULL);
}

static inline size_t skiplist_len(const skiplist *sl) {
    return sl->size;
}

static inline bool skiplist_min(const skiplist *sl, int64_t *key, int64_t *value) {
    skiplist_node *n = sl->desc ? last_node(sl) : first_node(sl);
    if (n == NULL) {
        return false;
    }
    if (key != NULL) {
        *key = n->key;
    }
    if (value != NULL) {
        *value = n->value;
    }
    return true;
}

static inline bool skiplist_max(const skiplist *sl, int64_t *key, int64_t *value) {
    skiplist_node *n = sl->desc ? first_node(sl) : last_node(sl);
    if (n == NULL) {
        return false;
    }
    if (key != NULL) {
        *key = n->key;
    }
    if (value != NULL) {
        *value = n->value;
    }
    return true;
}

// skiplist_ceiling 返回 key 大于等于给定 key 的最小元素（自然序），无解返回 false。
static inline bool skiplist_ceiling(const skiplist *sl, int64_t key, int64_t *key_out, int64_t *value_out) {
    skiplist_node *preds[SKIPLIST_MAX_LEVEL];
    skiplist_node *cand = find_predecessors(sl, key, preds);
    skiplist_node *n;
    if (sl->desc) {
        // 原生降序：cand 为第一个 <= key（自然序），Ceiling 需取其前驱（最小 >= key）
        if (cand != NULL && cand->key == key) {
            n = cand;
        } else {
            n = preds[0];
            if (n == sl->head) {
                return false;
            }
        }
    } else {
        // 原生升序：cand 即第一个 >= key
        if (cand == NULL) {
            return false;
        }
        n = cand;
    }
    if (key_out != NULL) {
        *key_out = n->key;
    }
    if (value_out != NULL) {
        *value_out = n->value;
    }
    return true;
}

// skiplist_floor 返回 key 小于等于给定 key 的最大元素（自然序），无解返回 false。
static inline bool skiplist_floor(const skiplist *sl, int64_t key, int64_t *key_out, int64_t *value_out) {
    skiplist_node *preds[SKIPLIST_MAX_LEVEL];
    skiplist_node *cand = find_predecessors(sl, key, preds);
    skiplist_node *n;
    if (sl->desc) {
        // 原生降序：cand 即第一个 <= key（自然序），即最大 <= key
        if (cand == NULL) {
            return false;
        }
        n = cand;
    } else {
        // 原生升序：命中返回 cand，否则取前驱（最大 <= key）
        if (cand != NULL && cand->key == key) {
            n = cand;
        } else {
            n = preds[0];
            if (n == sl->head) {
                return false;
            }
        }
    }
    if (key_out != NULL) {
        *key_out = n->key;
    }
    if (value_out != NULL) {
        *value_out = n->value;
    }
    return true;
}

// for_each_forward 沿物理存储顺序（前向指针）遍历，O(1) 额外内存。
static inline void for_each_forward(const skiplist *sl, skiplist_fn fn, void *ctx) {
    for (skiplist_node *x = sl->head->forward[0]; x != NULL; x = x->forward[0]) {
        fn(x->key, x->value, ctx);
    }
}

// for_each_backward 沿物理存储顺序的逆序遍历（快照反转），O(n) 额外内存。
static inline void for_each_backward(const skiplist *sl, skiplist_fn fn, void *ctx) {
    size_t n = sl->size;
    skiplist_node **nodes = (skiplist_node **)malloc(n * sizeof(*nodes));
    if (nodes == NULL && n > 0) {
        abort();
    }
    size_t i = 0;
    for (skiplist_node *x = sl->head->forward[0]; x != NULL; x = x->forward[0]) {
        nodes[i++] = x;
    }
    while (i > 0) {
        skiplist_node *nd = nodes[--i];
        fn(nd->key, nd->value, ctx);
    }
    free(nodes);
}

// skiplist_range_asc 按 key 自然升序遍历所有元素。
static inline void skiplist_range_asc(skiplist *sl, skiplist_fn fn, void *ctx) {
    if (sl->desc) {
        for_each_backward(sl, fn, ctx); // 原生降序：自然升序即物理逆序
    } else {
        for_each_forward(sl, fn, ctx);
    }
}

// skiplist_range_desc 按 key 自然降序遍历所有元素。
static inline void skiplist_range_desc(skiplist *sl, skiplist_fn fn, void *ctx) {
    if (sl->desc) {
        for_each_forward(sl, fn, ctx); // 原生降序：自然降序即物理正向
    } else {
        for_each_backward(sl, fn, ctx);
    }
}

// phys_forward 沿物理存储顺序遍历 [lo, hi] 闭区间（lo/hi 为物理端点）。
static inline void phys_forward(const skiplist *sl, int64_t lo, int64_t hi, skiplist_fn fn, void *ctx) {
    skiplist_node *x = sl->head;
    for (int i = SKIPLIST_MAX_LEVEL - 1; i >= 0; i--) {
        while (x->forward[i] != NULL && key_before(sl, x->forward[i]->key, lo)) {
            x = x->forward[i];
        }
    }
    for (x = x->forward[0]; x != NULL; x = x->forward[0]) {
        if (key_before(sl, hi, x->key)) {
            break; // x->key 已越过 hi
        }
        fn(x->key, x->value, ctx);
    }
}

// phys_backward 沿物理存储顺序的逆序遍历 [lo, hi] 闭区间（快照反转）。
static inline void phys_backward(const skiplist *sl, int64_t lo, int64_t hi, skiplist_fn fn, void *ctx) {
    skiplist_node *x = sl->head;
    for (int i = SKIPLIST_MAX_LEVEL - 1; i >= 0; i--) {
        while (x->forward[i] != NULL && key_before(sl, x->forward[i]->key, lo)) {
            x = x->forward[i];
        }
    }
    skiplist_node **nodes = (skiplist_node **)malloc(sl->size * sizeof(*nodes));
    if (nodes == NULL && sl->size > 0) {
        abort();
    }
    size_t i = 0;
    for (x = x->forward[0]; x != NULL; x = x->forward[0]) {
        if (key_before(sl, hi, x->key)) {
            break;
        }
        nodes[i++] = x;
    }
    while (i > 0) {
        skiplist_node *nd = nodes[--i];
        fn(nd->key, nd->value, ctx);
    }
    free(nodes);
}

// skiplist_range_between 遍历 [start, end] 闭区间内的元素。
// 方向由端点决定（自然序）：start < end 升序，start > end 降序，start == end 仅该 key。
static inline void skiplist_range_between(skiplist *sl, int64_t start, int64_t end, skiplist_fn fn, void *ctx) {
    if (start > end) {
        // 自然降序 [end, start]
        if (sl->desc) {
            phys_forward(sl, start, end, fn, ctx); // 原生降序：自然降序即物理正向
        } else {
            phys_backward(sl, end, start, fn, ctx); // 原生升序：自然降序即物理逆序
        }
        return;
    }
    // 自然升序 [start, end]
    if (sl->desc) {
        phys_backward(sl, end, start, fn, ctx); // 原生降序：自然升序即物理逆序
    } else {
        phys_forward(sl, start, end, fn, ctx); // 原生升序：自然升序即物理正向
    }
}

#ifdef __cplusplus
}
#endif

#endif /* SHARK_SKIPLIST_H */
