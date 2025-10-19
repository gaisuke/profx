package workers

import (
	"context"
	"sync"
	"log"

	"github.com/gaisuke/profx/internal/services"
)

func StartWorkerPool(ctx context.Context, wg *sync.WaitGroup, numWorkers int, jobQueue <-chan string, evaluationService *services.EvaluationService) {
	log.Printf("Starting worker pool with %d workers", numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go worker(ctx, wg, i, jobQueue, evaluationService)
	}
}

func worker(ctx context.Context, wg *sync.WaitGroup, id int, jobQueue <-chan string, evaluationService *services.EvaluationService) {
	defer wg.Done()
	log.Printf("[Worker %d] Started", id)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Worker %d] Shutting down gracefully", id)
			return
		case jobID, ok := <-jobQueue:
			if !ok {
				log.Printf("[Worker %d] Job queue closed, exiting", id)
				return
			}

			log.Printf("[Worker %d] Processing job: %s", id, jobID)

			if err := evaluationService.EvaluateJob(ctx, jobID); err != nil {
				log.Printf("[Worker %d] Job %s failed: %v", id, jobID, err)
			} else {
				log.Printf("[Worker %d] Job %s completed successfully", id, jobID)
			}
		}
	}
}