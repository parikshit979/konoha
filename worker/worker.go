package worker

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	scheduler "github.com/konoha/proto"
)

type WorkerNode struct {
	scheduler.UnimplementedWorkerServer

	id           string
	masterAddr   string
	workerAddr   string
	capacity     int32
	masterClient scheduler.SchedulerClient
	mu           sync.RWMutex
	runningJobs  map[string]*scheduler.Job
	status       scheduler.WorkerStatus
	grpcServer   *grpc.Server
}

func NewWorker(masterAddr, workerAddr string, capacity int32) (*WorkerNode, error) {
	hostname, _ := os.Hostname()
	id := fmt.Sprintf("%s:%s", hostname, workerAddr)

	// Connect to the master
	conn, err := grpc.NewClient(masterAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to master: %w", err)
	}

	masterClient := scheduler.NewSchedulerClient(conn)

	return &WorkerNode{
		id:           id,
		masterAddr:   masterAddr,
		workerAddr:   workerAddr,
		capacity:     capacity,
		masterClient: masterClient,
		runningJobs:  make(map[string]*scheduler.Job),
		status:       scheduler.WorkerStatus_IDLE,
	}, nil
}

func (w *WorkerNode) Register() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &scheduler.RegisterWorkerRequest{
		WorkerId: w.workerAddr,
		Capacity: w.capacity,
	}

	resp, err := w.masterClient.RegisterWorker(ctx, req)
	if err != nil {
		return fmt.Errorf("could not register with master: %w", err)
	}

	if !resp.Registered {
		return fmt.Errorf("registration rejected by master")
	}

	log.Infof("Successfully registered with master %s", resp.MasterId)
	return nil
}

func (w *WorkerNode) StartHeartbeats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		w.mu.RLock()
		status := w.status
		runningCount := int32(len(w.runningJobs))
		w.mu.RUnlock()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		req := &scheduler.HeartbeatRequest{
			WorkerId:    w.workerAddr,
			Status:      status,
			RunningJobs: runningCount,
		}

		_, err := w.masterClient.SendHeartbeat(ctx, req)
		if err != nil {
			log.Infof("Failed to send heartbeat: %v", err)
		}
		cancel()
	}
}

func (w *WorkerNode) ExecuteJob(ctx context.Context, req *scheduler.ExecuteJobRequest) (*scheduler.ExecuteJobResponse, error) {
	job := req.GetJobToRun()
	log.Infof("Received job %s to execute: %s", job.Id, job.Command)

	w.mu.Lock()
	if int32(len(w.runningJobs)) >= w.capacity {
		w.mu.Unlock()
		log.Infof("Job %s rejected: worker at capacity", job.Id)
		return &scheduler.ExecuteJobResponse{Accepted: false}, nil
	}
	w.runningJobs[job.Id] = job
	w.updateStatus()
	w.mu.Unlock()

	go w.runJob(job)

	return &scheduler.ExecuteJobResponse{Accepted: true}, nil
}

func (w *WorkerNode) runJob(job *scheduler.Job) {
	defer func() {
		w.mu.Lock()
		delete(w.runningJobs, job.Id)
		w.updateStatus()
		w.mu.Unlock()
	}()

	w.updateMasterJobStatus(job.Id, scheduler.JobStatus_RUNNING, "")

	cmd := exec.Command("sh", "-c", job.Command)
	output, err := cmd.CombinedOutput()

	if err != nil {
		log.Infof("Job %s failed: %v", job.Id, err)
		w.updateMasterJobStatus(job.Id, scheduler.JobStatus_FAILED, string(output))
	} else {
		log.Infof("Job %s completed successfully", job.Id)
		w.updateMasterJobStatus(job.Id, scheduler.JobStatus_COMPLETED, string(output))
	}
}

func (w *WorkerNode) updateMasterJobStatus(jobID string, status scheduler.JobStatus, output string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := &scheduler.UpdateJobStatusRequest{
		WorkerId:  w.workerAddr,
		JobId:     jobID,
		NewStatus: status,
		Output:    output,
	}

	_, err := w.masterClient.UpdateJobStatus(ctx, req)
	if err != nil {
		log.Infof("Failed to update job status %s on master: %v", jobID, err)
	}
}

func (w *WorkerNode) updateStatus() {
	if len(w.runningJobs) > 0 {
		w.status = scheduler.WorkerStatus_BUSY
	} else {
		w.status = scheduler.WorkerStatus_IDLE
	}
}

func (w *WorkerNode) StartServer() error {
	lis, err := net.Listen("tcp", w.workerAddr)
	if err != nil {
		return fmt.Errorf("worker failed to listen: %w", err)
	}

	s := grpc.NewServer()
	w.grpcServer = s
	scheduler.RegisterWorkerServer(s, w)

	log.Infof("Worker server listening at %v", lis.Addr())
	return s.Serve(lis)
}

func (w *WorkerNode) Stop() {
	if w.grpcServer != nil {
		w.grpcServer.GracefulStop()
		log.Infoln("Worker server stopped gracefully")
	} else {
		log.Infoln("Worker server was not running")
	}
}
