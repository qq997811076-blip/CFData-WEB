package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

const progressEmitInterval = 250 * time.Millisecond

func runBoundedWorkers(ctx context.Context, total, maxWorkers, progressEvery int, onProgress func(current, total int), work func(idx int)) bool {
	if total <= 0 {
		return false
	}
	if maxWorkers <= 0 {
		maxWorkers = 1
	}

	var wg sync.WaitGroup
	slots := make(chan struct{}, maxWorkers)
	var count int
	var countMutex sync.Mutex
	var lastEmit time.Time
	wasCanceled := false

	for i := 0; i < total; i++ {
		select {
		case <-ctx.Done():
			wasCanceled = true
			return wasCanceled
		case slots <- struct{}{}:
		}

		wg.Add(1)
		go func(idx int) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("worker panic (idx=%d): %v\n%s\n", idx, r, debug.Stack())
				}
				<-slots
				wg.Done()

				countMutex.Lock()
				count++
				current := count
				shouldEmit := false
				now := time.Now()
				if current == total {
					shouldEmit = true
				} else if progressEvery > 1 && current%progressEvery == 0 {
					shouldEmit = true
				} else if now.Sub(lastEmit) >= progressEmitInterval {
					shouldEmit = true
				}
				if shouldEmit {
					lastEmit = now
				}
				countMutex.Unlock()

				if onProgress != nil && shouldEmit {
					func() {
						defer func() {
							if r := recover(); r != nil {
								fmt.Printf("onProgress panic: %v\n%s\n", r, debug.Stack())
							}
						}()
						onProgress(current, total)
					}()
				}
			}()

			select {
			case <-ctx.Done():
				return
			default:
			}

			work(idx)
		}(i)
	}

	wg.Wait()
	return wasCanceled
}

