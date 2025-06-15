package master

import (
	"context"
	"fmt"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	scheduler "github.com/konoha/proto"
)

type WorkerInfo struct {
	ID             string
	Address        string // e.g., "localhost:50052"
	Status         scheduler.WorkerStatus
	Capacity       int32
	RunningJobs    int32
	LastHeartbeat  time.Time
	grpcClientConn *grpc.ClientConn
	grpcClient     scheduler.WorkerClient
}

type MasterNode struct {
	scheduler.UnimplementedSchedulerServer

	mu         sync.RWMutex
	workers    map[string]*WorkerInfo // workerID -> WorkerInfo
	jobQueue   chan *scheduler.Job
	jobStore   map[string]*scheduler.Job // jobID -> Job
	workerIdx  int
	workerKeys []string
}

func NewMaster() *MasterNode {
	return &MasterNode{
		workers:    make(map[string]*WorkerInfo),
		jobQueue:   make(chan *scheduler.Job, 100),
		jobStore:   make(map[string]*scheduler.Job),
		workerKeys: make([]string, 0),
	}
}

func (m *MasterNode) RegisterWorker(ctx context.Context, req *scheduler.RegisterWorkerRequest) (*scheduler.RegisterWorkerResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workerID := req.GetWorkerId()
	log.Infof("Received registration request from worker: %s", workerID)

	workerAddr := workerID
	conn, err := grpc.NewClient(workerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Infof("Failed to connect back to worker %s: %v", workerID, err)
		return nil, err
	}

	workerInfo := &WorkerInfo{
		ID:             workerID,
		Address:        workerAddr,
		Status:         scheduler.WorkerStatus_IDLE,
		Capacity:       req.GetCapacity(),
		LastHeartbeat:  time.Now(),
		grpcClientConn: conn,
		grpcClient:     scheduler.NewWorkerClient(conn),
	}

	m.workers[workerID] = workerInfo
	m.updateWorkerKeys()

	log.Infof("Worker %s registered successfully with capacity %d", workerID, req.GetCapacity())

	return &scheduler.RegisterWorkerResponse{Registered: true, MasterId: "master-01"}, nil
}

func (m *MasterNode) SendHeartbeat(ctx context.Context, req *scheduler.HeartbeatRequest) (*scheduler.HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	workerID := req.GetWorkerId()
	worker, ok := m.workers[workerID]
	if !ok {
		log.Infof("Received heartbeat from unregistered worker: %s", workerID)
		return nil, fmt.Errorf("worker %s not registered", workerID)
	}

	log.Infof("Received heartbeat from worker: %s", workerID)
	worker.LastHeartbeat = time.Now()
	worker.Status = req.GetStatus()
	worker.RunningJobs = req.GetRunningJobs()

	return &scheduler.HeartbeatResponse{Acknowledged: true}, nil
}

func (m *MasterNode) SubmitJob(ctx context.Context, req *scheduler.SubmitJobRequest) (*scheduler.SubmitJobResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	newJob := &scheduler.Job{
		Id:        jobID,
		Command:   req.GetCommand(),
		Status:    scheduler.JobStatus_PENDING,
		CreatedAt: timestamppb.Now(),
	}

	m.jobStore[jobID] = newJob
	m.jobQueue <- newJob

	log.Infof("Submitted new job %s: %s", jobID, req.GetCommand())

	return &scheduler.SubmitJobResponse{JobId: jobID, Status: scheduler.JobStatus_PENDING}, nil
}

func (m *MasterNode) UpdateJobStatus(ctx context.Context, req *scheduler.UpdateJobStatusRequest) (*scheduler.UpdateJobStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobStore[req.JobId]
	if !ok {
		return nil, fmt.Errorf("job %s not found", req.JobId)
	}

	log.Infof("Updating status for job %s from worker %s to %s", req.JobId, req.WorkerId, req.NewStatus)
	job.Status = req.NewStatus
	job.Output = req.Output
	if req.NewStatus == scheduler.JobStatus_COMPLETED || req.NewStatus == scheduler.JobStatus_FAILED {
		job.CompletedAt = timestamppb.Now()
	}

	return &scheduler.UpdateJobStatusResponse{Acknowledged: true}, nil
}

func (m *MasterNode) startScheduler() {
	log.Infoln("Scheduler loop started...")
	for job := range m.jobQueue {
		log.Infof("Scheduler picked up job: %s", job.GetId())

		var selectedWorker *WorkerInfo
		for {
			m.mu.RLock()
			if len(m.workers) == 0 {
				m.mu.RUnlock()
				log.Infoln("No workers available. Waiting...")
				time.Sleep(5 * time.Second)
				continue
			}

			// Simple Round Robin
			m.workerIdx = (m.workerIdx + 1) % len(m.workers)
			workerID := m.workerKeys[m.workerIdx]
			worker := m.workers[workerID]

			if worker.Status == scheduler.WorkerStatus_IDLE || worker.RunningJobs < worker.Capacity {
				selectedWorker = worker
				m.mu.RUnlock()
				break
			}
			m.mu.RUnlock()

			// If all workers are busy, wait a bit before retrying
			time.Sleep(1 * time.Second)
		}

		log.Infof("Assigning job %s to worker %s", job.Id, selectedWorker.ID)

		m.mu.Lock()
		job.Status = scheduler.JobStatus_RUNNING
		job.StartedAt = timestamppb.Now()
		m.mu.Unlock()

		go func(w *WorkerInfo, j *scheduler.Job) {
			_, err := w.grpcClient.ExecuteJob(context.Background(), &scheduler.ExecuteJobRequest{JobToRun: j})
			if err != nil {
				log.Infof("Failed to send job %s to worker %s: %v", j.Id, w.ID, err)
				m.mu.Lock()
				j.Status = scheduler.JobStatus_PENDING
				m.mu.Unlock()
				m.jobQueue <- j
			}
		}(selectedWorker, job)
	}
}

func (m *MasterNode) startHealthChecks() {
	log.Infoln("Health check loop started...")
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		for id, worker := range m.workers {
			if time.Since(worker.LastHeartbeat) > 30*time.Second {
				log.Infof("Worker %s is considered dead. Removing.", id)
				worker.grpcClientConn.Close()
				delete(m.workers, id)
				m.updateWorkerKeys()
			}
		}
		m.mu.Unlock()
	}
}

func (m *MasterNode) updateWorkerKeys() {
	keys := make([]string, 0, len(m.workers))
	for k := range m.workers {
		keys = append(keys, k)
	}
	m.workerKeys = keys
}
