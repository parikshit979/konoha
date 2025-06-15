package main

import (
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"

	"github.com/konoha/master"
	"github.com/konoha/worker"
)

func main() {
	log.SetFormatter(&log.TextFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.DebugLevel)

	workerConfig := &worker.WorkerConfig{
		MasterAddr: ":50051",
		WorkerAddr: ":50052",
		Capacity:   10,
	}
	workerServer1 := worker.NewServer(workerConfig)
	go workerServer1.Start()

	workerConfig = &worker.WorkerConfig{
		MasterAddr: ":50051",
		WorkerAddr: ":50053",
		Capacity:   10,
	}
	workerServer2 := worker.NewServer(workerConfig)
	go workerServer2.Start()

	masterConfig := &master.MasterConfig{
		HttpListenAddr: ":8080",
		ListenAddr:     ":50051",
	}
	masterServer := master.NewServer(masterConfig)
	masterServer.Start()

	// --- Graceful Shutdown ---
	// Listen for OS signals to gracefully shut down
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Block until a signal is received
	sig := <-sigChan
	log.Printf("Received signal: %v. Shutting down...", sig)

	workerServer1.Stop()
	workerServer2.Stop()
	masterServer.Stop()
}
