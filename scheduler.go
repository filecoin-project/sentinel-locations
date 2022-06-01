package main

import (
	"context"
	"sync"

	"github.com/libp2p/go-libp2p-core/peer"
	"golang.org/x/sync/errgroup"
)

type minerRouter struct {
	// number of worker routine to spawn
	numWorkers int64
	// buffer holds tasks until they are processed
	stack []*task
	// inbound and outbound tasks
	in, out chan *task
	// tracks number of inflight tasks
	taskWg sync.WaitGroup
	// launches workers and collects errors if any occur
	grp *errgroup.Group

	fp *filecoinPeer
}

type task struct {
	miner minerInfo
}

func newParallelMinerRouter(ctx context.Context, numWorkers int64, rootTasks ...*task) (*minerRouter, context.Context) {
	grp, ctx := errgroup.WithContext(ctx)
	s := &minerRouter{
		numWorkers: numWorkers,
		stack:      rootTasks,
		in:         make(chan *task, numWorkers),
		out:        make(chan *task, numWorkers),
		grp:        grp,
	}
	s.taskWg.Add(len(rootTasks))
	return s, ctx
}

func (s *minerRouter) work(ctx context.Context, todo *task, results chan minerInfo) error {
	defer s.taskWg.Done()
	decodedPid, err := peer.Decode(todo.miner.PeerID)
	if err != nil {
		log.Warnw("could not decode peer ID ", "ID", todo.miner.PeerID, "error", err)
	}
	peerIDs := minerWithDecodedPeerID{miner: todo.miner, decodedPeerID: decodedPid}
	f, err := s.fp.findPeersWithDHT(ctx, peerIDs)
	if err != nil {
		return err
	}

	results <- f
	return nil
}

func (s *minerRouter) enqueueTask(task *task) {
	s.taskWg.Add(1)
	s.in <- task
}

func (s *minerRouter) startScheduler(ctx context.Context) {
	s.grp.Go(func() error {
		defer func() {
			close(s.out)
			// Because the workers may have exited early (due to the context being canceled).
			for range s.out {
				s.taskWg.Done()
			}
			// Because the workers may have enqueued additional tasks.
			for range s.in {
				s.taskWg.Done()
			}
			// now, the waitgroup should be at 0, and the goroutine that was _waiting_ on it should have exited.
		}()
		go func() {
			s.taskWg.Wait()
			close(s.in)
		}()
		for {
			if n := len(s.stack) - 1; n >= 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case newJob, ok := <-s.in:
					if !ok {
						return nil
					}
					s.stack = append(s.stack, newJob)
				case s.out <- s.stack[n]:
					s.stack[n] = nil
					s.stack = s.stack[:n]
				}
			} else {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case newJob, ok := <-s.in:
					if !ok {
						return nil
					}
					s.stack = append(s.stack, newJob)
				}
			}
		}
	})
}

func (s *minerRouter) startWorkers(ctx context.Context, out chan minerInfo) {
	for i := int64(0); i < s.numWorkers; i++ {
		s.grp.Go(func() error {
			for task := range s.out {
				if err := s.work(ctx, task, out); err != nil {
					return err
				}
			}
			return nil
		})
	}
}
