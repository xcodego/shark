package realcommands

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

func run(ctx context.Context, c *redis.Client, sentinel *redis.SentinelClient, ch chan string) {
	c.FlushAllAsync(ctx)
	c.FlushDB(ctx)
	c.ConfigGet(ctx, "*")
	c.ConfigSet(ctx, "x", "y")
	c.DebugObject(ctx, "x")
	c.ShutdownNoSave(ctx)
	c.Save(ctx)
	c.BgSave(ctx)
	c.BgRewriteAOF(ctx)
	c.ReplicaOf(ctx, "host", "6379")
	c.SlaveOf(ctx, "host", "6379")
	c.Pipeline().SwapDB(ctx, 0, 1)
	c.Migrate(ctx, "host", "6379", "key", 0, time.Second)
	c.Monitor(ctx, ch)
	c.ClientKill(ctx, "host:6379")
	c.ClientKillByFilter(ctx, "id", "1")
	c.ClientPause(ctx, time.Second)
	c.ClientList(ctx)
	sentinel.Monitor(ctx, "name", "host", "6379", "1")
}
