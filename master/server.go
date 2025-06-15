package master

import (
	"net"
	"net/http"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"

	scheduler "github.com/konoha/proto"
)

type MasterConfig struct {
	HttpListenAddr string
	ListenAddr     string
}

type Server struct {
	config     *MasterConfig
	grpcServer *grpc.Server
}

func NewServer(config *MasterConfig) *Server {
	return &Server{
		config: config,
	}
}

func (s *Server) Start() {
	log.Infof("Starting master server on %s", s.config.ListenAddr)

	lis, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	grpcServer := grpc.NewServer()
	s.grpcServer = grpcServer
	master := NewMaster()
	scheduler.RegisterSchedulerServer(s.grpcServer, master)

	httpMux := http.NewServeMux()
	httpMux.HandleFunc("/jobs", master.handleJobSubmit)

	go func() {
		log.Printf("HTTP API server listening at %s", s.config.HttpListenAddr)
		if err := http.ListenAndServe(s.config.HttpListenAddr, httpMux); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	go master.startScheduler()
	go master.startHealthChecks()

	go func() {
		if err := s.grpcServer.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()
	log.Infof("Master server started on %s", s.config.ListenAddr)
}

func (s *Server) Stop() {
	s.grpcServer.GracefulStop()
	log.Infoln("Master server stopped gracefully")
}
