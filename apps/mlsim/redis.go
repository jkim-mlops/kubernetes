package main

// The queue client. mlsim used to carry a hand-written RESP implementation;
// it worked, but it taught nothing about Kubernetes and it could fail in ways
// that looked exactly like a scenario failing — which is fatal in a lab whose
// whole value rests on `verify` being an honest signal.

import (
	"context"
	"errors"
	"fmt"
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

// blockingPop wraps BLPOP/BRPOP. It returns ("", nil) for exactly one
// condition — the block timeout expired with nothing to pop — because that is
// a normal outcome the gateway reports as 504.
//
// Nothing else may take that path. An earlier version also returned ("", nil)
// when the command came back empty, which meant a cancelled request was
// reported to the caller as "timed out waiting for a worker" after 900ms and
// counted in mlsim_timeouts_total. A lab whose scenarios are judged on a
// timeout count cannot afford a timeout that did not happen.
func blockingPop(
	ctx context.Context,
	pop func(context.Context, time.Duration, ...string) *redis.StringSliceCmd,
	key string,
	timeout time.Duration,
) (string, error) {
	vals, err := pop(ctx, timeout, key).Result()
	switch {
	case errors.Is(err, redis.Nil): // the block timeout expired
		return "", nil
	case err != nil:
		return "", err
	case len(vals) < 2: // BLPOP answers [key, value] or nothing at all
		return "", fmt.Errorf("malformed pop reply for %s: %v", key, vals)
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
