package worker

import (
	"time"

	log "github.com/sirupsen/logrus"
)

type WorkerConfig struct {
	MasterAddr string
	WorkerAddr string
	Capacity   int32
}

type Server struct {
	config *WorkerConfig
	worker *WorkerNode
}

func NewServer(config *WorkerConfig) *Server {
	return &Server{
		config: config,
	}
}

func (s *Server) Start() {
	log.Infof("Starting worker server on %s", s.config.WorkerAddr)

	worker, err := NewWorker(s.config.MasterAddr, s.config.WorkerAddr, s.config.Capacity)
	if err != nil {
		log.Fatalf("Failed to create worker: %v", err)
	}
	s.worker = worker

	go func() {
		if err := worker.StartServer(); err != nil {
			log.Fatalf("Worker server failed: %v", err)
		}
	}()

	time.Sleep(5 * time.Second)

	if err := worker.Register(); err != nil {
		log.Fatalf("Failed to register with master: %v", err)
	}

	worker.StartHeartbeats()
}

func (s *Server) Stop() {
	s.worker.Stop()
}
