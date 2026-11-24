package realkeys

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/redis/go-redis/v9"
)

func run(ctx context.Context, c redis.UniversalClient, key string) {
	c.Get(ctx, "key") // want
	c.Set(ctx, key, "value", 0)
	c.HGet(ctx, key, "field")
	c.Del(ctx, key, fmt.Sprint(1))                                                           // want
	c.BitOpAnd(ctx, "dest", key)                                                             // want
	c.XDel(ctx, "stream", "id")                                                              // want
	c.XAdd(ctx, &redis.XAddArgs{Stream: "stream", Values: map[string]any{"field": "value"}}) // want
	c.XAdd(ctx, &redis.XAddArgs{Stream: key, Values: map[string]any{"field": "value"}})
	c.XRead(ctx, &redis.XReadArgs{Streams: []string{key, "0"}})
	c.XRead(ctx, &redis.XReadArgs{Streams: []string{"stream", "0"}}) // want
	c.MSet(ctx, map[string]any{key: "value"})
	c.MSet(ctx, map[string]any{"key": "value"}) // want
	c.Do(ctx, "GET", "key")                     // want
	args := []any{"SET", "key", "value"}
	c.Do(ctx, args...) // want
	args = []any{"SET", key, "value"}
	c.Do(ctx, args...)
	var builder strings.Builder
	c.Get(ctx, builder.String()) // want
}

func runRaw(ctx context.Context, c *redis.Client, p *redis.Pipeline, auto *redis.AutoPipeliner, w io.Writer, key string) {
	c.DoRaw(ctx, "GET", "key") // want
	c.DoRaw(ctx, "SET", key, "value")
	p.DoRaw(ctx, "GET", fmt.Sprint(key)) // want
	auto.DoRaw(ctx, "GET", "key")        // want
	c.DoRawWriteTo(ctx, w, "GET", "key") // want
	auto.DoRawWriteTo(ctx, w, "SET", key, "value")
	args := []any{"GET", "key"}
	c.DoRaw(ctx, args...)                                 // want
	c.DoRawWriteTo(ctx, w, args...)                       // want
	(*redis.Client).DoRaw(c, ctx, "GET", "key")           // want
	(*redis.Client).DoRawWriteTo(c, ctx, w, "GET", "key") // want
	// force ignore sharklint: redis
	c.DoRaw(ctx, "GET", "ignored")
}

func runContainers(ctx context.Context, c *redis.Client, key string) {
	values := []any{key, "value"}
	(*redis.Client).MSet(c, ctx, values...)
	values = append(values, "bad", "value")
	(*redis.Client).MSet(c, ctx, values...) // want
	keys := make([]string, 1)
	keys[0] = fmt.Sprint(1)
	c.Del(ctx, keys...) // want
}
