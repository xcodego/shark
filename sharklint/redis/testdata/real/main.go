package real

import (
	"context"

	"github.com/redis/go-redis/v9"
)

func run(ctx context.Context, client *redis.Client, cluster *redis.ClusterClient, universal redis.UniversalClient, pipe redis.Pipeliner) {
	client.Keys(ctx, "*")
	cluster.Keys(ctx, "*")
	universal.Keys(ctx, "*")
	pipe.Keys(ctx, "*")
	client.Do(ctx, "KEYS", "*")
	pipe.Do(ctx, "keys", "*")
	client.Process(ctx, redis.NewCmd(ctx, "KEYS", "*"))
	client.Scan(ctx, 0, "*", 10)
	client.Process(ctx, redis.NewStringSliceCmd(ctx, "keys", "*"))
}
