package main

// The queue client. mlsim used to carry a hand-written RESP implementation;
// it worked, but it taught nothing about Kubernetes and it could fail in ways
// that looked exactly like a scenario failing — which is fatal in a lab whose
// whole value rests on `verify` being an honest signal.

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

func newRedisClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr: addr,

		// The gateway holds a connection for the entire life of a request:
		// BLPOP occupies it until a worker answers or the timeout expires. So
		// the pool has to be at least as large as the peak number of in-flight
		// requests, or requests queue for a *connection* on top of queueing for
		// a worker — invented latency that would corrupt every measurement the
		// scenarios take. go-redis defaults to 10 per CPU, which is not enough
		// under the load these scenarios generate.
		PoolSize: 512,

		// Blocking commands set their own read deadline from the block timeout,
		// so this only governs ordinary commands like LPUSH and LLEN.
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	})
}

// blockingPop wraps BLPOP/BRPOP. It returns ("", nil) when the block timeout
// expires: a timeout is a normal outcome here, not a failure, and the gateway
// reports it as 504 rather than 502.
func blockingPop(
	ctx context.Context,
	pop func(context.Context, time.Duration, ...string) *redis.StringSliceCmd,
	key string,
	timeout time.Duration,
) (string, error) {
	vals, err := pop(ctx, timeout, key).Result()
	switch {
	case errors.Is(err, redis.Nil):
		return "", nil
	case err != nil:
		return "", err
	case len(vals) < 2: // [key, value]
		return "", nil
	default:
		return vals[1], nil
	}
}

// pushReply publishes a worker's answer and expires it, so a gateway that has
// already given up waiting does not leak the key.
func pushReply(ctx context.Context, rdb *redis.Client, key, value string, ttl time.Duration) error {
	pipe := rdb.TxPipeline()
	pipe.LPush(ctx, key, value)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}
