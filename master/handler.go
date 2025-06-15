package master

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	scheduler "github.com/konoha/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type httpJobRequest struct {
	Command string `json:"command"`
}

func (m *MasterNode) handleJobSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Only POST method is allowed", http.StatusMethodNotAllowed)
		return
	}

	var req httpJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Command == "" {
		http.Error(w, "Command cannot be empty", http.StatusBadRequest)
		return
	}

	grpcReq := &scheduler.SubmitJobRequest{Command: req.Command}

	m.mu.Lock()
	defer m.mu.Unlock()

	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	newJob := &scheduler.Job{
		Id:        jobID,
		Command:   grpcReq.GetCommand(),
		Status:    scheduler.JobStatus_PENDING,
		CreatedAt: timestamppb.Now(),
	}
	m.jobStore[jobID] = newJob
	m.jobQueue <- newJob

	log.Printf("Submitted new job %s via HTTP API: %s", jobID, grpcReq.GetCommand())

	resp := &scheduler.SubmitJobResponse{
		JobId:  jobID,
		Status: scheduler.JobStatus_PENDING,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
